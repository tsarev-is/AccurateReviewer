@cli
Feature: CLI command surface
  As a developer using AccurateReviewer locally or in CI
  I want a stable command-line entry point with predictable commands
  So that all other interfaces (GitHub Action, web viewer) can be built as wrappers around it

  Background:
    Given a temporary working directory
    And the accurate-reviewer binary is on PATH

  Scenario: The binary reports the version from the VERSION file
    When I run "accurate-reviewer --version"
    Then the exit code is 0
    And the output contains "accurate-reviewer"
    And the output matches the regex "\d+\.\d+\.\d+"
    And the output contains the value of the VERSION file

  Scenario: The version output includes the build commit identifier
    When I run "accurate-reviewer --version"
    Then the exit code is 0
    And the output matches the regex "commit [0-9a-f]{7,}"

  Scenario: A dedicated version subcommand returns the same string
    When I run "accurate-reviewer version"
    Then the exit code is 0
    And the output contains the value of the VERSION file
    And the output matches the regex "commit [0-9a-f]{7,}"

  Scenario: The top-level help lists the MVP commands
    When I run "accurate-reviewer --help"
    Then the exit code is 0
    And the output contains the lines:
      | line     |
      | init     |
      | analyze  |
      | review   |

  Scenario: init writes a .review.yml in the current directory
    Given there is no .review.yml in the working directory
    When I run "accurate-reviewer init"
    Then the exit code is 0
    And a file ".review.yml" exists in the working directory
    And the file ".review.yml" contains the key "version"
    And the file ".review.yml" contains the key "checks"
    And the file ".review.yml" contains the key "llm"

  Scenario: init refuses to overwrite an existing config
    Given a file ".review.yml" exists with content:
      """
      version: 1
      checks:
        security: false
      """
    When I run "accurate-reviewer init"
    Then the exit code is 2
    And stderr contains "already exists"
    And the file ".review.yml" still contains "security: false"

  Scenario: init --force overwrites an existing config
    Given a file ".review.yml" exists with content:
      """
      version: 1
      checks:
        security: false
      """
    When I run "accurate-reviewer init --force"
    Then the exit code is 0
    And the file ".review.yml" contains the key "llm"

  Scenario: review accepts a diff via stdin
    Given a .review.yml with llm.provider set to "mock"
    And the mock LLM is configured to return no findings
    When I pipe the file "testdata/diffs/empty.diff" into "accurate-reviewer review --diff -"
    Then the exit code is 0
    And the output contains "0 findings"

  Scenario: review reads a diff from a file
    Given a .review.yml with llm.provider set to "mock"
    And the mock LLM is configured to return no findings
    When I run "accurate-reviewer review --diff testdata/diffs/empty.diff"
    Then the exit code is 0
    And the output contains "0 findings"

  Scenario: review against git refs uses the working repo
    Given a git repository with two commits, the latest adding a file "hello.go"
    And a .review.yml with llm.provider set to "mock"
    And the mock LLM is configured to return no findings
    When I run "accurate-reviewer review --from HEAD~1 --to HEAD"
    Then the exit code is 0
    And the output contains the file "hello.go"

  Scenario: review fails fast when no diff source is given
    When I run "accurate-reviewer review"
    Then the exit code is 2
    And stderr contains "no diff source"

  Scenario: analyze writes a project snapshot to .review-cache/
    Given a sample Go project at "testdata/repos/sample-go"
    When I run "accurate-reviewer analyze" inside "testdata/repos/sample-go"
    Then the exit code is 0
    And a file ".review-cache/project.json" exists in "testdata/repos/sample-go"
    And the JSON file ".review-cache/project.json" contains:
      | path                | value  |
      | language.primary    | go     |
      | manifests[0].kind   | go.mod |
