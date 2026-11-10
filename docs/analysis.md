# Analysis and reports

Muninn indexes attributable coding-agent session metadata for one repository
and turns it into a ranked intervention queue. This page covers cohort and
report selection. See [Signal interpretation](signals.md) for the evidence
model, [Comparing periods](comparison.md) for longitudinal analysis, and
[Decretion experiments](decretion.md) for reversible subtraction tests.

## Retained Workbench reviews

When the repository has `.workbench/tasks`, compact and detailed analysis also
include `workbenchReviews`. Muninn reads the latest retained result per task
and reviewer at analysis time, using its recorded date and the analysis time
window. Linked worktrees use their primary checkout's Workbench directory.
This section is repository-wide: provider and session `--task` filters do not
attribute these results to particular sessions or skills.

The summary counts passes, failed reviews, runner errors, blocking and
recommendation findings, escalations, total elapsed milliseconds and maximum
review duration. `byRole` uses fixed `code`, `qa`, `adr`, `jira` and `other`
buckets. Review text, identifiers, commands and paths are never reported or
stored in the session index. Malformed, oversized, symlinked or unreadable
results contribute to coverage counters rather than being treated as passes.

Receipt match counts compare result trees with each task's retained check
receipt. A match does not prove the current worktree is unchanged or that a
task succeeded. Workbench overwrites earlier results, so
`coverage: latest-per-task-reviewer` cannot show historical review rounds,
total review cost or tokens. A previous-period comparison includes this
snapshot only under `current`; it does not infer a historical baseline or
change the quality verdict. Session-observed review/edit loops remain separate.

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

## Context profile

Both compact and detailed reports include `contextProfile`. It describes the
input carried into model requests separately from cumulative token usage:

- `observedInputTokens`: the distribution of observed request input, including
  cached input.
- `firstObservedInputTokens`: one first observed request per session, only
  when that observation is inside the selected time window and repository
  scope. This is a starting-context proxy: it may already contain a task,
  inherited history, and injected guidance.
- `peakInputTokens`: one maximum observed input per sampled session.
- `segmentGrowthTokens`: peak input minus first input in each observed segment
  with at least two samples. Compaction, leaving repository scope, and the
  selected window bound segments; a completion alone does not reset context.
- `reportedWindowTokens`: model-window values attached to sampled requests
  where the provider reports them. These are not auto-compaction settings.

Each distribution has `count`, `p50`, `p75`, `p90`, and `max`. A `count` of zero
means no observations; its zero-valued percentiles are unavailable measurements.
`sessionsWithSamples` and `sessionsWithoutSamples` expose coverage. A session
already in progress at the lookback boundary can contribute request and growth
samples without contributing a first-observed sample.

`compactionRecovery` counts explicit compaction boundaries, fixed trigger labels
(`auto`, `manual`, or `unknown`), and coverage for before/after measurements.
The before value is the provider's pre-compaction count when available,
otherwise the latest observed input in the same repository context. The after
value is the first observed request following the boundary, not the exact size
of the compaction summary. `inputReductionTokens` uses paired before/after
values and keeps negative reductions when input grew. The source counts
`withReportedBeforeTokens` and `withBeforeSample` distinguish the before-value
bases; `pairedSamples` and `withAfterSample` expose missing follow-up data.

Recovery windows run from a boundary through the next completion marker after
observed request or tool activity. A new compaction, leaving repository scope,
or the report ending censors an unfinished window. Only completed windows
contribute distributions of request samples, tool-call attempts, read/search
attempts, failed calls, and elapsed seconds to completion. Failures of calls
started before the boundary and tool refusals do not count as recovery
failures. Elapsed time includes idle time. A completion marker is an analytical
proxy, not independent proof of successful delivery, and post-compaction work
does not establish that compaction caused rework or information loss.

Claude samples sum fresh input, cache creation, and cache reads once per
response message; streaming corrections update that observation. Codex samples
use `last_token_usage.input_tokens` only when cumulative input changes, avoiding
repeated telemetry notifications. Older Codex records without last-request
usage contribute no context sample; cumulative totals are never substituted.
Neither provider's context profile estimates missing startup components or
recommends a compaction threshold.

```bash
muninn analyze --provider claude --repo . --since 7d
muninn analyze --provider codex --repo . --since 7d
```

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

`unconfirmedLoadAttempts` counts candidate reads whose success cannot be
established from the outer result. Nested shell wrappers and compound commands
do not activate observation windows, even when the wrapper itself succeeds.
Native skill/file reads and standalone `cat` calls with literal, non-option
operands can activate
them after positive terminal-success evidence. Unknown or invalid status and
reads still pending at the observation boundary remain unconfirmed. Codex shell
status is separated from stdout, including structured results. Claude's optional
`is_error` follows its protocol: omission is valid success; explicit null is
unconfirmed.
Shell escapes, comments and expansion characters remain unconfirmed even when
quoted; recovering those reads would require more shell interpretation.
Successful background completion is matched to the originating load.
This conservative coverage avoids treating failed
nested reads as loaded instructions; zero observed usage is not proof of zero
cost when attempts are unconfirmed.

Usage starts at the successful load result and stops at compaction or session
end. `tokenBasis: telemetry-after-load-result` describes source event order,
not request-level attribution. Invocation telemetry emitted before the result
is excluded; if it arrives afterward, it is included. Loading estimates and
these observed tokens can therefore overlap and must not be added together.
Skills stay active across turn-end
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
