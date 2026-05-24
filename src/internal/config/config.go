// Package config loads and validates .review.yml.
// The loaded representation never contains secrets — api keys are only
// resolved from env at call time, never stored on the Config struct.
package config

import (
	"fmt"
	"io"
	"os"

	"gopkg.in/yaml.v3"
)

const SupportedVersion = 1

type Config struct {
	Version      int          `yaml:"version" json:"version"`
	Checks       Checks       `yaml:"checks" json:"checks"`
	Severity     Severity     `yaml:"severity" json:"severity"`
	Exclude      []string     `yaml:"exclude" json:"exclude"`
	Budget       Budget       `yaml:"budget" json:"budget"`
	LLM          LLM          `yaml:"llm" json:"llm"`
	Secrets      Secrets      `yaml:"secrets" json:"secrets"`
	Sanitizer    SanitizerCfg `yaml:"sanitizer" json:"sanitizer"`
	Integrations Integrations `yaml:"integrations" json:"integrations"`
	Cache        Cache        `yaml:"cache" json:"cache"`

	Warnings []string `yaml:"-" json:"-"`
}

// Cache controls the per-(unit, worker) findings cache. The cache is on
// by default — disabling it forces every unit through the model on every
// run, which is what scenarios that script per-call mock responses need
// for repeatable assertions.
type Cache struct {
	Enabled *bool `yaml:"enabled" json:"enabled"`
}

// CacheEnabled returns the effective cache toggle: present-and-true,
// absent (defaults to true), or present-and-false. Wrapping the YAML field
// in *bool lets us distinguish "unset" from "explicit false" without
// breaking back-compat configs that never knew about this section.
func (c Cache) IsEnabled() bool {
	if c.Enabled == nil {
		return true
	}
	return *c.Enabled
}

// Integrations declares the subprocess commands that fetch task/issue
// context from external trackers. We stay consistent with the LLM
// access model: no HTTP client lives in this binary — fetching always
// shells out to a user-provided CLI (`gh`, `jira`, etc.) that already
// handles auth on the developer's machine. The `{id}` token in any arg
// is substituted with the issue id at call time.
type Integrations struct {
	GitHub IntegrationSpec `yaml:"github" json:"github"`
	Jira   IntegrationSpec `yaml:"jira" json:"jira"`
}

type IntegrationSpec struct {
	Cmd            []string `yaml:"cmd" json:"cmd"`
	TimeoutSeconds int      `yaml:"timeout_seconds" json:"timeout_seconds"`
}

type Checks struct {
	Security     bool `yaml:"security" json:"security"`
	Logic        bool `yaml:"logic" json:"logic"`
	Architecture bool `yaml:"architecture" json:"architecture"`
}

type Severity struct {
	Blocking      string `yaml:"blocking" json:"blocking"`
	ReportMinimum string `yaml:"report_minimum" json:"report_minimum"`
}

type Budget struct {
	MaxTokens int     `yaml:"max_tokens" json:"max_tokens"`
	MaxUSD    float64 `yaml:"max_usd" json:"max_usd"`
}

// LLM holds the chosen provider, the per-role model overrides, and the CLI
// invocation parameters that the exec-provider needs. We deliberately
// removed the in-process HTTP mock: in MVP the only way the tool talks to a
// model is by spawning a CLI subprocess (`claude`, `codex`, or a test fake).
type LLM struct {
	Provider  string    `yaml:"provider" json:"provider"`
	Master    ModelSpec `yaml:"master" json:"master"`
	Worker    ModelSpec `yaml:"worker" json:"worker"`
	APIKeyEnv string    `yaml:"api_key_env" json:"api_key_env"`
	CLI       CLISpec   `yaml:"cli" json:"cli"`
}

type ModelSpec struct {
	Model           string `yaml:"model" json:"model"`
	MaxOutputTokens int    `yaml:"max_output_tokens" json:"max_output_tokens"`
}

// CLISpec configures how the exec-provider spawns the LLM CLI. All fields
// are optional; defaults are filled in from the provider name in
// applyCLIDefaults. The defaults match the upstream CLI conventions:
//
//	claude  -> claude -p   (prompt comes on stdin)
//	codex   -> codex exec  (prompt comes on stdin)
//	mock    -> ar-mock-cli (a test fake the BDD harness puts on PATH)
type CLISpec struct {
	Bin            string   `yaml:"bin" json:"bin"`
	Args           []string `yaml:"args" json:"args"`
	ModelFlag      string   `yaml:"model_flag" json:"model_flag"`
	TimeoutSeconds int      `yaml:"timeout_seconds" json:"timeout_seconds"`
	PassEnv        []string `yaml:"pass_env" json:"pass_env"`
}

