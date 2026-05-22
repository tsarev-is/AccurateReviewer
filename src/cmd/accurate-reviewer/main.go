package main

import (
	"fmt"
	"os"

	"github.com/scaratec/accurate-reviewer/internal/cli"
)

func main() {
	if err := cli.NewRoot().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		var exit *cli.ExitError
		if cli.AsExitError(err, &exit) {
			os.Exit(exit.Code)
		}
		os.Exit(1)
	}
}
