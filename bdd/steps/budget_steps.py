"""Steps specific to the @budget feature.

The fake CLI records each invocation's `argv` (every flag the Go provider
added to the child command line, including `--model <name>`) plus a
synthetic `model` field sourced from the ACCURATE_REVIEWER_MODEL env var.
We assert against argv here so the test would still catch a regression
where the master forgot to thread the new model into the actual command.
"""

from behave import then

from environment import mock_prompts


def _model_in_argv(prompt_record, model: str) -> bool:
    argv = prompt_record.get("argv") or []
    # The Go provider always emits the model right after the --model flag,
    # so a positional check would also work. Substring-search is enough
    # though, and keeps the assertion resilient if we ever add an extra
    # flag between them.
    return model in argv


@then('the first two LLM calls used model "{model}"')
def step_first_two_used_model(context, model):
    prompts = mock_prompts(context)
    assert len(prompts) >= 2, f"expected at least 2 LLM calls, got {len(prompts)}"
    for i, p in enumerate(prompts[:2]):
        assert _model_in_argv(p, model), (
            f"call #{i + 1} argv {p.get('argv')!r} does not contain model {model!r}"
        )


@then('later LLM calls used model "{model}"')
def step_later_used_model(context, model):
    prompts = mock_prompts(context)
    assert len(prompts) > 2, (
        f"expected at least 3 LLM calls so 'later' calls exist; got {len(prompts)}"
    )
    for i, p in enumerate(prompts[2:], start=3):
        assert _model_in_argv(p, model), (
            f"call #{i} argv {p.get('argv')!r} does not contain model {model!r}"
        )
