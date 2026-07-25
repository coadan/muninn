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
- model/effort labels are bounded; lineage IDs are one-way digests used only
  for aggregation
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
muninn checkpoint before-tooling-change --repo .
muninn analyze --repo . --compare before-tooling-change
muninn analyze --repo . --overview
muninn analyze --repo . --since 24h --operations bwb
muninn analyze --repo . --focus structure
muninn analyze --repo . --focus output
muninn analyze --repo . --focus quality
muninn analyze --repo . --details
muninn failures --repo . --operation bwb/comments-wait --since 14d
muninn failures --repo . --operation bwb/test-nses --reason "test harness protocol" --json
muninn feedback add \
  --category roundtrip \
  --target bwb/pr \
  --signal existing-pr-create-failed \
  --source codex
muninn feedback list --repo .
muninn feedback resolve \
  --category roundtrip \
  --target bwb/pr \
  --signal existing-pr-create-failed
```

`muninn sessions` is a compatibility alias for `muninn analyze`.

`muninn failures` gives a bounded event timeline for one configured
`ownedTools.operations` ID. It refreshes the local index, defaults to 20
events, and caps `--limit` at 100. Its output is limited to timestamps, fixed
failure and command-family labels, output byte counts, and operation
attribution quality; it does not reveal raw session content or identifiers.

The default human report is findings-first. Use `--overview` for compact
family totals and `--details` for command, transition, source-target, failure,
and output rankings. `--focus friction` explicitly selects the same broad
action queue as the default report. Use `--focus` with `tooling`,
`instructions`, `interface`, `structure`, `discovery`, `failures`, `loops`, or
`quality` to narrow it without loading the aggregate overview or details. Use
`output` to isolate individual oversized tool responses. Findings are ordered
by most recent supporting activity, show a privacy-safe relative `Last seen`
age, and retain the exact timestamp in JSON. This keeps already-fixed
historical friction from outranking current work.

Checkpoint comparisons require the same rolling lookback or resolved
`--since-commit` boundary, task filter, archive inclusion, and finding focus.
Muninn rejects mismatched cohorts with the exact `--since` correction instead
of labeling incomparable rates as improvements or regressions.

Use `--operations <owned-tool>` for a bounded drill-down that contains only
that locally controlled tool's configured operation IDs, calls, sessions,
output, failures, truncations, and fixed failure-reason labels. This is the
compact bridge from an `owned-tool` finding to the exact operation worth
improving. Add `--details` to show every operation row, or combine it with an
explicit `--limit` to retain a chosen bound. Add `--json` when another local
tool will consume the result. `--focus <category> --details` is accepted as the
focused findings view because each finding already includes its full evidence;
it does not expand into unrelated aggregate rankings.

Muninn highlights:

- completion episodes segmented inside long-lived provider sessions, with
  response-only turns separated from tool-using work and window boundaries
  marked as left-censored
- p50/p75/p90 fresh-token, tool-call, output, duration, failure, and compaction
  outcomes for fully observed completed tool tasks
- model, reasoning-effort, and root/subagent cohorts with cost per completed
  tool task, task duration, and observed throughput
- delegation share, coordination cost, parent/child edit and read overlap,
  unattributed work, and observed child concurrency
- discovery, editing, verification, delivery, and review-driven rework phase
  outcomes, including phase-mix differences in the completed-task cost tail
- explicit finding attribution to tooling, instructions/docs, source code, or
  unknown, with confidence kept separate from impact
- post-delivery quality signals: pushes/lands, pre- and post-delivery review,
  review-to-edit cycles, and deliveries that required rework
- generic repository-relative delivery cohorts, with tests and review counted
  only when observed after the latest edit and before delivery
- verification effectiveness by configured test operation or generic test
  family, including failed runs, fail-fix-pass deliveries, and rework rates
  with versus without each check
- downstream delivery outcomes kept separate from review cleanup: terminal
  test/build/check failures, explicit reverts, exact-target fix cycles,
  checked redeliveries, recovery, and time-to-failure/recovery by cohort
- total, cached, uncached, and per-session input-token cost
- provider-specific root and scoped repository-instruction footprint, with a
  static token estimate that is stored in checkpoints
- fresh-token and visible tool-output volume
- long low-output tool waits, with tests, builds, and review waits reported
  separately from candidate progress stalls
- individual tool outputs of at least 30,000 bytes, capped to three actionable
  findings with privacy-safe context attribution; compound shell calls use a
  bounded command-family shape instead of collapsing into `mixed shell`
- privacy-safe tool and command-family attribution
- mixed commands bundled into one outer tool call
- substantive command-family transitions across separate tool calls
- fixed failure reasons and their privacy-safe command context
- repeated safe repository-relative read targets and current file size
- repeated or individually long inline code/orchestration payloads without
  retaining their content
- normalized friction reported directly by Codex, Claude, OpenCode, humans,
  or a provider-neutral agent

For repositories with BWB's `.worktrees/<task>/<repo>` plus
`.workbench/repos/<repo>` layout, Muninn canonicalizes task-worktree reads to
the cached repo owner. Identical source files therefore aggregate across tasks
instead of appearing as unrelated task-specific targets. Other repositories'
`.worktrees` paths are preserved unless that BWB layout is present.

Muninn includes recent tool events from sessions that started before the
lookback boundary. This avoids losing active work simply because a session is
long-lived.

The default SQLite index lives in the user cache directory. A cold analysis
first prefilters session metadata to the requested repository, then indexes
only attributable files. Subsequent reports reuse unchanged sources. Use
`--refresh` to rebuild selected repository sources, `--db <path>` to select a
different local store, or `--no-cache` for a direct scan.

Named checkpoints support before/after measurement. Use
`muninn checkpoint <name> [analysis flags]` for a quiet save without printing
the full report; `analyze --checkpoint` remains available when the report is
also useful. Trend output compares
normalized rates such as calls and compactions per session, output per call,
completion ratio, and failures/truncations per 1,000 calls.
Interactive `--refresh` runs emit one bounded start message and one completion
summary so long reclassification passes do not look stalled; JSON stays clean.

`muninn feedback` captures friction while it is fresh. It accepts only fixed
categories plus bounded logical target/signal labels; it cannot store prose,
commands, output, paths, URLs, prompts, session IDs, or secrets. Use
`--control local` for tooling you can change directly, `repository` for
repository interfaces or guidance, `third-party` for upstream dependencies,
and `unknown` when ownership still needs triage. Repeated reports from
different agent sources aggregate into one finding. Resolve the signal after
its improvement lands so checkpoint comparisons can show it disappearing.

## Repository guidance

Repositories can add `.muninn.json` to replace generic recommendations with
their preferred tooling surface:

```json
{
  "schemaVersion": 1,
  "suppressSignals": [
    "session-loop/progress-stall/repository-cli/status"
  ],
  "actions": {
    "sourceContext": "Use the repository's bounded source-context command."
  },
  "ownedTools": [
    {
      "id": "repository-cli",
      "repository": "path-or-logical-repo-name",
      "executables": ["repo-cli"],
      "operationsOnly": false,
      "operations": [
        {
          "id": "status",
          "args": ["task", "*", "status"]
        },
        {
          "id": "review-wait",
          "args": ["review", "--wait"],
          "expectedFailureReasons": ["timeout"]
        }
      ],
      "recommendation": "Prefer improving this locally controlled surface."
    }
  ]
}
```

Each finding prints a stable `Signal` ID. `suppressSignals` hides only exact
matching derived findings, which keeps false positives out of the action queue
without deleting their normalized metrics or checkpoint history. Prefer exact
IDs over suppressing a whole finding family.

Configuration does not weaken the privacy boundary or expose repository
command text from sessions. Owned-tool selectors are stored as one-way digests
in normalized events; reports use only the configured ID and logical
repository name.

Owned-tool attribution lets Muninn prioritize fixes with a short local path.
Optional operation patterns use anchored argument prefixes with `*` matching
one privacy-safe variable segment and `**` matching zero or more segments.
`expectedFailureReasons` accepts exact fixed Muninn failure labels for
operation outcomes that are intentionally non-actionable, such as an outer
tool-call timeout while a review waiter remains pending. The failures remain
in metrics and drill-down reports but do not create an actionable operation
finding or refresh its friction timestamp.
When patterns overlap, Muninn attributes an invocation only to the most
specific match; equally specific matches are retained when one command
deliberately carries multiple classified flags. Reports retain only configured
IDs such as `repository-cli/status`, never the matched arguments.
Set `operationsOnly` for a CLI reached through a shared launcher such as `npm`;
configured operations remain attributable without claiming every invocation of
the launcher as owned tooling.
When an outer tool call bundles several commands, output and failures are
reported as ambiguous instead of being charged to every matched operation.
Owned-operation findings require recurring actionable failures, truncation, or
material absolute and per-call output; frequent successful use alone is demand,
not friction. Generic test/build/check/verify nonzero exits remain product
evidence unless a more specific tooling failure reason is observed.
Use `--refresh` after changing operation patterns so cached source events are
reclassified.
Findings that do not map to an owned tool can instead recommend repository
guidance, an agent-optimized façade for a fragmented workflow, or an upstream
follow-up.

## Improvement loop

Muninn is intended to close a repository improvement loop:

1. Take a checkpoint with `muninn checkpoint <name>`.
2. Select one current, cross-session finding.
3. Improve owned tooling, instructions, an agent-facing interface, or source
   ownership.
4. Re-run the same window and compare the checkpoint.
5. Add an exact printed signal ID to `.muninn.json` only when a finding is a
   known false positive; the underlying trend data remains available.

Useful evidence includes repeated privacy-safe failure contexts across
sessions, recurring cross-call transitions, repeated context compactions,
large inline orchestration payloads, repeated source reads, and files that are
frequently searched or sliced together. Cache hits are reported separately:
they are beneficial by themselves, but high cached-context volume combined
with compactions and recurring loops indicates stale session context.
Agent-interface workflow findings include the three dominant normalized
transition families so the next tooling improvement can be selected without a
raw-log drilldown. Their aggregate session count is reported as a lower bound
because one session can contribute to several transitions in the same workflow.

The overview should lead with findings. Focused detail may identify safe
repository-relative source targets, but paths outside the analyzed repository
must remain private.

Completion episodes are observational task proxies, not semantic claims about
user intent. Muninn starts a new episode after each provider completion marker.
It excludes response-only and left-censored episodes from completed tool-task
distributions. Fresh-token tail associations compare operation, command-family,
and source-cohort prevalence in the highest-cost 10% with ordinary completed
tasks. They identify useful comparison cohorts, not causal overhead; one-call
associations may only mark a harder task class. Delivery rework similarly
measures sequence evidence; when it cannot distinguish tooling, guidance, and
source ownership, it reports `unknown` instead of guessing from review text.

Delivery cohorts use bounded repository-relative directory prefixes. Exact
rework hotspots are shown only for files that both belonged to the preceding
delivery and were edited after a review check. They work for ordinary
repositories and monorepos; nested repository managers are only
path-normalization inputs. Test/review comparisons are observational and do not
claim that the executed check covered the edited subsystem. Long-running tool
outputs count verification or delivery only at the terminal continuation, so
polling does not create duplicate check outcomes.

Downstream failures count only after a successful delivery and before unrelated
editing. Recovery requires an edit to an exact delivered target, the same
failed check passing, and another successful delivery. This deliberately
excludes ordinary review-driven cleanup and unrelated work in a long-lived
session.

Phase boundaries come from typed tool families, configured operations, edits,
terminal delivery, and post-delivery review. Token deltas belong to the latest
observable phase; they are useful comparative estimates, not exact attribution
of model-internal work.

Model and effort labels plus parent/child links come from the provider's local
state index and are joined to normalized rollouts at analysis time. Reports do
not expose the source paths or lineage keys. Speed and overlap are observational:
compare similar task cohorts before changing model or delegation policy.

## Direction

Muninn's local corpus is append-oriented and is often queried repeatedly by
repository, time window, task, command family, and failure reason. A
privacy-safe SQLite index is the intended primary store for incremental
ingestion and fast repeated reports. DuckDB may later consume exported
normalized data for larger cross-repository analysis; it is not needed in the
interactive CLI path.
