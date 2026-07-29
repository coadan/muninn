package cli

import "testing"

func TestOwnershipIndexCacheKeyTracksOnlyStoredClassification(t *testing.T) {
	base := []ownedToolConfig{{
		ID:                "repo-cli",
		Repository:        ".",
		Executables:       []string{"repo"},
		ToolCalls:         []string{"connector"},
		TaskArgumentAfter: "task",
		Recommendation:    "improve the tool",
		Operations: []ownedOperationConfig{{
			ID:                     "check",
			Args:                   []string{"task", "*", "check"},
			ExpectedWait:           true,
			ExpectedFailureReasons: []string{"timeout"},
		}},
	}}
	baseKey := newOwnershipCatalog(base).cacheKey

	presentationOnly := append([]ownedToolConfig(nil), base...)
	presentationOnly[0].Repository = "../repo"
	presentationOnly[0].Recommendation = "new guidance"
	presentationOnly[0].ToolCalls = []string{"different-connector"}
	presentationOnly[0].Operations = append([]ownedOperationConfig(nil), base[0].Operations...)
	presentationOnly[0].Operations[0].ExpectedWait = false
	presentationOnly[0].Operations[0].ExpectedFailureReasons = nil
	if got := newOwnershipCatalog(presentationOnly).cacheKey; got != baseKey {
		t.Fatalf("analysis-only config invalidated stored classification: %q != %q", got, baseKey)
	}

	classificationChange := append([]ownedToolConfig(nil), base...)
	classificationChange[0].Operations = append([]ownedOperationConfig(nil), base[0].Operations...)
	classificationChange[0].Operations[0].Args = []string{"check"}
	if got := newOwnershipCatalog(classificationChange).cacheKey; got == baseKey {
		t.Fatal("operation classification change reused stale index scope")
	}
}
