"""Steps for the optional task/issue context feature.

These steps mutate two scenario-scoped files:

  - .review.yml: written with both the mock LLM provider and an integration
    command, since both pieces of config matter at once.
  - .ar-task-fake-script.json: scripted output for the `gh`/`jira` fakes
    the harness places in workdir/bin (see _task_fake_cli.py).

Assertions then either re-read the worker prompt log (via mock_prompts)
or the task-fake invocations log to verify the wiring end-to-end.
"""

import json

from behave import given, then

from environment import mock_prompts


def _write_config(context, *, integrations=None):
    body = (
        "version: 1\n"
        "checks: { security: true, logic: true }\n"
        "severity: { blocking: critical }\n"
        "llm: { provider: mock }\n"
        "sanitizer: { enabled: true }\n"
        "secrets: { enabled: true, entropy_threshold: 4.5 }\n"
    )
    if integrations:
        body += "integrations:\n"
        for kind, cmd in integrations.items():
            quoted = ", ".join(f'"{part}"' for part in cmd)
            body += f"  {kind}:\n    cmd: [{quoted}]\n"
    (context.workdir / ".review.yml").write_text(body, encoding="utf-8")


@given('a .review.yml with llm.provider "mock" and {kind} integration command "{cmd}"')
def step_review_yml_with_integration(context, kind, cmd):
    _write_config(context, integrations={kind: cmd.split()})


@given('the task-fetch CLI "{name}" is scripted to print')
def step_task_fake_script_text(context, name):
    script = _load_task_script(context)
    script[name] = {"text": context.text}
    context.task_fake_script.write_text(json.dumps(script), encoding="utf-8")


@given('the task-fetch CLI "{name}" is scripted to fail with "{message}"')
def step_task_fake_script_error(context, name, message):
    script = _load_task_script(context)
    script[name] = {"error": message, "exit": 1}
    context.task_fake_script.write_text(json.dumps(script), encoding="utf-8")


@then('every worker prompt contains "{needle}"')
def step_every_worker_prompt_contains(context, needle):
    prompts = mock_prompts(context)
    assert prompts, "no worker prompts were captured"
    for p in prompts:
        assert needle in p["prompt"], (
            f"worker {p['worker']!r} prompt missing {needle!r}:\n{p['prompt']}"
        )


@then('no worker prompt contains "{needle}"')
def step_no_worker_prompt_contains(context, needle):
    prompts = mock_prompts(context)
    assert prompts, "no worker prompts were captured"
    for p in prompts:
        assert needle not in p["prompt"], (
            f"worker {p['worker']!r} prompt unexpectedly contains {needle!r}:\n{p['prompt']}"
        )


@then('the task-fetch CLI "{name}" was invoked with the id "{wanted}"')
def step_task_fake_invoked_with_id(context, name, wanted):
    raw = context.task_fake_invocations.read_text(encoding="utf-8").strip()
    invocations = [json.loads(line) for line in raw.splitlines() if line.strip()]
    matches = [i for i in invocations if i["name"] == name]
    assert matches, f"{name} was not invoked; invocations: {invocations}"
    assert any(wanted in i["argv"] for i in matches), (
        f"{name} was invoked but argv never contained {wanted!r}: {matches}"
    )


def _load_task_script(context):
    raw = context.task_fake_script.read_text(encoding="utf-8").strip() or "{}"
    return json.loads(raw)
