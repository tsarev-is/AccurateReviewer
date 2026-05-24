package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

const defaultConfig = `version: 1

checks:
  security: true
  logic: true
  architecture: false

severity:
  blocking: critical
  report_minimum: low

exclude:
  - "vendor/**"
  - "node_modules/**"
  - "**/*.generated.*"
  - "**/migrations/**"
  - ".review-cache/**"

budget:
  max_tokens: 200000
  max_usd: 1.00

llm:
  # provider: claude | codex | mock
  #   claude  -> spawns the Claude Code CLI (claude -p, prompt on stdin)
  #   codex   -> spawns the Codex CLI       (codex exec, prompt on stdin)
  #   mock    -> spawns ar-mock-cli (the BDD test fake; not for prod)
  provider: claude
  master:
    model: claude-opus-4-7
    max_output_tokens: 4096
  worker:
    model: claude-sonnet-4-6
    max_output_tokens: 2048
  # API key is read from env by the CLI itself (claude/codex handle auth);
  # named here only so it gets passed through to the subprocess.
  api_key_env: ANTHROPIC_API_KEY
  # Override the spawn parameters if your CLI is named differently, lives
  # outside PATH, or needs extra flags. Empty fields fall back to defaults
  # derived from the provider.
  cli:
    bin: ""
    args: []
    model_flag: ""
    timeout_seconds: 300
    pass_env: []

secrets:
  enabled: true
  entropy_threshold: 4.5

sanitizer:
  enabled: true
  delimiter: "===CODE-UNDER-REVIEW==="

# Optional task/issue trackers. When configured, ` + "`accurate-reviewer review`" + `
# can be invoked with --jira <ID> or --github <ID> to fetch the issue body
# via the named CLI and attach it as task context to every worker prompt.
# Both fetchers shell out to a user-provided CLI that already handles auth
# (gh, jira, etc.) — the binary never opens an HTTP connection itself.
# The literal "{id}" token in any cmd arg is substituted with the issue id.
integrations:
  github:
    cmd: []   # e.g. ["gh", "issue", "view", "{id}"]
    timeout_seconds: 30
  jira:
    cmd: []   # e.g. ["jira", "issue", "view", "{id}", "--plain"]
    timeout_seconds: 30
`

func newInitCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Write a starter .review.yml in the current directory",
		RunE: func(cmd *cobra.Command, _ []string) error {
			path := ".review.yml"
			if _, err := os.Stat(path); err == nil && !force {
				return Exit(2, ".review.yml already exists (use --force to overwrite)")
			}
			if err := os.WriteFile(path, []byte(defaultConfig), 0o644); err != nil {
				return Exit(1, "write %s: %v", path, err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "wrote %s\n", path)
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing .review.yml")
	return cmd
}
