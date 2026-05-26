@cves
Feature: Dependency-vulnerability pre-flight via osv-scanner
  As the AccurateReviewer pipeline
  Before running the LLM workers, I scan the project's dependency
  manifests for known CVEs using a locally installed `osv-scanner`
  Because dependency CVEs are deterministic (regex / version-match
  on a curated database) and should never depend on an LLM's guesswork
  And the cost of missing one is catastrophic — supply-chain-grade.

  The binary itself opens no network sockets — osv-scanner handles all
  OSV-database lookups under its own auth and caching. A missing
  osv-scanner CLI is logged once and the review continues (pre-flight
  treats the tool as optional); the standalone `scan-cves` subcommand
  treats it as required and exits cleanly when missing.

  Background:
    Given the accurate-reviewer binary is on PATH
    And the mock LLM is reset

  Scenario: review prepends CVE findings to the report when osv-scanner finds vulns
    Given a file ".review.yml" with content:
      """
      version: 1
      checks: { security: true, logic: true, vulnerabilities: true }
      llm: { provider: mock }
      """
    And the mock LLM is configured to return no findings
    And the osv-scanner fake is scripted to report a CRITICAL vuln in "go.mod" affecting "github.com/foo/bar@1.0.0" (GHSA-aaaa-bbbb-cccc, fixed in 1.0.1)
    And a diff that adds a file "hello.go" with content:
      """
      package main
      func main() {}
      """
    When I run "accurate-reviewer review --diff -" with that diff on stdin
    Then the exit code is 1
    And the output contains "GHSA-aaaa-bbbb-cccc"
    And the output contains "github.com/foo/bar"
    And the output contains "1.0.1"

  Scenario: review with vulnerabilities=false skips the scan entirely
    Given a file ".review.yml" with content:
      """
      version: 1
      checks: { security: true, logic: true, vulnerabilities: false }
      llm: { provider: mock }
      """
    And the mock LLM is configured to return no findings
    And the osv-scanner fake is scripted to report a HIGH vuln in "go.mod" affecting "github.com/foo/bar@1.0.0" (GHSA-aaaa-bbbb-cccc, fixed in 1.0.1)
    And a diff that adds a file "hello.go" with content:
      """
      package main
      """
    When I run "accurate-reviewer review --diff -" with that diff on stdin
    Then the exit code is 0
    And the output does NOT contain "GHSA-aaaa-bbbb-cccc"
    And the osv-scanner fake was not invoked

  Scenario: cve.min_severity drops findings below the threshold
    Given a file ".review.yml" with content:
      """
      version: 1
      checks: { security: true, logic: true, vulnerabilities: true }
      llm: { provider: mock }
      cve: { min_severity: high }
      """
    And the mock LLM is configured to return no findings
    And the osv-scanner fake is scripted to report a LOW vuln in "go.mod" affecting "github.com/baz/qux@2.0.0" (GHSA-low-only-here, fixed in 2.0.1)
    And a diff that adds a file "hello.go" with content:
      """
      package main
      """
    When I run "accurate-reviewer review --diff -" with that diff on stdin
    Then the exit code is 0
    And the output does NOT contain "GHSA-low-only-here"

  Scenario: scan-cves subcommand emits the findings standalone
    Given the osv-scanner fake is scripted to report a HIGH vuln in "go.mod" affecting "github.com/foo/bar@1.0.0" (GHSA-aaaa-bbbb-cccc, fixed in 1.0.1)
    When I run "accurate-reviewer scan-cves ."
    Then the exit code is 1
    And the output contains "GHSA-aaaa-bbbb-cccc"
    And the output contains "github.com/foo/bar@1.0.0"
    And the output contains "1 findings"

  Scenario: scan-cves with a clean repo reports zero findings and exits 0
    When I run "accurate-reviewer scan-cves ."
    Then the exit code is 0
    And the output contains "0 findings"
