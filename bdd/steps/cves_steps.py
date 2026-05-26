"""Steps specific to the @cves feature.

The fake osv-scanner script reads its scripted JSON payload from
$ACCURATE_REVIEWER_OSV_SCRIPT and records invocations to
$ACCURATE_REVIEWER_OSV_INVOCATIONS. Steps here write the payload and read
the invocation log.

Scripting is done at JSON granularity rather than via individual fields
so the scenarios drive the same osv-scanner output shape the real CLI
emits — keeping the parser honest.
"""

import json

from behave import given, then


def _write_osv_script(context, payload: dict) -> None:
    context.osv_script.write_text(json.dumps(payload), encoding="utf-8")


@given('the osv-scanner fake is scripted to report a {severity} vuln in "{manifest}" affecting "{pkg_at_version}" ({advisory_id}, fixed in {fixed_in})')
def step_osv_script_one_vuln(context, severity, manifest, pkg_at_version, advisory_id, fixed_in):
    name, _, version = pkg_at_version.partition("@")
    payload = {
        "results": [
            {
                "source": {"path": manifest, "type": "lockfile"},
                "packages": [
                    {
                        "package": {
                            "name": name,
                            "version": version,
                            "ecosystem": "Go",
                        },
                        "vulnerabilities": [
                            {
                                "id": advisory_id,
                                "summary": f"Vulnerability {advisory_id} in {name}",
                                "aliases": [],
                                "affected": [
                                    {
                                        "ranges": [
                                            {
                                                "type": "SEMVER",
                                                "events": [
                                                    {"introduced": "0"},
                                                    {"fixed": fixed_in},
                                                ],
                                            }
                                        ]
                                    }
                                ],
                                "database_specific": {"severity": severity},
                                "severity": [],
                            }
                        ],
                    }
                ],
            }
        ]
    }
    _write_osv_script(context, payload)
    context.osv_exit = "1"  # osv-scanner exits 1 when vulns are present


@then("the osv-scanner fake was not invoked")
def step_osv_not_invoked(context):
    raw = context.osv_invocations.read_text(encoding="utf-8").strip()
    assert raw == "", (
        f"expected no osv-scanner invocations, got:\n{raw}"
    )


@then("the osv-scanner fake was invoked at least once")
def step_osv_invoked(context):
    raw = context.osv_invocations.read_text(encoding="utf-8").strip()
    assert raw != "", "expected osv-scanner to have been invoked"
