"""Shared step definitions: filesystem fixtures, CLI invocation, output assertions."""

import json
import os
import re
import shutil
import subprocess
from pathlib import Path

from behave import given, when, then, use_step_matcher

from environment import CLI_BIN, REPO_ROOT, run_cli, mock_reset, mock_script, mock_prompts


# ---------------------------------------------------------------------------
# Background / setup steps
# ---------------------------------------------------------------------------


@given("a temporary working directory")
def step_temp_dir(context):
    # before_scenario already created context.workdir
    assert context.workdir.exists()


@given("the accurate-reviewer binary is on PATH")
def step_binary_on_path(context):
    assert CLI_BIN.exists(), f"missing: {CLI_BIN}"


@given('a file "{path}" with content')
def step_file_with_content(context, path):
    target = context.workdir / path
    target.parent.mkdir(parents=True, exist_ok=True)
    # behave dedents the docstring's leading whitespace already.
    target.write_text(context.text, encoding="utf-8")


@given('a file "{path}" exists with content')
def step_file_exists_with_content(context, path):
    step_file_with_content(context, path)


@given('there is no .review.yml in the working directory')
def step_no_config(context):
    p = context.workdir / ".review.yml"
    if p.exists():
        p.unlink()


@given('a .review.yml with llm.provider set to "{provider}"')
def step_review_yml_provider(context, provider):
    (context.workdir / ".review.yml").write_text(
        "version: 1\n"
        "checks: { security: true, logic: true }\n"
        "severity: { blocking: critical }\n"
        f"llm: {{ provider: {provider} }}\n"
        "sanitizer: { enabled: true }\n"
        "secrets: { enabled: true, entropy_threshold: 4.5 }\n",
        encoding="utf-8",
    )


@given('a .review.yml with severity.blocking set to "{level}"')
def step_review_yml_blocking(context, level):
    (context.workdir / ".review.yml").write_text(
        "version: 1\n"
        "checks: { security: true, logic: true }\n"
        f"severity: {{ blocking: {level} }}\n"
        "llm: { provider: mock }\n",
        encoding="utf-8",
    )


@given('a .review.yml with sanitizer.enabled set to false')
def step_review_yml_sanitizer_off(context):
    (context.workdir / ".review.yml").write_text(
        "version: 1\n"
        "checks: { security: true, logic: true }\n"
        "llm: { provider: mock }\n"
        "sanitizer: { enabled: false }\n",
        encoding="utf-8",
    )


@given('a .review.yml that excludes "{pattern}"')
def step_review_yml_excludes(context, pattern):
    (context.workdir / ".review.yml").write_text(
        "version: 1\n"
        "checks: { security: true, logic: true }\n"
        "llm: { provider: mock }\n"
        f'exclude: [ "{pattern}" ]\n',
        encoding="utf-8",
    )


@given('a .review.yml that enables only the worker "{worker}"')
def step_review_yml_only_worker(context, worker):
    other = {"security": "logic", "logic": "security"}[worker]
    (context.workdir / ".review.yml").write_text(
        "version: 1\n"
        f"checks: {{ {worker}: true, {other}: false }}\n"
        "llm: { provider: mock }\n",
        encoding="utf-8",
    )


@given('a .review.yml with budget.max_tokens set to {n:d}')
def step_review_yml_budget(context, n):
    (context.workdir / ".review.yml").write_text(
        "version: 1\n"
        "checks: { security: true, logic: true }\n"
        "llm: { provider: mock }\n"
        f"budget: {{ max_tokens: {n} }}\n",
        encoding="utf-8",
    )


@given('the environment variable "{name}" is set to "{value}"')
def step_set_env(context, name, value):
    context.extra_env = getattr(context, "extra_env", {})
    context.extra_env[name] = value


# ---------------------------------------------------------------------------
# Mock LLM scripting
# ---------------------------------------------------------------------------


@given("the mock LLM is reset")
def step_mock_reset(context):
    mock_reset(context)


@given("the mock LLM is configured to return no findings")
def step_mock_no_findings(context):
    mock_reset(context)
    mock_script(context, [
        {"worker": "security", "text": "[]"},
        {"worker": "logic", "text": "[]"},
    ])


@given("the mock LLM records every prompt it receives")
def step_mock_record(context):
    mock_reset(context)


@given("the mock LLM records every worker call it receives")
def step_mock_record_workers(context):
    mock_reset(context)


@given("the mock LLM is scripted with")
def step_mock_scripted_table(context):
    mock_reset(context)
    entries = []
    for row in context.table:
        e = {"worker": row["worker"]}
        if "findings_json" in row.headings:
            e["text"] = row["findings_json"]
        elif "findings" in row.headings:
            e["text"] = row["findings"] or "[]"
        if "delay_ms" in row.headings and row["delay_ms"]:
            e["delay_ms"] = int(row["delay_ms"])
        if "behaviour" in row.headings:
            beh = row["behaviour"]
            if beh == "error":
                e["error"] = row["payload"]
            elif beh == "findings":
                e["text"] = row["payload"]
        entries.append(e)
    mock_script(context, entries)


