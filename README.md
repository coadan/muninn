# Muninn

<img src="assets/muninn-logo.png" alt="Muninn logo" width="512">

Muninn is a privacy-safe CLI for finding avoidable cost and friction in coding
agent sessions. It turns local session metadata into actionable signals about
tooling, instructions, code navigation, verification, and delivery rework.

Muninn currently ingests Codex sessions. Its normalized analysis is designed
to support additional providers without coupling reports to provider-specific
formats.

## Install

```bash
make install
```

This runs the test suite and installs `muninn` into `$(go env GOPATH)/bin`.

## Quick start

```bash
muninn analyze --repo .
muninn analyze --repo . --since 24h
muninn analyze --repo . --since 72h --compare-previous
muninn checkpoint before-change --repo .
muninn analyze --repo . --compare before-change
```

The default report leads with current findings. Narrow it when investigating a
specific kind of friction:

```bash
muninn analyze --repo . --focus tooling
muninn analyze --repo . --focus structure
muninn analyze --repo . --focus quality
muninn analyze --repo . --details
```

`--compare-previous` compares the current rolling lookback with the immediately
preceding window of the same size. The cohorts do not overlap, and the
comparison leads with completed-task duration and outer tool roundtrips.

Muninn does not report prompts, messages, raw commands, raw tool output,
absolute paths, secrets, or provider session identifiers.

## Documentation

- [Documentation index](docs/README.md)
- [Analysis and reports](docs/analysis.md)
- [Repository configuration](docs/repository-configuration.md)
- [Improvement workflow](docs/improvement-workflow.md)
- [Privacy and data model](docs/privacy.md)

Run `muninn help` for the complete command surface.

## Development

```bash
go test ./...
go build .
```
