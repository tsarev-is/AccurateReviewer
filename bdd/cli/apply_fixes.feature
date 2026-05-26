@apply-fixes
Feature: Apply model-suggested fixes via git apply
  As a developer using AccurateReviewer
  When a worker proposes a mechanically-applicable fix alongside a finding
  I can apply it to my working tree without copy-pasting line numbers
  By running `accurate-reviewer apply-fixes --report findings.json`

  The fix shape is structured Replacements ({file, start_line, end_line,
  new_text}), validated at review time so only fixes touching ADDED lines
  of the unit under review survive into the report. apply-fixes
  synthesises a unified diff from the Replacements and pipes it to
  `git apply` — the binary never patches files directly.

  --dry-run prints the synthesised diff to stdout for inspection without
  invoking git apply.

  Background:
    Given the accurate-reviewer binary is on PATH
    And a .review.yml with llm.provider set to "mock"
    And the mock LLM is reset

  Scenario: A fix proposed on an added line surfaces in the JSON report
    Given the mock LLM is scripted with:
      | worker   | findings_json |
      | security | [{"file":"db.go","line":3,"severity":"critical","cwe":"CWE-89","title":"SQL injection","why":"concat","fix":{"description":"use parameterised query","replacements":[{"file":"db.go","start_line":3,"end_line":3,"new_text":"    return db.Query(\"SELECT * FROM u WHERE id = ?\", id)\n"}]}}] |
      | logic    | [] |
    And a diff that adds a file "db.go" with content:
      """
      package db
      func Q(id string) string {
          return "SELECT * FROM u WHERE id='" + id + "'"
      }
      """
    When I run "accurate-reviewer review --diff - --output report.json" with that diff on stdin
    Then the exit code is 1
    And the JSON file "report.json" contains:
      | path                                       | value |
      | findings[0].fix.replacements[0].file       | db.go |
      | findings[0].fix.replacements[0].start_line | 3     |

  Scenario: Console output flags findings that carry a fix
    Given the mock LLM is scripted with:
      | worker   | findings_json |
      | security | [{"file":"db.go","line":3,"severity":"critical","cwe":"CWE-89","title":"SQL injection","why":"concat","fix":{"replacements":[{"file":"db.go","start_line":3,"end_line":3,"new_text":"    return db.Query(\"SELECT * FROM u WHERE id = ?\", id)\n"}]}}] |
      | logic    | [] |
    And a diff that adds a file "db.go" with content:
      """
      package db
      func Q(id string) string {
          return "SELECT * FROM u WHERE id='" + id + "'"
      }
      """
    When I run "accurate-reviewer review --diff -" with that diff on stdin
    Then the exit code is 1
    And the output contains "[fix available]"

  Scenario: A fix that targets a line outside the diff is dropped at parse time
    # The unit adds three lines (1..3). A fix targeting line 5 is a
    # hallucination — the validator drops it before it reaches the
    # report, so the finding survives but with no fix attached.
    Given the mock LLM is scripted with:
      | worker   | findings_json |
      | security | [{"file":"db.go","line":3,"severity":"critical","cwe":"CWE-89","title":"SQL injection","why":"concat","fix":{"replacements":[{"file":"db.go","start_line":5,"end_line":5,"new_text":"x\n"}]}}] |
      | logic    | [] |
    And a diff that adds a file "db.go" with content:
      """
      package db
      func Q(id string) string {
          return "SELECT * FROM u WHERE id='" + id + "'"
      }
      """
    When I run "accurate-reviewer review --diff - --output report.json" with that diff on stdin
    Then the exit code is 1
    And the output does NOT contain "[fix available]"
    And the JSON file "report.json" does NOT contain the key "findings[0].fix"

  Scenario: A fix that targets a different file is dropped at parse time
    Given the mock LLM is scripted with:
      | worker   | findings_json |
      | security | [{"file":"db.go","line":3,"severity":"critical","cwe":"CWE-89","title":"SQL injection","why":"concat","fix":{"replacements":[{"file":"unrelated.go","start_line":3,"end_line":3,"new_text":"x\n"}]}}] |
      | logic    | [] |
    And a diff that adds a file "db.go" with content:
      """
      package db
      func Q(id string) string {
          return "SELECT * FROM u WHERE id='" + id + "'"
      }
      """
    When I run "accurate-reviewer review --diff - --output report.json" with that diff on stdin
    Then the JSON file "report.json" does NOT contain the key "findings[0].fix"

  Scenario: apply-fixes --dry-run prints the synthesised unified diff
    Given a JSON report at "report.json" with one fix replacing line 1 of "hello.go" with "package main // patched\n"
    And a file "hello.go" with content:
      """
      package main
      """
    When I run "accurate-reviewer apply-fixes --report report.json --dry-run"
    Then the exit code is 0
    And the output contains "--- a/hello.go"
    And the output contains "+++ b/hello.go"
    And the output contains "-package main"
    And the output contains "+package main // patched"
    And the file "hello.go" still contains "package main"
    And the file "hello.go" does NOT contain "patched"

  Scenario: apply-fixes rewrites the working tree via git apply
    Given a git repo at the working directory
    And a JSON report at "report.json" with one fix replacing line 1 of "hello.go" with "package main // patched\n"
    And a tracked file "hello.go" with content:
      """
      package main
      """
    When I run "accurate-reviewer apply-fixes --report report.json"
    Then the exit code is 0
    And the output contains "applied 1 fix"
    And the file "hello.go" contains "package main // patched"

  Scenario: apply-fixes reports gracefully when there are no fixes
    Given a JSON report at "report.json" with one critical finding on "db.go:12"
    When I run "accurate-reviewer apply-fixes --report report.json"
    Then the exit code is 0
    And the output contains "no fixes available"
