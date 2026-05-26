@gitlab
Feature: post-comments — GitLab platform via the `glab` CLI
  The `post-comments` subcommand fans out to one of three platform
  CLIs by --platform. On GitLab, it shells out to `glab` and posts
  inline notes via the discussions API. Auth / proxy / base URL stay
  where glab already manages them.

  Background:
    Given the accurate-reviewer binary is on PATH
    And the mock LLM is reset

  Scenario: post-comments --platform gitlab refuses to run when `glab` is missing
    Given the glab CLI is removed from PATH
    When I run "accurate-reviewer post-comments --report findings.json --pr 42 --platform gitlab"
    Then the exit code is 2
    And stderr contains "'glab' CLI not found on PATH"

  Scenario: post-comments --platform gitlab invokes `glab api` once per finding
    Given the task-fetch CLI "glab" is scripted to print
      """
      abc123def456
      """
    And a JSON report at "findings.json" with one critical finding on "db.go:12"
    When I run "accurate-reviewer post-comments --report findings.json --pr 7 --commit-sha deadbeef --repo octo/repo --platform gitlab"
    Then the exit code is 0
    And the output contains "posted 1 new comment"
    And the task-fetch CLI "glab" was invoked with the id "projects/octo%2Frepo/merge_requests/7/discussions"
    And the task-fetch CLI "glab" was invoked with the id "position[new_path]=db.go"

  Scenario: A second post-comments run on GitLab skips findings already posted
    Given the task-fetch CLI "glab" is scripted to print
      """
      abc123def456
      """
    And a JSON report at "findings.json" with one critical finding on "db.go:12"
    And I run "accurate-reviewer post-comments --report findings.json --pr 7 --commit-sha deadbeef --repo octo/repo --platform gitlab"
    When I run "accurate-reviewer post-comments --report findings.json --pr 7 --commit-sha deadbeef --repo octo/repo --platform gitlab"
    Then the exit code is 0
    And the output contains "posted 0 new comment"
    And the output contains "skipped 1 already-posted"

  Scenario: GitLab and GitHub dedupe caches are platform-isolated
    # The same finding posted to PR #7 on GitHub MUST be postable to
    # MR #7 on GitLab — the dedupe key includes the platform so the two
    # are independent.
    Given the task-fetch CLI "gh" is scripted to print
      """
      abc123def456
      """
    And the task-fetch CLI "glab" is scripted to print
      """
      abc123def456
      """
    And a JSON report at "findings.json" with one critical finding on "db.go:12"
    And I run "accurate-reviewer post-comments --report findings.json --pr 7 --commit-sha deadbeef --repo octo/repo --platform github"
    When I run "accurate-reviewer post-comments --report findings.json --pr 7 --commit-sha deadbeef --repo octo/repo --platform gitlab"
    Then the exit code is 0
    And the output contains "posted 1 new comment"
