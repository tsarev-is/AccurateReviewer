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
  provider: mock
  master:
    model: claude-opus-4-7
    max_output_tokens: 4096
  worker:
    model: claude-sonnet-4-6
    max_output_tokens: 2048
  api_key_env: ANTHROPIC_API_KEY

secrets:
  enabled: true
  entropy_threshold: 4.5

sanitizer:
  enabled: true
  delimiter: "===CODE-UNDER-REVIEW==="
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