type Secrets struct {
	Enabled          bool    `yaml:"enabled" json:"enabled"`
	EntropyThreshold float64 `yaml:"entropy_threshold" json:"entropy_threshold"`
}

type SanitizerCfg struct {
	Enabled   bool   `yaml:"enabled" json:"enabled"`
	Delimiter string `yaml:"delimiter" json:"delimiter"`
}

var validSeverities = map[string]bool{
	"critical": true, "high": true, "medium": true, "low": true, "info": true,
}

var validProviders = map[string]bool{
	"claude": true, "codex": true, "mock": true,
}

// Load reads + validates a config file. Unknown top-level keys are recorded
// as warnings (not errors). Missing required sections are errors.
func Load(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return Parse(f)
}

func Parse(r io.Reader) (*Config, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}

	// First pass: capture unknown keys as warnings.
	var generic map[string]any
	if err := yaml.Unmarshal(raw, &generic); err != nil {
		return nil, fmt.Errorf("invalid YAML: %w", err)
	}
	known := map[string]bool{
		"version": true, "checks": true, "severity": true, "exclude": true,
		"budget": true, "llm": true, "secrets": true, "sanitizer": true,
		"integrations": true, "cache": true,
	}
	var warnings []string
	for k := range generic {
		if !known[k] {
			warnings = append(warnings, fmt.Sprintf("unknown key '%s' — ignored", k))
		}
	}

	var cfg Config
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("invalid YAML: %w", err)
	}
	cfg.Warnings = warnings

	if cfg.Version == 0 {
		return nil, fmt.Errorf("version: required")
	}
	if cfg.Version != SupportedVersion {
		return nil, fmt.Errorf("unsupported config version %d (this binary speaks v%d)", cfg.Version, SupportedVersion)
	}
	if cfg.LLM.Provider == "" {
		return nil, fmt.Errorf("llm: required")
	}
	if !validProviders[cfg.LLM.Provider] {
		return nil, fmt.Errorf("llm.provider: '%s' is not one of [claude, codex, mock]", cfg.LLM.Provider)
	}
	if cfg.Severity.Blocking != "" && !validSeverities[cfg.Severity.Blocking] {
		return nil, fmt.Errorf("severity.blocking: '%s' is not one of [critical, high, medium, low, info]", cfg.Severity.Blocking)
	}
	if cfg.Severity.ReportMinimum != "" && !validSeverities[cfg.Severity.ReportMinimum] {
		return nil, fmt.Errorf("severity.report_minimum: '%s' is not one of [critical, high, medium, low, info]", cfg.Severity.ReportMinimum)
	}

	// Defaults.
	if cfg.Sanitizer.Delimiter == "" {
		cfg.Sanitizer.Delimiter = "===CODE-UNDER-REVIEW==="
	}
	if cfg.Secrets.EntropyThreshold == 0 {
		cfg.Secrets.EntropyThreshold = 4.5
	}
	if cfg.Severity.Blocking == "" {
		cfg.Severity.Blocking = "critical"
	}
	applyCLIDefaults(&cfg.LLM)
	return &cfg, nil
}

// applyCLIDefaults fills in provider-specific CLI defaults so a minimal
// config like `llm: { provider: claude }` still produces a working
// invocation. Explicit values in `llm.cli.*` always win.
func applyCLIDefaults(l *LLM) {
	switch l.Provider {
	case "claude":
		if l.CLI.Bin == "" {
			l.CLI.Bin = "claude"
		}
		if len(l.CLI.Args) == 0 {
			l.CLI.Args = []string{"-p"}
		}
		if l.CLI.ModelFlag == "" {
			l.CLI.ModelFlag = "--model"
		}
	case "codex":
		if l.CLI.Bin == "" {
			l.CLI.Bin = "codex"
		}
		if len(l.CLI.Args) == 0 {
			l.CLI.Args = []string{"exec"}
		}
		// codex exec takes --model but we leave it unset by default so the
		// codex CLI picks up its own configured default.
	case "mock":
		if l.CLI.Bin == "" {
			l.CLI.Bin = "ar-mock-cli"
		}
		// No args / model flag by default — the fake is dumb on purpose.
		if l.CLI.TimeoutSeconds == 0 {
			l.CLI.TimeoutSeconds = 30
		}
	}
	if l.CLI.TimeoutSeconds == 0 {
		// 5 minutes covers a slow opus-class review prompt with margin. Real
		// `claude`/`codex` calls routinely take 60–120 s; 30 s — our previous
		// default — was killing real workers mid-answer.
		l.CLI.TimeoutSeconds = 300
	}
}
