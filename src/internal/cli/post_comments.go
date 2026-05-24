package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/scaratec/accurate-reviewer/internal/report"
	"github.com/scaratec/accurate-reviewer/internal/severity"
	"github.com/scaratec/accurate-reviewer/internal/worker"
)

func newPostCommentsCmd() *cobra.Command {
	var (
		reportPath string
		prNumber   int
		commitSHA  string
		repoSlug   string
		dryRun     bool
		minSev     string
	)
	cmd := &cobra.Command{
		Use:   "post-comments",
		Short: "Post review findings as inline comments on a GitHub PR via `gh`",
		Long: `Read a JSON review report (produced by 'review --output X.json') and
publish each finding as an inline pull-request comment using the locally
installed 'gh' CLI. The binary never speaks to GitHub directly — auth,
proxies, and base URL all stay where 'gh' already manages them.

Re-running against the same findings is safe: each posted (file, line,
title) tuple is hashed and stored in .review-cache/posted-comments.json
so the second run skips the already-posted comments. This is what keeps
force-push from spamming the PR.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			logf := func(format string, args ...any) {
				fmt.Fprintf(cmd.ErrOrStderr(), "[post-comments] "+format+"\n", args...)
			}

			// Preflight: refuse to run if `gh` is not on PATH. The error
			// message names the binary so the operator knows exactly what
			// to install.
			ghPath, err := exec.LookPath("gh")
			if err != nil {
				return Exit(2, "'gh' CLI not found on PATH — install https://cli.github.com/ before running post-comments")
			}
			logf("found gh at %s", ghPath)

			if prNumber <= 0 {
				return Exit(2, "--pr is required and must be > 0")
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

			// Resolve the commit SHA we want to anchor inline comments to.
			// The user can pass it explicitly (CI knows it) or we ask `gh`
			// for the PR head SHA.
			if commitSHA == "" {
				commitSHA, err = ghPRHeadSHA(ghPath, prNumber)
				if err != nil {
					return Exit(1, "resolve PR head SHA: %v", err)
				}
				logf("resolved PR #%d head sha: %s", prNumber, commitSHA)
			}

			// Filter by --min-severity (default "info" = everything). The
			// pre-allocated slice deliberately does NOT alias rep.Findings —
			// the in-place [:0] idiom is correct here but easy to break in a
			// later refactor, so the explicit make is worth a few bytes.
			eligible := make([]worker.Finding, 0, len(rep.Findings))
			for _, f := range rep.Findings {
				if severity.AtLeast(f.Severity, minSev) {
					eligible = append(eligible, f)
				}
			}

			// Stable order so the dedupe file is human-friendly and so
			// the BDD assertions land deterministically.
			sort.SliceStable(eligible, func(i, j int) bool {
				if eligible[i].File != eligible[j].File {
					return eligible[i].File < eligible[j].File
				}
				return eligible[i].Line < eligible[j].Line
			})

			posted, err := loadPostedSet()
			if err != nil {
				logf("warning: could not read posted-comments cache: %v", err)
				posted = map[string]bool{}
			}

			var (
				newCount   int
				skippedDup int
				failures   []error
			)
			for _, f := range eligible {
				key := commentKey(prNumber, commitSHA, f.File, f.Line, f.Title)
				if posted[key] {
					skippedDup++
					continue
				}
				body := renderCommentBody(f)
				args := []string{
					"api",
					"-X", "POST",
					"repos/" + repoSlugOrAuto(ghPath, repoSlug) + "/pulls/" + strconv.Itoa(prNumber) + "/comments",
					"-f", "body=" + body,
					"-f", "commit_id=" + commitSHA,
					"-f", "path=" + f.File,
					"-F", "line=" + strconv.Itoa(f.Line),
					"-f", "side=RIGHT",
				}
				if dryRun {
					logf("dry-run: would post on %s:%d (%s)", f.File, f.Line, f.Severity)
					newCount++
					continue
				}
				out, err := exec.Command(ghPath, args...).CombinedOutput()
				if err != nil {
					failures = append(failures, fmt.Errorf("post on %s:%d: %v (%s)", f.File, f.Line, err, strings.TrimSpace(string(out))))
					continue
				}
				posted[key] = true
				newCount++
				logf("posted on %s:%d (%s)", f.File, f.Line, f.Severity)
			}

			if !dryRun {
				if err := savePostedSet(posted); err != nil {
					logf("warning: could not persist posted-comments cache: %v", err)
				}
			}

			// Surface failures BEFORE the success summary so CI tooling
			// that greps stdout for "posted N new comment" does not
			// declare success while comments silently failed to post.
			if len(failures) > 0 {
				for _, e := range failures {
					logf("error: %v", e)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "posted %d new comment(s), skipped %d already-posted, %d failed\n",
					newCount, skippedDup, len(failures))
				return Exit(1, "%d comment(s) failed to post", len(failures))
			}
			fmt.Fprintf(cmd.OutOrStdout(), "posted %d new comment(s), skipped %d already-posted\n", newCount, skippedDup)
			return nil
		},
	}
	cmd.Flags().StringVarP(&reportPath, "report", "r", "", "path to the JSON report from `review --output X.json`")
	cmd.Flags().IntVar(&prNumber, "pr", 0, "PR number to post comments on")
	cmd.Flags().StringVar(&commitSHA, "commit-sha", "", "commit SHA the comments anchor to (default: PR head)")
	cmd.Flags().StringVar(&repoSlug, "repo", "", "owner/repo (default: inferred from the current directory via gh)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "log what would be posted without calling gh")
	cmd.Flags().StringVar(&minSev, "min-severity", "info", "skip findings below this severity (info|low|medium|high|critical)")
	return cmd
}

// renderCommentBody composes the markdown body posted to GitHub. We lead
// with the severity badge and CWE so reviewers can triage at a glance; the
// "why" prose follows on the next line. The "[posted by accurate-reviewer]"
// trailer is what makes a future search-and-replace dedupe possible if the
// per-PR cache file is ever lost.
func renderCommentBody(f worker.Finding) string {
	var b strings.Builder
	fmt.Fprintf(&b, "**[%s] %s**", strings.ToUpper(f.Severity), f.Title)
	if f.CWE != "" {
		fmt.Fprintf(&b, " · `%s`", f.CWE)
	}
	b.WriteString("\n\n")
	b.WriteString(f.Why)
	b.WriteString("\n\n_posted by accurate-reviewer_")
	return b.String()
}

// commentKey is the deduplication key. Including the PR number means a
// finding posted on PR #1 does not block the same finding being posted on
// PR #2. Including the commit SHA means a force-push that rewrites the
// flagged line re-posts the comment against the new commit instead of
// being silently swallowed — the old comment is anchored to a now-stale
// SHA and the developer needs to see the finding against the live code.
func commentKey(pr int, commitSHA, file string, line int, title string) string {
	h := sha256.New()
	fmt.Fprintf(h, "pr=%d|sha=%s|file=%s|line=%d|title=%s",
		pr, commitSHA, file, line, strings.ToLower(strings.TrimSpace(title)))
	return hex.EncodeToString(h.Sum(nil))
}

const postedCachePath = ".review-cache/posted-comments.json"

func loadPostedSet() (map[string]bool, error) {
	data, err := os.ReadFile(postedCachePath)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]bool{}, nil
		}
		return nil, err
	}
	var keys []string
	if err := json.Unmarshal(data, &keys); err != nil {
		return map[string]bool{}, nil
	}
	out := make(map[string]bool, len(keys))
	for _, k := range keys {
		out[k] = true
	}
	return out, nil
}

func savePostedSet(set map[string]bool) error {
	dir := filepath.Dir(postedCachePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	data, err := json.MarshalIndent(keys, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(postedCachePath, data, 0o644)
}

func ghPRHeadSHA(ghPath string, pr int) (string, error) {
	out, err := exec.Command(ghPath, "pr", "view", strconv.Itoa(pr), "--json", "headRefOid", "-q", ".headRefOid").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%v (%s)", err, strings.TrimSpace(string(out)))
	}
	sha := strings.TrimSpace(string(out))
	if sha == "" {
		return "", fmt.Errorf("empty SHA from gh")
	}
	return sha, nil
}

// repoSlugOrAuto returns the user-provided slug or queries gh for the
// current repo's owner/name. We do not cache between calls — the helper
// runs at most once per `post-comments` invocation.
func repoSlugOrAuto(ghPath, override string) string {
	if override != "" {
		return override
	}
	out, err := exec.Command(ghPath, "repo", "view", "--json", "nameWithOwner", "-q", ".nameWithOwner").CombinedOutput()
	if err != nil {
		// Falling back to the literal "OWNER/REPO" placeholder is
		// intentional — the subsequent `gh api` call will fail with a
		// clear 404 message naming the bogus repo, which is more useful
		// than a generic "could not detect repo" error here.
		return "OWNER/REPO"
	}
	return strings.TrimSpace(string(out))
}
