# Muninn

<img src="assets/muninn-logo.png" alt="Muninn logo" width="512">

Agent harness session analysis for agentic improvement loops.

Muninn is a CLI for finding avoidable cost and friction in coding-agent
sessions. It turns local session metadata into actionable signals about tooling,
instructions, code navigation, verification, and delivery rework.

Muninn helps identify:

- locally controlled tools and operations with recurring failures, truncation,
  oversized output, or excessive repetition;
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

- `muninn analyze` returns a ranked intervention queue that consolidates
  related findings, and supports focused evidence, owned-operation drill-down,
  and adjacent-period comparison. `--details` exposes the constituent findings.
- `muninn failures --operation <tool/operation>` returns a bounded failure
  timeline for one configured locally controlled operation.

Narrow the analysis when investigating a specific kind of friction:

```bash
muninn analyze --repo . --focus tooling
muninn analyze --repo . --focus structure
muninn analyze --repo . --focus quality
muninn analyze --repo . --operation repository-cli/test
muninn analyze --repo . --details
muninn failures --repo . --operation repository-cli/test --since 14d
```

`--compare previous` compares the current rolling lookback with the immediately
preceding window of the same size. Choose `--since 1d` after a day of work or
`--since 7d` after a week. The cohorts do not overlap, and the comparison leads
with completed-task duration and outer tool roundtrips. When enough shared
evidence exists, it also compares matched agent, model, reasoning effort, and
task-family cohorts and gives a quality-adjusted verdict. The default trend
tracks stable intervention IDs, so changing supporting evidence does not look
like resolved-and-new work. Add `--details` to inspect raw finding churn.

Muninn does not report prompts, messages, raw commands, raw tool output,
absolute paths, secrets, or provider session identifiers.

## Documentation

- [Documentation index](docs/README.md)
- [Analysis and reports](docs/analysis.md)
- [Repository configuration](docs/repository-configuration.md)
- [Improvement workflow](docs/improvement-workflow.md)
- [Privacy and data model](docs/privacy.md)
- [Adding session providers](docs/session-providers.md)

Run `muninn --help` for the complete command surface.

## Development

```bash
go test ./...
go vet ./...
go build ./cmd/muninn
```

Muninn is released under the [MIT License](LICENSE).
