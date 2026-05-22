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
	Version   int         `yaml:"version" json:"version"`
	Checks    Checks      `yaml:"checks" json:"checks"`
	Severity  Severity    `yaml:"severity" json:"severity"`
	Exclude   []string    `yaml:"exclude" json:"exclude"`
	Budget    Budget      `yaml:"budget" json:"budget"`
	LLM       LLM         `yaml:"llm" json:"llm"`
	Secrets   Secrets     `yaml:"secrets" json:"secrets"`
	Sanitizer SanitizerCfg `yaml:"sanitizer" json:"sanitizer"`

	Warnings []string `yaml:"-" json:"-"`
}

type Checks struct {
	Security     bool `yaml:"security" json:"security"`
	Logic        bool `yaml:"logic" json:"logic"`
	Architecture bool `yaml:"architecture" json:"architecture"`
}

type Severity struct {
	Blocking       string `yaml:"blocking" json:"blocking"`
	ReportMinimum  string `yaml:"report_minimum" json:"report_minimum"`
}

type Budget struct {
	MaxTokens int     `yaml:"max_tokens" json:"max_tokens"`
	MaxUSD    float64 `yaml:"max_usd" json:"max_usd"`
}

type LLM struct {
	Provider   string    `yaml:"provider" json:"provider"`
	Master     ModelSpec `yaml:"master" json:"master"`
	Worker     ModelSpec `yaml:"worker" json:"worker"`
	APIKeyEnv  string    `yaml:"api_key_env" json:"api_key_env"`
}

type ModelSpec struct {
	Model           string `yaml:"model" json:"model"`
	MaxOutputTokens int    `yaml:"max_output_tokens" json:"max_output_tokens"`
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
	return &cfg, nil
}
