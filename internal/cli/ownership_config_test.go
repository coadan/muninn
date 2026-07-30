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

func TestOwnedOperationKindAndBypassPatternValidation(t *testing.T) {
	valid := ownedToolConfig{
		ID:          "repo",
		Executables: []string{"repo"},
		Operations: []ownedOperationConfig{{
			ID:   "search",
			Args: []string{"search"},
			Kind: "search",
			BypassPatterns: []ownedBypassPatternConfig{{
				Executable: "rg",
				Args:       []string{"**"},
			}},
		}},
	}
	if err := validateOwnedToolConfig([]ownedToolConfig{valid}); err != nil {
		t.Fatalf("valid operation semantics: %v", err)
	}
	invalidKind := valid
	invalidKind.Operations = append([]ownedOperationConfig(nil), valid.Operations...)
	invalidKind.Operations[0].Kind = "magic"
	if err := validateOwnedToolConfig([]ownedToolConfig{invalidKind}); err == nil ||
		!strings.Contains(err.Error(), "operation kind") {
		t.Fatalf("invalid kind error=%v", err)
	}
	invalidBypass := valid
	invalidBypass.Operations = append([]ownedOperationConfig(nil), valid.Operations...)
	invalidBypass.Operations[0].BypassPatterns = []ownedBypassPatternConfig{{
		Executable: "../rg",
		Args:       []string{"**"},
	}}
	if err := validateOwnedToolConfig([]ownedToolConfig{invalidBypass}); err == nil ||
		!strings.Contains(err.Error(), "bypassPatterns") {
		t.Fatalf("invalid bypass error=%v", err)
	}
}
