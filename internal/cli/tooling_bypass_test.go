package cli

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestBypassPatternsClassifyPreferredOperationWithoutClaimingIt(t *testing.T) {
	catalog := newOwnershipCatalog([]ownedToolConfig{{
		ID:          "bwb",
		Executables: []string{"bwb"},
		Operations: []ownedOperationConfig{{
			ID:   "cli",
			Args: []string{"cli"},
			BypassPatterns: []ownedBypassPatternConfig{{
				Executable: "breyta",
				Args:       []string{"**"},
			}},
		}},
	}})
	invocations := []ownedCommandInvocation{{Executable: "breyta", Args: []string{"flows", "push"}}}
	if got := catalog.classifyBypassedOperations(invocations); !reflect.DeepEqual(got, []string{"bwb/cli"}) {
		t.Fatalf("bypassed operations=%#v", got)
	}
	if got := catalog.classifyOperations(invocations); len(got) != 0 {
		t.Fatalf("bypass was counted as preferred operation: %#v", got)
	}
}

func TestBuildToolingBypassFindingsRequiresDeclaredCrossSessionRecurrence(t *testing.T) {
	report := newSessionInsightsReport("codex", nil, t.TempDir(), time.Time{}, time.Now().UTC())
	report.Summary.ToolingBypasses["bwb/cli"] = codexTransitionMetrics{Count: 7, Sessions: 3}
	findings := buildToolingBypassFindings(report)
	if len(findings) != 1 || findings[0].Target != "bwb/cli" ||
		findings[0].Confidence != "high" ||
		!strings.Contains(findings[0].Evidence, "configured bypass") {
		t.Fatalf("tooling bypass findings=%#v", findings)
	}
	report.Summary.ToolingBypasses["bwb/cli"] = codexTransitionMetrics{Count: 4, Sessions: 3}
	if findings := buildToolingBypassFindings(report); len(findings) != 0 {
		t.Fatalf("sub-threshold bypass produced findings: %#v", findings)
	}
}
