package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
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
		diffPath string
		fromRef  string
		toRef    string
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

			// Pre-flight secrets scan over the raw diff content. Excludes do not apply.
			logf("pre-flight secrets scan")
			preFindings, err := scanDiffForSecrets(bytes.NewReader(diffData), cfg.Secrets.EntropyThreshold)
			if err != nil {
				return Exit(1, "secrets pre-flight: %v", err)
			}
			if len(preFindings) > 0 && cfg.Secrets.Enabled {
				logf("secrets scan: %d finding(s) — aborting", len(preFindings))
				for _, f := range preFindings {
					fmt.Fprintf(cmd.OutOrStdout(), "  [%s] %s:%d rule=%s match=%s\n",
						f.Severity, f.File, f.Line, f.Rule, f.Match)
				}
				fmt.Fprintln(cmd.OutOrStdout(), "secrets detected — aborting review")
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
			blocked := report.Console(cmd.OutOrStdout(), rep.Findings, cfg.Severity.Blocking, reviewed)

			for _, e := range rep.WorkerErrors {
				fmt.Fprintln(cmd.ErrOrStderr(), e)
			}
			if rep.BudgetExceeded {
				fmt.Fprintln(cmd.OutOrStdout(), "budget exceeded")
				return Exit(2, "")
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
	return cmd
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
