@action
Feature: GitHub Action — post review findings as inline PR comments via `gh`
  The action.yml is a thin composite wrapper; the real work happens in the
  `post-comments` subcommand. It reads a JSON report produced by `review
  --output X.json`, then shells out to the locally installed `gh` CLI to
  publish each finding as an inline pull-request comment. Re-runs against
  the same findings dedupe so force-push does not spam the PR.

  Background:
    Given the accurate-reviewer binary is on PATH
    And a .review.yml with llm.provider set to "mock"
    And the mock LLM is reset

  Scenario: post-comments refuses to run when `gh` is missing from PATH
    Given the gh CLI is removed from PATH
    When I run "accurate-reviewer post-comments --report findings.json --pr 42"
    Then the exit code is 2
    And stderr contains "'gh' CLI not found on PATH"

  Scenario: review --output produces a JSON report that post-comments understands
    Given the mock LLM is scripted with:
      | worker   | findings_json |
      | security | [{"file":"db.go","line":12,"severity":"critical","cwe":"CWE-89","title":"SQL injection","why":"concatenated input"}] |
      | logic    | [] |
    And a diff that adds a file "db.go" with content:
      """
      package db
      func Find(id string) string { return "SELECT * FROM u WHERE id='" + id + "'" }
      """
    When I run "accurate-reviewer review --diff - --output findings.json" with that diff on stdin
    Then a file "findings.json" exists in the working directory
    And the JSON file "findings.json" contains
      | path                       | value     |
      | schema_version             | 1         |
      | blocking_severity          | critical  |
      | findings.[0].file          | db.go     |
      | findings.[0].line          | 12        |
      | findings.[0].severity      | critical  |

  Scenario: post-comments invokes `gh api` once per finding and records the dedupe cache
    Given the task-fetch CLI "gh" is scripted to print
      """
      abc123def456
      """
    And a JSON report at "findings.json" with one critical finding on "db.go:12"
    When I run "accurate-reviewer post-comments --report findings.json --pr 7 --commit-sha deadbeef --repo octo/repo"
    Then the exit code is 0
    And the output contains "posted 1 new comment"
    And the task-fetch CLI "gh" was invoked with the id "repos/octo/repo/pulls/7/comments"
    And the task-fetch CLI "gh" was invoked with the id "path=db.go"
    And a file ".review-cache/posted-comments.json" exists in the working directory

  Scenario: A second post-comments run skips findings already in the dedupe cache
    Given the task-fetch CLI "gh" is scripted to print
      """
      abc123def456
      """
    And a JSON report at "findings.json" with one critical finding on "db.go:12"
    And I run "accurate-reviewer post-comments --report findings.json --pr 7 --commit-sha deadbeef --repo octo/repo"
    When I run "accurate-reviewer post-comments --report findings.json --pr 7 --commit-sha deadbeef --repo octo/repo"
    Then the exit code is 0
    And the output contains "posted 0 new comment"
    And the output contains "skipped 1 already-posted"

  Scenario: --min-severity filters out low-severity findings
    Given the task-fetch CLI "gh" is scripted to print
      """
      abc123def456
      """
    And a JSON report at "findings.json" with one low finding on "x.go:3" and one critical finding on "y.go:5"
    When I run "accurate-reviewer post-comments --report findings.json --pr 7 --commit-sha deadbeef --repo octo/repo --min-severity high"
    Then the exit code is 0
    And the output contains "posted 1 new comment"

  Scenario: --dry-run logs the plan without invoking gh
    Given the task-fetch CLI "gh" is scripted to print
      """
      abc123def456
      """
    And a JSON report at "findings.json" with one critical finding on "db.go:12"
    When I run "accurate-reviewer post-comments --report findings.json --pr 7 --commit-sha deadbeef --repo octo/repo --dry-run"
    Then the exit code is 0
    And stderr contains "dry-run: would post on db.go:12"
    And no file ".review-cache/posted-comments.json" exists in the working directory

  Scenario: The shipped action.yml exists and wires both review and post-comments
    Then the file "action.yml" exists at the repo root
    And the repo-root file "action.yml" contains "review"
    And the repo-root file "action.yml" contains "post-comments"
    And the repo-root file "action.yml" contains "min-severity"
