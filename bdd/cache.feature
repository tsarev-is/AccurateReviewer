@cache
Feature: Findings cache by hash
  The master stores per-(unit, worker) findings under .review-cache/findings/.
  An unchanged hunk on a follow-up run is replayed from disk without spending
  another LLM round-trip. Any change to the file path, hunk content, worker
  prompt, or tool version invalidates automatically — there is no TTL.

  Background:
    Given the accurate-reviewer binary is on PATH
    And a .review.yml with llm.provider set to "mock"
    And the mock LLM is reset

  Scenario: A second review of an unchanged diff is served entirely from cache
    Given the mock LLM is scripted with:
      | worker   | findings_json |
      | security | [{"file":"db.go","line":12,"severity":"critical","cwe":"CWE-89","title":"SQL injection","why":"concatenated input"}] |
      | logic    | [] |
    And a diff that adds a file "db.go" with content:
      """
      package db
      func Find(id string) string {
          return "SELECT * FROM users WHERE id = '" + id + "'"
      }
      """
    When I run "accurate-reviewer review --diff -" with that diff on stdin
    And I record the prompt-log size
    And I run "accurate-reviewer review --diff -" with that diff on stdin
    Then the exit code is 1
    And the output contains "SQL injection"
    And the prompt-log has not grown since the recorded size
    And stderr contains "cache hit"

  Scenario: --no-cache forces a re-run even when an entry exists
    Given the mock LLM is scripted with:
      | worker   | findings |
      | security | []       |
      | logic    | []       |
    And a diff that adds a file "x.go" with content:
      """
      package main
      """
    When I run "accurate-reviewer review --diff -" with that diff on stdin
    And I record the prompt-log size
    And I run "accurate-reviewer review --diff - --no-cache" with that diff on stdin
    Then the exit code is 0
    And the prompt-log has grown by at least 2 entries

  Scenario: A changed hunk invalidates the stored entry and the workers run again
    Given the mock LLM is scripted with:
      | worker   | findings |
      | security | []       |
      | logic    | []       |
    And a diff that adds a file "y.go" with content:
      """
      package y
      func F() {}
      """
    When I run "accurate-reviewer review --diff -" with that diff on stdin
    And I record the prompt-log size
    And a diff that adds a file "y.go" with content:
      """
      package y
      func F() int { return 1 }
      """
    And I run "accurate-reviewer review --diff -" with that diff on stdin
    Then the exit code is 0
    And the prompt-log has grown by at least 2 entries

  Scenario: cache.enabled=false in the config disables the cache wholesale
    Given a .review.yml that disables the findings cache
    And the mock LLM is scripted with:
      | worker   | findings |
      | security | []       |
      | logic    | []       |
    And a diff that adds a file "z.go" with content:
      """
      package z
      """
    When I run "accurate-reviewer review --diff -" with that diff on stdin
    And I record the prompt-log size
    And I run "accurate-reviewer review --diff -" with that diff on stdin
    Then the exit code is 0
    And the prompt-log has grown by at least 2 entries
    And stderr does NOT contain "cache hit"
