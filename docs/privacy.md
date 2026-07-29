# Privacy and data model

Muninn reports normalized metadata needed to improve cost per completed task.
It does not report:

- prompts or conversation messages;
- raw command arguments or tool output;
- absolute paths or provider session identifiers;
- secrets or arbitrary user prose.

Model and effort labels are bounded. Lineage identifiers are one-way digests
used only for aggregation. Command, output, and failure classifications use
fixed labels. External diagnostic fingerprints are rehashed; Muninn retains
only bounded classifications, phases, counts, and diagnostic status—not raw
reports, database names, servers, tests, paths, or failure messages.

Provider ingestion remains behind an adapter boundary. Provider-specific call
IDs and paths may be used transiently while ingesting, but shared analysis,
storage, findings, and reports operate on the normalized model.

SQLite is the incremental local index. The normalized model remains exportable
for later analytical consumers without making them part of the ingestion
contract.
