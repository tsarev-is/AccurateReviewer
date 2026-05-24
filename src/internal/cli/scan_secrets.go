package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/scaratec/accurate-reviewer/internal/secrets"
)

func newScanSecretsCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "scan-secrets <file>",
		Short: "Deterministic pre-flight secrets scan (no LLM)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			logf := func(format string, args ...any) {
				fmt.Fprintf(cmd.ErrOrStderr(), "[scan-secrets] "+format+"\n", args...)
			}

			logf("scanning %d file(s)", len(args))
			var all []secrets.Finding
			for i, path := range args {
				logf("(%d/%d) %s", i+1, len(args), path)
				f, err := os.Open(path)
				if err != nil {
					return Exit(1, "open %s: %v", path, err)
				}
				findings, err := secrets.Scan(path, f, 4.5)
				f.Close()
				if err != nil {
					return Exit(1, "scan %s: %v", path, err)
				}
				all = append(all, findings...)
			}
			logf("done: %d finding(s)", len(all))

			out := cmd.OutOrStdout()
			if jsonOut {
				return json.NewEncoder(out).Encode(all)
			}
			if len(all) == 0 {
				fmt.Fprintln(out, "0 findings")
				return nil
			}
			for _, f := range all {
				fmt.Fprintf(out, "  [%s] %s:%d rule=%s match=%s\n",
					f.Severity, f.File, f.Line, f.Rule, f.Match)
			}
			fmt.Fprintf(out, "%d findings\n", len(all))
			return Exit(1, "")
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit findings as JSON")
	return cmd
}
