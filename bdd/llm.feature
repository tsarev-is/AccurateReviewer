@llm
Feature: CLI LLM provider and per-role model selection
  As the AccurateReviewer pipeline
  The CLI provider spawns the configured LLM CLI per worker call
  Per-role models (llm.master.model vs llm.worker.model) flow through to
  the spawned subprocess so a fast local model can serve one role while a
  larger hosted model serves another.

  These scenarios also pin down the real-world failure modes we hit with
  `claude` and `codex`: JSON wrapped in markdown fences, JSON with surrounding
  prose, empty responses, subprocess timeouts, and per-provider argv shape.

  Background:
    Given the accurate-reviewer binary is on PATH

  Scenario: Worker subprocess receives the configured llm.worker.model
    # The mock provider records the value of ACCURATE_REVIEWER_MODEL in the
    # prompt log for every invocation. We assert the model name the worker
    # was spawned with matches `llm.worker.model` from the loaded config.
    Given a file ".review.yml" with content:
      """
      version: 1
      checks: { security: true, logic: true }
      severity: { blocking: critical }
      llm:
        provider: mock
        master:
          model: planner-large
        worker:
          model: reviewer-small
      """
    And the mock LLM is reset
    And the mock LLM is scripted with:
      | worker   | findings_json |
      | security | []            |
      | logic    | []            |
    And a diff that adds a file "f.go" with content:
      """
      package f
      func F() {}
      """
    When I run "accurate-reviewer review --diff -" with that diff on stdin
    Then the exit code is 0
    And every prompt logged to the mock LLM has model "reviewer-small"
    And every prompt logged to the mock LLM has role "worker"

  Scenario: Master and worker models are independently surfaced by `config show`
    # `llm.master.model` is configured but the master itself currently makes
    # no LLM call (master is a coordinator in the MVP). The config must
    # still preserve both values so the resolved view tells the user which
    # model each role would use once master-side calls are wired up.
    Given a file ".review.yml" with content:
      """
      version: 1
      checks: { security: true }
      llm:
        provider: mock
        master:
          model: planner-large
        worker:
          model: reviewer-small
      """
    When I run "accurate-reviewer config show"
    Then the exit code is 0
    And the output contains "model: planner-large"
    And the output contains "model: reviewer-small"

  Scenario: A different llm.worker.model produces a different recorded model
    # Guards against the model name being hard-coded somewhere along the
    # config -> master -> worker -> provider path.
    Given a file ".review.yml" with content:
      """
      version: 1
      checks: { security: true, logic: false }
      llm:
        provider: mock
        worker:
          model: local-llama-3-8b
      """
    And the mock LLM is reset
    And the mock LLM is scripted with:
      | worker   | findings_json |
      | security | []            |
    And a diff that adds a file "g.go" with content:
      """
      package g
      func G() {}
      """
    When I run "accurate-reviewer review --diff -" with that diff on stdin
    Then the exit code is 0
    And every prompt logged to the mock LLM has model "local-llama-3-8b"

  Scenario: The claude provider spawns the binary named "claude"
    # No `cli:` block — defaults from config.applyCLIDefaults must resolve
    # to bin=`claude` with args containing `-p` and `--model <model>`. The
    # fake is exposed in PATH under that name too, so the test exercises
    # the real argv shape Go assembles.
    Given a file ".review.yml" with content:
      """
      version: 1
      checks: { security: true, logic: false }
      llm:
        provider: claude
        worker:
          model: claude-sonnet-4-6
      """
    And the mock LLM is reset
    And the mock LLM is scripted with:
      | worker   | findings_json |
      | security | []            |
    And a diff that adds a file "hello.go" with content:
      """
      package main
      """
    When I run "accurate-reviewer review --diff -" with that diff on stdin
    Then the exit code is 0
    And the LLM CLI was invoked as "claude"
    And the LLM CLI args include "-p"
    And the LLM CLI args include "--model"
    And the LLM CLI args include "claude-sonnet-4-6"

  Scenario: The codex provider spawns "codex exec" without a model flag
    Given a file ".review.yml" with content:
      """
      version: 1
      checks: { security: true, logic: false }
      llm:
        provider: codex
        worker:
          model: o4-mini
      """
    And the mock LLM is reset
    And the mock LLM is scripted with:
      | worker   | findings_json |
      | security | []            |
    And a diff that adds a file "hello.go" with content:
      """
      package main
      """
    When I run "accurate-reviewer review --diff -" with that diff on stdin
    Then the exit code is 0
    And the LLM CLI was invoked as "codex"
    And the LLM CLI args include "exec"
    And the LLM CLI args do NOT include "--model"

  Scenario: A custom cli.bin overrides the per-provider default
    # Explicit `cli.bin` and `cli.args` win over the per-provider defaults
    # in config.applyCLIDefaults. We do not assert that `--model` is absent
    # here — that's a separate, orthogonal axis covered by the codex
    # scenario (where the provider default sets no model flag at all).
    Given a file ".review.yml" with content:
      """
      version: 1
      checks: { security: true, logic: false }
      llm:
        provider: claude
        worker:
          model: claude-sonnet-4-6
        cli:
          bin: ar-mock-cli
          args: ["--custom"]
      """
    And the mock LLM is reset
    And the mock LLM is scripted with:
      | worker   | findings_json |
      | security | []            |
    And a diff that adds a file "hello.go" with content:
      """
      package main
      """
    When I run "accurate-reviewer review --diff -" with that diff on stdin
    Then the exit code is 0
    And the LLM CLI was invoked as "ar-mock-cli"
    And the LLM CLI args include "--custom"

  Scenario: A markdown-wrapped JSON response is parsed into findings
    # Real claude almost always wraps its JSON in a ```json … ``` fence
    # even when the prompt forbids it. The worker must extract the array
    # instead of failing with "non-JSON response".
    Given a .review.yml with llm.provider set to "mock"
    And the mock LLM is reset
    And the mock LLM replies for "security" with the markdown-wrapped JSON:
      """
      [{"file":"a.go","line":3,"severity":"critical","title":"Wrapped finding","why":"works"}]
      """
    And the mock LLM is also scripted with:
      | worker | findings_json |
      | logic  | []            |
    And a diff that adds a file "a.go" with content:
      """
      package a
      func F() {}
      """
    When I run "accurate-reviewer review --diff -" with that diff on stdin
    Then the exit code is 1
    And the output contains "Wrapped finding"
    And the output contains "[critical]"

  Scenario: Prose around the JSON array does not break parsing
    # Some models prepend "Sure! Here are the findings:" before the array.
    # The worker must locate the outermost balanced [...] anyway.
    Given a .review.yml with llm.provider set to "mock"
    And the mock LLM is reset
    And the mock LLM replies for "security" with the prose-wrapped JSON:
      """
      Sure! Here are the issues I found:
      [{"file":"b.go","line":1,"severity":"medium","title":"Prose-prefixed finding","why":"still parses"}]
      Let me know if you need more detail.
      """
    And the mock LLM is also scripted with:
      | worker | findings_json |
      | logic  | []            |
    And a diff that adds a file "b.go" with content:
      """
      package b
      """
    When I run "accurate-reviewer review --diff -" with that diff on stdin
    Then the exit code is 0
    And the output contains "Prose-prefixed finding"

  Scenario: An empty response is treated as "no findings", not a parse error
    Given a .review.yml with llm.provider set to "mock"
    And the mock LLM is reset
    And the mock LLM is scripted with:
      | worker   | text |
      | security |      |
      | logic    |      |
    And a diff that adds a file "c.go" with content:
      """
      package c
      """
    When I run "accurate-reviewer review --diff -" with that diff on stdin
    Then the exit code is 0
    And the output contains "0 findings"

  Scenario: A non-zero exit from the CLI surfaces stderr verbatim to the master
    Given a .review.yml with llm.provider set to "mock"
    And the mock LLM is reset
    And the mock LLM is scripted with:
      | worker   | behaviour | payload         |
      | security | error     | invalid_api_key |
      | logic    | findings  | []              |
    And a diff that adds a file "d.go" with content:
      """
      package d
      """
    When I run "accurate-reviewer review --diff -" with that diff on stdin
    Then the exit code is 2
    And stderr contains "worker security failed: invalid_api_key"

  Scenario: A CLI that exceeds the configured timeout is killed and reported
    # delay_ms greater than timeout_seconds*1000 makes the fake hang past
    # the deadline; the Go provider must surface this as an error and the
    # master must surface it as a worker failure.
    Given a file ".review.yml" with content:
      """
      version: 1
      checks: { security: true, logic: false }
      llm:
        provider: mock
        cli:
          timeout_seconds: 1
      """
    And the mock LLM is reset
    And the mock LLM is scripted with:
      | worker   | findings_json | delay_ms |
      | security | []            | 3000     |
    And a diff that adds a file "e.go" with content:
      """
      package e
      """
    When I run "accurate-reviewer review --diff -" with that diff on stdin
    Then the exit code is 2
    And stderr contains "worker security failed"

  Scenario: pass_env forwards the named variable to the subprocess
    # The exec-provider must copy whitelisted env vars (e.g. API keys) into
    # the child while keeping unrelated variables out.
    Given the environment variable "MY_SECRET_TOKEN" is set to "sk-test-passthrough"
    And the environment variable "MY_PRIVATE_NOTE" is set to "should-not-leak"
    And a file ".review.yml" with content:
      """
      version: 1
      checks: { security: true, logic: false }
      llm:
        provider: mock
        cli:
          pass_env: ["MY_SECRET_TOKEN"]
      """
    And the mock LLM is reset
    And the mock LLM is scripted with:
      | worker   | findings_json |
      | security | []            |
    And a diff that adds a file "f.go" with content:
      """
      package f
      """
    When I run "accurate-reviewer review --diff -" with that diff on stdin
    Then the exit code is 0
    And the LLM CLI received env var "MY_SECRET_TOKEN" with value "sk-test-passthrough"
    And the LLM CLI did NOT receive env var "MY_PRIVATE_NOTE"

  Scenario: A bracketed prose prefix does not hijack JSON extraction
    # Real-world claude responses frequently open with phrases like
    # "Found [2] issues:" or "[WARNING] Several problems detected". The
    # previous extractor took the first '[' it saw and tried to parse
    # "[2]" or "[WARNING]" as a findings array — masking the real JSON
    # that came later. The fix must scan past those false starts.
    Given a .review.yml with llm.provider set to "mock"
    And the mock LLM is reset
    And the mock LLM replies for "security" with the prose-wrapped JSON:
      """
      Found [2] issues. [WARNING] Severity high.
      [{"file":"p.go","line":7,"severity":"high","title":"Bracket-prose finding","why":"survives prose with [brackets]"}]
      """
    And the mock LLM is also scripted with:
      | worker | findings_json |
      | logic  | []            |
    And a diff that adds a file "p.go" with content:
      """
      package p
      """
    When I run "accurate-reviewer review --diff -" with that diff on stdin
    Then the exit code is 0
    And the output contains "Bracket-prose finding"

  Scenario: Triple-backticks inside a JSON string do not truncate fence stripping
    # The closing ``` must be found on its own line — strings.LastIndex
    # was previously taking any rightmost ``` and chopping content mid-
    # string. The "why" field below contains a literal triple-backtick;
    # the worker must still parse the surrounding fence cleanly.
    Given a .review.yml with llm.provider set to "mock"
    And the mock LLM is reset
    And the mock LLM replies for "security" with the markdown-wrapped JSON:
      """
      [{"file":"t.go","line":4,"severity":"critical","title":"Interior fence finding","why":"use the ``` literal carefully"}]
      """
    And the mock LLM is also scripted with:
      | worker | findings_json |
      | logic  | []            |
    And a diff that adds a file "t.go" with content:
      """
      package t
      """
    When I run "accurate-reviewer review --diff -" with that diff on stdin
    Then the exit code is 1
    And the output contains "Interior fence finding"
    And the output contains "[critical]"

  Scenario: A prose-prefixed single object is still parsed as one finding
    # The "single object response" branch must work even when the response
    # opens with explanatory prose — find the outermost balanced {…},
    # don't just check `strings.HasPrefix(s, "{")`.
    Given a .review.yml with llm.provider set to "mock"
    And the mock LLM is reset
    And the mock LLM replies for "security" with the prose-wrapped JSON:
      """
      Here's the one issue I found:
      {"file":"u.go","line":2,"severity":"medium","title":"Prose-prefixed single object","why":"object form also parses"}
      """
    And the mock LLM is also scripted with:
      | worker | findings_json |
      | logic  | []            |
    And a diff that adds a file "u.go" with content:
      """
      package u
      """
    When I run "accurate-reviewer review --diff -" with that diff on stdin
    Then the exit code is 0
    And the output contains "Prose-prefixed single object"
    And the output contains "[medium]"

  Scenario: An LLM-supplied bogus severity is normalised, never bypasses blocking
    # A prompt-injected worker could return severity:"suppressed" and the
    # previous code path would rank it as 0, exiting clean despite a real
    # finding. Bogus values must collapse to a known severity rather than
    # passing through unchanged.
    Given a file ".review.yml" with content:
      """
      version: 1
      checks: { security: true, logic: false }
      severity: { blocking: critical }
      llm: { provider: mock }
      """
    And the mock LLM is reset
    And the mock LLM is scripted with:
      | worker   | findings_json |
      | security | [{"file":"v.go","line":1,"severity":"suppressed","title":"Bogus-severity finding","why":"should not exit clean"}] |
    And a diff that adds a file "v.go" with content:
      """
      package v
      """
    When I run "accurate-reviewer review --diff -" with that diff on stdin
    Then the exit code is 0
    And the output contains "Bogus-severity finding"
    And the output does NOT contain "[suppressed]"
    And the output contains "[low]"

  Scenario: ANSI/control-character escapes from the LLM never reach the terminal
    # A finding whose title contains a real ESC byte (0x1B) followed by
    # a CSI sequence would otherwise repaint the terminal. The reporter
    # must replace control characters with literal '?' before printing.
    Given a .review.yml with llm.provider set to "mock"
    And the mock LLM is reset
    And the mock LLM is scripted with:
      | worker   | findings_json |
      | security | [{"file":"w.go","line":1,"severity":"high","title":"Title\u001b[31m injected RED","why":"why\u001b[2J cleared"}] |
      | logic    | []            |
    And a diff that adds a file "w.go" with content:
      """
      package w
      """
    When I run "accurate-reviewer review --diff -" with that diff on stdin
    Then the exit code is 0
    And the output contains "Title?[31m injected RED"
    And the output contains "why?[2J cleared"

  Scenario: A __USED_TOKENS marker on stdout is stripped, not surfaced
    # The Go provider uses this marker to drive budget tests without a real
    # tokenizer. Real CLIs never emit it; the line must not leak into the
    # parsed text, and the token count must hit the budget.
    Given a file ".review.yml" with content:
      """
      version: 1
      checks: { security: true, logic: false }
      budget: { max_tokens: 100 }
      llm: { provider: mock }
      """
    And the mock LLM is reset
    And the mock LLM is scripted with:
      | worker   | findings_json | tokens |
      | security | []            | 250    |
    And a diff that adds files "g.go" and "h.go" with content "package x"
    When I run "accurate-reviewer review --diff -" with that diff on stdin
    Then the exit code is 2
    And the output contains "budget exceeded"
    And the output does NOT contain "__USED_TOKENS"
