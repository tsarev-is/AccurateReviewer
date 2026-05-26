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
  # When true (default) and a project snapshot exists, workers receive a
  # short language-specific guidance paragraph in their prompt.
  language_specific_prompts: true
  # Pre-flight CVE scan over dependency manifests via osv-scanner. Off by
  # default because osv-scanner is an extra install — enable once you
  # have it on PATH.
  vulnerabilities: false

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
  # Fraction of max_tokens at which the master switches subsequent worker
  # calls to llm.fallback.model (when configured). 0.8 by default.
  fallback_at: 0.8

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
  # Cheaper worker model the master switches to when the budget crosses
  # budget.fallback_at. Same provider as the worker. Leave model empty
  # to disable and keep the legacy "hard stop at MaxTokens" behaviour.
  fallback:
    model: claude-haiku-4-5-20251001
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

cache:
  # When enabled (default), per-(unit, worker) findings are stored under
  # .review-cache/findings/. Unchanged hunks are replayed from disk on the
  # next run with no LLM round-trip. Disable for one-off audits or when you
  # need to force a re-evaluation (the --no-cache flag does the same per-run).
  enabled: true

sanitizer:
  enabled: true
  delimiter: "===CODE-UNDER-REVIEW==="

# Dependency-vulnerability scanner config. Shells out to osv-scanner; no
# HTTP client is embedded. Leave bin empty to use the default "osv-scanner".
cve:
  bin: osv-scanner
  timeout_seconds: 60
  min_severity: medium

# How 'post-comments' reaches the forge. platform: github | gitlab |
# bitbucket. Auto-detected from the git remote when omitted. Per-platform
# bin overrides the CLI name (defaults: gh / glab / bb). The binary opens
# no HTTP connections — every request goes through the platform CLI.
comments:
  platform: github
  github:    { bin: gh }
  gitlab:    { bin: glab }
  bitbucket: { bin: bb }

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
  linear:
    cmd: []   # e.g. ["linear", "issue", "view", "{id}", "--format", "markdown"]
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
