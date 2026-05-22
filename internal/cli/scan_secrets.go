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
			var all []secrets.Finding
			for _, path := range args {
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
