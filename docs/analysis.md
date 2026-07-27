# Analysis and reports

Muninn indexes attributable coding-agent session metadata for one repository
and reports patterns that may increase cost per completed task.

## Select a cohort

```bash
muninn analyze --repo .
muninn analyze --repo . --since 24h
muninn analyze --repo . --since 72h --compare-previous
muninn analyze --repo . --since-commit HEAD~3
muninn analyze --repo . --task task-id
muninn analyze --repo . --since 14d --include-archived
muninn analyze --repo . --json
```

Sessions that began before the lookback boundary still contribute recent tool
events. This keeps long-lived active work in the selected cohort.

## Choose a report

The default human report is a findings-first action queue.

- `--overview` shows compact family totals.
- `--details` adds bounded command, transition, source-target, failure, and
  output rankings.
- `--focus friction` explicitly selects the default broad action queue.
- `--focus tooling`, `instructions`, `interface`, `structure`, `discovery`,
  `failures`, `loops`, `output`, or `quality` narrows the report.
- `--operations <tool-or-operation>` drills into configured locally controlled
  tooling without loading unrelated findings.

Examples:

```bash
muninn analyze --repo . --overview
muninn analyze --repo . --focus output
muninn analyze --repo . --operations repository-cli
muninn analyze --repo . --operations repository-cli/test --details
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
- oversized output, truncation, repeated failures, and long inline scripts;
- root-agent and delegated work, including coordination and overlap;
- repository navigation, repeated reads, and instruction footprint;
- explicit privacy-safe friction feedback from agents or people.

Findings include a stable signal ID, likely improvement lever, confidence, and
recent supporting activity. Counts alone do not establish causality.

## Compare adjacent periods

Use `--compare-previous` for a non-overlapping performance comparison without
creating a checkpoint first:

```bash
muninn analyze --repo . --since 72h --compare-previous
```

This compares the preceding 72 hours with the latest 72 hours. It reports
completed-task duration and outer tool roundtrips at p50 and p90, plus
cross-call transitions, repeated same-family transitions, rapid polls, and
progress stalls normalized by completed tool tasks. Quality comparison includes
pre-delivery test evidence, deliveries requiring review-driven fixes,
review-to-edit cycles, downstream failures, follow-up edits, recovery, and
reverts. Rate directions require at least ten observations in both periods.
The periods can still contain different task or model mixes, so the result is
observational rather than causal.

`--details` also reports frequently edited files with their completed-task
share, edit calls, roundtrip and duration distributions, and post-review edit
count. Edit frequency alone is demand, not friction. Use the table to find
targets where demand also correlates with high task cost or review rework;
do not restructure a healthy frequently edited owner solely because it is
popular.

## Local index

The SQLite index lives in the user cache directory. Cold analysis prefilters
session metadata to the requested repository before indexing attributable
files; later reports reuse unchanged sources.

```bash
muninn analyze --repo . --refresh
muninn analyze --repo . --db /path/to/index.sqlite
muninn analyze --repo . --no-cache
```

`muninn sessions` remains a compatibility alias for `muninn analyze`.
