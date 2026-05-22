package cli

import (
	"encoding/json"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/scaratec/accurate-reviewer/internal/config"
	"github.com/scaratec/accurate-reviewer/internal/diff"
)

func newParseDiffCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "parse-diff [file|-]",
		Short: "Parse a unified diff and emit review units as JSON",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var src io.Reader = os.Stdin
			if len(args) == 1 && args[0] != "-" {
				f, err := os.Open(args[0])
				if err != nil {
					return Exit(1, "open %s: %v", args[0], err)
				}
				defer f.Close()
				src = f
			}

			var excludes []string
			if cfg, err := config.Load(".review.yml"); err == nil {
				excludes = cfg.Exclude
			}

			units, err := diff.Parse(src, excludes)
			if err != nil {
				return Exit(1, "parse: %v", err)
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(units)
		},
	}
	return cmd
}
