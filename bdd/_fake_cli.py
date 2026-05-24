#!/usr/bin/env python3
"""Per-scenario LLM CLI stand-in.

This file is symlinked into each scenario's `workdir/bin/` as `ar-mock-cli`,
`claude`, and `codex` so that any `llm.provider` value the feature picks ends
up exec'ing the same controllable fake. Real CLIs are not invoked from the
BDD suite — that would burn tokens and require a network.

Behaviour, end to end:

  1. read the entire prompt off stdin;
  2. look up the scripted reply for $ACCURATE_REVIEWER_WORKER in the JSON
     file at $ACCURATE_REVIEWER_MOCK_SCRIPT (missing entry => empty findings);
  3. append the prompt to $ACCURATE_REVIEWER_MOCK_PROMPT_LOG so step
     definitions can assert on which workers were called;
  4. sleep `delay_ms` if scripted;
  5. either print the scripted `text` to stdout (with a trailing
     `__USED_TOKENS=<n>` marker the Go provider knows to strip) or write
     the scripted `error` to stderr and exit non-zero.
"""

import json
import os
import sys
import time


def _load_script(path: str) -> dict:
    if not path or not os.path.exists(path):
        return {}
    with open(path) as fh:
        data = json.load(fh)
    return {entry["worker"]: entry for entry in data}


def _append_prompt(path: str, prompt: str, argv: list) -> None:
    if not path:
        return
    entry = {
        "role": os.environ.get("ACCURATE_REVIEWER_ROLE", ""),
        "worker": os.environ.get("ACCURATE_REVIEWER_WORKER", ""),
        "model": os.environ.get("ACCURATE_REVIEWER_MODEL", ""),
        "prompt": prompt,
        # argv[0] is the basename we were invoked as (`claude`, `codex`,
        # or `ar-mock-cli`); steps assert on it to prove the right
        # provider default was picked. argv[1:] captures `-p`, `--model`,
        # `exec`, etc. — everything the Go provider added to the call.
        "argv0": os.path.basename(argv[0]) if argv else "",
        "argv": argv[1:] if len(argv) > 1 else [],
        # Capture the full child environment so pass_env / env-isolation
        # scenarios can assert on what the Go provider actually forwarded.
        # PATH/HOME are filtered out — they are always present and just add
        # noise to log inspections.
        "env": {k: v for k, v in os.environ.items() if k not in ("PATH", "HOME")},
    }
    with open(path, "a") as fh:
        fh.write(json.dumps(entry) + "\n")


def main() -> int:
    prompt = sys.stdin.read()
    worker = os.environ.get("ACCURATE_REVIEWER_WORKER", "")

    script = _load_script(os.environ.get("ACCURATE_REVIEWER_MOCK_SCRIPT", ""))
    _append_prompt(
        os.environ.get("ACCURATE_REVIEWER_MOCK_PROMPT_LOG", ""),
        prompt,
        sys.argv,
    )

    entry = script.get(worker, {"text": "[]"})
    if entry.get("delay_ms"):
        time.sleep(entry["delay_ms"] / 1000.0)
    if entry.get("error"):
        sys.stderr.write(entry["error"])
        return 1
    text = entry.get("text", "[]")
    sys.stdout.write(text)
    if not text.endswith("\n"):
        sys.stdout.write("\n")
    tokens = entry.get("tokens", 0)
    if tokens:
        sys.stdout.write(f"__USED_TOKENS={tokens}\n")
    return 0


if __name__ == "__main__":
    sys.exit(main())
