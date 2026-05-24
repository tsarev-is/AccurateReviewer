package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/scaratec/accurate-reviewer/internal/analyzer"
	"github.com/scaratec/accurate-reviewer/internal/config"
	"github.com/scaratec/accurate-reviewer/internal/diff"
	"github.com/scaratec/accurate-reviewer/internal/llm"
	"github.com/scaratec/accurate-reviewer/internal/master"
	"github.com/scaratec/accurate-reviewer/internal/report"
	"github.com/scaratec/accurate-reviewer/internal/secrets"
)

func newReviewCmd() *cobra.Command {
	var (
		diffPath   string
		fromRef    string
		toRef      string
		outputPath string
	)
	cmd := &cobra.Command{
		Use:   "review",
		Short: "Run incremental review on a diff",
		RunE: func(cmd *cobra.Command, _ []string) error {
			progress := cmd.ErrOrStderr()
			logf := func(format string, args ...any) {
				fmt.Fprintf(progress, "[review] "+format+"\n", args...)
			}

			// Diff-source resolution comes first: "no diff source" is a misuse
			// error that should win over a missing/invalid .review.yml so the
			// developer fixes the obvious mistake before the subtler one.
			logf("loading diff")
			diffData, err := loadDiff(diffPath, fromRef, toRef)
			if err != nil {
				return err
			}
			logf("diff loaded: %d byte(s)", len(diffData))

			cfg, err := config.Load(".review.yml")
			if err != nil {
				return Exit(2, "%v", err)
			}
			logf("config loaded: provider=%s blocking=%s", cfg.LLM.Provider, cfg.Severity.Blocking)

			// Report sink: stdout by default, file when --output is given.
			// Progress (stderr) and the "report written to ..." confirmation
			// (stdout) stay separate so callers can still see that the run
			// finished even when the body is redirected to a file.
			reportOut := cmd.OutOrStdout()
			if outputPath != "" {
				if err := validateOutputPath(outputPath); err != nil {
					return Exit(2, "invalid --output: %v", err)
				}
				f, err := os.Create(outputPath)
				if err != nil {
					return Exit(1, "open output: %v", err)
				}
				defer f.Close()
				reportOut = f
			}

			// Pre-flight secrets scan over the raw diff content. Excludes do not apply.
			logf("pre-flight secrets scan")
			preFindings, err := scanDiffForSecrets(bytes.NewReader(diffData), cfg.Secrets.EntropyThreshold)
			if err != nil {
				return Exit(1, "secrets pre-flight: %v", err)
			}
			if len(preFindings) > 0 && cfg.Secrets.Enabled {
				logf("secrets scan: %d finding(s) — aborting", len(preFindings))
				// CWE-312: the report sink may be a user-supplied path
				// (--output), so per-finding detail — including the
				// redacted match value — must not be written there. It
				// goes to stderr only; the sink gets a generic notice.
				for _, f := range preFindings {
					fmt.Fprintf(cmd.ErrOrStderr(), "  [%s] %s:%d rule=%s match=%s\n",
						f.Severity, f.File, f.Line, f.Rule, f.Match)
				}
				fmt.Fprintf(reportOut, "secrets detected — aborting review (%d finding(s), see stderr)\n", len(preFindings))
				if outputPath != "" {
					fmt.Fprintf(cmd.OutOrStdout(), "report written to %s\n", outputPath)
				}
				return Exit(1, "")
			}
			logf("secrets scan: clean")

			units, err := diff.Parse(bytes.NewReader(diffData), cfg.Exclude)
			if err != nil {
				return Exit(1, "parse diff: %v", err)
			}
			logf("parsed %d review unit(s)", len(units))

			provider := selectProvider(cfg)
			snap, _ := analyzer.ReadSnapshot(".")
			if snap == nil {
				logf("no project snapshot — running without it")
			} else {
				logf("project snapshot: language=%s", snap.Language.Primary)
			}

			m := &master.Master{Cfg: cfg, Provider: provider, Snapshot: snap, Progress: progress}
			rep, err := m.Review(context.Background(), units)
			if err != nil {
				return Exit(1, "review: %v", err)
			}

			reviewed := make([]string, 0, len(units))
			for _, u := range units {
				reviewed = append(reviewed, u.File)
			}
			blocked := report.Console(reportOut, rep.Findings, cfg.Severity.Blocking, reviewed)

			for _, e := range rep.WorkerErrors {
				fmt.Fprintln(cmd.ErrOrStderr(), e)
			}
			if rep.BudgetExceeded {
				fmt.Fprintln(reportOut, "budget exceeded")
				if outputPath != "" {
					fmt.Fprintf(cmd.OutOrStdout(), "report written to %s\n", outputPath)
				}
				return Exit(2, "")
			}
			if outputPath != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "report written to %s\n", outputPath)
			}
			if len(rep.WorkerErrors) > 0 {
				return Exit(2, "")
			}
			if blocked {
				return Exit(1, "")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&diffPath, "diff", "", "diff source (path or '-' for stdin)")
	cmd.Flags().StringVar(&fromRef, "from", "", "git ref to diff from")
	cmd.Flags().StringVar(&toRef, "to", "", "git ref to diff to")
	cmd.Flags().StringVarP(&outputPath, "output", "o", "", "write the report to this file instead of stdout")
	return cmd
}

