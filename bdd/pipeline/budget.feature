@budget
Feature: Token-budget fallback to a cheaper worker model
  As the AccurateReviewer pipeline
  When token consumption crosses budget.fallback_at * max_tokens
  I switch every subsequent worker call to llm.fallback.model
  And only hard-stop the run if I exceed max_tokens AFTER switching
  Because a graceful degrade preserves partial coverage on big PRs
  instead of cutting the review off at half-done.

  The switch is sticky for the rest of the run, and the cache key
  includes the model so fallback-quality findings never replay against
  a budget-healthy run.

  Background:
    Given the accurate-reviewer binary is on PATH
    And the mock LLM is reset

  Scenario: Fallback engages mid-run and later workers see the fallback model
    # Unit 1 (security+logic) burns 90 tokens, crossing the
    # 0.4 * 200 = 80-token fallback threshold. The master flips to the
    # cheap model for unit 2. Headroom of 200 - 90 = 110 tokens covers
    # unit 2's second batch (another 90) without triggering the hard
    # stop. The fake CLI records --model so we can verify the switch.
    Given a file ".review.yml" with content:
      """
      version: 1
      checks: { security: true, logic: true }
      budget: { max_tokens: 200, fallback_at: 0.4 }
      llm:
        provider: mock
        worker: { model: expensive-model }
        fallback: { model: cheap-model }
        cli: { model_flag: "--model" }
      """
    And the mock LLM is scripted with:
      | worker   | findings_json | tokens |
      | security | []            | 45     |
      | logic    | []            | 45     |
    And a diff that adds files "a.go" and "b.go" with content "package x"
    When I run "accurate-reviewer review --diff -" with that diff on stdin
    Then the exit code is 0
    And the first two LLM calls used model "expensive-model"
    And later LLM calls used model "cheap-model"

  Scenario: No fallback configured — hard stop at max_tokens (legacy behaviour)
    Given a file ".review.yml" with content:
      """
      version: 1
      checks: { security: true, logic: true }
      budget: { max_tokens: 50 }
      llm: { provider: mock }
      """
    And the mock LLM is scripted to always report 200 tokens used per call
    And a diff that adds files "a.go" and "b.go" with content "package main"
    When I run "accurate-reviewer review --diff -" with that diff on stdin
    Then the exit code is 2
    And the output contains "budget exceeded"

  Scenario: Fallback configured but cannot stay within budget — hard stop after the switch
    # First unit blows past the fallback threshold (200 > 0.8*100=80) so the
    # switch happens; second unit also overshoots and we are already on the
    # fallback, so the only safety left is BudgetExceeded.
    Given a file ".review.yml" with content:
      """
      version: 1
      checks: { security: true, logic: true }
      budget: { max_tokens: 100, fallback_at: 0.8 }
      llm:
        provider: mock
        worker: { model: expensive-model }
        fallback: { model: cheap-model }
        cli: { model_flag: "--model" }
      """
    And the mock LLM is scripted to always report 200 tokens used per call
    And a diff that adds files "a.go" and "b.go" with content "package x"
    When I run "accurate-reviewer review --diff -" with that diff on stdin
    Then the exit code is 2
    And the output contains "budget exceeded"
