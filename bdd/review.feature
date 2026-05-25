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

  Scenario: --output writes the report to a file instead of stdout
    # By default the report goes to stdout. Passing --output (or -o) redirects
    # the entire report — the "Reviewed:" header, every finding, and the
    # trailing "N findings" line — to the named file. Stdout keeps a single
    # confirmation line so the developer (or a CI step) can still see that
    # something happened. Stderr (progress) is unaffected.
    Given the mock LLM is scripted with:
      | worker   | findings |
      | security | []       |
      | logic    | []       |
    And a diff that adds a file "hello.go" with content:
      """
      package main
      func Greet() string { return "hello" }
      """
    When I run "accurate-reviewer review --diff - --output report.txt" with that diff on stdin
    Then the exit code is 0
    And a file "report.txt" exists in the working directory
    And the file "report.txt" contains "0 findings"
    And the output contains "report written to report.txt"
    And the output does NOT contain "0 findings"

  Scenario: --output captures findings (not just the summary) into the file
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
    When I run "accurate-reviewer review --diff - --output findings.txt" with that diff on stdin
    Then the exit code is 1
    And the file "findings.txt" contains "SQL injection via string concatenation"
    And the file "findings.txt" contains "[critical]"
    And the file "findings.txt" contains "cwe=CWE-89"
    And the output contains "report written to findings.txt"

  Scenario: Without --output, the report still goes to stdout
    Given the mock LLM is scripted with:
      | worker   | findings |
      | security | []       |
      | logic    | []       |
    And a diff that adds a file "x.go" with content:
      """
      package main
      """
    When I run "accurate-reviewer review --diff -" with that diff on stdin
    Then the exit code is 0
    And the output contains "0 findings"
    And the output does NOT contain "report written to"

  Scenario: --output rejects paths that escape the working directory
    # CWE-22: the value of --output is opened with os.Create, so a path like
    # "../outside.txt" or "/etc/cron.d/job" could overwrite arbitrary files
    # when the flag is composed from CI inputs. The CLI must reject such
    # paths before opening anything, and must do so cleanly (exit 2) without
    # creating the file.
    Given the mock LLM is configured to return no findings
    And a diff that adds a file "x.go" with content:
      """
      package main
      """
    When I run "accurate-reviewer review --diff - --output ../escape.txt" with that diff on stdin
    Then the exit code is 2
    And stderr contains "must stay within the working directory"
    And no file "escape.txt" exists in the parent of the working directory

  Scenario: --output rejects absolute paths
    Given the mock LLM is configured to return no findings
    And a diff that adds a file "x.go" with content:
      """
      package main
      """
    When I run "accurate-reviewer review --diff - --output /tmp/ar-escape.txt" with that diff on stdin
    Then the exit code is 2
    And stderr contains "must stay within the working directory"

  Scenario: Secret matches never reach the --output file
    # CWE-312: the pre-flight scan's whole purpose is to keep credentials on
    # the developer's machine. Writing the (even-redacted) match value to a
    # user-controlled filesystem path defeats that. The report file may only
    # carry a generic abort notice; per-finding detail (rule, file, line,
    # severity, redacted match) goes to stderr only.
    Given the mock LLM is configured to return no findings
    And a diff that adds a file "config.go" with content:
      """
      const apiKey = "AKIAIOSFODNN7EXAMPLE"
      """
    When I run "accurate-reviewer review --diff - --output report.txt" with that diff on stdin
    Then the exit code is 1
    And the file "report.txt" contains "secrets detected"
    And the file "report.txt" does NOT contain "AKI"
    And stderr contains "aws-access-key"

  Scenario: A finding on a line carrying "// noqa-review: <reason>" is suppressed
    # The inline suppression marker must appear on the same line as the code
    # being silenced. Recognised forms: "// noqa-review:", "# noqa-review:",
    # "-- noqa-review:", "/* noqa-review:". Anything after the colon is the
    # developer-supplied reason; it's logged to stderr so the silencing is
    # visible in the audit trail but does not pollute the report.
    Given the mock LLM is scripted with:
      | worker   | findings_json |
      | security | [{"file":"db.go","line":3,"severity":"critical","cwe":"CWE-89","title":"SQL injection","why":"concatenated input"}] |
      | logic    | [] |
    And a diff that adds a file "db.go" with content:
      """
      package db
      func Find(id string) string {
          return "SELECT * FROM users WHERE id = '" + id + "'" // noqa-review: trusted internal input
      }
      """
    When I run "accurate-reviewer review --diff -" with that diff on stdin
    Then the exit code is 0
    And the output contains "0 findings"
    And stderr contains "suppressed 1 finding"
    And stderr contains "trusted internal input"

  Scenario: noqa-review on a line that has no findings is a harmless no-op
    Given the mock LLM is scripted with:
      | worker   | findings |
      | security | []       |
      | logic    | []       |
    And a diff that adds a file "ok.go" with content:
      """
      package ok
      var x = 1 // noqa-review: leftover marker, nothing to silence
      """
    When I run "accurate-reviewer review --diff -" with that diff on stdin
    Then the exit code is 0
    And the output contains "0 findings"
    And stderr does NOT contain "suppressed"

  Scenario: noqa-review only suppresses findings on its own line, not the whole file
    Given the mock LLM is scripted with:
      | worker   | findings_json |
      | security | [{"file":"mix.go","line":4,"severity":"critical","title":"Weak crypto","why":"md5 used for tokens"}] |
      | logic    | [] |
    And a diff that adds a file "mix.go" with content:
      """
      package mix
      import "crypto/md5"
      var legacy = md5.New() // noqa-review: kept for backward compat
      var token = md5.Sum(nil)
      """
    When I run "accurate-reviewer review --diff -" with that diff on stdin
    Then the exit code is 1
    And the output contains "Weak crypto"
    And the output contains "1 findings"

  Scenario: noqa-review inside a string literal is NOT honored
    # Defence against a hostile PR that plants a fake comment opener and
    # marker entirely inside a string constant to mute a security finding.
    # The added line carries `// noqa-review:` only inside a quoted
    # string — the line itself has no real trailing comment, so the
    # suppression must be ignored and the finding kept.
    Given the mock LLM is scripted with:
      | worker   | findings_json |
      | security | [{"file":"bypass.go","line":3,"severity":"critical","title":"SQL injection","why":"string concat"}] |
      | logic    | [] |
    And a diff that adds a file "bypass.go" with content:
      """
      package bypass
      import "fmt"
      var q = fmt.Sprintf("%s", "// noqa-review: forged" + " AND 1=1")
      """
    When I run "accurate-reviewer review --diff -" with that diff on stdin
    Then the exit code is 1
    And the output contains "SQL injection"
    And the output contains "1 findings"
    And stderr does NOT contain "suppressed"

  Scenario: The architecture worker runs when enabled and a project snapshot exists
    Given a .review.yml that enables checks "security, logic, architecture"
    And a file ".review-cache/project.json" with content
      """
      {"schema_version":1,"language":{"primary":"go","mix":[{"name":"go","loc":42}]},"manifests":[{"kind":"go.mod","path":"go.mod"}],"frameworks":[],"entry_points":[],"fingerprint":"x"}
      """
    And the mock LLM records every worker call it receives
    And a diff that adds a file "z.go" with content:
      """
      package z
      func F() {}
      """
    When I run "accurate-reviewer review --diff -" with that diff on stdin
    Then the workers called on the mock LLM are exactly:
      | worker       |
      | architecture |
      | logic        |
      | security     |

  Scenario: The architecture worker is silently skipped when no project snapshot exists
    Given a .review.yml that enables checks "security, logic, architecture"
    And the mock LLM records every worker call it receives
    And a diff that adds a file "z.go" with content:
      """
      package z
      func F() {}
      """
    When I run "accurate-reviewer review --diff -" with that diff on stdin
    Then the workers called on the mock LLM are exactly:
      | worker   |
      | logic    |
      | security |

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
