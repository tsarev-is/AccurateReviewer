"""Steps specific to the @llm feature: assertions about what the mock CLI
provider was spawned with on each invocation, plus extra scripting helpers
that drive the response-shape edge cases (markdown fences, prose prefixes).

The fake CLI at bdd/_fake_cli.py records one JSON line per invocation in
$ACCURATE_REVIEWER_MOCK_PROMPT_LOG with the role/worker/model/argv/env it
saw at spawn time. These steps assert on the per-invocation metadata.
"""

import json

from behave import given, then

from environment import mock_prompts, mock_script


@then('every prompt logged to the mock LLM has model "{model}"')
def step_every_prompt_has_model(context, model):
    prompts = mock_prompts(context)
    assert prompts, "no prompts were logged — the mock CLI was never invoked"
    mismatched = [p for p in prompts if p.get("model") != model]
    assert not mismatched, (
        f"expected every prompt to have model {model!r}, "
        f"got mismatches: {[(p.get('worker'), p.get('model')) for p in mismatched]}"
    )


@then('every prompt logged to the mock LLM has role "{role}"')
def step_every_prompt_has_role(context, role):
    prompts = mock_prompts(context)
    assert prompts, "no prompts were logged — the mock CLI was never invoked"
    mismatched = [p for p in prompts if p.get("role") != role]
    assert not mismatched, (
        f"expected every prompt to have role {role!r}, "
        f"got mismatches: {[(p.get('worker'), p.get('role')) for p in mismatched]}"
    )


# ---------------------------------------------------------------------------
# Per-invocation argv / bin-name assertions
# ---------------------------------------------------------------------------


@then('the LLM CLI was invoked as "{name}"')
def step_invoked_as(context, name):
    prompts = mock_prompts(context)
    assert prompts, "no prompts were logged — the mock CLI was never invoked"
    actual = sorted({p.get("argv0", "") for p in prompts})
    assert actual == [name], (
        f"expected every invocation to use argv[0]={name!r}, "
        f"got distinct argv[0] values: {actual}"
    )


@then('the LLM CLI args include "{flag}"')
def step_args_include(context, flag):
    prompts = mock_prompts(context)
    assert prompts, "no prompts were logged — the mock CLI was never invoked"
    for p in prompts:
        assert flag in p.get("argv", []), (
            f"expected argv {p.get('argv')} to include {flag!r}"
        )


@then('the LLM CLI args do NOT include "{flag}"')
def step_args_excludes(context, flag):
    prompts = mock_prompts(context)
    assert prompts, "no prompts were logged — the mock CLI was never invoked"
    for p in prompts:
        assert flag not in p.get("argv", []), (
            f"argv {p.get('argv')} should not contain {flag!r}"
        )


# ---------------------------------------------------------------------------
# Env-passthrough assertions
# ---------------------------------------------------------------------------


@then('the LLM CLI received env var "{name}" with value "{value}"')
def step_env_received(context, name, value):
    prompts = mock_prompts(context)
    assert prompts, "no prompts were logged — the mock CLI was never invoked"
    for p in prompts:
        env = p.get("env", {})
        assert env.get(name) == value, (
            f"expected env[{name}]={value!r}, got {env.get(name)!r} "
            f"(worker={p.get('worker')})"
        )


@then('the LLM CLI did NOT receive env var "{name}"')
def step_env_absent(context, name):
    prompts = mock_prompts(context)
    assert prompts, "no prompts were logged — the mock CLI was never invoked"
    for p in prompts:
        env = p.get("env", {})
        assert name not in env, (
            f"env var {name!r} unexpectedly forwarded to the child "
            f"(value={env.get(name)!r}, worker={p.get('worker')})"
        )


# ---------------------------------------------------------------------------
# Response-shape scripting helpers
# ---------------------------------------------------------------------------
#
# These helpers append to whatever the previous scripted-with step set up.
# They have to read the existing script file rather than just calling
# mock_script() (which overwrites), because a feature commonly wants to
# script "this worker produces markdown-wrapped JSON" AND "this other
# worker produces []" in two steps.


def _read_script(context):
    raw = context.mock_script_path.read_text(encoding="utf-8") or "[]"
    try:
        return json.loads(raw)
    except json.JSONDecodeError:
        return []


def _write_script(context, entries):
    context.mock_script_path.write_text(json.dumps(entries), encoding="utf-8")


def _upsert(entries, worker, **fields):
    for e in entries:
        if e.get("worker") == worker:
            e.update(fields)
            return entries
    entries.append({"worker": worker, **fields})
    return entries


@given('the mock LLM replies for "{worker}" with the markdown-wrapped JSON')
def step_reply_markdown(context, worker):
    """Wrap the docstring body in a ```json fence — claude's habitual shape."""
    body = context.text.strip()
    wrapped = f"```json\n{body}\n```"
    entries = _upsert(_read_script(context), worker, text=wrapped)
    _write_script(context, entries)


@given('the mock LLM replies for "{worker}" with the prose-wrapped JSON')
def step_reply_prose(context, worker):
    """Use the docstring verbatim — it should contain `[…]` somewhere inside."""
    entries = _upsert(_read_script(context), worker, text=context.text.strip())
    _write_script(context, entries)


@given("the mock LLM is also scripted with")
def step_also_scripted(context):
    """Same shape as `the mock LLM is scripted with` but does NOT reset."""
    entries = _read_script(context)
    for row in context.table:
        e = {"worker": row["worker"]}
        if "findings_json" in row.headings:
            e["text"] = row["findings_json"]
        elif "findings" in row.headings:
            e["text"] = row["findings"] or "[]"
        elif "text" in row.headings:
            e["text"] = row["text"]
        if "behaviour" in row.headings:
            if row["behaviour"] == "error":
                e["error"] = row["payload"]
            elif row["behaviour"] == "findings":
                e["text"] = row["payload"]
        _upsert(entries, e["worker"], **{k: v for k, v in e.items() if k != "worker"})
    _write_script(context, entries)
