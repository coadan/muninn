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

In Code Mode, within each bounded stage, run independent, functions.exec-available tool calls concurrently in one functions.exec call. Use await Promise.allSettled([...]) when partial results are useful, and inspect every result; use await Promise.all([...]) only when any failure should abort the batch. Keep dependencies, waits/resumes, approvals, conflicting or interdependent mutations, and adaptive investigations where each result may change the next step sequential. Do not split otherwise batchable inspections across outer tool calls.

Keep each nested call's output bounded. Prefer focused queries and per-call
output limits; broad outputs that can truncate task evidence are not a valid
efficiency gain. If a result is truncated, narrow or page only that result
instead of rerunning the whole batch.
