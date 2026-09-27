---
name: meaningful-tests
description: Use when writing or changing automated tests.
---

# Meaningful tests

Write the smallest set of tests that protects important behavior against plausible regressions. A test earns its maintenance cost when it can catch a specific bug that static checks and existing tests would miss.

## Find the risk first

Inspect the requirement, the changed production code, and nearby tests before writing a test. Follow the repository's test conventions and commands.

List the distinct behavioral risks in the change. Spend tests on consumer-visible contracts, decisions, boundaries, state transitions, side effects, failure handling, and past bugs. Merge risks that one scenario can prove without hiding the test's purpose.

For each candidate test, answer these questions before writing its body:

1. What realistic production-code mutation would make this test fail?
2. Why would that mutation be a bug rather than an intentional redesign?
3. What observable result proves the behavior?
4. Do existing tests, the type system, the compiler, a schema validator, or a linter already catch it?

Write the test only when the answers identify a gap. Otherwise, skip it, merge it into a stronger scenario, or move the assertion to the consumer boundary where the bug becomes visible.

Static definitions, trivial accessors, pass-through calls, framework behavior, deleted behavior, and source-text shape normally have no independent behavioral risk. Test them only when they enforce a runtime contract that a consumer can observe.

## Test through a stable boundary

Choose the narrowest stable boundary that proves the behavior. "Narrowest" does not mean one class in isolation. A small integration or component test is often cheaper and more trustworthy than a unit test surrounded by mocks.

Drive the code as its consumer does:

- Call a public API and assert its returned value, state change, emitted event, persisted data, or external request.
- For UI behavior, use accessible roles, labels, visible content, and realistic user actions.
- For a command, assert its exit status, output, and filesystem or process effects.
- For an external integration, assert the request and response contract at the adapter boundary.

Keep private methods, internal state, component structure, call order, and framework plumbing outside the assertion unless the interaction itself is the public contract.

## Keep the oracle independent

Derive expected results by hand from the requirement. Prefer literals and small, hand-checked fixtures. Never compute the expected result with production helpers or by repeating the algorithm under test.

Assert the complete meaningful result when the whole object is the contract. Use focused assertions when unrelated fields may change independently. Make assertion failures explain which behavior broke.

Keep test bodies direct. A little repetition is cheaper than helpers, loops, conditionals, or builders that hide inputs and expected outputs. Extract setup only when the helper removes irrelevant detail and its name states the resulting condition.

## Use test doubles at system boundaries

Keep real, deterministic, in-process collaborators when they make the test clearer. Replace a dependency when it is external, slow, nondeterministic, destructive, or needed to force a rare failure.

Place the double at the narrowest external boundary. Preserve the real code that transforms inputs, coordinates domain behavior, and produces the result under test.

Before adding a double:

1. Identify the real dependency's outputs and side effects used by this scenario.
2. Make the fixture match the real contract, including every field the exercised path may read.
3. Assert the component's outcome. Assert calls, arguments, or order only when those interactions are themselves required behavior.

If mock setup is harder to understand than the scenario, move the test boundary outward and use more real components. Put cleanup and controls needed only by tests in test utilities, not in production APIs.

## Prove that the test has signal

For a bug fix or new behavior developed test-first, run the focused test before the production change and confirm that it fails for the expected reason. A crash during setup, a missing fixture, or an unrelated assertion is not a useful red state.

When the implementation already exists, perform a safe mutation check when practical. Temporarily introduce the realistic mistake named by the test, run the focused test, confirm the expected failure, and restore the production code. If changing the working tree would risk user work, reason through the mutation instead and state that limitation.

Run the smallest relevant test command first. Then run the repository's required broader checks. Keep tests deterministic, isolated from execution order, and free of real time, random data, shared mutable state, and live services unless the chosen test level explicitly requires them.

## Finish with a test-value review

Before finishing, remove or combine any test that:

- has no named realistic bug;
- fails only when an intentional implementation detail changes;
- duplicates protection already supplied by a stronger test or a static check;
- can pass while the required consumer-visible behavior is broken;
- mainly verifies a mock, fixture, helper, or third-party framework;
- exists only to increase coverage.

Report the behavior protected, the focused command you ran, and its result. Mention a tempting test you omitted only when the omission may surprise the user.
