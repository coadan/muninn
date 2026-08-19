# Decretion experiments

Decretion is a reversible removal or narrowing experiment. Its purpose is not
to reward fewer files or lines; it tests whether accumulated code, guidance,
state, abstractions, tests, documentation, or tooling still earns its ongoing
understanding and context cost.

Muninn supplies candidate evidence and outcome guardrails. It cannot prove an
artifact is unused merely because no session touched it. Removal is justified
only by a named hypothesis and a comparison in which task outcomes and quality
do not regress.

## Choose a candidate

Start with the detailed intervention queue:

```bash
muninn analyze --repo . --since 7d --details
```

Strong candidates have an attributable cost and a reversible boundary, such
as:

- large always-on or scoped instructions whose stable detail already has a
  documentation owner;
- large loaded skills whose workflow can route optional reference material;
- same-skill reloads before completion or compaction, where Muninn reports the
  estimated redundant tokens and percentage of observed loads;
- repeated wrapper/helper/tool paths that duplicate a locally owned operation;
- outputs or state with a named producer but no identified current consumer,
  after repository search and owner review confirm the missing consumer.

Repeated reads can mean an artifact is valuable, not removable. Large or
frequently edited source files are navigation leads, not deletion evidence.
Tests, safety checks, recovery behavior, and trust-boundary validation need a
replacement oracle before removal.

## Write the experiment contract

Record five items in the change or planning brief:

1. **Candidate:** the exact artifact or behavior being removed or narrowed.
2. **Consumer check:** current consumers and how they were verified; state
   explicitly when no consumer was found.
3. **Cost hypothesis:** the Muninn signal expected to improve, such as loaded
   context, fresh tokens, output, roundtrips, compactions, or rework.
4. **Subtraction:** the smallest reversible deletion, consolidation, default
   change, or route behind focused context.
5. **Guardrails:** behavior checks plus Muninn outcome metrics that must remain
   stable. Include a revert trigger.

Prefer one candidate per experiment. Commit the subtraction separately so it
can be restored without disentangling unrelated improvements.

## Compare matched periods

Capture a baseline and use the same horizon after enough comparable work:

```bash
muninn analyze --repo . --since 1d --compare previous
muninn analyze --repo . --since 7d --compare previous --details
```

Use one day for concentrated workflow changes and one week for broader
guidance or tooling changes. Evaluate:

- `trends.matchedPerformance` for duration and outer roundtrips;
- `trends.matchedQuality` and quality-adjusted verdicts for rework, downstream
  failures, follow-up edits, and reverts;
- `trends.rates` for normalized stalls, polling, and transitions;
- intervention churn for the candidate becoming resolved without a new higher
  priority replacement;
- the detailed guidance metrics for loaded and redundant estimated tokens.

Keep the subtraction when its focused behavior checks pass and matched outcome
or quality evidence is stable or better. Revert when a required consumer,
failure mode, or quality regression appears. When evidence is insufficient,
run a longer cohort or a targeted benchmark; disappearance from session logs
alone is not success.
