"""Steps specific to the @dedupe-groups feature.

The grouping itself is tested via the JSON report (existing `the JSON
file "..." contains` step in common_steps.py) and the console output
(plain `output contains` steps). The only step that lives here is the
fixture that materialises a *pre-grouped* JSON report for the
post-comments fan-out scenario — none of the existing JSON-report
fixtures know about the `occurrences` field.
"""

import json

from behave import given


@given('a JSON report with one grouped finding at "{primary}" and an occurrence at "{occ}" stored at "{path}"')
def step_json_grouped_report(context, primary, occ, path):
    pf, pl = primary.split(":")
    of, ol = occ.split(":")
    report = {
        "schema_version": 1,
        "blocking_severity": "critical",
        "reviewed": [pf, of],
        "findings": [
            {
                "file": pf,
                "line": int(pl),
                "severity": "critical",
                "title": "SQL injection",
                "why": "concatenated input across two sites",
                "cwe": "CWE-89",
                "worker": "security",
                "occurrences": [
                    {"file": of, "line": int(ol)},
                ],
            }
        ],
    }
    (context.workdir / path).write_text(json.dumps(report), encoding="utf-8")
