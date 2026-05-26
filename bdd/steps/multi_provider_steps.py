"""Steps specific to the @multi-provider feature.

We assert on the basename the fake CLI was invoked under (`argv0`) rather
than the configured provider string, because the *interesting* question
is "which binary actually got spawned" — that's what proves the master
threaded the per-worker override all the way to the subprocess. The fake
captures `os.path.basename(sys.argv[0])` for every call, so the
assertion is independent of how we configure the override.
"""

from behave import then

from environment import mock_prompts


@then('every "{worker}" worker call used argv0 "{name}"')
def step_every_worker_used_argv0(context, worker, name):
    prompts = [p for p in mock_prompts(context) if p.get("worker") == worker]
    assert prompts, f"no prompts captured for worker {worker!r}"
    bad = [p for p in prompts if p.get("argv0") != name]
    assert not bad, (
        f"expected every {worker!r} call to use argv0={name!r}, "
        f"got argv0 values: {sorted({p.get('argv0') for p in bad})}"
    )


@then('the first two LLM calls used argv0 "{name}"')
def step_first_two_argv0(context, name):
    prompts = mock_prompts(context)
    assert len(prompts) >= 2, f"expected at least 2 LLM calls, got {len(prompts)}"
    for i, p in enumerate(prompts[:2]):
        assert p.get("argv0") == name, (
            f"call #{i + 1} used argv0={p.get('argv0')!r}, want {name!r}"
        )


@then('later LLM calls used argv0 "{name}"')
def step_later_argv0(context, name):
    prompts = mock_prompts(context)
    assert len(prompts) > 2, (
        f"expected >2 LLM calls so 'later' calls exist; got {len(prompts)}"
    )
    for i, p in enumerate(prompts[2:], start=3):
        assert p.get("argv0") == name, (
            f"call #{i} used argv0={p.get('argv0')!r}, want {name!r}"
        )
