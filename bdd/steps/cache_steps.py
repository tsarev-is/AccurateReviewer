"""Steps for the findings-cache feature.

The prompt-log file (`.ar-mock-prompts.jsonl`) grows by one line per LLM
call. Tracking its line count between runs is the cheapest way to assert
"how many LLM calls did this run trigger" without parsing run output.
"""

from behave import given, when, then


def _prompt_log_lines(context):
    raw = context.mock_prompt_log.read_text(encoding="utf-8")
    return [ln for ln in raw.splitlines() if ln.strip()]


@given("a .review.yml that disables the findings cache")
def step_review_yml_no_cache(context):
    (context.workdir / ".review.yml").write_text(
        "version: 1\n"
        "checks: { security: true, logic: true }\n"
        "llm: { provider: mock }\n"
        "cache: { enabled: false }\n",
        encoding="utf-8",
    )


@when("I record the prompt-log size")
def step_record_prompt_log_size(context):
    context.recorded_prompt_count = len(_prompt_log_lines(context))


@then("the prompt-log has not grown since the recorded size")
def step_prompt_log_unchanged(context):
    actual = len(_prompt_log_lines(context))
    assert actual == context.recorded_prompt_count, (
        f"prompt log grew from {context.recorded_prompt_count} to {actual} entries — "
        f"expected the second review to be served entirely from cache"
    )


@then("the prompt-log has grown by at least {n:d} entries")
def step_prompt_log_grew(context, n):
    actual = len(_prompt_log_lines(context))
    delta = actual - context.recorded_prompt_count
    assert delta >= n, (
        f"prompt log grew by {delta} entries (from {context.recorded_prompt_count} to {actual}), "
        f"expected at least {n}"
    )