@given("the mock LLM is scripted to always report {n:d} tokens used per call")
def step_mock_always_tokens(context, n):
    mock_reset(context)
    mock_script(context, [
        {"worker": "security", "text": "[]", "tokens": n},
        {"worker": "logic", "text": "[]", "tokens": n},
    ])


# ---------------------------------------------------------------------------
# Diff fixtures
# ---------------------------------------------------------------------------


@given('a diff that adds a file "{path}" with content')
def step_diff_adds_file(context, path):
    body = context.text
    lines = body.splitlines()
    diff = _make_add_diff(path, lines)
    context.last_diff = diff.encode("utf-8")


@given('a diff that adds files "{a}" and "{b}" with content "{content}"')
def step_diff_adds_two_files(context, a, b, content):
    d1 = _make_add_diff(a, [content])
    d2 = _make_add_diff(b, [content])
    context.last_diff = (d1 + d2).encode("utf-8")


def _make_add_diff(path: str, lines):
    n = len(lines)
    header = (
        f"diff --git a/{path} b/{path}\n"
        f"new file mode 100644\n"
        f"index 0000000..1111111\n"
        f"--- /dev/null\n"
        f"+++ b/{path}\n"
        f"@@ -0,0 +1,{n} @@\n"
    )
    return header + "".join("+" + ln + "\n" for ln in lines)


@given('a unified diff')
def step_unified_diff(context):
    context.last_diff = context.text.encode("utf-8")


@given('a unified diff stored at "{path}"')
def step_unified_diff_stored(context, path):
    target = context.workdir / path
    target.parent.mkdir(parents=True, exist_ok=True)
    target.write_text(context.text, encoding="utf-8")
    context.last_diff = context.text.encode("utf-8")


# ---------------------------------------------------------------------------
# CLI invocation
# ---------------------------------------------------------------------------


use_step_matcher("re")


@when(r'I run "(?P<cmdline>[^"]+)"')
def step_run(context, cmdline):
    extra_env = getattr(context, "extra_env", None)
    # If a previous step prepared a diff, pipe it on stdin when the command
    # asks for stdin via the conventional "-" positional. Commands that do
    # not read stdin (init, analyze, config show) ignore the input.
    stdin = None
    parts = cmdline.split()
    if "-" in parts[1:] and getattr(context, "last_diff", None) is not None:
        stdin = context.last_diff
    run_cli(context, cmdline, stdin=stdin, extra_env=extra_env)


use_step_matcher("parse")


@when('I run "{cmdline}" with that diff on stdin')
def step_run_with_diff_stdin(context, cmdline):
    extra_env = getattr(context, "extra_env", None)
    run_cli(context, cmdline, stdin=context.last_diff or b"", extra_env=extra_env)


@when('I pipe the file "{path}" into "{cmdline}"')
def step_pipe_file(context, path, cmdline):
    src = REPO_ROOT / path
    if not src.exists():
        # allow path relative to workdir
        src = context.workdir / path
    data = src.read_bytes()
    run_cli(context, cmdline, stdin=data)


@when('I run "{cmdline}" inside "{rel}"')
def step_run_inside(context, cmdline, rel):
    # Run inside the workdir copy of the fixture, not the actual repo, so
    # state writes (.review-cache/, .review.yml) do not leak between runs.
    cwd = context.workdir / rel
    cwd.mkdir(parents=True, exist_ok=True)
    context.sample_project_dir = cwd
    run_cli(context, cmdline, cwd=cwd)


@when('I run "{cmdline}" inside the sample project')
def step_run_inside_sample(context, cmdline):
    run_cli(context, cmdline, cwd=context.sample_project_dir)


# ---------------------------------------------------------------------------
# Output assertions
# ---------------------------------------------------------------------------


@then("the exit code is {n:d}")
def step_exit_code(context, n):
    assert context.last_run["returncode"] == n, (
        f"expected exit {n}, got {context.last_run['returncode']}\n"
        f"stdout:\n{context.last_run['stdout']}\n"
        f"stderr:\n{context.last_run['stderr']}\n"
    )


@then('the output contains "{needle}"')
def step_output_contains(context, needle):
    assert needle in context.last_run["stdout"], (
        f'expected stdout to contain {needle!r}\n'
        f'actual stdout:\n{context.last_run["stdout"]}'
    )


@then('the output does NOT contain "{needle}"')
def step_output_not_contains(context, needle):
    assert needle not in context.last_run["stdout"], (
        f'expected stdout to NOT contain {needle!r}\n'
        f'actual stdout:\n{context.last_run["stdout"]}'
    )


@then('stderr contains "{needle}"')
def step_stderr_contains(context, needle):
    assert needle in context.last_run["stderr"], (
        f'expected stderr to contain {needle!r}\n'
        f'actual stderr:\n{context.last_run["stderr"]}'
    )


@then('the output matches the regex "{pattern}"')
def step_output_regex(context, pattern):
    assert re.search(pattern, context.last_run["stdout"]), (
        f"regex {pattern!r} did not match stdout:\n{context.last_run['stdout']}"
    )


