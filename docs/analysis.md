# Analysis and reports

Muninn indexes attributable coding-agent session metadata for one repository
and reports patterns that may increase cost per completed task.

## Select a cohort

```bash
muninn analyze --repo .
muninn analyze --repo . --since 24h
muninn analyze --repo . --since 1d --compare previous
muninn analyze --repo . --since 7d --compare previous
muninn analyze --repo . --since-commit HEAD~3
muninn analyze --repo . --task task-id
muninn analyze --repo . --since 14d --include-archived
muninn analyze --repo . --json
```

Sessions that began before the lookback boundary still contribute recent tool
events. This keeps long-lived active work in the selected cohort.

## Choose a report

The default human report is a findings-first action queue.

- `--details` adds bounded command, transition, source-target, failure, and
  output rankings.
- `--focus friction` explicitly selects the default broad action queue.
- `--focus tooling`, `instructions`, `interface`, `structure`, `discovery`,
  `failures`, `loops`, `output`, or `quality` narrows the report.
- `--operation <tool-or-operation>` drills into configured locally controlled
  tooling without loading unrelated findings.

Examples:

```bash
muninn analyze --repo . --focus output
muninn analyze --repo . --operation repository-cli
muninn analyze --repo . --operation repository-cli/test --details
```

## Failure timelines

`muninn failures` gives a bounded event timeline for one configured operation:

```bash
muninn failures --repo . --operation repository-cli/test --since 14d
muninn failures --repo . --operation repository-cli/test --task task-id
muninn failures --repo . --operation repository-cli/test --reason "test harness protocol" --json
```

The timeline contains timestamps, fixed failure and command-family labels,
output byte counts, and attribution quality. It does not expose raw session
content or provider identifiers. The default limit is 20 events and the
maximum is 100.

## Signals Muninn models

Muninn separates activity that is often conflated:

- completed tool tasks and response-only turns;
- fresh, cached, and output tokens;
- discovery, editing, verification, delivery, and review-driven rework;
- actionable progress stalls, expected waits, and rapid continuation polling;
- test outcomes after the latest edit and before delivery;
- repeated exact configured checks without an intervening edit and
  pre-delivery checks associated with fewer downstream escapes;
- oversized output, truncation, repeated failures, and long inline scripts;
- context compactions attributed to discovery, editing, verification,
  delivery, rework, or delegation cost;
- structured Heimdal failure fingerprints, phases, diagnostic availability,
  and post-failure cost by model and effort;
- root-agent and delegated work, including coordination and overlap;
- repository navigation, repeated reads, and instruction footprint.

In `--details` output, Muninn also compares completed tasks that used a locally
owned operation with tasks that did not, within the same agent, model,
reasoning-effort, and task-family cohort. These with/without deltas are
observational and never create findings by themselves because tool use may
still correlate with unobserved task difficulty.

Findings include a stable signal ID, likely improvement lever, confidence, and
recent supporting activity. Counts alone do not establish causality.

Failed Heimdal reports are routed by evidence: fixture startup failures point
to tooling, executed-test failures to source code, and test-selection failures
to tests or instructions. Recurring fingerprints become findings; one-off
failures remain available in `--details` and JSON.

## Compare adjacent periods

Use `--compare previous` for an automatically matched, non-overlapping
performance comparison:

```bash
muninn analyze --repo . --since 1d --compare previous
muninn analyze --repo . --since 7d --compare previous
```

This compares the preceding horizon with the latest horizon. It reports
completed-task duration and outer tool roundtrips at p50 and p90, plus
cross-call transitions, repeated same-family transitions, rapid polls, and
progress stalls normalized by completed tool tasks. Quality comparison includes
pre-delivery test evidence, deliveries requiring review-driven fixes,
review-to-edit cycles, downstream failures, follow-up edits, recovery, and
reverts. Rate directions require at least ten observations in both periods.
Muninn separately compares cohorts with the same agent kind, model, reasoning
effort, and derived task family when each period has at least three completed
tasks in that cohort. Each matched cohort has equal weight in the aggregate
efficiency direction, so one high-volume task family does not dominate it. The
quality comparison further requires at least five deliveries in both periods
and only uses task families also present in the matched performance comparison.
The quality-adjusted verdict prefers these equal-weight matched quality rates
and distinguishes faster work with stable quality from work shifted into
review, failure, follow-up edits, or reverts. It falls back to repository-wide
quality rates when matched delivery evidence is insufficient.

Diagnostic fingerprints are compared by post-failure fresh tokens, tool calls,
and elapsed time per occurrence. States are `improving`, `unchanged`,
`regressed`, `new`, `resolved`, or `not-observed`. `resolved` requires a
current passed Heimdal report for the same rehashed test target; disappearance
alone is `not-observed`.

Task families are privacy-safe derived labels based on owned targets and
operation families; prompts and raw commands are not retained. Even matched
periods are observational rather than causal because task difficulty can still
vary inside a cohort. If matched evidence is unavailable, the verdict falls
back to aggregate task medians.

`--details` also reports frequently edited files with their completed-task
share, edit calls, roundtrip and duration distributions, and post-review edit
and downstream-failure counts. Each hotspot is classified as healthy demand,
an expensive owner, review/rework, or downstream risk. Edit frequency alone is
demand, not friction; do not restructure a healthy frequently edited owner
solely because it is popular.

Actionable hotspot classes also feed the default findings report. Expensive owners require at
least three completed tasks and a median of at least 50 roundtrips. Rework and
downstream-risk findings require at least two attributed observations. Healthy
demand never creates a finding. Each finding has a stable target-specific
signal so an intervention can be suppressed, compared, or observed as resolved
without conflating it with ordinary navigation findings for the same file.

When multiple findings point to the same exact file owner, Muninn reports one
primary owner finding. Delivery quality outranks task cost, which outranks
navigation-only evidence; every component signal ID remains visible as a
supporting signal. Corroboration raises impact and confidence and adds a
concise `Why this matters` explanation. An isolated repeated-navigation signal
is retained but demoted to medium confidence until cost or quality evidence
supports it.

## Local index

The SQLite index lives in the user cache directory. Cold analysis prefilters
session metadata to the requested repository before indexing attributable
files; later reports reuse unchanged sources.

```bash
muninn analyze --repo . --refresh
muninn analyze --repo . --db /path/to/index.sqlite
muninn analyze --repo . --no-cache
```
