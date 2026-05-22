"""Steps specific to the diff-parsing feature."""

import json

from behave import then

from environment import run_cli


def _ensure_parsed(context):
    if context.last_units is None:
        out = context.last_run["stdout"]
        context.last_units = json.loads(out or "null") or []


@then("the parsed output contains exactly {n:d} review unit")
@then("the parsed output contains exactly {n:d} review units")
def step_parsed_n_units(context, n):
    _ensure_parsed(context)
    assert len(context.last_units) == n, (
        f"expected {n} units, got {len(context.last_units)}:\n"
        f"{json.dumps(context.last_units, indent=2)}"
    )


@then("review unit {i:d} has")
def step_review_unit_has(context, i):
    _ensure_parsed(context)
    unit = context.last_units[i]
    for row in context.table:
        field = row["field"]
        expected = row["value"]
        actual = unit.get(field)
        if isinstance(actual, list):
            actual_str = str(len(actual))
        elif isinstance(actual, bool):
            actual_str = "true" if actual else "false"
        else:
            actual_str = str(actual)
        assert actual_str == expected, (
            f'unit {i} field {field}: expected {expected!r}, got {actual_str!r}'
        )


@then('review unit {i:d}\'s file is "{name}"')
def step_unit_file(context, i, name):
    _ensure_parsed(context)
    assert context.last_units[i]["file"] == name


@then("the files of the review units are")
def step_units_files(context):
    _ensure_parsed(context)
    expected = [row["file"] for row in context.table]
    actual = [u["file"] for u in context.last_units]
    assert actual == expected, f"{actual} != {expected}"


@then("review unit {i:d}'s hunk {h:d} has {n:d} context lines before the change")
def step_hunk_context_before(context, i, h, n):
    _ensure_parsed(context)
    hunk = context.last_units[i]["hunks"][h]
    assert len(hunk["context_before"]) == n, hunk


@then("review unit {i:d}'s hunk {h:d} has {n:d} context lines after the change")
def step_hunk_context_after(context, i, h, n):
    _ensure_parsed(context)
    hunk = context.last_units[i]["hunks"][h]
    assert len(hunk["context_after"]) == n, hunk
