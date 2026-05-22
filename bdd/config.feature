@config
Feature: .review.yml configuration parsing and validation
  As the AccurateReviewer pipeline
  I must load and validate .review.yml deterministically
  Because every other module's behaviour depends on the parsed config

  Background:
    Given the accurate-reviewer binary is on PATH

  Scenario: A well-formed config loads with no warnings
    Given a file ".review.yml" with content:
      """
      version: 1
      checks:
        security: true
        logic: true
      severity:
        blocking: critical
        report_minimum: low
      exclude:
        - "vendor/**"
      budget:
        max_tokens: 100000
        max_usd: 0.50
      llm:
        provider: mock
        master:
          model: claude-opus-4-7
        worker:
          model: claude-sonnet-4-6
      """
    When I run "accurate-reviewer config show"
    Then the exit code is 0
    And the output contains:
      | line                          |
      | provider: mock                |
      | blocking: critical            |
      | security: true                |

  Scenario: An unknown top-level key is reported as a warning but does not fail
    Given a file ".review.yml" with content:
      """
      version: 1
      checks: { security: true }
      llm: { provider: mock }
      mystery_key: 42
      """
    When I run "accurate-reviewer config show"
    Then the exit code is 0
    And stderr contains "unknown key 'mystery_key' — ignored"

  Scenario: An invalid severity level is rejected
    Given a file ".review.yml" with content:
      """
      version: 1
      checks: { security: true }
      severity: { blocking: extreme }
      llm: { provider: mock }
      """
    When I run "accurate-reviewer config show"
    Then the exit code is 2
    And stderr contains "severity.blocking: 'extreme' is not one of [critical, high, medium, low, info]"

  Scenario: Missing required fields are reported one at a time
    # We list every missing required field in one pass — partial errors lead
    # to whack-a-mole config editing.
    Given a file ".review.yml" with content:
      """
      version: 1
      """
    When I run "accurate-reviewer config show"
    Then the exit code is 2
    And stderr contains "llm: required"

  Scenario: A future schema version is rejected with a clear message
    Given a file ".review.yml" with content:
      """
      version: 999
      llm: { provider: mock }
      """
    When I run "accurate-reviewer config show"
    Then the exit code is 2
    And stderr contains "unsupported config version 999"

  Scenario: API keys are never written to the loaded config representation
    Given the environment variable "ANTHROPIC_API_KEY" is set to "sk-test-do-not-leak"
    And a file ".review.yml" with content:
      """
      version: 1
      llm:
        provider: claude
        api_key_env: ANTHROPIC_API_KEY
      """
    When I run "accurate-reviewer config show"
    Then the output does NOT contain "sk-test-do-not-leak"
    And the output contains "api_key: REDACTED"

  Scenario: An unknown provider is rejected
    Given a file ".review.yml" with content:
      """
      version: 1
      checks: { security: true }
      llm: { provider: openai }
      """
    When I run "accurate-reviewer config show"
    Then the exit code is 2
    And stderr contains "llm.provider: 'openai' is not one of [claude, codex, mock]"

  Scenario: CLI defaults are exposed by `config show`
    # `provider: claude` with no `cli:` block should resolve to the upstream
    # Claude Code CLI invocation. The resolved view must show those defaults
    # so users can spot when their override is wrong.
    Given a file ".review.yml" with content:
      """
      version: 1
      checks: { security: true }
      llm: { provider: claude }
      """
    When I run "accurate-reviewer config show"
    Then the exit code is 0
    And the output contains "provider: claude"
    And the output contains "bin: claude"
    And the output contains "- -p"

  Scenario: A custom CLI bin override is preserved
    Given a file ".review.yml" with content:
      """
      version: 1
      checks: { security: true }
      llm:
        provider: codex
        cli:
          bin: /opt/codex/bin/codex
          args: ["exec", "--quiet"]
          timeout_seconds: 120
      """
    When I run "accurate-reviewer config show"
    Then the exit code is 0
    And the output contains "bin: /opt/codex/bin/codex"
    And the output contains "timeout_seconds: 120"
