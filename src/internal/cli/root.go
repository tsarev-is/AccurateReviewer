package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"
)

const Version = "0.1.0"

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
	root.SetVersionTemplate("accurate-reviewer {{.Version}}\n")

	root.AddCommand(
		newInitCmd(),
		newAnalyzeCmd(),
		newReviewCmd(),
		newScanSecretsCmd(),
		newParseDiffCmd(),
		newSanitizeCmd(),
		newConfigCmd(),
	)
	return root
}
