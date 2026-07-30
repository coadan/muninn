# Analysis and reports

Muninn indexes attributable coding-agent session metadata for one repository
and turns it into a ranked intervention queue. This page covers cohort and
report selection. See [Signal interpretation](signals.md) for the evidence
model and [Comparing periods](comparison.md) for longitudinal analysis.

## Select a cohort

Choose a horizon that matches the work being evaluated:

```bash
muninn analyze --repo .
muninn analyze --repo . --since 24h
muninn analyze --repo . --since 1d --compare previous
muninn analyze --repo . --since 7d --compare previous
muninn analyze --repo . --since-commit HEAD~3
muninn analyze --repo . --task task-id
muninn analyze --repo . --since 14d --include-archived
```

Sessions that began before the lookback boundary still contribute recent tool
events, so long-lived work remains in the selected cohort.

## Choose a view

The default report is compact JSON containing a bounded queue of distinct
interventions. Related findings that point to the same locally controlled
tool, owner, discovery workflow, or verification workflow are consolidated.
Each intervention includes a `focus` value that can be passed directly to
`muninn analyze --focus <value>` for the bounded analytical view that owns it.

- `--details` selects the full JSON report.
- `--focus friction` explicitly selects the broad default queue.
- `--focus tooling`, `instructions`, `interface`, `structure`, `discovery`,
  `failures`, `loops`, `output`, or `quality` selects one concern.
- `--focus loops` includes verification repair loops as well as session and
  interface loops.
- `--focus discovery` adds repository-relative read targets and the
  highest-output bundled search/read shapes. Compact JSON includes the top five
  as `focusEvidence`. Targets inside a managed product repository expose
  `repository` and a path relative to that repository; disposable cache and
  worktree prefixes remain internal.
- `--operation <tool-or-operation>` drills into one configured locally
  controlled tool or operation.

```bash
muninn analyze --repo . --focus output
muninn analyze --repo . --focus structure
muninn analyze --repo . --operation repository-cli
muninn analyze --repo . --operation repository-cli/test --details
muninn analyze --repo . --details
```

Operation associations are not physical tool-call counts: one outer call can
match multiple operations, and `operationsOnly` tools intentionally do not
claim every invocation of a shared executable. A tool-level drill-down reports
owned calls, matched associations, and unmatched calls separately. The
`(unmatched)` row retains failure, truncation, and output totals so incomplete
operation configuration cannot hide the evidence behind a tool-level finding.

## Inspect failure events

`muninn failures` returns a bounded, privacy-safe event timeline for one
configured operation:

```bash
muninn failures repository-cli/test --repo . --since 14d
muninn failures repository-cli/test --repo . --task task-id
muninn failures repository-cli/test --repo . \
  --reason "test harness protocol"
```

The JSON report separates `definiteEvents` from `ambiguousEvents`, so failures
elsewhere in a mixed command cannot be mistaken for failures of the selected
operation. Each class is bounded independently; ambiguous evidence is capped
at five and cannot crowd authoritative events out of the requested `--limit`.
The summary reports total and returned counts for both classes.

Events contain timestamps, fixed failure and command-family labels, output byte
counts, and attribution quality. They never contain raw session content or
provider identifiers. The default definite-event limit is five and the maximum
is 100.

## Control the local index

The disposable SQLite index lives in the user cache directory. Each repository
gets an isolated derived view of normalized sessions, targets, and configured
operations. A session contributes when its initial CWD or a tool working
directory enters the repository; activity attributed elsewhere stays excluded.

```bash
muninn analyze --repo . --refresh
muninn analyze --repo . --db /path/to/index.sqlite
muninn analyze --repo . --no-cache
```

Unrelated and unchanged sources are negatively cached. Complete discovery
prunes sources that disappeared or left the selected provider scope.
Incompatible schemas rebuild the derived cache instead of carrying migration
compatibility.

## Error contract

Successful analysis and failure timelines emit JSON to stdout. Command errors
emit a compact JSON envelope to stderr with `schemaVersion`, `status: "error"`,
and fixed `error.code`, `error.message`, and `error.nextAction` fields. Explicit
`--help` remains human-readable. There is no output-format flag.
