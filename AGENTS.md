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
- Use `make help` for bounded workflow discovery. Format changed Go files,
  then run `make check`; use `make install` after a user-visible CLI change.
  Do not reread the Makefile for target syntax.
- Use `apply_patch` for source edits.
- Do not add a review gate unless the user requests one.

## Code map

- `cmd/muninn/main.go`: thin process entry point.
- `internal/cli/cli.go`: top-level command routing.
- `internal/cli/analyze_command.go` and `internal/cli/config.go`: analysis CLI
  orchestration and repository policy.
- `internal/cli/session_provider.go`: provider contract and declarative
  registry.
- `internal/cli/<provider>_discovery.go`,
  `internal/cli/<provider>_metadata.go`, and
  `internal/cli/<provider>_normalizer.go`: provider-owned discovery, optional
  enrichment, wire types, and normalization; compact formats may keep these
  together.
- `internal/cli/ownership_config.go`, `internal/cli/ownership.go`,
  `internal/cli/operation_matching.go`, and
  `internal/cli/command_invocations.go`: owned-tool policy, catalog
  construction, operation matching, and privacy-safe invocation extraction.
- `internal/cli/report_model.go` and `internal/cli/session_analysis.go`:
  normalized report model and cross-session aggregation.
- `internal/cli/session_model.go`: provider-neutral ingestion contract.
- `internal/cli/session_record.go`: normalized-event conversion and
  per-session accounting.
- `internal/cli/shell_commands.go`,
  `internal/cli/session_continuations.go`, and
  `internal/cli/session_events.go`: privacy-safe command, continuation, output,
  and failure interpretation.
- `internal/cli/session_friction.go`: oversized output, wait, and rapid-poll
  classification policy.
- `internal/cli/operation_chains.go`: privacy-safe recurring configured
  operation-chain accounting and finding policy.
- `internal/cli/diagnostic_contract.go`: recurring generic owned-operation
  failure-contract findings.
- `internal/cli/recovery_retries.go`: unchanged immediate owned-operation retry
  outcomes and recovery-loop findings.
- `internal/cli/store.go`, `internal/cli/session_index.go`,
  `internal/cli/session_query.go`, and `internal/cli/failure_store.go`: SQLite
  lifecycle, derived indexing, analysis reads, and failure timelines.
- `internal/cli/findings.go`, `internal/cli/interventions.go`,
  `internal/cli/discovery_evidence.go`, `internal/cli/outcomes.go`, and
  `internal/cli/trend.go`: signals, prioritization, bounded drill-down
  evidence, outcome cohorts, and comparison.
- `internal/cli/output_findings.go`: oversized-output finding thresholds,
  evidence, control attribution, and repair actions.
- `internal/cli/interface_findings.go`: workflow-transition, inline
  orchestration, wait, polling, and yielded-operation finding policy.
- `internal/cli/finding_focus.go`: public focus-to-signal routing policy.
- `internal/cli/finding_owners.go`: current-owner eligibility, consolidation,
  and corroboration policy.
- `internal/cli/report_json.go`: compact, detailed, and adjacent-period JSON
  rendering.
- `internal/cli/format.go`: privacy-safe count and label formatting shared by
  JSON evidence and the failure timeline.

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

Keep the combined planned output budget for one parallel inspection stage at
or below 12,000 tokens, normally 2,000-4,000 per nested call. Run an
independently high-output owner sequentially instead of letting it crowd out
the other results. Prefer focused queries and per-call output limits; broad
outputs that can truncate task evidence are not a valid efficiency gain. If a
result is truncated, narrow or page only that result instead of rerunning the
whole batch.

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