// validateOutputPath rejects --output values that point outside the current
// working directory (CWE-22). In CI/CD, the flag is often composed from
// external inputs; an attacker-controlled "../../etc/cron.d/job" or absolute
// path would otherwise let os.Create silently overwrite arbitrary files.
func validateOutputPath(p string) error {
	if filepath.IsAbs(p) {
		return fmt.Errorf("%q must stay within the working directory", p)
	}
	cleaned := filepath.Clean(p)
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%q must stay within the working directory", p)
	}
	return nil
}

func loadDiff(path, from, to string) ([]byte, error) {
	switch {
	case path == "-":
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return nil, Exit(1, "read stdin: %v", err)
		}
		return b, nil
	case path != "":
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, Exit(1, "read %s: %v", path, err)
		}
		return b, nil
	case from != "" || to != "":
		args := []string{"diff", "--no-color"}
		if from != "" && to != "" {
			args = append(args, from+".."+to)
		} else if from != "" {
			args = append(args, from)
		}
		out, err := exec.Command("git", args...).Output()
		if err != nil {
			return nil, Exit(1, "git diff: %v", err)
		}
		return out, nil
	default:
		return nil, Exit(2, "no diff source — use --diff, or --from/--to")
	}
}

func selectProvider(cfg *config.Config) llm.Provider {
	passEnv := append([]string{}, cfg.LLM.CLI.PassEnv...)
	if cfg.LLM.APIKeyEnv != "" {
		passEnv = append(passEnv, cfg.LLM.APIKeyEnv)
	}
	return &llm.CLIProvider{
		Name_:     cfg.LLM.Provider,
		Bin:       cfg.LLM.CLI.Bin,
		Args:      cfg.LLM.CLI.Args,
		ModelFlag: cfg.LLM.CLI.ModelFlag,
		Timeout:   time.Duration(cfg.LLM.CLI.TimeoutSeconds) * time.Second,
		PassEnv:   passEnv,
	}
}

// scanDiffForSecrets feeds added lines from the raw diff into the secrets
// scanner. We do this on the diff (not parsed units) so excludes never apply.
func scanDiffForSecrets(r io.Reader, threshold float64) ([]secrets.Finding, error) {
	units, err := diff.Parse(r, nil)
	if err != nil {
		return nil, err
	}
	var all []secrets.Finding
	for _, u := range units {
		for _, h := range u.Hunks {
			for i, line := range h.Added {
				ln := h.NewStart
				if i < len(h.AddedLineNumbers) {
					ln = h.AddedLineNumbers[i]
				}
				findings, err := secrets.Scan(u.File, bytes.NewReader([]byte(line+"\n")), threshold)
				if err != nil {
					return nil, err
				}
				for _, f := range findings {
					f.Line = ln
					all = append(all, f)
				}
			}
		}
	}
	return all, nil
}
