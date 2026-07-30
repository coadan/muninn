# Muninn

<img src="assets/muninn-logo.png" alt="Muninn logo" width="512">

Agent harness session analysis for agentic improvement loops.

Muninn is a CLI for finding avoidable cost and friction in coding-agent
sessions. It turns local session metadata into actionable signals about tooling,
instructions, code navigation, verification, and delivery rework.

Muninn helps identify:

- locally controlled tools and operations with recurring failures, truncation,
  oversized output, or excessive repetition;
- recurring three-operation chains that suggest a missing combined command or
  a workflow boundary that should own the sequence;
- opaque non-zero exits that lack a specific failure contract, plus unchanged
  retries that repeatedly reproduce the same owned-operation failure;
- long CLI flags supplied on at least 80% of locally owned tool calls across
  multiple sessions, which may belong in defaults or repository inference;
- progress stalls, rapid polling, abandoned continuations, and costly
  cross-call workflow loops;
- repeated source discovery and navigation into owners associated with high
  task cost, review rework, or downstream failures;
- oversized always-on instructions and recurring instruction-discovery gaps;
- repeated exact verification without intervening edits, expensive repair
  loops, and checks whose absence is associated with downstream escapes;
- workflow phases that concentrate context compactions and their observed
  fresh-token, output, and roundtrip cost;
- delegated work split into implementation, delivery, verification,
  research/review, other-tool, and response-only modes, with lineage coverage,
  parent overlap, concurrency, and explicit coordination observability;
- performance changes across matched agent, model, reasoning-effort, and task
  cohorts without ignoring observed delivery quality, plus observational
  with/without comparisons for owned operations.

These signals distinguish tool-interface, repository-structure, instruction,
verification, and delivery problems so improvements target the system that
produced the friction.

Muninn currently ingests Codex sessions. Additional coding harnesses plug in
through one declarative discovery/normalization adapter; storage, repository
scoping, analysis, findings, and reports stay independent of the source format.

## Install

```bash
go install github.com/coadan/muninn/cmd/muninn@latest
```

To install from a source checkout:

```bash
make install
```

This runs the test suite and installs `muninn` into `$(go env GOPATH)/bin`.

## Quick start

```bash
muninn
muninn analyze --repo . --since 24h
muninn analyze --repo . --since 1d --compare previous
muninn analyze --repo . --since 7d --compare previous
```

The main surface has two commands:

- `muninn analyze` returns a compact JSON intervention queue that consolidates
  related findings, and supports focused evidence, owned-operation drill-down,
  and adjacent-period comparison. Each intervention provides its bounded
  `focus` route. `--details` selects the full JSON report.
- `muninn failures <tool/operation>` returns bounded, separately attributed
  definite and ambiguous JSON failure timelines for one configured locally
  controlled operation.

Narrow the analysis when investigating a specific kind of friction:

```bash
muninn analyze --repo . --focus tooling
muninn analyze --repo . --focus structure
muninn analyze --repo . --focus quality
muninn analyze --repo . --operation repository-cli/test
muninn analyze --repo . --details
muninn failures repository-cli/test --repo . --since 14d
```

`--compare previous` returns the current rolling lookback and the immediately
preceding window of the same size in one JSON report. Choose `--since 1d` after
a day of work or `--since 7d` after a week. The cohorts do not overlap. The
report retains completed-task, agent, model, reasoning-effort, task-family, and
delivery-quality evidence for both periods. Structured `trends` include
completed-task cost, matched performance and quality, and normalized rates;
diagnostic fingerprints and stable intervention IDs have their own comparison
states. Add `--details` for the full reports behind both cohorts.

Muninn does not report prompts, messages, raw commands, raw tool output,
absolute paths, secrets, or provider session identifiers.

## Documentation

- [Documentation index](docs/README.md)
- [Analysis and reports](docs/analysis.md)
- [Signal interpretation](docs/signals.md)
- [Comparing periods](docs/comparison.md)
- [Repository configuration](docs/repository-configuration.md)
- [Improvement workflow](docs/improvement-workflow.md)
- [Privacy and data model](docs/privacy.md)
- [Adding session providers](docs/session-providers.md)

Run `muninn --help` for the complete command surface.

## Development

```bash
make help
make check
make build
```

Muninn is released under the [MIT License](LICENSE).
