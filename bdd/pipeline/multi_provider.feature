@multi-provider
Feature: Per-worker LLM provider overrides
  As an operator running AccurateReviewer
  I want different workers to talk to different LLM providers in the
  same review run — for example, security via Claude (deeper security
  reasoning) and logic via Codex (cheaper, fast logic sweep) — because
  the strengths of each model don't overlap and forcing one provider on
  every worker either overpays or underdelivers.

  Configuration lives under `llm.workers.<name>.{provider, model}`.
  Empty values inherit the top-level `llm.provider` and `llm.worker.model`.
  Per-worker overrides use the BUILT-IN CLI defaults for their provider
  (`claude -p --model …`, `codex exec`, `ar-mock-cli`) — operator
  `llm.cli.*` overrides apply ONLY to the top-level provider.

  When the budget threshold flips into fallback, the per-worker overrides
  are intentionally abandoned: every subsequent call resolves through
  `llm.fallback` for a uniform cheap path.

  Background:
    Given the accurate-reviewer binary is on PATH
    And the mock LLM is reset

  Scenario: Security via "claude", logic via "codex" — each worker spawns its own CLI
    Given a file ".review.yml" with content:
      """
      version: 1
      checks: { security: true, logic: true }
      severity: { blocking: critical }
      llm:
        provider: claude
        workers:
          security: { provider: claude }
          logic:    { provider: codex }
      """
    And the mock LLM is configured to return no findings
    And a diff that adds a file "hello.go" with content:
      """
      package main
      """
    When I run "accurate-reviewer review --diff -" with that diff on stdin
    Then the exit code is 0
    And every "security" worker call used argv0 "claude"
    And every "logic" worker call used argv0 "codex"

  Scenario: A per-worker override falls back to the top-level provider when empty
    Given a file ".review.yml" with content:
      """
      version: 1
      checks: { security: true, logic: true }
      severity: { blocking: critical }
      llm:
        provider: claude
        workers:
          logic: { provider: codex }
      """
    And the mock LLM is configured to return no findings
    And a diff that adds a file "hello.go" with content:
      """
      package main
      """
    When I run "accurate-reviewer review --diff -" with that diff on stdin
    Then the exit code is 0
    And every "security" worker call used argv0 "claude"
    And every "logic" worker call used argv0 "codex"

  Scenario: An unknown provider in a worker override is rejected at config-load time
    Given a file ".review.yml" with content:
      """
      version: 1
      checks: { security: true, logic: true }
      llm:
        provider: claude
        workers:
          security: { provider: bogus }
      """
    And a diff that adds a file "hello.go" with content:
      """
      package main
      """
    When I run "accurate-reviewer review --diff -" with that diff on stdin
    Then the exit code is 2
    And stderr contains "llm.workers.security.provider"

  Scenario: Fallback provider override swaps the whole cheap path on budget-trip
    # Threshold = 0.4 * 200 = 80 tokens. First unit (security+logic at
    # 45 each) crosses 80 and flips the master to fallback. The fallback
    # provider override sends every subsequent call to "codex" regardless
    # of the per-worker (claude) overrides.
    Given a file ".review.yml" with content:
      """
      version: 1
      checks: { security: true, logic: true }
      budget: { max_tokens: 200, fallback_at: 0.4 }
      llm:
        provider: claude
        workers:
          security: { provider: claude }
          logic:    { provider: claude }
        fallback: { provider: codex, model: cheap }
      """
    And the mock LLM is scripted with:
      | worker   | findings_json | tokens |
      | security | []            | 45     |
      | logic    | []            | 45     |
    And a diff that adds files "a.go" and "b.go" with content "package x"
    When I run "accurate-reviewer review --diff -" with that diff on stdin
    Then the exit code is 0
    And the first two LLM calls used argv0 "claude"
    And later LLM calls used argv0 "codex"
