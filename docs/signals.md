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
- recurring three-step chains of definitely attributed configured operations.
- owned operations whose recurring failures lack an actionable fixed
  classification.

## Confidence and recurrence

Reading an authoritative manifest or document once per session is not
rediscovery friction. Instruction-discovery findings require repeated reads or
a search/read loop without an observed edit in at least three sessions and at
least 20% of sessions that read the owner. Sessions that edit the target remain
visible as implementation evidence but do not count as rediscovery friction.
High confidence requires unedited rediscovery in at least half.

Output-heavy bundled discovery remains low confidence when it appears in only
one session. The same recurrence rule prevents an isolated locally owned stall
from becoming a high-priority intervention.

Per-category caps retain the highest-impact evidence rather than merely the
newest evidence.

File-cost findings are observational correlations, not proof that source
structure caused task cost. A standalone `code-structure/file-cost` signal is
therefore low priority until another finding corroborates the same owner; it
remains available in the detailed report for the required ownership trace.

Owned-tool findings use definitely attributed invocations. Excessive-operation
findings use definitely attributed successful invocations so failure-repair
loops are not also described as redundant final verification. Tools or
operations that merely co-occur in a command bundle remain drill-down evidence
but do not establish attributable friction. Definite and ambiguous bundled
failures, truncation, and output cost remain separate.

Tool- and operation-level recurring failure and truncation findings require
events across at least two sessions. Five or more definite events concentrated
in one session remain visible as medium-priority burst evidence, but are not
described or ranked as cross-session recurrence. Successful operation output
must exceed both a 50,000-token floor and the cohort-relative cost threshold;
output-only operation findings remain medium priority rather than inheriting
highest priority solely from local ownership.

When at least three failures across two sessions use `other non-zero exit`, and
that generic label accounts for at least half of an owned operation's
actionable failures, Muninn flags the operation's diagnostic contract. The
repair target is a stable machine-readable failure class and concise
next-action metadata, not another prose pattern in Muninn.

Candidate-default findings require a long flag on at least five definitely
attributed calls, at least two sessions, and 80% of the locally owned
operation's definite calls (or the tool's calls when no operation matches).
Muninn retains only the normalized operation and flag names, such as
`repository-cli/check/quiet`; it never retains the flag value. `--help` and
`--version` are excluded because they describe discovery rather than a
workflow default. For an
`operationsOnly` shared launcher, fixed flags in the matched operation prefix
belong to the launcher and are excluded; flags passed to the owned command
remain eligible.

Cross-call workflow findings require at least three matching transitions per
affected session as well as recurrence across three sessions. A normal
status-then-diff or search-then-read pair therefore remains evidence without
becoming an interface intervention. Git inspection transitions use only fixed
sub-operation labels—`status`, `diff`, `history`, `refs`, or `search`—so
repeated change inspection is actionable without retaining revisions, paths,
patterns, or other arguments.

Owned-operation chain findings use exactly three adjacent outer calls with one
definitely attributed configured operation per call. Ambiguous command bundles,
unowned calls, non-adjacent rounds, and chains containing only one repeated
operation are excluded. A chain must recur across at least two sessions, with
at least six occurrences and three occurrences per affected session. Reports
retain only configured operation IDs; raw commands and arguments are never
retained.

Rapid-poll findings likewise require at least three short continuation polls
per affected session and at least five polls overall. Occasional early polling
remains in the metrics without competing with concentrated wait/resume loops.
The target is the owned operation when available; otherwise it is the
privacy-safe continuation API, such as `write_stdin` or `wait`, rather than the
unrelated command family being resumed.

Generic compaction findings require at least two compactions per affected
session, a pattern spanning at least five sessions and 20% of the cohort, or a
five-compaction single-session burst. Evidence reports fresh-token and cache
cost for affected sessions only. Phase-specific compaction findings require
pressure in at least two sessions; their evidence reports both completed-task
episodes and actual session recurrence.

Concurrent-batch output findings retain the number of nested calls, never
their code or arguments. Evidence reports average visible output per nested
call and the largest batch size so agents can divide a shared stage budget
without serializing independent work. The default concurrent stage budget is
12,000 visible tokens; the action divides that budget by the largest observed
batch size to suggest an even per-result cap.

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
deliveries. Limited correction evidence stays medium confidence. Downstream
quality accounting requires an observed task-local edit before delivery;
left-censored or coordination-only delivery commands cannot establish that a
changed delivery caused a later failure.

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

Default JSON includes compact scope, outcome, profile, delegation, diagnostic,
and top-five intervention summaries. `totalInterventions` reports the full
queue size. The compact report omits constituent findings, task rows, and
high-cardinality maps except bounded focus evidence.

Use `--details` for the complete analytical maps. `detailLevel` is `summary`
or `full`. Both the complete `interventions` queue and constituent `findings`
are available in the full report. Adjacent-period comparisons contain one
report at the selected detail level for each non-overlapping cohort.
