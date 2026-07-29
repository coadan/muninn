# Session providers

Muninn keeps session-format parsing behind one provider adapter. Discovery and
normalization may be provider-specific; storage, findings, interventions, and
reports consume only the normalized session model.

## Add a provider

A provider implements the `sessionProvider` interface in
`session_provider.go`:

- `Name` returns the stable CLI and index label.
- `Discover` resolves configured/default locations and returns the exact files
  belonging to the provider. The adapter owns extensions, archives, and layout.
- `SessionCWD` cheaply reads only the working directory so cold indexing can
  reject sessions outside the selected repository before full parsing.
- `NormalizeSession` converts one file into `normalizedSession`.
- `Metadata` optionally enriches normalized model, effort, agent kind, lineage,
  and spawn status from provider-owned local state.

Register the constructor in `sessionProviders`. No cache, analysis, finding, or
report code should require a provider-specific branch.

Use `TestSessionProviderContractSupportsAnotherFileFormat` as the minimal
contract test. It deliberately uses a non-JSONL filename and verifies both
direct analysis and SQLite ingestion. Add parser fixtures beside the provider
for format-specific edge cases.

## Normalization contract

Every event has a provider-independent kind and timestamp. Tool calls and
outputs share a tool round. Token events contain cumulative provider totals.
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
