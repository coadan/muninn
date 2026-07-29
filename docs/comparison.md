# Comparing periods

Use `--compare previous` to compare adjacent, non-overlapping horizons:

```bash
muninn analyze --repo . --since 1d --compare previous --human
muninn analyze --repo . --since 7d --compare previous --human
```

Choose one day after concentrated session work and one week for a broader
workflow change. The preceding horizon is the baseline and the latest horizon
is current.

## Performance evidence

Muninn compares completed-task duration and outer tool roundtrips at p50 and
p90. It also normalizes cross-call transitions, repeated same-family
transitions, rapid polls, and progress stalls by completed tool tasks.

Matched cohorts require the same agent kind, model, reasoning effort, and
derived task family, with at least three completed tasks in each period. Each
matched cohort has equal weight so one high-volume family cannot dominate the
aggregate direction.

The default comparison classifies stable intervention IDs as `resolved`,
`persistent`, or `new`. Bounded examples are ordered by priority and
corroboration. Churn classification requires at least five sessions in each
period; smaller windows keep the current queue but report the trend as
insufficient. Use `--details` when lower-level finding churn is useful.

The session-rate table always shows the observed values, but only labels a
direction when both periods have an adequate denominator: five sessions for
session rates, ten completed tasks for task rates, and 100 tool calls for
call-normalized rates. Smaller samples are marked `insufficient`.

## Quality-adjusted verdict

Quality evidence includes:

- pre-delivery test coverage;
- deliveries requiring review-driven fixes;
- review-to-edit cycles;
- downstream failures;
- follow-up edits and recovery;
- reverts.

Rate directions require at least ten observations in both periods. The
quality-adjusted verdict additionally requires at least five deliveries per
period and only uses task families present in the matched performance
comparison. It distinguishes faster work with stable quality from work shifted
into review, failure, follow-up edits, or reverts.

When matched quality evidence is insufficient, Muninn falls back to
repository-wide quality rates. If matched performance evidence is unavailable,
it falls back to aggregate task medians.

## Diagnostic comparison

Diagnostic fingerprints compare post-failure fresh tokens, tool calls, and
elapsed time per occurrence. Recovery begins when the agent observes the
diagnostic report and ends at the next diagnostic observation, task completion,
or 30 minutes without activity.

States are `improving`, `unchanged`, `regressed`, `new`, `resolved`, or
`not-observed`. `resolved` requires a current passed Heimdal report for the same
rehashed test target; disappearance alone is `not-observed`.

Task families are privacy-safe derived labels based on owned targets and
operation families. Prompts and raw commands are not retained. Even matched
periods remain observational because task difficulty can vary within a cohort.
