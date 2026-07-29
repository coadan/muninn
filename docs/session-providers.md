# Session providers

Muninn keeps session-format parsing behind one provider adapter. Discovery and
normalization may be provider-specific; storage, findings, interventions, and
reports consume only the normalized session model.

## Add a provider

Keep the provider parser and its fixtures in one provider-owned file, then add
one declarative `sessionProviderAdapter` entry in `session_provider.go`:

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

## Normalization contract

Every event has a provider-independent kind and timestamp. Tool calls and
outputs share a tool round. Token events contain cumulative
`normalizedTokenUsage` provider totals.
Completion and compaction events are explicit when the source exposes them.
Candidates for commands and paths exist only during normalization and are
reduced to privacy-safe ownership and repository-relative attribution before
storage.

The adapter must not pass prompts, messages, raw commands, raw output, absolute
paths, secrets, or provider session identifiers into reports. `SourcePath` and
`CWD` are local index metadata only. Provider identifiers used for lineage must
be irreversibly digested before entering the normalized model.

Do not add placeholder providers. Add a provider when representative fixtures
and real sessions are available to validate its semantics.
