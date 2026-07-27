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

Comparisons require the same lookback or resolved `--since-commit` boundary,
task filter, archive selection, and focus. Muninn rejects mismatched cohorts
instead of presenting them as improvement or regression.

Trend output leads with completed tool-task cost and tail outcomes. Cached
input, uncached input, and model output remain separate when no reliable
credit weighting is available. Treat unmatched rolling trends as
observational because task mix may have changed.

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

