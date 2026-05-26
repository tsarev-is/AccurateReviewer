@dedupe-groups
Feature: Cross-location grouping of duplicate findings
  As the AccurateReviewer master
  When the same worker flags the same problem class — identified by
  (worker, normalised title, CWE) — at more than one (file, line)
  Collapse those findings into a single group: one primary finding with
  the highest severity, and the other locations carried as `occurrences`.

  Cross-worker grouping is intentionally out of scope. Two workers seeing
  the "same" title still represent distinct epistemic claims (security
  vs. logic perspective) and merging them would be hard to justify in
  the report.

  Background:
    Given the accurate-reviewer binary is on PATH
    And a .review.yml with llm.provider set to "mock"
    And the mock LLM is reset

  Scenario: Same-worker findings at different locations group into one finding with occurrences
    Given the mock LLM is scripted with:
      | worker   | findings_json |
      | security | [{"file":"a.go","line":10,"severity":"critical","cwe":"CWE-89","title":"SQL injection via concatenation","why":"User input is interpolated into the query."}, {"file":"b.go","line":20,"severity":"critical","cwe":"CWE-89","title":"SQL injection via concatenation","why":"Same vector, different site."}] |
      | logic    | [] |
    And a diff that adds a file "a.go" with content:
      """
      package a
      """
    When I run "accurate-reviewer review --diff - --output report.json" with that diff on stdin
    Then the exit code is 1
    And the JSON file "report.json" has at least 1 entries in "findings"
    And the JSON file "report.json" contains:
      | path                            | value                              |
      | findings[0].file                | a.go                               |
      | findings[0].line                | 10                                 |
      | findings[0].cwe                 | CWE-89                             |
      | findings[0].occurrences[0].file | b.go                               |
      | findings[0].occurrences[0].line | 20                                 |

  Scenario: A higher-severity occurrence is promoted to the primary location
    # When a second hit of the same class is HIGHER severity than the first one
    # seen, the master promotes it to the primary slot and demotes the previous
    # primary into the occurrences list. The group's severity always tracks the
    # worst member.
    Given the mock LLM is scripted with:
      | worker   | findings_json |
      | security | [{"file":"low.go","line":5,"severity":"medium","cwe":"CWE-89","title":"SQL injection","why":"first hit, medium"}, {"file":"hi.go","line":99,"severity":"critical","cwe":"CWE-89","title":"SQL injection","why":"second hit, critical"}] |
      | logic    | [] |
    And a diff that adds a file "hi.go" with content:
      """
      package hi
      """
    When I run "accurate-reviewer review --diff - --output report.json" with that diff on stdin
    Then the exit code is 1
    And the JSON file "report.json" contains:
      | path                            | value      |
      | findings[0].severity            | critical   |
      | findings[0].file                | hi.go      |
      | findings[0].line                | 99         |
      | findings[0].occurrences[0].file | low.go     |
      | findings[0].occurrences[0].line | 5          |

  Scenario: Different workers reporting the same title are kept distinct
    # Cross-worker grouping is intentionally out of scope — only same-worker
    # duplicates collapse. Here security@a.go:10 and logic@b.go:20 share a title
    # but the master keeps two findings, each with no occurrences.
    Given the mock LLM is scripted with:
      | worker   | findings_json |
      | security | [{"file":"a.go","line":10,"severity":"critical","cwe":"CWE-89","title":"SQL injection","why":"sec view"}] |
      | logic    | [{"file":"b.go","line":20,"severity":"critical","cwe":"CWE-89","title":"SQL injection","why":"logic view"}] |
    And a diff that adds a file "a.go" with content:
      """
      package a
      """
    When I run "accurate-reviewer review --diff - --output report.json" with that diff on stdin
    Then the exit code is 1
    And the JSON file "report.json" has at least 2 entries in "findings"

  Scenario: Console output shows occurrences under the primary finding
    Given the mock LLM is scripted with:
      | worker   | findings_json |
      | security | [{"file":"a.go","line":10,"severity":"critical","cwe":"CWE-89","title":"SQL injection","why":"first site"}, {"file":"b.go","line":20,"severity":"critical","cwe":"CWE-89","title":"SQL injection","why":"second site"}] |
      | logic    | [] |
    And a diff that adds a file "a.go" with content:
      """
      package a
      """
    When I run "accurate-reviewer review --diff -" with that diff on stdin
    Then the exit code is 1
    And the output contains "also at:"
    And the output contains "b.go:20"
    And the output contains "1 findings"

  Scenario: post-comments fans the group out — one comment per location with sibling references
    Given the task-fetch CLI "gh" is scripted to print:
      """
      abc123def456
      """
    And a JSON report with one grouped finding at "a.go:10" and an occurrence at "b.go:20" stored at "findings.json"
    When I run "accurate-reviewer post-comments --report findings.json --pr 7 --commit-sha deadbeef --repo octo/repo --platform github"
    Then the exit code is 0
    And the output contains "posted 2 new comment"
    And the task-fetch CLI "gh" was invoked with the id "path=a.go"
    And the task-fetch CLI "gh" was invoked with the id "path=b.go"
