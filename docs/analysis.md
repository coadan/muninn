# Analysis and reports

Muninn indexes attributable coding-agent session metadata for one repository
and turns it into a ranked intervention queue. This page covers cohort and
report selection. See [Signal interpretation](signals.md) for the evidence
model, [Comparing periods](comparison.md) for longitudinal analysis, and
[Decretion experiments](decretion.md) for reversible subtraction tests.

## Select a cohort

Choose a horizon that matches the work being evaluated:

```bash
muninn analyze --repo .
muninn analyze --repo . --since 24h
muninn analyze --repo . --since 1d --compare previous
muninn analyze --repo . --since 7d --compare previous
muninn analyze --repo . --since-commit HEAD~3
muninn analyze --repo . --since-commit HEAD~3 --compare previous
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
- `--focus instructions` includes always-on and scoped instruction footprint,
  loaded-skill footprint, instruction and documentation routing gaps, and
  repeated skill-instruction reloads.
- `--focus loops` includes verification repair loops as well as session and
  interface loops.
- `--focus discovery` adds repository-relative read targets and the
  highest-output bundled search/read shapes. Compact JSON includes the top five
  as `focusEvidence`. Targets inside a managed product repository expose
  `repository` and a path relative to that repository; disposable cache and
  worktree prefixes remain internal. For every provider, a read in a task
  worktree that has since been removed still counts when the file exists at
  its canonical repository path.
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

## Provider cost and format drift

When a provider reports its own cost, the summary and each task carry
`providerCost`: the number of sessions with attributed cost, `costUSD`,
`apiDurationMs`, and `toolDurationMs`. Claude Code reports it; Codex does not,
and the field is then omitted rather than reported as zero. The values are the
provider's own cumulative counters, so they include requests that never appear
as transcript messages. A Claude Code root session's cost includes its
subagents.

Providers update cost coarsely, often once per session. Each growth is split
by the repository's share of the session's tokens since the previous update,
and a lookback window counts only growth observed inside it, at the time the
provider wrote it. Cost is not divided across individual completed tasks in
`outcomes`.

`providerFormatDrift` counts sessions, records, and message content blocks
whose type the provider adapter does not recognize. A non-zero value after a
provider update means some telemetry may be missing; it reports counts only.

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

Guidance load events remain cached, but loaded-skill token estimates are
resolved again at analysis time from the provider's current installed skill
roots. Editing an installed `SKILL.md` therefore updates footprint and
decretion evidence without `--refresh`. When no current installed artifact can
be resolved, Muninn retains the ingestion-time estimate rather than guessing.

## Skill cost and workflow loops

Use `muninn analyze --provider claude --repo . --since 7d --details` and inspect
`summary.skillLoads` (also present per task). Each skill separates:

- `estimatedTokens`, `estimatedLoadedTokens`, and `estimatedRedundantTokens`:
  loading overhead estimated from the current skill file, not historical
  tokenizer measurements or a billing amount.
- `usageAfterLoad`: recorded token increments, fresh tokens, tool calls,
  failed tool calls, review calls, review-to-edit cycles, and completed or
  incomplete turn counts after a successful load.

Usage starts at the successful load result, excluding the request that invoked
it, and stops at compaction or session end. Skills stay active across turn-end
markers, including user feedback and retakes in a multi-turn workflow.
Reloading an active skill does not
multiply its usage. Only loads observed in the selected repository and time
window qualify. Implausible token streams contribute no measured tokens.
Child sessions are not charged to their parent's skill.

Several active skills can observe the same subsequent work, so their counters
overlap and must not be added together. Later unrelated work in the same
context can also contribute: metadata cannot identify the semantic end of a
workflow. A completed turn is the provider's turn-end marker; it does not prove
the task met its acceptance criteria. A review-to-edit
cycle records sequence, not whether a review finding justified the edit.
Review calls use the existing command-family and configured-operation rules.
The standard `node <path>/workbench.mjs review` command is recognized directly;
configured operations ending in `/review` also count.

These are investigation leads: costly repeated review/edit cycles can identify
a process worth shortening without retaining review comments. Compare the same
skill before and after one small change, alongside repository checks and the
existing delivery-quality comparison. A lower token count alone is not proof
that the process improved.

## Error contract

Successful analysis and failure timelines emit JSON to stdout. Command errors
emit a compact JSON envelope to stderr with `schemaVersion`, `status: "error"`,
and fixed `error.code`, `error.message`, and `error.nextAction` fields. Explicit
`--help` remains human-readable. There is no output-format flag.
