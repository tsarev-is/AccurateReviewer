#!/usr/bin/env python3
"""Per-scenario fake for the task-fetch CLIs (`gh`, `jira`).

This script is copied into each scenario's `workdir/bin/` under the names
`gh` and `jira` so that whatever `integrations.<x>.cmd` resolves to ends up
exec'ing this controllable stand-in. Real `gh` / `jira` CLIs are never
called from the BDD suite (no network, no auth, no tokens burned).

Behaviour:

  1. read scripted entries from $ACCURATE_REVIEWER_TASK_FAKE_SCRIPT (a JSON
     object keyed by basename: {"gh": {...}, "jira": {...}});
  2. append our argv + name to $ACCURATE_REVIEWER_TASK_FAKE_INVOCATIONS so
     step definitions can assert how we were called and with which id;
  3. print the scripted `text` to stdout, or `error` to stderr (exit 1).
"""

import json
import os
import sys


def main() -> int:
    name = os.path.basename(sys.argv[0])
    script_path = os.environ.get("ACCURATE_REVIEWER_TASK_FAKE_SCRIPT", "")
    invocations_path = os.environ.get("ACCURATE_REVIEWER_TASK_FAKE_INVOCATIONS", "")

    script = {}
    if script_path and os.path.exists(script_path):
        with open(script_path) as fh:
            script = json.load(fh)

    if invocations_path:
        with open(invocations_path, "a") as fh:
            fh.write(json.dumps({"name": name, "argv": sys.argv[1:]}) + "\n")

    entry = script.get(name, {"text": ""})
    if entry.get("error"):
        sys.stderr.write(entry["error"])
        return int(entry.get("exit", 1))
    text = entry.get("text", "")
    sys.stdout.write(text)
    if text and not text.endswith("\n"):
        sys.stdout.write("\n")
    return 0


if __name__ == "__main__":
    sys.exit(main())
