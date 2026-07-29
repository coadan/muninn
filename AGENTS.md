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

## Code map

- `main.go` and `cli.go`: process entry and top-level command routing.
- `analyze_command.go` and `config.go`: analysis CLI orchestration and
  repository policy.
- `session_provider.go` and provider-named files: session discovery and
  normalization only.
- `report_model.go` and `session_analysis.go`: normalized report model and
  cross-session aggregation.
- `shell_commands.go`, `session_continuations.go`, and `session_events.go`:
  privacy-safe command, continuation, output, and failure interpretation.
- `store.go`, `session_index.go`, `session_query.go`, and `failure_store.go`:
  SQLite lifecycle, derived indexing, analysis reads, and failure timelines.
- `findings.go`, `interventions.go`, `outcomes.go`, and `trend.go`: evidence,
  prioritization, outcome cohorts, and comparison.
- `report_print.go` and `report_json.go`: human and machine rendering.

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

## Context-Efficient Coding

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

### Navigation-First Structure

Structure code for the shortest predictable route from entry point to
authoritative owner to verification:

- Organize by domain behavior and data ownership. Co-locate code that changes
  together; separate code with independent reasons to change.
- Give each concept and invariant one canonical owner. State the rule where it
  is enforced, make boundary contracts explicit, and keep dependencies
  directional.
- Keep entry points thin and named after the behavior they route to. Avoid
  re-export chains, mirrored representations, and dynamic indirection that
  hide the owner.
- Keep focused tests and fixtures beside or directly adjacent to the owner.
  Name files, namespaces, and symbols after domain behavior; isolate generated
  and vendor code.
- Avoid `common`, `shared`, or `utils` dumping grounds and tiny one-use file
  fragmentation. Shared code should own a stable concept, not convenience.
- When a flow must cross layers, provide one bounded map or inspection surface
  that identifies the path without requiring broad repository search.

A representative change should reach its owner and focused verification in at
most three bounded discovery stages without reading unrelated domain code.

### Evidence Before Restructuring

Optimize for the context needed to make one safe change, not file or line
count. A large file alone is not evidence. Before a structural change:

1. Trace a representative change through entry point, authoritative behavior
   and data owner, invariants, callers, shared state, and smallest verification.
2. Record files and symbols needed, why each is needed, independent change
   reasons, repeated discovery, failures, and review-driven rework.
3. Compare current and proposed navigation using available task, review, or
   session evidence.
4. Choose the smallest supported intervention: split independent ownership
   seams; rename or improve routing for discovery; improve inspection for a
   cohesive owner; extract duplicated policy; otherwise leave code together.

Structural changes require evidence of a clearer ownership or navigation
boundary. Do not add generic layers merely to shorten files.
