package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"
)

// Version and Commit are populated at build time via -ldflags by the
// project's build scripts (Makefile, setup.sh). When built without
// ldflags (e.g. plain `go build`) they fall back to dev placeholders so
// every binary still answers --version coherently.
var (
	Version = "dev"
	Commit  = "unknown"
)

type ExitError struct {
	Code int
	Msg  string
}

func (e *ExitError) Error() string { return e.Msg }

func Exit(code int, format string, args ...any) error {
	return &ExitError{Code: code, Msg: fmt.Sprintf(format, args...)}
}

func AsExitError(err error, target **ExitError) bool {
	var e *ExitError
	if errors.As(err, &e) {
		*target = e
		return true
	}
	return false
}

func NewRoot() *cobra.Command {
	root := &cobra.Command{
		Use:           "accurate-reviewer",
		Short:         "AI code review tool — security first, LLM-based, BDD-specified",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       Version,
	}
	root.SetVersionTemplate(versionLine() + "\n")

	root.AddCommand(
		newInitCmd(),
		newAnalyzeCmd(),
		newReviewCmd(),
		newScanSecretsCmd(),
		newParseDiffCmd(),
		newSanitizeCmd(),
		newConfigCmd(),
		newServeCmd(),
		newPostCommentsCmd(),
		newVersionCmd(),
	)
	return root
}

func versionLine() string {
	return fmt.Sprintf("accurate-reviewer %s (commit %s)", Version, Commit)
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version and build commit",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), versionLine())
			return nil
		},
	}
}
