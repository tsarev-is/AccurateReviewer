"""Steps specific to the full (megadiff) review mode."""

from behave import given


@given('a binary file "{path}" exists in the working directory')
def step_binary_file(context, path):
    target = context.workdir / path
    target.parent.mkdir(parents=True, exist_ok=True)
    # PNG signature + a few NUL bytes — enough for the Go heuristic to
    # classify this as binary and skip it on the full-mode walk.
    target.write_bytes(b"\x89PNG\r\n\x1a\n\x00\x00\x00\x0dIHDR\x00\x00")
