@progress
Feature: Progress output on stderr
  As an operator running AccurateReviewer locally or in CI
  I need to see what the tool is doing while it works
  So that a slow LLM call never looks like a hang

  Why stderr, not stdout: stdout is reserved for the structured report so
  downstream tools (jq, CI annotators, the future HTML viewer) can parse
  it without first stripping progress noise. Every command therefore
  writes its `[<command>] <message>` log lines to stderr only.

  Background:
    Given the accurate-reviewer binary is on PATH
    And the mock LLM is reset

  Scenario: review logs every pipeline stage to stderr
    # The point isn't that any *one* line is essential — it's that the
    # operator sees the work progressing past each long-running stage:
    # config load, secrets pre-flight, diff parse, master dispatch, and
    # the per-worker round-trip.
    Given a .review.yml with llm.provider set to "mock"
    And the mock LLM is scripted with:
      | worker   | findings_json |
      | security | []            |
      | logic    | []            |
    And a diff that adds a file "p.go" with content:
      """
      package p
      func F() {}
      """
    When I run "accurate-reviewer review --diff -" with that diff on stdin
    Then the exit code is 0
    And stderr contains "[review] loading diff"
    And stderr contains "[review] config loaded"
    And stderr contains "[review] pre-flight secrets scan"
    And stderr contains "[review] parsed 1 review unit"
    And stderr contains "-> security on p.go"
    And stderr contains "-> logic on p.go"
    And stderr contains "<- security on p.go done"
    And stderr contains "[review] done:"

  Scenario: review progress goes to stderr, not stdout
    # The structured report on stdout must not be contaminated by the
    # progress lines — `accurate-reviewer review … | jq` must keep working.
    Given a .review.yml with llm.provider set to "mock"
    And the mock LLM is scripted with:
      | worker   | findings_json |
      | security | []            |
      | logic    | []            |
    And a diff that adds a file "q.go" with content:
      """
      package q
      """
    When I run "accurate-reviewer review --diff -" with that diff on stdin
    Then the exit code is 0
    And the output does NOT contain "[review]"
    And stderr contains "[review]"

  Scenario: review logs worker failures with elapsed time
    # When a worker errors out the progress line is also the first place
    # the operator sees the failure — it should carry the worker name,
    # the unit, and how long the call took before failing.
    Given a .review.yml with llm.provider set to "mock"
    And the mock LLM is scripted with:
      | worker   | behaviour | payload   |
      | security | error     | boom      |
      | logic    | findings  | []        |
    And a diff that adds a file "r.go" with content:
      """
      package r
      """
    When I run "accurate-reviewer review --diff -" with that diff on stdin
    Then the exit code is 2
    And stderr contains "!! security on r.go failed"
    And stderr contains "boom"

  Scenario: review surfaces budget overrun in progress, before exit
    Given a file ".review.yml" with content:
      """
      version: 1
      checks: { security: true, logic: false }
      budget: { max_tokens: 50 }
      llm: { provider: mock }
      """
    And the mock LLM is scripted with:
      | worker   | findings_json | tokens |
      | security | []            | 200    |
    And a diff that adds files "s.go" and "t.go" with content "package x"
    When I run "accurate-reviewer review --diff -" with that diff on stdin
    Then the exit code is 2
    And stderr contains "[review] budget exceeded"

  Scenario: analyze logs each stage of the project scan
    Given a sample Go project at "testdata/repos/sample-go"
    When I run "accurate-reviewer analyze" inside "testdata/repos/sample-go"
    Then the exit code is 0
    And stderr contains "[analyze] scanning"
    And stderr contains "[analyze] detected language=go"
    And stderr contains "[analyze] snapshot saved to .review-cache/project.json"
    And the output does NOT contain "[analyze]"

  Scenario: analyze logs the fast path when the snapshot is already current
    # A second run with no fingerprint change must still tell the user
    # *why* nothing happened — silence here is indistinguishable from a
    # hang on large projects.
    Given a sample Go project at "testdata/repos/sample-go" with an existing snapshot
    When I run "accurate-reviewer analyze" inside the sample project
    Then the exit code is 0
    And stderr contains "[analyze] fingerprint unchanged"
    And the output contains "snapshot up to date"

  Scenario: scan-secrets logs per-file progress
    Given a file "creds.txt" with content:
      """
      AKIAIOSFODNN7EXAMPLE
      """
    When I run "accurate-reviewer scan-secrets creds.txt"
    Then the exit code is 1
    And stderr contains "[scan-secrets] scanning 1 file"
    And stderr contains "[scan-secrets] (1/1) creds.txt"
    And stderr contains "[scan-secrets] done: 1 finding"
    And the output does NOT contain "[scan-secrets]"
