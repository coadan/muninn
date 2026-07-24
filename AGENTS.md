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
