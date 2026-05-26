package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/scaratec/accurate-reviewer/internal/report"
	"github.com/scaratec/accurate-reviewer/internal/worker"
)

// apply-fixes reads a JSON review report and applies every Finding.Fix
// against the working tree. Like every other forge-touching subcommand
// (post-comments → gh/glab/bb), it shells out — `git apply` here — rather
// than reimplementing patch application in Go. That keeps the binary's
// blast radius bounded by what `git apply` already gates (whitespace
// checks, exec mode, three-way fallback, etc.).
//
// The model-supplied fix shape (worker.Fix) is NOT a unified diff: it's a
// list of (file, line-range, new-text) Replacements. We translate to a
// unified diff at apply time so we control the hunk headers and never
// trust an LLM with arithmetic. The diff is built from the *current* file
// content on disk — if the working tree has drifted from what the model
// saw (later edits, reverts, etc.), `git apply` rejects the patch cleanly
// rather than silently rewriting the wrong span.
//
// --dry-run prints the synthesised unified diff to stdout instead of
// invoking `git apply`. Useful for piping into `git apply --check`,
// inspecting in CI logs, or just eyeballing.
func newApplyFixesCmd() *cobra.Command {
	var (
		reportPath string
		dryRun     bool
	)
	cmd := &cobra.Command{
		Use:   "apply-fixes",
		Short: "Apply model-suggested fixes from a review report via git apply",
		Long: `Read a JSON review report (produced by 'review --output X.json') and
mechanically apply every finding's "fix" replacements to the working
tree by piping a synthesised unified diff into 'git apply'.

The replacements were already validated at review time — only fixes
touching ADDED lines of the diff under review survive into the report,
so this command never rewrites legacy context. Multiple fixes on the
same file are concatenated into a single git apply call so they land
atomically.

--dry-run prints the synthesised diff without invoking git apply.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			logf := func(format string, args ...any) {
				fmt.Fprintf(cmd.ErrOrStderr(), "[apply-fixes] "+format+"\n", args...)
			}
			if reportPath == "" {
				return Exit(2, "--report is required")
			}
			data, err := os.ReadFile(reportPath)
			if err != nil {
				return Exit(1, "read %s: %v", reportPath, err)
			}
			var rep report.JSONReport
			if err := json.Unmarshal(data, &rep); err != nil {
				return Exit(1, "parse %s: %v", reportPath, err)
			}
			if rep.SchemaVersion != 0 && rep.SchemaVersion != report.JSONSchemaVersion {
				return Exit(1, "report schema version %d not supported by this binary (expected %d)", rep.SchemaVersion, report.JSONSchemaVersion)
			}

			fixable := make([]worker.Finding, 0, len(rep.Findings))
			for _, f := range rep.Findings {
				if f.Fix != nil && len(f.Fix.Replacements) > 0 {
					fixable = append(fixable, f)
				}
			}
			if len(fixable) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "no fixes available — nothing to apply")
				return nil
			}
			logf("%d finding(s) carry a fix", len(fixable))

			patch, err := synthesiseUnifiedDiff(fixable)
			if err != nil {
				return Exit(1, "synthesise patch: %v", err)
			}

			if dryRun {
				fmt.Fprint(cmd.OutOrStdout(), patch)
				return nil
			}

			if _, err := exec.LookPath("git"); err != nil {
				return Exit(2, "'git' not found on PATH — apply-fixes shells out to `git apply`")
			}
			gitCmd := exec.Command("git", "apply", "--whitespace=nowarn", "-")
			gitCmd.Stdin = strings.NewReader(patch)
			var stdout, stderr bytes.Buffer
			gitCmd.Stdout = &stdout
			gitCmd.Stderr = &stderr
			if err := gitCmd.Run(); err != nil {
				msg := strings.TrimSpace(stderr.String())
				if msg == "" {
					msg = err.Error()
				}
				return Exit(1, "git apply failed: %s", msg)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "applied %d fix(es) to %d file(s)\n", len(fixable), countFiles(fixable))
			return nil
		},
	}
	cmd.Flags().StringVarP(&reportPath, "report", "r", "", "path to the JSON report from `review --output X.json`")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the synthesised unified diff without invoking git apply")
	return cmd
}

func countFiles(findings []worker.Finding) int {
	seen := map[string]bool{}
	for _, f := range findings {
		for _, r := range f.Fix.Replacements {
			seen[r.File] = true
		}
	}
	return len(seen)
}

// synthesiseUnifiedDiff builds one unified diff covering every fixable
// finding. Replacements are grouped per file; within a file, hunks are
// sorted by start_line. Each Replacement becomes one `@@` hunk — the
// caller has already validated non-overlap implicitly (line ranges live
// inside ADDED lines of one unit), so we don't merge adjacent hunks.
//
// File content is read from disk at apply time, not from the report,
// because the report only carries the post-replacement text. The
// pre-image lines (the `-` side of the hunk) MUST match the current
// working tree exactly or `git apply` refuses — that's the safety we
// want when the working tree has drifted.
func synthesiseUnifiedDiff(findings []worker.Finding) (string, error) {
	byFile := map[string][]worker.Replacement{}
	for _, f := range findings {
		for _, r := range f.Fix.Replacements {
			byFile[r.File] = append(byFile[r.File], r)
		}
	}
	files := make([]string, 0, len(byFile))
	for f := range byFile {
		files = append(files, f)
	}
	sort.Strings(files)

	var buf bytes.Buffer
	for _, file := range files {
		reps := byFile[file]
		sort.SliceStable(reps, func(i, j int) bool { return reps[i].StartLine < reps[j].StartLine })

		content, err := os.ReadFile(file)
		if err != nil {
			return "", fmt.Errorf("read %s: %w", file, err)
		}
		lines := splitKeepEOL(string(content))

		fmt.Fprintf(&buf, "--- a/%s\n", file)
		fmt.Fprintf(&buf, "+++ b/%s\n", file)
		for _, r := range reps {
			if r.StartLine < 1 || r.EndLine < r.StartLine || r.EndLine > len(lines) {
				return "", fmt.Errorf("%s: replacement range %d-%d is outside the file (1..%d)", file, r.StartLine, r.EndLine, len(lines))
			}
			oldSpan := lines[r.StartLine-1 : r.EndLine]
			newSpan := splitKeepEOL(ensureTrailingNewline(r.NewText))
			fmt.Fprintf(&buf, "@@ -%d,%d +%d,%d @@\n", r.StartLine, len(oldSpan), r.StartLine, len(newSpan))
			for _, ln := range oldSpan {
				buf.WriteByte('-')
				buf.WriteString(ln)
				if !strings.HasSuffix(ln, "\n") {
					buf.WriteByte('\n')
				}
			}
			for _, ln := range newSpan {
				buf.WriteByte('+')
				buf.WriteString(ln)
				if !strings.HasSuffix(ln, "\n") {
					buf.WriteByte('\n')
				}
			}
		}
	}
	return buf.String(), nil
}

// splitKeepEOL splits s on '\n' but keeps the trailing '\n' on each line
// (except the last when s doesn't end with one). That lets the patch
// writer round-trip lines verbatim — important for `git apply` which
// matches bytes, not stripped text.
func splitKeepEOL(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	for {
		nl := strings.IndexByte(s, '\n')
		if nl < 0 {
			out = append(out, s)
			return out
		}
		out = append(out, s[:nl+1])
		s = s[nl+1:]
		if s == "" {
			return out
		}
	}
}

func ensureTrailingNewline(s string) string {
	if s == "" || strings.HasSuffix(s, "\n") {
		return s
	}
	return s + "\n"
}
