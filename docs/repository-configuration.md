# Repository configuration

Add `.muninn.json` at the repository root to identify locally controlled tools,
map their operations, customize actions, and suppress known false-positive
signals.

```json
{
  "schemaVersion": 1,
  "suppressSignals": [
    "session-loop/progress-stall/repository-cli/status"
  ],
  "actions": {
    "sourceContext": "Use the repository's bounded source-context command."
  },
  "ownedTools": [
    {
      "id": "repository-cli",
      "repository": "repository-name",
      "executables": ["repo-cli"],
      "taskArgumentAfter": "task",
      "operationsOnly": false,
      "operations": [
        {
          "id": "status",
          "args": ["task", "*", "status"]
        },
        {
          "id": "review-wait",
          "args": ["review", "--wait"],
          "expectedWait": true,
          "expectedFailureReasons": ["timeout"]
        }
      ],
      "recommendation": "Prefer improving this locally controlled surface."
    }
  ]
}
```

## Owned tools and operations

Owned-tool attribution identifies improvements with a short local path.
Operation patterns are anchored argument prefixes:

- `*` matches one privacy-safe variable segment.
- `**` matches zero or more segments.
- the most specific matching operation wins;
- equally specific matches are retained when one invocation deliberately
  carries multiple classified flags.

`expectedWait` classifies a long, low-output operation as necessary latency
rather than a progress stall. Use it for tests, builds, reviews, CI, and other
bounded external waits.

`expectedFailureReasons` keeps an expected fixed failure label in metrics while
preventing it from becoming an actionable operation finding.

`taskArgumentAfter` retains one bounded logical argument following the named
token so failure reports can group by task without retaining other command
text. `operationsOnly` is useful when the executable is a shared launcher and
only configured subcommands belong to the local tool.

When an outer tool call bundles multiple commands, output and failures remain
ambiguous rather than being charged to every matched operation. Frequent
successful use is demand, not friction.

For configured executables, Muninn also counts normalized long switch names
without retaining values. Value-bearing options such as `--message <text>` or
`--repo=<path>` are excluded because their repeated presence does not imply a
useful default. A switch supplied on at least 80% of definite calls across
multiple sessions becomes a candidate-default finding.

Run with `--refresh` after changing owned tools or operation patterns so cached
source events are reclassified.

## Suppress a signal

Every finding prints a stable signal ID. Add an exact ID to
`suppressSignals` only after confirming it is a false positive. Suppression
hides the finding from the action queue without deleting normalized metrics.

Configuration never weakens Muninn's privacy boundary. Selectors are stored as
one-way digests; reports show only configured logical IDs.
