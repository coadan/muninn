package cli

import (
	"strings"
	"testing"
)

func TestFilterSessionFindingsUsesActionFamilies(t *testing.T) {
	findings := []sessionFinding{
		{Category: "owned-tool", Title: "tool"},
		{Category: "code-structure", Title: "source"},
		{Category: "session-loop", Title: "loop"},
		{Category: "delivery-quality", Title: "quality"},
	}
	filtered, err := filterSessionFindings(findings, "structure")
	if err != nil {
		t.Fatalf("filter findings: %v", err)
	}
	if len(filtered) != 1 || filtered[0].Title != "source" {
		t.Fatalf("unexpected focused findings: %#v", filtered)
	}
	friction, err := filterSessionFindings(findings, "friction")
	if err != nil {
		t.Fatalf("friction focus: %v", err)
	}
	if len(friction) != len(findings) {
		t.Fatalf("friction focus should preserve the broad action queue: %#v", friction)
	}
	if _, err := filterSessionFindings(findings, "unknown"); err == nil {
		t.Fatal("unsupported focus did not fail")
	}
	output, err := filterSessionFindings([]sessionFinding{{Category: "output-cost", Title: "large"}}, "output")
	if err != nil || len(output) != 1 {
		t.Fatalf("output focus mismatch: %#v, %v", output, err)
	}
	quality, err := filterSessionFindings(findings, "quality")
	if err != nil || len(quality) != 1 || quality[0].Title != "quality" {
		t.Fatalf("quality focus mismatch: %#v, %v", quality, err)
	}
}

func TestFilterSessionFindingsKeepsPublicFocusCategoriesDisjoint(t *testing.T) {
	categories := []string{
		"owned-tool",
		"owned-operation",
		"recurring-failure",
		"diagnostic-failure",
		"output-cost",
		"default-candidate",
		"instruction-discovery",
		"instruction-footprint",
		"session-loop",
		"verification-loop",
		"agent-interface",
		"tooling-bypass",
		"help-effectiveness",
		"verification-escalation",
		"search-inefficiency",
		"code-structure",
		"discovery",
		"delegation-cost",
		"delivery-quality",
		"task-cost",
	}
	findings := make([]sessionFinding, 0, len(categories))
	for _, category := range categories {
		findings = append(findings, sessionFinding{Category: category, Title: category})
	}
	want := map[string]string{
		"tooling":      "owned-tool,owned-operation,recurring-failure,diagnostic-failure,output-cost,default-candidate,tooling-bypass,help-effectiveness,verification-escalation,search-inefficiency",
		"instructions": "instruction-discovery,instruction-footprint",
		"interface":    "default-candidate,agent-interface,tooling-bypass,help-effectiveness,search-inefficiency",
		"structure":    "code-structure",
		"discovery":    "instruction-discovery,search-inefficiency,discovery",
		"failures":     "recurring-failure,diagnostic-failure,help-effectiveness",
		"loops":        "session-loop,verification-loop,agent-interface,verification-escalation,delegation-cost",
		"output":       "output-cost",
		"quality":      "delegation-cost,delivery-quality,task-cost",
	}
	for focus, expected := range want {
		filtered, err := filterSessionFindings(findings, focus)
		if err != nil {
			t.Fatalf("%s focus: %v", focus, err)
		}
		got := make([]string, 0, len(filtered))
		for _, finding := range filtered {
			got = append(got, finding.Category)
		}
		if strings.Join(got, ",") != expected {
			t.Fatalf("%s focus categories=%q want %q", focus, strings.Join(got, ","), expected)
		}
	}
}

func TestEveryFindingCategoryHasAValidPreferredFocus(t *testing.T) {
	categories := []string{
		"owned-tool",
		"owned-operation",
		"default-candidate",
		"recurring-failure",
		"diagnostic-failure",
		"output-cost",
		"instruction-discovery",
		"instruction-footprint",
		"agent-interface",
		"tooling-bypass",
		"help-effectiveness",
		"verification-escalation",
		"search-inefficiency",
		"code-structure",
		"discovery",
		"session-loop",
		"verification-loop",
		"delegation-cost",
		"delivery-quality",
		"task-cost",
	}
	for _, category := range categories {
		finding := sessionFinding{Category: category}
		focus := sessionFindingFocus(finding)
		if focus == "" || !sessionFocusCategories[focus][category] {
			t.Fatalf("category %q has invalid preferred focus %q", category, focus)
		}
	}
}
