# Signal interpretation

Muninn reports recurring, attributable patterns that may increase cost per
completed task. Counts are evidence, not proof of causality.

## What Muninn models

Muninn keeps these activities distinct:

- completed tool tasks and response-only turns;
- fresh, cached, reasoning, and output tokens;
- discovery, editing, verification, delivery, and review-driven rework;
- actionable progress stalls, expected waits, and rapid continuation polling;
- test outcomes after the latest edit and before delivery;
- repeated exact configured checks without intervening edits;
- oversized output, truncation, recurring failures, and long inline scripts;
- context compactions attributed to the phase that preceded them;
- structured Heimdal failures and post-failure recovery cost;
- root-agent and delegated work, coordination, and overlap;
- repository navigation, repeated reads, and instruction footprint.

## Confidence and recurrence

Reading an authoritative manifest or document once per session is not
rediscovery friction. Instruction-discovery findings require repeated reads or
a search/read loop in at least three sessions and at least 20% of sessions that
read the owner. High confidence requires rediscovery in at least half.

Output-heavy bundled discovery remains low confidence when it appears in only
one session. The same recurrence rule prevents an isolated locally owned stall
from becoming a high-priority intervention.

Per-category caps retain the highest-impact evidence rather than merely the
newest evidence.

Owned-tool and excessive-operation findings use definitely attributed
invocations. Tools or operations that merely co-occur in a command bundle
remain drill-down evidence but do not establish attributable friction.
Definite and ambiguous bundled failures, truncation, and output cost remain
separate.

Tool-level failure and truncation findings require recurrence across sessions,
unless at least five definite events occur in one session. Successful
operation output must exceed both a 50,000-token floor and the cohort-relative
cost threshold; output-only operation findings remain medium priority rather
than inheriting highest priority solely from local ownership.

Candidate-default findings require a long flag on at least five definitely
attributed calls, at least two sessions, and 80% of the locally owned tool's
definite calls. Muninn retains only the normalized flag name, such as `json`;
it never retains the flag value. `--help` and `--version` are excluded because
they describe discovery rather than a workflow default.

Cross-call workflow findings require at least three matching transitions per
affected session as well as recurrence across three sessions. A normal
status-then-diff or search-then-read pair therefore remains evidence without
becoming an interface intervention.

Rapid-poll findings likewise require at least three short continuation polls
per affected session and at least five polls overall. Occasional early polling
remains in the metrics without competing with concentrated wait/resume loops.

Generic compaction findings require at least two compactions per affected
session, a pattern spanning at least five sessions and 20% of the cohort, or a
five-compaction single-session burst. Evidence reports fresh-token and cache
cost for affected sessions only. Phase-specific compaction evidence remains
available for completed tasks.

## Intervention construction

An intervention contains:

- a stable intervention ID and primary signal;
- any supporting signals;
- the likely improvement lever;
- confidence and explicit priority;
- recent supporting activity;
- a concise explanation and next action.

`highest` priority is reserved for high-confidence locally controlled changes.
Other high-confidence changes follow, then medium- and low-confidence
candidates. Evidence orders interventions within a priority tier; unlike
signal categories are not treated as one numeric scale.

Failures, output, waits, and delivery evidence for one configured local
operation consolidate at that exact operation. The compact queue leads with
one operation per tool while retaining other operations later in the queue.
Multiple findings for one exact file owner also consolidate: delivery quality
outranks task cost, which outranks navigation-only evidence. Component signal
IDs remain visible. A navigation-only owner remains medium confidence until
cost or quality evidence corroborates it.

Historical edit evidence only creates a current-owner intervention while that
repository-relative target still exists. Deleting or moving an owner therefore
resolves its old intervention instead of keeping a stale path in the queue.

## Operation cohorts

Detailed reports compare completed tasks that used a locally owned operation
with tasks that did not, within the same agent, model, reasoning-effort, and
task-family cohort. These with/without deltas are observational and never
create findings by themselves because tool use may correlate with unobserved
task difficulty.

## Quality and file hotspots

Muninn distinguishes healthy demand from actionable file friction.

- `healthy-demand`: frequently edited without material cost or quality
  evidence; never creates a finding.
- `expensive-owner`: at least three completed tasks and a median of at least
  50 outer tool roundtrips.
- `review/rework`: at least two attributed review or follow-up corrections.
- `downstream-risk`: at least two attributed downstream failures or reverts.

When deliveries fail a recurring downstream check without fresh
pre-verification, the check is the intervention target—not merely the most
frequently edited file cohort. If the check belongs to a configured local
tool, the quality evidence consolidates into that tool intervention.

A failed post-delivery check remains observational until matching follow-up
edits, redeliveries, or reverts recur for a material share of affected
deliveries. Limited correction evidence stays medium confidence.

## Delegation and diagnostics

Delegated sessions are classified by their strongest observable mode:
implementation, delivery, verification, research/review, other tool work, or
response only. This is descriptive. Coordination metrics are unavailable—not
zero—when lineage exists but provider events omit spawn, wait, or message
calls.

Failed Heimdal reports route by evidence: fixture startup failures point to
tooling, executed-test failures to source code, and selection failures to tests
or instructions. Recurring fingerprints become findings; one-off failures
remain in detailed reports.

## JSON detail levels

Default JSON includes compact scope, intervention, outcome, profile,
delegation, and diagnostic summaries. It omits task rows and high-cardinality
maps except bounded focus evidence.

Use `--details --json` for the complete analytical maps. `detailLevel` is
`summary` or `full`. Both `interventions` and constituent `findings` remain
available in the full report.
