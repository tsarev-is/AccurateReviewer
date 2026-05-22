"""Steps specific to the master + worker review feature.

The cross-feature step `the output contains the finding` lives in
common_steps.py.
"""

from behave import then

from environment import mock_prompts


@then('the report contains exactly {n:d} finding for "{loc}"')
def step_report_n_findings_for(context, n, loc):
    out = context.last_run["stdout"]
    count = sum(1 for line in out.splitlines() if loc in line and line.lstrip().startswith("["))
    assert count == n, f"expected {n} findings at {loc}, got {count}\n{out}"


@then('the surviving finding has severity "{level}"')
def step_surviving_severity(context, level):
    out = context.last_run["stdout"]
    assert f"[{level}]" in out, f"severity {level} not in:\n{out}"


@then("the workers called on the mock LLM are exactly")
def step_workers_called(context):
    prompts = mock_prompts(context)
    actual = sorted({p["worker"] for p in prompts if p["worker"]})
    expected = sorted(row["worker"] for row in context.table)
    assert actual == expected, f"workers called: {actual}, expected: {expected}"


@then("the total wall-clock time is less than {n:d} ms")
def step_wallclock(context, n):
    elapsed_ms = context.last_run["elapsed"] * 1000
    assert elapsed_ms < n, f"elapsed {elapsed_ms:.0f}ms, expected < {n}ms"
