package cli

import (
	"strings"
	"testing"
)

func TestOperationsOnlyRequiresExecutableOperations(t *testing.T) {
	err := validateOwnedToolConfig([]ownedToolConfig{{
		ID:             "invalid",
		ToolCalls:      []string{"exec"},
		OperationsOnly: true,
	}})
	if err == nil || !strings.Contains(err.Error(), "requires executables and operations") {
		t.Fatalf("expected bounded operations-only validation error, got %v", err)
	}
}

func TestOwnedOperationTaskRejectsUnsafeArgument(t *testing.T) {
	catalog := newOwnershipCatalog([]ownedToolConfig{{
		ID:                "bwb",
		Executables:       []string{"bwb"},
		TaskArgumentAfter: "task",
	}})
	invocations := []ownedCommandInvocation{{
		Executable: "bwb",
		Args:       []string{"task", "/private/task", "test"},
	}}
	if got := catalog.taskForInvocations(invocations); got != "" {
		t.Fatalf("unsafe task=%q want empty", got)
	}
}
