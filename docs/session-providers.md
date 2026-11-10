# Session providers

Muninn keeps session-format parsing behind one provider adapter. Discovery and
normalization may be provider-specific; storage, findings, interventions, and
reports consume only the normalized session model.

## Add a provider

Keep provider code and fixtures under a provider-named prefix in
`internal/cli`, then add one declarative `sessionProviderAdapter` entry in
`internal/cli/session_provider.go`. A compact format can use one file. When the
responsibilities are independently substantial, use
`internal/cli/<provider>_discovery.go`,
`internal/cli/<provider>_metadata.go`, and
`internal/cli/<provider>_normalizer.go`; provider wire types stay with the
normalizer:

```go
var otherSessionProvider = sessionProviderAdapter{
	name:      "other",
	discover:  discoverOtherSessions,
	normalize: parseOtherNormalizedSession,
	metadata:  otherSessionMetadata, // optional
}
```

Then add `"other": otherSessionProvider` to `sessionProviders`.

- `name` is the stable CLI and index label and must match its
  `sessionProviders` registry key.
- `discover` resolves configured/default locations and returns the exact files
  belonging to the provider. It owns extensions, archives, and layout.
- `normalize` converts one file into `normalizedSession`.
- `metadata` optionally enriches normalized model, effort, agent kind, lineage,
  and spawn status from provider-owned local state. Omit it when unused.

Registry validation rejects mismatched names or missing required callbacks.
No cache, analysis, finding, or report code should require a provider-specific
branch.

Repository scope is derived after normalization from the session CWD and
per-tool working directories. The SQLite index stores separate privacy-safe
derived views for each repository, so a session may contribute only its
relevant events to multiple repositories without reusing another repository's
targets or configured operation IDs.

Use `TestSessionProviderContractSupportsAnotherFileFormat` as the minimal
contract test. It deliberately uses a non-JSONL filename and verifies both
direct analysis and SQLite ingestion. Add parser fixtures beside the provider
for format-specific edge cases.

## Claude Code

`--provider claude` reads Claude Code transcripts from
`$CLAUDE_CONFIG_DIR/projects` (default `~/.claude/projects`), including the
`subagents/` transcripts stored beside each session. The adapter lives in
`internal/cli/claude_discovery.go` and `internal/cli/claude_normalizer.go`.

- Claude Code writes one record per content block and repeats the API
  message's usage on each; usage is counted once per message ID. Cached input is
  cache reads; cache creation counts as fresh input. Reasoning tokens are
  `usage.output_tokens_details.thinking_tokens`. Claude Code counts them inside
  `output_tokens`, as Codex counts reasoning inside output, so output and total
  tokens keep the same meaning for both providers. A reasoning count above
  output is clamped to output. A repeated message whose usage differs only in
  thinking tokens adds no token event; the change is carried by the next one.
- The session model and effort are the values used on the most API messages.
  Effort is each assistant record's `perTurnEffort`, or `effort` when that is
  absent, because effort can change during a session. Ties go to the
  alphabetically first label.
- A root transcript's `cost-state` records hold the Claude Code process's
  cumulative `totalCostUSD`, `totalAPIDuration`, and `totalToolDuration`. They
  cover the session's subagents and also side requests that never appear as
  transcript messages, so they can exceed what transcript usage would price.
  Subagent transcripts carry none, so cost is never double counted. Each
  changed cost-state becomes a `provider_cost` event. It has no timestamp, so
  it is placed at the time of the preceding user, assistant, or compaction
  record; one before any such record is skipped. `totalDuration` is process
  wall time including idle time, and `modelUsage` repeats the totals per model,
  so neither is used. `thinkingDurationMs` on assistant records is not used:
  reasoning tokens already measure thinking, and the record's relation to API
  time is not documented.
- Record types and message content-block types outside the set the adapter
  knows are counted per session as format drift. Only the counts are kept,
  never the type names. The known set is the types seen in real transcripts
  plus a few that Claude Code writes elsewhere (`summary` and `progress`
  records, and the Messages API's redacted-thinking, server-tool, and document
  blocks).
- Tool names map onto the neutral vocabulary shared analysis already uses:
  `Bash` is `exec_command`; `Edit`, `MultiEdit`, `Write`, and `NotebookEdit`
  are `apply_patch`; `Agent` (and the older `Task`) is `spawn_agent`; and
  `SendMessage` is `send_message`. `Read`, `Grep`, and `Glob` keep their names
  and carry the `file reads` or `search` transition family, which shared
  analysis counts as discovery. Other tool names are kept only when they match
  a conservative identifier pattern.
- A turn ends at an assistant `end_turn`; `compact_boundary` records are
  compactions. A tool result the user or a permission rule declined is not
  counted as a tool failure. Output saved to a tool-results file counts as
  truncated.
- A subagent transcript is a `subagent` session whose parent lineage is the
  digested parent session ID. Session and agent IDs are digested immediately.
- Claude Code's own task checkouts, `.claude/worktrees/<name>`, are treated as
  task worktrees like `.worktrees/<name>`: they name the task and are skipped by
  the repository instruction scan. Because such a checkout is the repository
  itself, its file targets map to the in-repository path (`pkg/...`, not
  `.claude/worktrees/<name>/pkg/...`), so reports never show the prefix and one
  file is one target across task checkouts. This is a directory convention,
  not a provider branch.
- Only structure is decoded: prompts, assistant text, thinking, and
  attachments are never parsed into the normalized model. Skill loads from the
  `Skill` tool carry no size estimate, and Claude Code has no archive or
  separate metadata store, so those adapter hooks are omitted.

## Normalization contract

Every event has a provider-independent kind and timestamp. Tool calls and
outputs share a tool round. Token events contain cumulative
`normalizedTokenUsage` provider totals. A provider that reports its own cost
emits `provider_cost` events with a cumulative `normalizedProviderCost` (USD,
API milliseconds, tool milliseconds); others emit none, and their reports omit
provider cost. Cost events are bookkeeping, not activity: shared analysis
handles them before turn, episode, and session-end accounting. An adapter may count records and content blocks of unknown
types in `UnrecognizedRecords` and `UnrecognizedContentBlocks`, so format drift
shows up as a number. Session-level fields (model, effort, agent kind, lineage
keys, spawn status, and the drift counts) are stored with the session in the
SQLite index.
Completion and compaction events are explicit when the source exposes them.
Candidates for commands and paths exist only during normalization and are
reduced to privacy-safe ownership and repository-relative attribution before
storage.

When a provider exposes an explicit or tool-mediated guidance load, its adapter
may attach a normalized guidance artifact to the tool-call event. The artifact
contains only a fixed kind and validated logical ID. Codex derives skill IDs
transiently from `SKILL.md` read candidates and discards the source path before
storage. Shared analysis owns reload accounting and findings; it never branches
on the provider wire format.

A provider may resolve current guidance sizes for validated logical IDs after
cached analysis. Codex searches only its current user, system, plugin, and
repository-local skill roots and returns the largest matching artifact when a
logical ID is duplicated. Shared analysis overlays token estimates on summary
and task metrics; historical load and reload counts remain unchanged. Paths
used for resolution are transient and are never stored or reported. If the
provider cannot resolve an ID, the stored ingestion-time estimate remains.

The adapter must not pass prompts, messages, raw commands, raw output, absolute
paths, secrets, or provider session identifiers into reports. `SourcePath` and
`CWD` are local index metadata only. Provider identifiers used for lineage must
be irreversibly digested before entering the normalized model.

Do not add placeholder providers. Add a provider when representative fixtures
and real sessions are available to validate its semantics.
