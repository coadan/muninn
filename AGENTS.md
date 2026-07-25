# Muninn agent guide

Muninn is a standalone CLI for privacy-safe coding-agent session analysis.

## Priorities

- Optimize for lower tooling roundtrips and lower total cost per completed
  task, not merely shorter individual commands.
- Preserve the privacy boundary: reports must not contain prompts, messages,
  raw commands, raw tool output, absolute paths, secrets, or provider session
  identifiers.
- Keep provider ingestion behind an explicit adapter boundary. Shared
  analysis, storage, findings, and reporting must not depend on Codex rollout
  shapes.
- Codex is implemented first. Leave clean seams for Claude Code and OpenCode
  without speculatively implementing their adapters.
- A command bundle inside one outer tool call is one roundtrip. Only adjacent
  substantive commands in separate outer calls count as cross-call
  transitions.
- When friction is clear before it becomes statistically recurrent, record it
  with `muninn feedback add` using only a fixed category and privacy-safe
  logical target/signal labels. Mark directly changeable tools as
  `--control local`, and resolve the same signal after its improvement lands.

## Workflow

- Work directly on `main` unless the user requests another branch.
- Commit coherent slices and reinstall after user-visible CLI changes.
- Run `gofmt` and `go test ./...` before committing.
- Use `apply_patch` for source edits.
- Do not add a review gate unless the user requests one.

## Storage

- Persist only normalized metadata needed for reports.
- Provider call IDs and source paths may be used internally while ingesting
  but must never appear in reports.
- Do not persist prompt text, conversation messages, raw tool input, or raw
  tool output.
- Prefer SQLite for the incremental local index. Keep the normalized model
  exportable so DuckDB or other analytical consumers can be added later.

## Code Mode Tool Batching

When `functions.exec` is available, run independent tool calls concurrently
within one bounded stage. Prefer `await Promise.allSettled([...])` and inspect
every result. `Promise.all(...)` rejects early but does not cancel calls that
already started, so use it only when discarding other results is intentional.
Keep dependencies, waits/resumes, approvals, adaptive investigations,
conflicting mutations, and builds or mutations that write the same outputs
sequential. Do not split otherwise batchable inspections across outer calls.

Keep each nested call's output bounded. Prefer focused queries and per-call
output limits; broad outputs that can truncate task evidence are not a valid
efficiency gain. If a result is truncated, narrow or page only that result
instead of rerunning the whole batch.

## Minimal Working Rules

- Understand the task and trace the real flow first. Then stop at the first
  sufficient rung: skip speculative work, reuse repository code, use the
  standard library, use native platform features, use an installed dependency,
  and only then write the minimum new code.
- Fix root causes at the shared boundary after checking callers. Prefer
  deletion, boring code, few files, and the shortest correct diff.
- Do not add one-use abstractions, future scaffolding/config, or dependencies
  when existing code or a few direct lines suffice.
- Do not simplify away requested behavior, security, trust-boundary validation,
  data-loss/error handling, or accessibility.
- Leave the smallest runnable regression check for non-trivial logic. Mark a
  deliberate ceiling with a `ponytail:` comment and its upgrade trigger.
