@analyzer
Feature: Project startup analysis
  As the AccurateReviewer pipeline
  I must build a structured snapshot of the project once, at onboarding
  And cache it as JSON so every subsequent review has the same context
  Because without this snapshot the LLM has no idea what kind of repo it is reviewing

  The output is a stable JSON document. Its schema is frozen at v1; new fields
  are added under known top-level keys, never by renaming or moving existing ones.

  Background:
    Given the accurate-reviewer binary is on PATH

  Scenario: A Go project is recognised by its go.mod manifest
    Given a sample project at "testdata/repos/sample-go" containing:
      | path        | content                           |
      | go.mod      | module example.com/sample\n\ngo 1.22 |
      | main.go     | package main\nfunc main(){}       |
      | cmd/app/.gitkeep | (empty)                      |
    When I run "accurate-reviewer analyze" inside "testdata/repos/sample-go"
    Then the file ".review-cache/project.json" exists
    And the JSON file ".review-cache/project.json" contains:
      | path                  | value           |
      | schema_version        | 1               |
      | language.primary      | go              |
      | language.mix[0].name  | go              |
      | manifests[0].kind     | go.mod          |
      | manifests[0].path     | go.mod          |
      | entry_points[0].path  | main.go         |

  Scenario: A Python project is recognised by requirements.txt
    Given a sample project at "testdata/repos/sample-python" containing:
      | path              | content                       |
      | requirements.txt  | flask==3.0.0\nrequests==2.32.0 |
      | app.py            | from flask import Flask       |
    When I run "accurate-reviewer analyze" inside "testdata/repos/sample-python"
    Then the JSON file ".review-cache/project.json" contains:
      | path                       | value              |
      | language.primary           | python             |
      | manifests[0].kind          | requirements.txt   |
      | frameworks[0].name         | flask              |

  Scenario: A polyglot project lists every language found, primary by lines of code
    Given a sample project containing:
      | path            | content                |
      | backend/go.mod  | module example.com/be  |
      | backend/main.go | package main\nfunc main(){}\n // 30 LOC of Go |
      | frontend/package.json | {"name":"fe","dependencies":{"react":"^18"}} |
      | frontend/src/index.jsx | // 5 LOC of JS |
    When I run "accurate-reviewer analyze" inside the sample project
    Then the JSON file ".review-cache/project.json" contains:
      | path                  | value  |
      | language.primary      | go     |
    And the JSON file ".review-cache/project.json" has at least 2 entries in "language.mix"

  Scenario: The snapshot is content-addressed and re-runs are cached
    Given a sample Go project at "testdata/repos/sample-go"
    And I have already run "accurate-reviewer analyze" inside it
    And I record the file ".review-cache/project.json"'s mtime as T0
    When I run "accurate-reviewer analyze" again inside it without changing any source file
    Then the mtime of ".review-cache/project.json" is still T0
    And the output contains "snapshot up to date"

  Scenario: --force re-runs analysis even when the cache is valid
    Given a sample Go project at "testdata/repos/sample-go" with an existing snapshot
    When I run "accurate-reviewer analyze --force" inside it
    Then the file ".review-cache/project.json" was rewritten

  Scenario: Unrecognised projects produce an empty-but-valid snapshot
    # We do not crash on unknown projects. The snapshot still parses; downstream
    # workers fall back to generic prompts when language.primary is "unknown".
    Given a sample project containing:
      | path     | content    |
      | NOTES.md | hello      |
    When I run "accurate-reviewer analyze" inside the sample project
    Then the exit code is 0
    And the JSON file ".review-cache/project.json" contains:
      | path             | value   |
      | language.primary | unknown |
