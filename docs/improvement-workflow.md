# Improvement workflow

Muninn is intended to support a continuous tooling and repository improvement
loop.

## Compare an intervention

1. Save the current cohort.
2. Select one recent cross-session finding.
3. Improve the smallest likely owner: tooling, instructions, an agent-facing
   interface, verification, or source structure.
4. Analyze the same cohort and compare it with the checkpoint.
5. Suppress only findings confirmed to be false positives.

```bash
muninn checkpoint before-change --repo . --since 14d
muninn analyze --repo . --since 14d --compare before-change
```

For a quick directional check without a saved baseline, compare adjacent,
non-overlapping windows:

```bash
muninn analyze --repo . --since 72h --compare-previous
```

Treat a faster period as an improvement only when delivery quality is stable or
better. Fewer roundtrips with more review-driven fixes, downstream failures, or
reverts means work moved later in the lifecycle rather than disappeared.
Prefer the matched agent/model/effort/task-family direction when it is present;
Muninn gives each adequately sampled shared cohort equal weight and incorporates
the observed quality rates into a concise verdict. Matched quality requires the
same performance cohort and at least five deliveries in each period; otherwise
the verdict explicitly relies on the broader repository quality rates.

File-hotspot findings provide target-specific intervention candidates. Treat
an expensive-owner finding as a request to trace ownership and verification,
not as proof that the file should be split. Review/rework and downstream-risk
findings justify inspecting the exact corrections or failures and strengthening
the owning invariant or focused check.

Prefer consolidated owner findings over isolated symptoms. Their supporting
signals show whether the recommendation is backed by navigation, task cost,
delivery quality, or several of these at once; the primary finding remains the
single reporting unit.

Comparisons require the same lookback or resolved `--since-commit` boundary,
task filter, archive selection, and focus. Muninn rejects mismatched cohorts
instead of presenting them as improvement or regression.

Trend output leads with completed tool-task cost and tail outcomes. Cached
input, uncached input, and model output remain separate when no reliable
credit weighting is available. Treat both matched and unmatched rolling trends
as observational: matching reduces task and model mix bias, but does not prove
that an intervention caused the change.

## Record fresh friction

When friction is clear before it becomes statistically recurrent, record a
privacy-safe signal:

```bash
muninn feedback add \
  --category roundtrip \
  --target repository-cli/publish \
  --signal existing-change-create-failed \
  --control local \
  --source codex

muninn feedback list --repo .

muninn feedback resolve \
  --category roundtrip \
  --target repository-cli/publish \
  --signal existing-change-create-failed
```

Feedback accepts fixed categories and bounded logical labels. It cannot store
prose, commands, output, paths, URLs, prompts, session IDs, or secrets.

Use `--control local` for tooling you can change directly, `repository` for
repository interfaces or guidance, `third-party` for upstream dependencies,
and `unknown` when ownership still needs triage. Resolve the signal after the
improvement lands.

## Interpreting structure findings

Repeated reads or searches can suggest that ownership is hard to discover, but
file size alone is not sufficient evidence. Before restructuring:

1. trace a representative change from entry point to authoritative owner and
   focused verification;
2. identify repeated discovery, ambiguous ownership, and review-driven rework;
3. compare the current route with the proposed route;
4. make the smallest change that creates a clearer ownership or navigation
   boundary.
