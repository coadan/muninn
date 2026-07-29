package main

import (
	"reflect"
	"testing"
)

func TestOperationsOnlyDoesNotClaimSharedLauncher(t *testing.T) {
	catalog := newOwnershipCatalog([]ownedToolConfig{{
		ID:             "void-cli",
		Executables:    []string{"npm"},
		OperationsOnly: true,
		Operations: []ownedOperationConfig{{
			ID:   "context",
			Args: []string{"run", "--silent", "void", "--", "context"},
		}},
	}})
	voidCommand := `{"cmd":"npm run --silent void -- context actor --path src"}`
	if matches := catalog.match(codexSelectorDigests("exec_command", voidCommand, "")); len(matches) != 0 {
		t.Fatalf("operations-only launcher should not be attributed as a whole tool: %#v", matches)
	}
	operations := catalog.classifyOperations(codexCommandInvocations("exec_command", voidCommand, ""))
	if len(operations) != 1 || operations[0] != "void-cli/context" {
		t.Fatalf("configured launcher operation was not recognized: %#v", operations)
	}
	unrelated := `{"cmd":"npm test"}`
	if operations := catalog.classifyOperations(codexCommandInvocations("exec_command", unrelated, "")); len(operations) != 0 {
		t.Fatalf("unrelated launcher use should remain unattributed: %#v", operations)
	}
}

func TestOwnedOperationClassificationPrefersSpecificRule(t *testing.T) {
	catalog := newOwnershipCatalog([]ownedToolConfig{{
		ID:          "bwb",
		Executables: []string{"bwb"},
		Operations: []ownedOperationConfig{
			{ID: "comments", Args: []string{"task", "*", "comments"}},
			{ID: "comments-wait", Args: []string{"task", "*", "comments", "**", "--wait"}},
		},
	}})
	invocations := []ownedCommandInvocation{{
		Executable: "bwb",
		Args:       []string{"task", "my-task", "comments", "--repo", "breyta", "--pr", "42", "--wait"},
	}}
	if got := catalog.classifyOperations(invocations); !reflect.DeepEqual(got, []string{"bwb/comments-wait"}) {
		t.Fatalf("specific operation was not preferred: %#v", got)
	}
}

func TestOwnedOperationTaskUsesConfiguredBoundedArgument(t *testing.T) {
	catalog := newOwnershipCatalog([]ownedToolConfig{{
		ID:                "bwb",
		Executables:       []string{"bwb"},
		TaskArgumentAfter: "task",
	}})
	invocations := []ownedCommandInvocation{
		{Executable: "bwb", Args: []string{"task", "installer-create-api-connections", "test"}},
		{Executable: "git", Args: []string{"status"}},
	}
	if got := catalog.taskForInvocations(invocations); got != "installer-create-api-connections" {
		t.Fatalf("task=%q want configured task argument", got)
	}
	invocations = append(invocations, ownedCommandInvocation{
		Executable: "bwb",
		Args:       []string{"task", "other-task", "status"},
	})
	if got := catalog.taskForInvocations(invocations); got != "" {
		t.Fatalf("ambiguous task=%q want empty", got)
	}
}

func TestOwnedOperationClassificationRetainsSpecificTies(t *testing.T) {
	catalog := newOwnershipCatalog([]ownedToolConfig{{
		ID:          "bwb",
		Executables: []string{"bwb"},
		Operations: []ownedOperationConfig{
			{ID: "test", Args: []string{"task", "*", "test"}},
			{ID: "test-nses", Args: []string{"task", "*", "test", "**", ":nses"}},
			{ID: "test-changed", Args: []string{"task", "*", "test", "**", ":changed-since"}},
		},
	}})
	invocations := []ownedCommandInvocation{{
		Executable: "bwb",
		Args:       []string{"task", "my-task", "test", ":nses", "[example-test]", ":changed-since", "origin/main"},
	}}
	want := []string{"bwb/test-changed", "bwb/test-nses"}
	if got := catalog.classifyOperations(invocations); !reflect.DeepEqual(got, want) {
		t.Fatalf("equally specific operations were not retained: got=%#v want=%#v", got, want)
	}
}

func TestOperationPatternDoubleWildcardMatchesZeroOrManySegments(t *testing.T) {
	pattern := []string{"task", "*", "comments", "**", "--wait"}
	for _, args := range [][]string{
		{"task", "one", "comments", "--wait"},
		{"task", "one", "comments", "--repo", "breyta", "--pr", "42", "--wait"},
	} {
		if !operationPatternMatches(pattern, args) {
			t.Fatalf("double wildcard did not match %#v", args)
		}
	}
	if operationPatternMatches(pattern, []string{"task", "one", "comments", "--repo", "breyta"}) {
		t.Fatal("double wildcard matched without required trailing flag")
	}
}