@then("the output contains the lines")
def step_output_contains_lines(context):
    out = context.last_run["stdout"]
    for row in context.table:
        line = row["line"]
        assert line in out, f"expected line {line!r} in stdout"


@then("the output contains")
def step_output_contains_table(context):
    out = context.last_run["stdout"]
    for row in context.table:
        line = row["line"]
        assert line in out, f"expected line {line!r} in stdout"


@then("the output contains the finding")
def step_output_contains_finding(context):
    # Unified for two output shapes:
    #   scan-secrets / review-pre-flight: "  [crit] file:line rule=X match=Y"
    #   master/worker review:             "  [crit] file:line Title"
    #                                     "      cwe=CWE-NN"
    #                                     "      why: ..."
    out = context.last_run["stdout"]
    for row in context.table:
        field = row["field"]
        value = row["value"]
        if field == "severity":
            needle = f"[{value}]"
        elif field == "line":
            needle_a = f":{value} "
            needle_b = f":{value}\n"
            assert needle_a in out or needle_b in out, (
                f"expected ':{value}' (followed by space/newline) in stdout\n{out}"
            )
            continue
        elif field in ("rule", "cwe"):
            needle = f"{field}={value}"
        else:
            needle = value
        assert needle in out, f"expected {needle!r} in stdout\n{out}"


@then('a file "{path}" exists in the working directory')
def step_file_exists(context, path):
    assert (context.workdir / path).exists(), f"missing: {context.workdir/path}"


@then('the file "{path}" exists')
def step_file_exists_any(context, path):
    full = _resolve_artifact(context, path)
    assert full.exists(), f"missing: {full}"


@then('the output still contains "{needle}"')
def step_output_still_contains(context, needle):
    assert needle in context.last_run["stdout"], (
        f'expected stdout to contain {needle!r}\n'
        f'actual stdout:\n{context.last_run["stdout"]}'
    )


@then('a file "{path}" exists in "{rel}"')
def step_file_exists_rel(context, path, rel):
    base = context.workdir / rel
    assert (base / path).exists(), f"missing: {base/path}"


@then('the file "{path}" contains the key "{key}"')
def step_file_contains_key(context, path, key):
    content = (context.workdir / path).read_text(encoding="utf-8")
    assert key in content, f'key {key!r} not found in {path}'


@then('the file "{path}" still contains "{needle}"')
def step_file_still_contains(context, path, needle):
    content = (context.workdir / path).read_text(encoding="utf-8")
    assert needle in content


@then('the file "{path}" contains "{needle}"')
def step_file_contains(context, path, needle):
    content = (context.workdir / path).read_text(encoding="utf-8")
    assert needle in content


@then('the file "{path}" does NOT contain "{needle}"')
def step_file_not_contains(context, path, needle):
    content = (context.workdir / path).read_text(encoding="utf-8")
    assert needle not in content, (
        f'expected file {path!r} to NOT contain {needle!r}\n'
        f'actual content:\n{content}'
    )


@then('no file "{name}" exists in the parent of the working directory')
def step_no_file_in_parent(context, name):
    candidate = context.workdir.parent / name
    assert not candidate.exists(), f"unexpected file at {candidate}"


@then('the JSON file "{path}" contains')
def step_json_file_contains(context, path):
    full = _resolve_artifact(context, path)
    data = json.loads(full.read_text(encoding="utf-8"))
    for row in context.table:
        actual = _resolve_json_path(data, row["path"])
        assert str(actual) == row["value"], (
            f'{path}: at {row["path"]} expected {row["value"]!r}, got {actual!r}'
        )


@then('the JSON file "{path}" has at least {n:d} entries in "{json_path}"')
def step_json_at_least_entries(context, path, n, json_path):
    full = _resolve_artifact(context, path)
    data = json.loads(full.read_text(encoding="utf-8"))
    actual = _resolve_json_path(data, json_path)
    assert isinstance(actual, list) and len(actual) >= n


def _resolve_artifact(context, path):
    # Look in: scenario sample_project_dir, the cwd of the last CLI run,
    # then plain workdir. The sample-project case is the trickiest because
    # `analyze inside "testdata/repos/sample-go"` writes the snapshot inside
    # that subdir, not at the workdir root.
    candidates = []
    if getattr(context, "sample_project_dir", None) is not None:
        candidates.append(context.sample_project_dir / path)
    if getattr(context, "last_run", None) and context.last_run.get("cwd"):
        candidates.append(Path(context.last_run["cwd"]) / path)
    candidates.append(context.workdir / path)
    for c in candidates:
        if c.exists():
            return c
    return candidates[-1]


def _resolve_json_path(data, path):
    cur = data
    parts = re.split(r"\.|(\[\d+\])", path)
    parts = [p for p in parts if p]
    for p in parts:
        if p.startswith("["):
            idx = int(p[1:-1])
            cur = cur[idx]
        else:
            cur = cur[p]
    return cur
