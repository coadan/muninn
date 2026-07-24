# Muninn

Muninn analyzes local coding-agent sessions for tooling friction and
cost-to-complete opportunities.

The name comes from one of Odin's ravens: Muninn is associated with memory,
which fits a tool that learns from completed work without exposing the work
itself.

## Status

Codex is the first supported session provider. The ingestion boundary is
provider-specific, while normalized analysis and reports are intended to be
shared by future Claude Code and OpenCode adapters.

Muninn is deliberately privacy-safe:

- prompts and conversation messages are not analyzed
- raw command arguments and tool output are not reported
- absolute paths and provider session IDs are not reported
- classifications use fixed command-family, output, and failure labels

## Install

```bash
make install
```

This tests Muninn and atomically installs the binary into
`$(go env GOPATH)/bin`.

## Use

```bash
muninn analyze --repo .
muninn analyze --repo . --since 24h
muninn analyze --repo . --task my-task
muninn analyze --repo . --since 14d --include-archived
muninn analyze --repo . --json
```

`muninn sessions` is a compatibility alias for `muninn analyze`.

The default human report highlights:

- fresh-token and visible tool-output volume
- privacy-safe tool and command-family attribution
- mixed commands bundled into one outer tool call
- substantive command-family transitions across separate tool calls
- fixed failure reasons and their privacy-safe command context

Muninn includes recent tool events from sessions that started before the
lookback boundary. This avoids losing active work simply because a session is
long-lived.

## Repository guidance

Repositories can add `.muninn.json` to replace generic recommendations with
their preferred tooling surface:

```json
{
  "schemaVersion": 1,
  "actions": {
    "sourceContext": "Use the repository's bounded source-context command."
  }
}
```

Configuration provides recommendations only. It does not weaken the privacy
boundary or expose repository command text from sessions.

## Direction

Muninn's local corpus is append-oriented and is often queried repeatedly by
repository, time window, task, command family, and failure reason. A
privacy-safe SQLite index is the intended primary store for incremental
ingestion and fast repeated reports. DuckDB may later consume exported
normalized data for larger cross-repository analysis; it is not needed in the
interactive CLI path.
