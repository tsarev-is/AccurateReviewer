@secrets
Feature: Deterministic secrets scanner (pre-flight, no LLM)
  As the AccurateReviewer pipeline
  I must catch hardcoded credentials before any code leaves the developer's machine
  Because the cost of leaking a real token through an LLM provider is catastrophic
  And LLMs have an unacceptable false-negative rate on this class of problem

  The scanner is deterministic. It runs before the master agent is even invoked.
  Findings here are always severity "critical" and always block the run, regardless
  of the .review.yml severity policy — secrets are non-negotiable.

  Background:
    Given the accurate-reviewer binary is on PATH

  Scenario Outline: A known token format is detected
    Given a file "leak.txt" with content:
      """
      <prefix><payload>
      """
    When I run "accurate-reviewer scan-secrets leak.txt"
    Then the exit code is 1
    And the output contains the finding:
      | field    | value         |
      | rule     | <rule>        |
      | file     | leak.txt      |
      | severity | critical      |

    Examples: token formats with stable prefixes
      | rule              | prefix     | payload                                                          |
      | aws-access-key    | AKIA       | IOSFODNN7EXAMPLE                                                 |
      | aws-access-key    | ASIA       | IOSFODNN7EXAMPLE                                                 |
      | github-pat        | ghp_       | 0123456789abcdef0123456789abcdef01234567                         |
      | github-fine-grain | github_pat_| 11ABCDEFG0123456789_abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGH |
      | stripe-live-key   | sk_live_   | 0123456789abcdef0123456789                                       |
      | slack-bot-token   | xoxb-      | 1234567890-1234567890-abcdefghijklmnopqrstuvwx                   |
      | google-api-key    | AIza       | SyA0123456789_abcdefghijklmnopqrstuv                             |

  Scenario: A PEM private-key header is detected
    Given a file "id_rsa" with content:
      """
      -----BEGIN RSA PRIVATE KEY-----
      MIIEpAIBAAKCAQEAxK4f4DKyz3Y3eW3p1234567890abcdefghijk
      -----END RSA PRIVATE KEY-----
      """
    When I run "accurate-reviewer scan-secrets id_rsa"
    Then the exit code is 1
    And the output contains the finding:
      | field    | value              |
      | rule     | pem-private-key    |
      | file     | id_rsa             |
      | severity | critical           |

  Scenario: A high-entropy string in an assignment to a sensitive name is detected
    Given a file "config.go" with content:
      """
      package config
      const apiToken = "Zk9pQ2RhV2hPbW1lTk5kS3F2WGtNcUxqVHJTd2VVZw=="
      """
    When I run "accurate-reviewer scan-secrets config.go"
    Then the exit code is 1
    And the output contains the finding:
      | field    | value             |
      | rule     | generic-entropy   |
      | file     | config.go         |
      | severity | critical          |
    And the finding's "match" field is redacted in the report

  Scenario: A low-entropy string under a sensitive name is NOT flagged
    Given a file "config.go" with content:
      """
      package config
      const apiToken = "TODO_FILL_ME_IN"
      """
    When I run "accurate-reviewer scan-secrets config.go"
    Then the exit code is 0
    And the output contains "0 findings"

  Scenario: Regular source code without secrets passes cleanly
    Given a file "hello.go" with content:
      """
      package main

      import "fmt"

      func main() {
          fmt.Println("hello, world")
      }
      """
    When I run "accurate-reviewer scan-secrets hello.go"
    Then the exit code is 0
    And the output contains "0 findings"

  Scenario: Secrets in excluded paths are still flagged
    # Excludes apply to LLM review, never to the secrets pre-flight.
    # The cost of a leaked token is the same whether it's in vendor/ or src/.
    Given a .review.yml that excludes "vendor/**"
    And a file "vendor/lib/leak.txt" with content:
      """
      AKIAIOSFODNN7EXAMPLE
      """
    When I run "accurate-reviewer scan-secrets vendor/lib/leak.txt"
    Then the exit code is 1
    And the output contains the finding:
      | field | value                 |
      | rule  | aws-access-key        |
      | file  | vendor/lib/leak.txt   |

  Scenario: Secrets pre-flight blocks the main review
    Given a .review.yml with llm.provider set to "mock"
    And the mock LLM is configured to return no findings
    And a diff that adds a file "config.go" with content:
      """
      const apiKey = "AKIAIOSFODNN7EXAMPLE"
      """
    When I run "accurate-reviewer review --diff -" with that diff on stdin
    Then the exit code is 1
    And the output contains "secrets detected — aborting review"
    And the mock LLM was called 0 times
