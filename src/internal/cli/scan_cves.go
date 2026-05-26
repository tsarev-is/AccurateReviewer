package cli

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/scaratec/accurate-reviewer/internal/config"
	"github.com/scaratec/accurate-reviewer/internal/cves"
)

func newScanCVEsCmd() *cobra.Command {
	var (
		jsonOut bool
		minSev  string
		rootArg string
	)
	cmd := &cobra.Command{
		Use:   "scan-cves [path]",
		Short: "Deterministic dependency-vulnerability scan via osv-scanner (no LLM)",
		Long: `Run osv-scanner over the given path (default: current directory)
and report any known vulnerabilities affecting the project's declared
dependencies. The binary itself opens no network sockets — osv-scanner
handles all OSV-database lookups under its own auth and caching.

A missing osv-scanner CLI is an error for this subcommand (the pre-flight
embedded in 'review' treats it as optional instead). Exit codes mirror
'review':
  0 — no findings at or above --min-severity
  1 — at least one finding survived the severity filter`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			logf := func(format string, args ...any) {
				fmt.Fprintf(cmd.ErrOrStderr(), "[scan-cves] "+format+"\n", args...)
			}
			root := rootArg
			if root == "" && len(args) > 0 {
				root = args[0]
			}
			if root == "" {
				root = "."
			}

			// Pull bin / timeout from .review.yml when present, else use
			// defaults. Missing config is not an error — `scan-cves` must
			// work in a freshly cloned repo before `accurate-reviewer init`.
			opts := cves.Options{
				Bin:            cves.DefaultBin,
				TimeoutSeconds: 60,
				MinSeverity:    minSev,
				Required:       true,
			}
			if cfg, err := config.Load(".review.yml"); err == nil {
				if cfg.CVE.Bin != "" {
					opts.Bin = cfg.CVE.Bin
				}
				if cfg.CVE.TimeoutSeconds > 0 {
					opts.TimeoutSeconds = cfg.CVE.TimeoutSeconds
				}
				if minSev == "" && cfg.CVE.MinSeverity != "" {
					opts.MinSeverity = cfg.CVE.MinSeverity
				}
			}

			logf("scanning %s with %s (timeout=%ds, min_severity=%q)", root, opts.Bin, opts.TimeoutSeconds, opts.MinSeverity)
			vulns, err := cves.Scan(context.Background(), root, opts)
			if err != nil {
				return Exit(1, "%v", err)
			}
			logf("done: %d finding(s)", len(vulns))

			out := cmd.OutOrStdout()
			if jsonOut {
				return json.NewEncoder(out).Encode(vulns)
			}
			if len(vulns) == 0 {
				fmt.Fprintln(out, "0 findings")
				return nil
			}
			for _, v := range vulns {
				cveTag := v.ID
				if v.CVE != "" {
					cveTag = v.CVE
				}
				fixed := ""
				if v.FixedIn != "" {
					fixed = " (fixed in " + v.FixedIn + ")"
				}
				fmt.Fprintf(out, "  [%s] %s %s@%s %s%s — %s\n",
					v.Severity, cveTag, v.Package, v.Version, v.File, fixed, v.Summary)
			}
			fmt.Fprintf(out, "%d findings\n", len(vulns))
			return Exit(1, "")
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit findings as JSON")
	cmd.Flags().StringVar(&minSev, "min-severity", "", "drop findings below this severity (info|low|medium|high|critical)")
	cmd.Flags().StringVar(&rootArg, "path", "", "directory to scan (default: current)")
	return cmd
}
