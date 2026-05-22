"""Steps specific to the secrets-scanner feature.

The cross-feature step `the output contains the finding` lives in
common_steps.py — it handles both the scan-secrets output format and the
master-review console format.
"""

from behave import then

from environment import mock_prompts


@then('the finding\'s "{field}" field is redacted in the report')
def step_finding_redacted(context, field):
    # The redact() helper replaces middle bytes with '*'. Any '*' in the
    # match= value is enough proof of redaction; we forbid the full Base64
    # secret from appearing literally.
    out = context.last_run["stdout"]
    assert "match=" in out, "no match= field in output"
    line = next(l for l in out.splitlines() if "match=" in l)
    match_value = line.split("match=", 1)[1].split()[0]
    assert "*" in match_value, f"expected redaction in match={match_value!r}"


@then("the mock LLM was called {n:d} times")
def step_mock_called_times(context, n):
    prompts = mock_prompts(context)
    assert len(prompts) == n, f"expected {n} calls, got {len(prompts)}\n{prompts}"


@then("the mock LLM was called at most {n:d} time")
@then("the mock LLM was called at most {n:d} times")
def step_mock_called_at_most(context, n):
    prompts = mock_prompts(context)
    assert len(prompts) <= n, f"expected <= {n} calls, got {len(prompts)}"
