package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/scaratec/accurate-reviewer/internal/analyzer"
)

func newAnalyzeCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "analyze",
		Short: "Build (or refresh) the project snapshot under .review-cache/",
		RunE: func(cmd *cobra.Command, _ []string) error {
			logf := func(format string, args ...any) {
				fmt.Fprintf(cmd.ErrOrStderr(), "[analyze] "+format+"\n", args...)
			}

			root := "."
			logf("scanning %s", root)
			if !force {
				if prev, err := analyzer.ReadSnapshot(root); err == nil {
					logf("previous snapshot found — checking for changes")
					snap, err := analyzer.Analyze(root)
					if err == nil && snap.Fingerprint == prev.Fingerprint {
						logf("fingerprint unchanged — reusing existing snapshot")
						fmt.Fprintln(cmd.OutOrStdout(), "snapshot up to date")
						return nil
					}
				}
			}
			logf("running full analysis")
			snap, err := analyzer.Analyze(root)
			if err != nil {
				return Exit(1, "analyze: %v", err)
			}
			logf("detected language=%s frameworks=%d manifests=%d",
				snap.Language.Primary, len(snap.Frameworks), len(snap.Manifests))
			if err := analyzer.WriteSnapshot(root, snap); err != nil {
				return Exit(1, "write snapshot: %v", err)
			}
			logf("snapshot saved to .review-cache/project.json")
			fmt.Fprintf(cmd.OutOrStdout(), "snapshot written: language=%s manifests=%d\n",
				snap.Language.Primary, len(snap.Manifests))
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "rebuild snapshot even if up to date")
	return cmd
}
