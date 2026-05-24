@full
Feature: Full (megadiff) review mode
  The default review is incremental — only changed lines are inspected. The
  full mode treats every tracked source file as "all-added" and runs the
  same pipeline across the whole repo. It is informational only: critical
  findings do NOT block the exit code, because gating a developer's merge
  on the legacy state of a project they did not write is unfair.

  Background:
    Given the accurate-reviewer binary is on PATH
    And a .review.yml with llm.provider set to "mock"
    And the mock LLM is reset

  Scenario: --full walks the working directory and produces a unit per file
    Given the mock LLM is scripted with:
      | worker   | findings |
      | security | []       |
      | logic    | []       |
    And a file "a.go" with content
      """
      package a
      func A() {}
      """
    And a file "b.go" with content
      """
      package b
      func B() {}
      """
    When I run "accurate-reviewer review --full"
    Then the exit code is 0
    And stderr contains "full mode"
    And the output contains "Reviewed: a.go, b.go"
    And the output contains "0 findings"

  Scenario: --full is informational — even a critical finding does not block
    Given the mock LLM is scripted with:
      | worker   | findings_json |
      | security | [{"file":"legacy.go","line":2,"severity":"critical","cwe":"CWE-89","title":"SQL injection","why":"concatenated input"}] |
      | logic    | [] |
    And a file "legacy.go" with content
      """
      package legacy
      func F(id string) string { return "SELECT * FROM u WHERE id='" + id + "'" }
      """
    When I run "accurate-reviewer review --full"
    Then the exit code is 0
    And the output contains "SQL injection"
    And stderr contains "informational"

  Scenario: --full respects exclude patterns from the config
    Given a .review.yml that excludes "vendor/**"
    And the mock LLM is scripted with:
      | worker   | findings |
      | security | []       |
      | logic    | []       |
    And a file "main.go" with content
      """
      package main
      """
    And a file "vendor/lib/x.go" with content
      """
      package x
      """
    When I run "accurate-reviewer review --full"
    Then the exit code is 0
    And the output contains "Reviewed: main.go"
    And the output does NOT contain "vendor/lib/x.go"

  Scenario: --full conflicts with --diff
    Given the mock LLM is scripted with:
      | worker   | findings |
      | security | []       |
      | logic    | []       |
    When I run "accurate-reviewer review --full --diff somefile.diff"
    Then the exit code is 2
    And stderr contains "--full cannot be combined"

  Scenario: --full skips binary files and the cache directory itself
    Given the mock LLM is scripted with:
      | worker   | findings |
      | security | []       |
      | logic    | []       |
    And a file "src.go" with content
      """
      package src
      """
    And a binary file "img.png" exists in the working directory
    And a file ".review-cache/findings/abc.json" with content
      """
      {"key": "abc"}
      """
    When I run "accurate-reviewer review --full"
    Then the exit code is 0
    And the output contains "Reviewed: src.go"
    And the output does NOT contain "img.png"
    And the output does NOT contain ".review-cache"
