#!/usr/bin/env python3
"""Per-scenario osv-scanner stand-in.

This file is copied into each scenario's `workdir/bin/` as `osv-scanner`
so any subprocess that resolves the binary from PATH ends up exec'ing the
fake. We never call the real osv-scanner from the BDD suite — it would
hit the network and need a manifest that matches a real advisory.

Behaviour:
  1. Read the scripted JSON from $ACCURATE_REVIEWER_OSV_SCRIPT (default:
     empty results object). The file contains the literal osv-scanner
     --format json output a scenario wants the Go code to parse.
  2. Read the scripted exit code from
     $ACCURATE_REVIEWER_OSV_EXIT (default: 0).
  3. Append the invocation (argv, cwd) to
     $ACCURATE_REVIEWER_OSV_INVOCATIONS so steps can assert on what the
     review pipeline actually passed.
  4. Write the JSON to stdout and exit with the scripted code.
"""

import json
import os
import sys


def _append_invocation(path: str, argv: list, cwd: str) -> None:
    if not path:
        return
    with open(path, "a") as fh:
        fh.write(json.dumps({"argv": argv, "cwd": cwd}) + "\n")


def main() -> int:
    script_path = os.environ.get("ACCURATE_REVIEWER_OSV_SCRIPT", "")
    payload = '{"results": []}'
    if script_path and os.path.exists(script_path):
        with open(script_path) as fh:
            payload = fh.read()

    _append_invocation(
        os.environ.get("ACCURATE_REVIEWER_OSV_INVOCATIONS", ""),
        sys.argv,
        os.getcwd(),
    )

    sys.stdout.write(payload)
    if not payload.endswith("\n"):
        sys.stdout.write("\n")
    try:
        return int(os.environ.get("ACCURATE_REVIEWER_OSV_EXIT", "0"))
    except ValueError:
        return 0


if __name__ == "__main__":
    sys.exit(main())
