"""Steps specific to the prompt-injection sanitizer feature."""

import subprocess

from behave import given, when, then

from environment import CLI_BIN, mock_prompts


@given("a code snippet")
def step_code_snippet(context):
    context.snippet = context.text


@when("I sanitize the snippet")
def step_sanitize(context):
    proc = subprocess.run(
        [str(CLI_BIN), "sanitize"],
        input=context.snippet.encode("utf-8"),
        capture_output=True,
    )
    context.sanitized = proc.stdout.decode("utf-8")


@then('the sanitized output starts with "{prefix}"')
def step_sanitized_startswith(context, prefix):
    assert context.sanitized.startswith(prefix), context.sanitized[:80]


@then('the sanitized output ends with "{suffix}"')
def step_sanitized_endswith(context, suffix):
    assert context.sanitized.rstrip().endswith(suffix), context.sanitized[-80:]


@then('the sanitized output contains "{needle}"')
def step_sanitized_contains(context, needle):
    assert needle in context.sanitized, context.sanitized


@then('the sanitized output does NOT contain the verbatim phrase "{needle}"')
def step_sanitized_not_contains_phrase(context, needle):
    assert needle not in context.sanitized, (
        f"verbatim phrase {needle!r} leaked through sanitizer\n{context.sanitized}"
    )


@then('the sanitized output contains the neutralised marker "{marker}"')
def step_sanitized_marker(context, marker):
    assert marker in context.sanitized, context.sanitized


@then('the sanitized output does NOT contain the neutralised marker "{marker}"')
def step_sanitized_no_marker(context, marker):
    assert marker not in context.sanitized, (
        f"unexpected neutralisation in sanitizer output:\n{context.sanitized}"
    )


@then('the prompts received by the mock LLM all contain "{needle}"')
def step_prompts_all_contain(context, needle):
    prompts = mock_prompts(context)
    assert prompts, "mock LLM received no prompts"
    for p in prompts:
        assert needle in p["prompt"], f"prompt missing {needle!r}:\n{p['prompt']}"


@then('none of the prompts received by the mock LLM contain the verbatim phrase "{needle}"')
def step_prompts_none_contain(context, needle):
    prompts = mock_prompts(context)
    for p in prompts:
        assert needle not in p["prompt"], (
            f"verbatim phrase {needle!r} leaked into LLM prompt:\n{p['prompt']}"
        )


@then('the sanitized output contains the phrase "{needle}" exactly {n:d} time')
@then('the sanitized output contains the phrase "{needle}" exactly {n:d} times')
def step_sanitized_count(context, needle, n):
    actual = context.sanitized.count(needle)
    assert actual == n, f"expected {needle!r} exactly {n}× in sanitized output, got {actual}\n{context.sanitized}"
