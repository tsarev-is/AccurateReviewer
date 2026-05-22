package cli

import (
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/scaratec/accurate-reviewer/internal/sanitizer"
)

func newSanitizeCmd() *cobra.Command {
	var disableNeutralise bool
	cmd := &cobra.Command{
		Use:   "sanitize",
		Short: "Wrap stdin in code-under-review delimiters; emit to stdout",
		RunE: func(cmd *cobra.Command, _ []string) error {
			data, err := io.ReadAll(os.Stdin)
			if err != nil {
				return Exit(1, "read stdin: %v", err)
			}
			out := sanitizer.Sanitize(string(data), sanitizer.Options{
				NeutraliseEnabled: !disableNeutralise,
			})
			_, _ = cmd.OutOrStdout().Write([]byte(out))
			return nil
		},
	}
	cmd.Flags().BoolVar(&disableNeutralise, "no-neutralise", false, "disable neutralisation passes (wrapping still applies)")
	return cmd
}
