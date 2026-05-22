@review
Feature: Master + worker review
  As the AccurateReviewer pipeline
  A single master agent coordinates per-check workers
  Each worker returns structured findings; the master deduplicates and reports

  This feature drives the MVP review path end-to-end against a mock LLM whose
  responses are scripted by the scenario. No real model is invoked. The mock
  is deliberately strict: any prompt it receives that does not match the
  scripted expectation is rejected, so we know our prompts are stable.

  Background:
    Given the accurate-reviewer binary is on PATH
    And a .review.yml with llm.provider set to "mock"
    And the mock LLM is reset

  Scenario: A diff with no issues produces an empty report and exit code 0
    Given the mock LLM is scripted with:
      | worker   | findings |
      | security | []       |
      | logic    | []       |
    And a diff that adds a file "hello.go" with content:
      """
      package main
      func Greet() string { return "hello" }
      """
    When I run "accurate-reviewer review --diff -" with that diff on stdin
    Then the exit code is 0
    And the output contains "0 findings"

  Scenario: A security finding from the security worker is reported with severity
    Given the mock LLM is scripted with:
      | worker   | findings_json |
      | security | [{"file":"db.go","line":12,"severity":"critical","cwe":"CWE-89","title":"SQL injection via string concatenation","why":"User input is concatenated into the SQL query without parameterisation."}] |
      | logic    | [] |
    And a diff that adds a file "db.go" with content:
      """
      package db
      func Find(id string) string {
          q := "SELECT * FROM users WHERE id = '" + id + "'"
          return q
      }
      """
    When I run "accurate-reviewer review --diff -" with that diff on stdin
    Then the exit code is 1
    And the output contains the finding:
      | field    | value                  |
      | file     | db.go                  |
      | line     | 12                     |
      | severity | critical               |
      | cwe      | CWE-89                 |
    And the output contains "SQL injection via string concatenation"

  Scenario: Severity below `blocking` does not fail the exit code
    Given a .review.yml with severity.blocking set to "critical"
    And the mock LLM is scripted with:
      | worker   | findings_json                                                                                              |
      | security | [{"file":"x.go","line":3,"severity":"medium","cwe":"CWE-330","title":"Weak RNG","why":"math/rand is not secure"}] |
      | logic    | []                                                                                                         |
    And a diff that adds a file "x.go" with content:
      """
      package main
      import "math/rand"
      func Token() int { return rand.Int() }
      """
    When I run "accurate-reviewer review --diff -" with that diff on stdin
    Then the exit code is 0
    And the output contains "Weak RNG"

  Scenario: The master deduplicates findings reported by multiple workers on the same line
    # The security worker and the logic worker can independently flag the same
    # bug under different framings (e.g. "uses unsafe deserialisation" and
    # "may panic on malformed input"). The master collapses them into one
    # comment per (file, line, normalised-title) tuple.
    Given the mock LLM is scripted with:
      | worker   | findings_json |
      | security | [{"file":"y.go","line":7,"severity":"high","cwe":"CWE-502","title":"Unsafe deserialisation","why":"untrusted input"}] |
      | logic    | [{"file":"y.go","line":7,"severity":"medium","title":"Unsafe deserialisation","why":"can panic"}] |
    And a diff that adds a file "y.go" with content:
      """
      package y
      import "encoding/gob"
      func Decode(b []byte) any { var v any; gob.NewDecoder(nil).Decode(&v); return v }
      """
    When I run "accurate-reviewer review --diff -" with that diff on stdin
    Then the report contains exactly 1 finding for "y.go:7"
    And the surviving finding has severity "high"

  Scenario: The master runs only the workers enabled in config
    Given a .review.yml that enables only the worker "security"
    And the mock LLM records every worker call it receives
    And a diff that adds a file "z.go" with content:
      """
      package z
      func F() {}
      """
    When I run "accurate-reviewer review --diff -" with that diff on stdin
    Then the workers called on the mock LLM are exactly:
      | worker   |
      | security |

  Scenario: Workers run in parallel and the master waits for all of them
    Given the mock LLM is scripted with:
      | worker   | findings_json | delay_ms |
      | security | []            | 200      |
      | logic    | []            | 200      |
    And a diff that adds a file "p.go" with content:
      """
      package p
      func F() {}
      """
    When I run "accurate-reviewer review --diff -" with that diff on stdin
    Then the total wall-clock time is less than 350 ms
    # Sequential would be >= 400 ms; parallel must stay close to the slowest worker.

  Scenario: When a worker errors, the master continues with the rest and exits non-zero
    Given the mock LLM is scripted with:
      | worker   | behaviour | payload                                                                                         |
      | security | error     | rate_limit                                                                                       |
      | logic    | findings  | [{"file":"q.go","line":2,"severity":"low","title":"Unused variable","why":"x is declared but never used"}] |
    And a diff that adds a file "q.go" with content:
      """
      package q
      func F() { x := 1; _ = x }
      """
    When I run "accurate-reviewer review --diff -" with that diff on stdin
    Then the exit code is 2
    And stderr contains "worker security failed: rate_limit"
    And the output still contains "Unused variable"

  Scenario: The token budget caps the review and the master reports the overage
    # Workers run in parallel per unit, so the budget is checked between
    # units, not within a single parallel batch. With two enabled workers
    # at 200 tokens apiece, one unit's batch already exceeds the 50-token
    # budget — the master must abort before touching the second unit.
    Given a .review.yml with budget.max_tokens set to 50
    And the mock LLM is scripted to always report 200 tokens used per call
    And a diff that adds files "a.go" and "b.go" with content "package main"
    When I run "accurate-reviewer review --diff -" with that diff on stdin
    Then the exit code is 2
    And the output contains "budget exceeded"
    And the mock LLM was called at most 2 times
