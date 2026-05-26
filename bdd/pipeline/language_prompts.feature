@language-prompts
Feature: Language-specific guidance in worker prompts
  As the AccurateReviewer pipeline
  Every worker prompt should include a short paragraph of language-specific
  guidance when a project snapshot identifies the primary language
  And the prompt should remain unchanged when there is no snapshot
  Because security/logic pitfalls differ sharply between Go, Python, JS,
  Rust, Java — generic prompts miss footguns that an LLM tuned to one
  language would catch.

  The guidance text is baked into the binary (not attacker-controlled),
  so it is NOT routed through the sanitizer. It is inserted before the
  JSON-schema instructions so the schema remains the last thing the
  model reads.

  Background:
    Given the accurate-reviewer binary is on PATH
    And the mock LLM is reset

  Scenario: Go snapshot triggers Go-specific guidance in the security prompt
    Given a sample Go project at "testdata/repos/go-app"
    And I have already run "accurate-reviewer analyze" inside it
    And a .review.yml inside "testdata/repos/go-app" with llm.provider set to "mock"
    And the mock LLM is configured to return no findings
    And a diff that adds a file "db.go" with content:
      """
      package db
      func Q(id string) string { return "SELECT * FROM u WHERE id='" + id + "'" }
      """
    When I run "accurate-reviewer review --diff -" with that diff on stdin inside "testdata/repos/go-app"
    Then the exit code is 0
    And every security worker prompt contains "Language-specific guidance:"
    And every security worker prompt contains "database/sql"

  Scenario: C# snapshot triggers C#/.NET-specific guidance
    Given a sample C# project at "testdata/repos/csharp-app"
    And I have already run "accurate-reviewer analyze" inside it
    And a .review.yml inside "testdata/repos/csharp-app" with llm.provider set to "mock"
    And the mock LLM is configured to return no findings
    And a diff that adds a file "Db.cs" with content:
      """
      using System.Data.SqlClient;
      public class Db {
        public string Q(string id) => "SELECT * FROM u WHERE id='" + id + "'";
      }
      """
    When I run "accurate-reviewer review --diff -" with that diff on stdin inside "testdata/repos/csharp-app"
    Then the exit code is 0
    And every security worker prompt contains "Language-specific guidance:"
    And every security worker prompt contains "SqlParameter"
    And every logic worker prompt contains "ConfigureAwait"

  Scenario: Python snapshot triggers Python-specific guidance
    Given a sample Python project at "testdata/repos/py-app"
    And I have already run "accurate-reviewer analyze" inside it
    And a .review.yml inside "testdata/repos/py-app" with llm.provider set to "mock"
    And the mock LLM is configured to return no findings
    And a diff that adds a file "loader.py" with content:
      """
      import pickle
      def load(b): return pickle.loads(b)
      """
    When I run "accurate-reviewer review --diff -" with that diff on stdin inside "testdata/repos/py-app"
    Then the exit code is 0
    And every security worker prompt contains "pickle"

  Scenario: JavaScript snapshot triggers JavaScript-specific guidance
    Given a sample JavaScript project at "testdata/repos/js-app"
    And I have already run "accurate-reviewer analyze" inside it
    And a .review.yml inside "testdata/repos/js-app" with llm.provider set to "mock"
    And the mock LLM is configured to return no findings
    And a diff that adds a file "server.js" with content:
      """
      const cp = require("child_process");
      function run(name) { return cp.exec("ls " + name); }
      """
    When I run "accurate-reviewer review --diff -" with that diff on stdin inside "testdata/repos/js-app"
    Then the exit code is 0
    And every security worker prompt contains "prototype pollution"
    And every logic worker prompt contains "missing await"

  Scenario: TypeScript snapshot triggers TypeScript-specific guidance
    Given a sample TypeScript project at "testdata/repos/ts-app"
    And I have already run "accurate-reviewer analyze" inside it
    And a .review.yml inside "testdata/repos/ts-app" with llm.provider set to "mock"
    And the mock LLM is configured to return no findings
    And a diff that adds a file "server.ts" with content:
      """
      function widen(x: unknown): string { return x as any; }
      """
    When I run "accurate-reviewer review --diff -" with that diff on stdin inside "testdata/repos/ts-app"
    Then the exit code is 0
    And every security worker prompt contains "as any"
    And every logic worker prompt contains "non-null assertions"

  Scenario: Rust snapshot triggers Rust-specific guidance
    Given a sample Rust project at "testdata/repos/rs-app"
    And I have already run "accurate-reviewer analyze" inside it
    And a .review.yml inside "testdata/repos/rs-app" with llm.provider set to "mock"
    And the mock LLM is configured to return no findings
    And a diff that adds a file "lib.rs" with content:
      """
      pub fn parse(s: &str) -> i32 { s.parse().unwrap() }
      """
    When I run "accurate-reviewer review --diff -" with that diff on stdin inside "testdata/repos/rs-app"
    Then the exit code is 0
    And every security worker prompt contains "unsafe blocks"
    And every logic worker prompt contains "unwrap/expect"

  Scenario: Java snapshot triggers Java-specific guidance
    Given a sample Java project at "testdata/repos/java-app"
    And I have already run "accurate-reviewer analyze" inside it
    And a .review.yml inside "testdata/repos/java-app" with llm.provider set to "mock"
    And the mock LLM is configured to return no findings
    And a diff that adds a file "Db.java" with content:
      """
      class Db {
        String q(String id) { return "SELECT * FROM u WHERE id='" + id + "'"; }
      }
      """
    When I run "accurate-reviewer review --diff -" with that diff on stdin inside "testdata/repos/java-app"
    Then the exit code is 0
    And every security worker prompt contains "PreparedStatement"
    And every logic worker prompt contains "NullPointerException"

  Scenario: Without a project snapshot, no language-specific guidance is injected
    # Reviewing without prior `analyze` leaves Master.Snapshot nil; the master
    # then passes language="" and the worker prompt is unchanged from
    # pre-v1.0 shape. This protects users who haven't onboarded yet.
    Given a .review.yml with llm.provider set to "mock"
    And the mock LLM is configured to return no findings
    And a diff that adds a file "hello.go" with content:
      """
      package main
      func main() {}
      """
    When I run "accurate-reviewer review --diff -" with that diff on stdin
    Then the exit code is 0
    And no worker prompt contains "Language-specific guidance:"

  Scenario: checks.language_specific_prompts=false disables hints even with a snapshot
    Given a sample Go project at "testdata/repos/go-app2"
    And I have already run "accurate-reviewer analyze" inside it
    And a .review.yml that disables language-specific prompts inside "testdata/repos/go-app2"
    And the mock LLM is configured to return no findings
    And a diff that adds a file "hello.go" with content:
      """
      package main
      func main() {}
      """
    When I run "accurate-reviewer review --diff -" with that diff on stdin inside "testdata/repos/go-app2"
    Then the exit code is 0
    And no worker prompt contains "Language-specific guidance:"
