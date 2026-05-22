package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/scaratec/accurate-reviewer/internal/config"
)

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "config", Short: "Inspect the resolved .review.yml"}
	cmd.AddCommand(&cobra.Command{
		Use:   "show",
		Short: "Print the resolved config to stdout (api keys redacted)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load(".review.yml")
			if err != nil {
				return Exit(2, "%v", err)
			}
			for _, w := range cfg.Warnings {
				fmt.Fprintln(cmd.ErrOrStderr(), w)
			}
			redacted := redactedView(cfg)
			out, err := yaml.Marshal(redacted)
			if err != nil {
				return Exit(1, "marshal: %v", err)
			}
			_, _ = cmd.OutOrStdout().Write(out)
			return nil
		},
	})
	return cmd
}

// redactedView returns the config in a form safe to print: api_key_env stays,
// but a placeholder api_key field is added so users see "<redacted>" rather
// than the env value.
type redacted struct {
	Version   int                  `yaml:"version"`
	Checks    config.Checks        `yaml:"checks"`
	Severity  config.Severity      `yaml:"severity"`
	Exclude   []string             `yaml:"exclude"`
	Budget    config.Budget        `yaml:"budget"`
	LLM       redactedLLM          `yaml:"llm"`
	Secrets   config.Secrets       `yaml:"secrets"`
	Sanitizer config.SanitizerCfg  `yaml:"sanitizer"`
}

type redactedLLM struct {
	Provider  string           `yaml:"provider"`
	Master    config.ModelSpec `yaml:"master"`
	Worker    config.ModelSpec `yaml:"worker"`
	APIKeyEnv string           `yaml:"api_key_env"`
	APIKey    string           `yaml:"api_key"`
}

func redactedView(c *config.Config) redacted {
	return redacted{
		Version:  c.Version,
		Checks:   c.Checks,
		Severity: c.Severity,
		Exclude:  c.Exclude,
		Budget:   c.Budget,
		LLM: redactedLLM{
			Provider:  c.LLM.Provider,
			Master:    c.LLM.Master,
			Worker:    c.LLM.Worker,
			APIKeyEnv: c.LLM.APIKeyEnv,
			APIKey:    "REDACTED",
		},
		Secrets:   c.Secrets,
		Sanitizer: c.Sanitizer,
	}
}
