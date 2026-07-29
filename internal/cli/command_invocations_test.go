package cli

import "testing"

func TestCodexSelectorDigestsRecognizeOwnedCommandSubstitution(t *testing.T) {
	catalog := newOwnershipCatalog([]ownedToolConfig{{
		ID:          "bwb",
		Executables: []string{"bwb"},
		Operations: []ownedOperationConfig{{
			ID:   "status",
			Args: []string{"task", "*", "status"},
		}},
	}})
	arguments := `{"cmd":"eval \"$(bwb task example status --env-only --env-format shell)\"\nbreyta flows list"}`
	digests := codexSelectorDigests(
		"exec_command",
		arguments,
		"",
	)
	matches := catalog.match(digests)
	if len(matches) != 1 || matches[0] != "bwb" {
		t.Fatalf("owned executable in command substitution was not recognized: %#v", matches)
	}
	operations := catalog.classifyOperations(codexCommandInvocations("exec_command", arguments, ""))
	if len(operations) != 1 || operations[0] != "bwb/status" {
		t.Fatalf("owned operation in command substitution was not classified: %#v", operations)
	}
	if got := len(codexCommandInvocations("exec_command", arguments, "")); got != 2 {
		t.Fatalf("eval wrapper should not add a duplicate invocation; got %d invocations", got)
	}
}

func TestCodexCommandInvocationsExposeBundledOperationAttribution(t *testing.T) {
	single := `{"cmd":"eval \"$(bwb task example status --env-only --env-format shell)\""}`
	if got := len(codexCommandInvocations("exec_command", single, "")); got != 1 {
		t.Fatalf("single wrapped operation produced %d invocations", got)
	}
	bundled := `{"cmd":"sed -n '1,20p' AGENTS.md; bwb task example status"}`
	if got := len(codexCommandInvocations("exec_command", bundled, "")); got != 2 {
		t.Fatalf("bundled command produced %d invocations", got)
	}
}
