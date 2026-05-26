@bitbucket
Feature: post-comments — Bitbucket platform via the `bb` CLI
  The `post-comments` subcommand can target Bitbucket Cloud through the
  `bb` CLI. Auth / proxy / base URL stay where bb already manages them.

  Background:
    Given the accurate-reviewer binary is on PATH
    And the mock LLM is reset

  Scenario: post-comments --platform bitbucket refuses to run when `bb` is missing
    Given the bb CLI is removed from PATH
    When I run "accurate-reviewer post-comments --report findings.json --pr 42 --platform bitbucket"
    Then the exit code is 2
    And stderr contains "'bb' CLI not found on PATH"

  Scenario: post-comments --platform bitbucket invokes `bb pr comment` once per finding
    Given the task-fetch CLI "bb" is scripted to print
      """
      abc123def456
      """
    And a JSON report at "findings.json" with one critical finding on "db.go:12"
    When I run "accurate-reviewer post-comments --report findings.json --pr 7 --commit-sha deadbeef --repo octo/repo --platform bitbucket"
    Then the exit code is 0
    And the output contains "posted 1 new comment"
    And the task-fetch CLI "bb" was invoked with the id "db.go"
    And the task-fetch CLI "bb" was invoked with the id "comment"

  Scenario: Auto-detect picks bitbucket from a bitbucket.org git remote
    # No --platform flag; the dispatcher reads `git remote get-url origin`
    # and matches the host. Demonstrates that auto-detect works end-to-end
    # against a configured-but-not-real remote URL.
    Given a git repo with origin "https://bitbucket.org/octo/repo.git"
    And the task-fetch CLI "bb" is scripted to print
      """
      abc123def456
      """
    And a JSON report at "findings.json" with one critical finding on "db.go:12"
    When I run "accurate-reviewer post-comments --report findings.json --pr 7 --commit-sha deadbeef --repo octo/repo"
    Then the exit code is 0
    And the task-fetch CLI "bb" was invoked with the id "comment"
