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
muninn analyze --repo . --since-commit HEAD~3
muninn analyze --repo . --task my-task
muninn analyze --repo . --since 14d --include-archived
muninn analyze --repo . --json
muninn analyze --repo . --checkpoint before-tooling-change
muninn analyze --repo . --compare before-tooling-change
muninn analyze --repo . --overview
muninn analyze --repo . --focus structure
muninn analyze --repo . --details
```

`muninn sessions` is a compatibility alias for `muninn analyze`.

The default human report is findings-first. Use `--overview` for compact
family totals and `--details` for command, transition, source-target, failure,
and output rankings. Use `--focus` with `tooling`, `instructions`, `interface`,
`structure`, `discovery`, `failures`, or `loops` to narrow the action queue
without loading details.

Muninn highlights:

- fresh-token and visible tool-output volume
- privacy-safe tool and command-family attribution
- mixed commands bundled into one outer tool call
- substantive command-family transitions across separate tool calls
- fixed failure reasons and their privacy-safe command context
- repeated safe repository-relative read targets and current file size
- repeated or individually long inline code/orchestration payloads without
  retaining their content

Muninn includes recent tool events from sessions that started before the
lookback boundary. This avoids losing active work simply because a session is
long-lived.

The default SQLite index lives in the user cache directory. A cold analysis
first prefilters session metadata to the requested repository, then indexes
only attributable files. Subsequent reports reuse unchanged sources. Use
`--refresh` to rebuild selected repository sources, `--db <path>` to select a
different local store, or `--no-cache` for a direct scan.

Named checkpoints support before/after measurement. Trend output compares
normalized rates such as calls and compactions per session, output per call,
completion ratio, and failures/truncations per 1,000 calls.

## Repository guidance

Repositories can add `.muninn.json` to replace generic recommendations with
their preferred tooling surface:

```json
{
  "schemaVersion": 1,
  "actions": {
    "sourceContext": "Use the repository's bounded source-context command."
  },
  "ownedTools": [
    {
      "id": "repository-cli",
      "repository": "path-or-logical-repo-name",
      "executables": ["repo-cli"],
      "operations": [
        {
          "id": "status",
          "args": ["task", "*", "status"]
        }
      ],
      "recommendation": "Prefer improving this locally controlled surface."
    }
  ]
}
```

Configuration provides recommendations only. It does not weaken the privacy
boundary or expose repository command text from sessions. Owned-tool selectors
are stored as one-way digests in normalized events; reports use only the
configured ID and logical repository name.

Owned-tool attribution lets Muninn prioritize fixes with a short local path.
Optional operation patterns use exact argument prefixes with `*` matching one
privacy-safe variable segment. Reports retain only configured IDs such as
`repository-cli/status`, never the matched arguments. Patterns may overlap to
provide both broad and targeted views, so operation totals are not additive.
Use `--refresh` after changing operation patterns so cached source events are
reclassified.
Findings that do not map to an owned tool can instead recommend repository
guidance, an agent-optimized façade for a fragmented workflow, or an upstream
follow-up.

## Improvement loop

Muninn is intended to close a repository improvement loop:

1. Take a checkpoint.
2. Select one current, cross-session finding.
3. Improve owned tooling, instructions, an agent-facing interface, or source
   ownership.
4. Re-run the same window and compare the checkpoint.
5. Suppress findings whose current target is gone, bounded, or no longer
   recurring.

Useful evidence includes repeated privacy-safe failure contexts across
sessions, recurring cross-call transitions, repeated context compactions,
large inline orchestration payloads, repeated source reads, and files that are
frequently searched or sliced together. Cache hits are reported separately:
they are beneficial by themselves, but high cached-context volume combined
with compactions and recurring loops indicates stale session context.

The overview should lead with findings. Focused detail may identify safe
repository-relative source targets, but paths outside the analyzed repository
must remain private.

## Direction

Muninn's local corpus is append-oriented and is often queried repeatedly by
repository, time window, task, command family, and failure reason. A
privacy-safe SQLite index is the intended primary store for incremental
ingestion and fast repeated reports. DuckDB may later consume exported
normalized data for larger cross-repository analysis; it is not needed in the
interactive CLI path.
