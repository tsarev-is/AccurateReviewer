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
	"github.com/scaratec/accurate-reviewer/internal/cves"
	"github.com/scaratec/accurate-reviewer/internal/diff"
	"github.com/scaratec/accurate-reviewer/internal/llm"
	"github.com/scaratec/accurate-reviewer/internal/master"
	"github.com/scaratec/accurate-reviewer/internal/report"
	"github.com/scaratec/accurate-reviewer/internal/secrets"
	"github.com/scaratec/accurate-reviewer/internal/severity"
	"github.com/scaratec/accurate-reviewer/internal/task"
	"github.com/scaratec/accurate-reviewer/internal/worker"
)

func newReviewCmd() *cobra.Command {
	var (
		diffPath   string
		fromRef    string
		toRef      string
		outputPath string
		taskFile   string
		jiraID     string
		githubID   string
		linearID   string
		noCache    bool
		fullMode   bool
	)
	cmd := &cobra.Command{
		Use:   "review",
		Short: "Run incremental review on a diff",
		RunE: func(cmd *cobra.Command, _ []string) error {
			progress := cmd.ErrOrStderr()
			logf := func(format string, args ...any) {
				fmt.Fprintf(progress, "[review] "+format+"\n", args...)
			}

			// --full and a diff source are mutually exclusive: the full mode
			// invents its own units by walking the working directory, so a
			// caller passing both is asking for two contradictory things.
			if fullMode && (diffPath != "" || fromRef != "" || toRef != "") {
				return Exit(2, "--full cannot be combined with --diff/--from/--to")
			}

			// Diff-source resolution comes first: "no diff source" is a misuse
			// error that should win over a missing/invalid .review.yml so the
			// developer fixes the obvious mistake before the subtler one.
			var (
				diffData []byte
				err      error
			)
			if !fullMode {
				logf("loading diff")
				diffData, err = loadDiff(diffPath, fromRef, toRef)
				if err != nil {
					return err
				}
				logf("diff loaded: %d byte(s)", len(diffData))
			} else {
				logf("full mode — walking working directory")
			}

			cfg, err := config.Load(".review.yml")
			if err != nil {
				return Exit(2, "%v", err)
			}
			logf("config loaded: provider=%s blocking=%s", cfg.LLM.Provider, cfg.Severity.Blocking)

			// Resolve the (optional) task description before any expensive
			// work — a misuse like "two task sources" or "missing file"
			// should fail fast rather than after a successful diff parse.
			taskOpts := task.Options{File: taskFile, GitHub: githubID, Jira: jiraID, Linear: linearID}
			if _, err := taskOpts.Validate(); err != nil {
				return Exit(2, "%v", err)
			}
			taskCtx, err := task.Load(cmd.Context(), taskOpts, cfg)
			if err != nil {
				return Exit(2, "%v", err)
			}
			if taskCtx != "" {
				logf("task context loaded: %d byte(s)", len(taskCtx))
			}

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
			// In full mode the "diff" is empty (no real diff to scan) — the
			// secrets scanner runs against the source files directly via a
			// separate pass would be ideal, but for v0.2 we skip the
			// pre-flight in full mode and rely on the per-worker findings.
			// Justification: full mode is an audit of legacy code that the
			// developer did not write, and aborting on a long-standing
			// committed credential makes the report useless. The skip is
			// announced to stderr so the operator never assumes "clean
			// --full run" means "no leaked tokens".
			var preFindings []secrets.Finding
			if !fullMode {
				logf("pre-flight secrets scan")
				preFindings, err = scanDiffForSecrets(bytes.NewReader(diffData), cfg.Secrets.EntropyThreshold)
				if err != nil {
					return Exit(1, "secrets pre-flight: %v", err)
				}
			} else {
				logf("full mode: skipping pre-flight secrets scan (audit-only mode — review the report for any hardcoded credentials)")
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

			var units []diff.Unit
			if fullMode {
				units, err = diff.WalkAsUnits(".", cfg.Exclude, 0)
				if err != nil {
					return Exit(1, "full-mode walk: %v", err)
				}
				logf("full mode: synthesised %d review unit(s) (informational — exit will not block on findings)", len(units))
			} else {
				units, err = diff.Parse(bytes.NewReader(diffData), cfg.Exclude)
				if err != nil {
					return Exit(1, "parse diff: %v", err)
				}
				logf("parsed %d review unit(s)", len(units))
			}

			providers := buildProviderSet(cfg)
			snap, _ := analyzer.ReadSnapshot(".")
			if snap == nil {
				logf("no project snapshot — running without it")
			} else {
				logf("project snapshot: language=%s", snap.Language.Primary)
			}

			if noCache {
				falseVal := false
				cfg.Cache.Enabled = &falseVal
			}
			m := &master.Master{Cfg: cfg, Providers: providers, Snapshot: snap, TaskContext: taskCtx, Progress: progress, ToolVersion: Version, CacheRoot: "."}
			rep, err := m.Review(context.Background(), units)
			if err != nil {
				return Exit(1, "review: %v", err)
			}

			// Pre-flight CVE scan via osv-scanner. Pre-pended to the
			// finding list so the report shows known-vulnerable
			// dependencies before the LLM-derived issues — high-severity
			// CVEs deserve the operator's first attention. A missing
			// osv-scanner CLI is logged once and the run continues
			// (Required=false here; the standalone `scan-cves`
			// subcommand sets Required=true so a missing tool is loud).
			if cfg.Checks.Vulnerabilities {
				logf("pre-flight CVE scan")
				cveOpts := cves.Options{
					Bin:            cfg.CVE.Bin,
					TimeoutSeconds: cfg.CVE.TimeoutSeconds,
					MinSeverity:    cfg.CVE.MinSeverity,
					Required:       false,
				}
				vulns, cerr := cves.Scan(context.Background(), ".", cveOpts)
				if cerr != nil {
					logf("CVE scan failed: %v — continuing without it", cerr)
				} else if len(vulns) > 0 {
					logf("CVE scan: %d advisory finding(s)", len(vulns))
					rep.Findings = append(vulnsToFindings(vulns), rep.Findings...)
				} else {
					logf("CVE scan: clean (or osv-scanner not installed)")
				}
			}

			reviewed := make([]string, 0, len(units))
			for _, u := range units {
				reviewed = append(reviewed, u.File)
			}
			var blocked bool
			switch ext := strings.ToLower(filepath.Ext(outputPath)); {
			case outputPath != "" && ext == ".html":
				if err := report.HTML(reportOut, rep.Findings, cfg.Severity.Blocking, reviewed); err != nil {
					return Exit(1, "html report: %v", err)
				}
				blocked = anyBlocking(rep.Findings, cfg.Severity.Blocking)
			case outputPath != "" && ext == ".json":
				if err := report.JSON(reportOut, rep.Findings, cfg.Severity.Blocking, reviewed); err != nil {
					return Exit(1, "json report: %v", err)
				}
				blocked = anyBlocking(rep.Findings, cfg.Severity.Blocking)
			default:
				blocked = report.Console(reportOut, rep.Findings, cfg.Severity.Blocking, reviewed)
			}
			if fullMode {
				// Full mode is explicitly informational. Squashing `blocked`
				// here keeps every other exit-code path (worker errors,
				// budget overrun, --output success line) untouched.
				if blocked {
					logf("full mode is informational — not blocking on findings even though some are at or above '%s'", cfg.Severity.Blocking)
				} else {
					logf("full mode complete (informational)")
				}
				blocked = false
			}

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
	cmd.Flags().StringVar(&taskFile, "task-file", "", "path to a text file with the task description")
	cmd.Flags().StringVar(&jiraID, "jira", "", "fetch the task description from the configured Jira CLI by issue id")
	cmd.Flags().StringVar(&githubID, "github", "", "fetch the task description from the configured GitHub CLI by issue/PR id")
	cmd.Flags().StringVar(&linearID, "linear", "", "fetch the task description from the configured Linear CLI by issue id")
	cmd.Flags().BoolVar(&noCache, "no-cache", false, "ignore the .review-cache/findings store and re-run every worker")
	cmd.Flags().BoolVar(&fullMode, "full", false, "review every file in the working directory (informational — never blocks)")
	return cmd
}

// anyBlocking lets the HTML/JSON output paths derive the same
// blocked/non-blocked decision the console path returns directly, without
// having to scrape rendered output.
func anyBlocking(findings []worker.Finding, blocking string) bool {
	for _, f := range findings {
		if severity.AtLeast(f.Severity, blocking) {
			return true
		}
	}
	return false
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

// buildProviderSet wires the multi-provider resolver. The TOP-LEVEL
// provider honours every operator-supplied CLI override (`llm.cli.*`) —
// that's the slot where a deployment can change how `claude`/`codex` get
// spawned. Per-worker overrides and the fallback provider use the
// built-in defaults for their named provider; operators who need to
// customise those CLIs further would have to live with that constraint
// (rare in practice, and adding per-override CLI specs is a 4x config
// surface explosion).
//
// The set always carries (Default, DefaultModel). Per-worker entries
// only appear when the operator named a worker in llm.workers. Fallback
// is only populated when llm.fallback.model is set — otherwise the
// master never switches paths and the budget enforcement becomes a hard
// stop (the legacy behaviour from before fallback existed).
func buildProviderSet(cfg *config.Config) *master.ProviderSet {
	set := &master.ProviderSet{
		Default:      selectProvider(cfg),
		DefaultModel: cfg.LLM.Worker.Model,
	}
	for name, spec := range cfg.LLM.Workers {
		var p llm.Provider
		if spec.Provider != "" && spec.Provider != cfg.LLM.Provider {
			p = builtinCLIProvider(spec.Provider)
		}
		set.RegisterWorker(name, p, spec.Model)
	}
	if cfg.LLM.Fallback.Model != "" {
		fbProvider := set.Default
		if cfg.LLM.Fallback.Provider != "" && cfg.LLM.Fallback.Provider != cfg.LLM.Provider {
			fbProvider = builtinCLIProvider(cfg.LLM.Fallback.Provider)
		}
		set.Fallback = fbProvider
		set.FallbackModel = cfg.LLM.Fallback.Model
	}
	return set
}

// builtinCLIProvider returns a CLIProvider for a named provider using
// only the binary's compiled-in defaults — no operator CLI overrides.
// Used for per-worker and fallback overrides where the operator can
// pick the provider name but not the spawn details. The defaults match
// applyCLIDefaults in the config package; if either drifts, the worker
// scenario "security via claude / logic via codex" notices because the
// fake CLI logs argv0 verbatim.
func builtinCLIProvider(name string) llm.Provider {
	switch name {
	case "claude":
		return &llm.CLIProvider{
			Name_:     "claude",
			Bin:       "claude",
			Args:      []string{"-p"},
			ModelFlag: "--model",
			Timeout:   300 * time.Second,
		}
	case "codex":
		return &llm.CLIProvider{
			Name_:   "codex",
			Bin:     "codex",
			Args:    []string{"exec"},
			Timeout: 300 * time.Second,
		}
	case "mock":
		return &llm.CLIProvider{
			Name_:   "mock",
			Bin:     "ar-mock-cli",
			Timeout: 30 * time.Second,
		}
	}
	return nil
}

// vulnsToFindings adapts CVE advisories into worker.Finding records so
// they share the same report/output pipeline as LLM-derived issues. The
// Worker field is set to "cves" — that label flows through dedupe() but
// CVE findings rarely collide with LLM findings (different Line values),
// so the practical effect is just a visible attribution in the report.
// Line is 0 because the advisory targets a manifest declaration, not a
// specific source line. Title concatenates the CVE/GHSA id with the
// affected package@version for a scannable single-line headline.
func vulnsToFindings(vulns []cves.Vuln) []worker.Finding {
	out := make([]worker.Finding, 0, len(vulns))
	for _, v := range vulns {
		id := v.ID
		if v.CVE != "" {
			id = v.CVE
		}
		title := fmt.Sprintf("%s in %s@%s", id, v.Package, v.Version)
		why := v.Summary
		if v.FixedIn != "" {
			why = strings.TrimSpace(why) + " — upgrade to " + v.FixedIn
		}
		out = append(out, worker.Finding{
			File:     v.File,
			Line:     0,
			Severity: v.Severity,
			Title:    title,
			Why:      why,
			CWE:      v.CVE,
			Worker:   "cves",
		})
	}
	return out
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
