// Package task resolves the optional task/issue context that the review
// command attaches to every worker prompt.
//
// Four source kinds are supported, in order of how the CLI exposes them:
//
//	--task-file <path>  : read description from a local text file
//	--github <id>       : shell out to the configured `github` integration
//	--jira <id>         : shell out to the configured `jira` integration
//	--linear <id>       : shell out to the configured `linear` integration
//
// The "shell out" half is deliberate. This binary owns no HTTP client for
// any vendor — fetching uses whatever CLI the developer already has
// authenticated locally (gh, jira-cli, etc.). Configuration lives under
// `integrations.<kind>.cmd` in .review.yml; the literal token "{id}" in
// any arg is substituted with the issue id at call time.
package task

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/scaratec/accurate-reviewer/internal/config"
)

// Source records which CLI flag asked for the task context. It's an enum
// rather than a free-form string so the validation in `Options.Validate`
// can't drift from the loader's switch.
type Source int

const (
	SourceNone Source = iota
	SourceFile
	SourceGitHub
	SourceJira
	SourceLinear
)

// Options groups the flag values the review command collects. Exactly
// zero or one field may be non-empty; Validate enforces that.
type Options struct {
	File   string
	GitHub string
	Jira   string
	Linear string
}

// Validate returns the selected source, or an error if the caller passed
// more than one. No source at all is a valid choice (returns SourceNone),
// the review then runs with no task context — that's the historical
// behaviour and must keep working.
func (o Options) Validate() (Source, error) {
	chosen := []string{}
	src := SourceNone
	if o.File != "" {
		chosen = append(chosen, "--task-file")
		src = SourceFile
	}
	if o.GitHub != "" {
		chosen = append(chosen, "--github")
		src = SourceGitHub
	}
	if o.Jira != "" {
		chosen = append(chosen, "--jira")
		src = SourceJira
	}
	if o.Linear != "" {
		chosen = append(chosen, "--linear")
		src = SourceLinear
	}
	if len(chosen) > 1 {
		return SourceNone, fmt.Errorf("only one task source may be given at a time (got: %s)", strings.Join(chosen, ", "))
	}
	return src, nil
}

// Load returns the task description for the chosen source, or an empty
// string when no source was selected. Errors from the underlying CLI
// surface verbatim so the user can debug "auth failed" / "not found"
// without an extra layer of indirection.
func Load(ctx context.Context, opts Options, cfg *config.Config) (string, error) {
	src, err := opts.Validate()
	if err != nil {
		return "", err
	}
	switch src {
	case SourceNone:
		return "", nil
	case SourceFile:
		return loadFile(opts.File)
	case SourceGitHub:
		return runIntegration(ctx, "github", cfg.Integrations.GitHub, opts.GitHub)
	case SourceJira:
		return runIntegration(ctx, "jira", cfg.Integrations.Jira, opts.Jira)
	case SourceLinear:
		return runIntegration(ctx, "linear", cfg.Integrations.Linear, opts.Linear)
	default:
		return "", fmt.Errorf("unsupported task source")
	}
}

func loadFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("task file %q: %w", path, err)
	}
	return string(b), nil
}

func runIntegration(ctx context.Context, kind string, spec config.IntegrationSpec, id string) (string, error) {
	if len(spec.Cmd) == 0 {
		return "", fmt.Errorf("%s integration not configured (set integrations.%s.cmd in .review.yml)", kind, kind)
	}
	if id == "" {
		return "", fmt.Errorf("%s: empty issue id", kind)
	}

	args := make([]string, len(spec.Cmd))
	for i, a := range spec.Cmd {
		args[i] = strings.ReplaceAll(a, "{id}", id)
	}

	timeout := time.Duration(spec.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(cctx, args[0], args[1:]...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		// Prefer the child's stderr — it's the message the user can act on.
		// Fall back to err.Error() (exit status / exec lookup failure).
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return "", fmt.Errorf("%s fetch failed: %s", kind, msg)
		}
		return "", fmt.Errorf("%s fetch failed: %s", kind, msg)
	}
	return stdout.String(), nil
}
