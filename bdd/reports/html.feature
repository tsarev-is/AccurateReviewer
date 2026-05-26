@html
Feature: HTML report and local web viewer
  The HTML renderer turns the same finding stream the console uses into a
  self-contained HTML document. The `serve` command boots a loopback-only
  HTTP server in front of that document so developers can browse it in a
  real browser instead of staring at terminal text.

  Background:
    Given the accurate-reviewer binary is on PATH
    And a .review.yml with llm.provider set to "mock"
    And the mock LLM is reset

  Scenario: review --output report.html produces a self-contained HTML report
    Given the mock LLM is scripted with:
      | worker   | findings_json |
      | security | [{"file":"db.go","line":12,"severity":"critical","cwe":"CWE-89","title":"SQL injection via string concatenation","why":"User input is concatenated into the SQL query."}] |
      | logic    | [] |
    And a diff that adds a file "db.go" with content:
      """
      package db
      func Find(id string) string { return "SELECT * FROM u WHERE id='" + id + "'" }
      """
    When I run "accurate-reviewer review --diff - --output report.html" with that diff on stdin
    Then the exit code is 1
    And a file "report.html" exists in the working directory
    And the file "report.html" contains "<!DOCTYPE html>"
    And the file "report.html" contains "AccurateReviewer report"
    And the file "report.html" contains "SQL injection via string concatenation"
    And the file "report.html" contains "sev-critical"
    And the file "report.html" contains "CWE-89"

  Scenario: HTML output escapes finding content to defeat prompt-injected markup
    # CWE-79: the model's finding text is untrusted. If a hostile diff
    # convinces the LLM to emit "<script>…</script>" inside a "why" field,
    # the HTML rendering must escape it so the browser sees literal text
    # rather than executing the script.
    Given the mock LLM is scripted with:
      | worker   | findings_json |
      | security | [{"file":"x.go","line":1,"severity":"high","title":"<script>alert(1)</script>","why":"&plain text"}] |
      | logic    | [] |
    And a diff that adds a file "x.go" with content:
      """
      package x
      """
    When I run "accurate-reviewer review --diff - --output report.html" with that diff on stdin
    Then the exit code is 0
    And the file "report.html" contains "&lt;script&gt;alert(1)&lt;/script&gt;"
    And the file "report.html" does NOT contain "<script>alert(1)</script>"
    And the file "report.html" contains "&amp;plain text"

  Scenario: serve refuses to run when the report file does not exist
    When I run "accurate-reviewer serve --report missing.html"
    Then the exit code is 1
    And stderr contains "report not found"

  Scenario: serve hosts the report on a loopback HTTP server and refuses other paths
    Given the mock LLM is scripted with:
      | worker   | findings |
      | security | []       |
      | logic    | []       |
    And a diff that adds a file "x.go" with content:
      """
      package x
      """
    And I run "accurate-reviewer review --diff - --output report.html" with that diff on stdin
    When I serve "report.html" in the background
    Then GET / returns 200 and contains "AccurateReviewer report"
    And GET /etc/passwd returns 404

  Scenario: serve refuses to bind to a non-loopback address
    When I run "accurate-reviewer serve --report report.html --addr 0.0.0.0:9999"
    Then the exit code is 2
    And stderr contains "refusing to bind"

  Scenario Outline: serve rejects non-loopback host shorthands
    # `localhost` can resolve to multiple interfaces; bare `:port` and `[::]`
    # both bind to every interface. None of these must be honoured.
    When I run "accurate-reviewer serve --report report.html --addr <addr>"
    Then the exit code is 2
    And stderr contains "<needle>"

    Examples:
      | addr            | needle                |
      | localhost:9999  | non-loopback          |
      | [::]:9999       | non-loopback          |
      | :9999           | non-loopback          |
      | 192.168.0.1:9999| non-loopback          |
