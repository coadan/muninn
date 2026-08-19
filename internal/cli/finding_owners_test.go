package cli

import "testing"

func TestOwnerRediscoveryPolicyMatchesOwnerKind(t *testing.T) {
	config := defaultRepositoryConfig()
	tests := []struct {
		target string
		title  string
		action string
	}{
		{
			target: "AGENTS.md",
			title:  "injected repository instructions are repeatedly reopened",
			action: "AGENTS.md is normally injected into the session; replace the ambiguous guidance with a concise task trigger and exact owner or tooling route, then avoid rereading the file.",
		},
		{
			target: "README.md",
			title:  "documentation entry point is repeatedly reopened",
			action: "Add a concise task trigger in repository instructions that routes to the exact heading in this document; keep reference detail in the document, or expose a repeatable procedure as a skill instead of copying it into always-on context.",
		},
		{
			target: "Makefile",
			title:  "build entry point is repeatedly reopened",
			action: "Expose canonical targets through a concise help target or injected workflow map, then invoke the target directly instead of reopening the build file.",
		},
		{
			target: "go.mod",
			title:  "repository manifest is repeatedly reopened",
			action: "Add or use a bounded repository command for dependency and script discovery instead of repeatedly rereading the full manifest.",
		},
		{
			target: "internal/cli/router.go",
			title:  "an authoritative owner is repeatedly rediscovered",
			action: config.Actions.SourceContext + " If the route remains recurrent, add only its task trigger and owner pointer to repository instructions; keep source detail out of always-on instructions.",
		},
	}

	for _, test := range tests {
		title, action := ownerRediscoveryPolicy(test.target, config)
		if title != test.title || action != test.action {
			t.Errorf("%s policy=(%q, %q) want (%q, %q)", test.target, title, action, test.title, test.action)
		}
	}
}

func TestInstructionRouteTargetSelectsNearestScope(t *testing.T) {
	files := []repositoryInstructionFile{
		{Path: "AGENTS.md"},
		{Path: "packages/AGENTS.md"},
		{Path: "packages/api/AGENTS.md"},
	}
	if got := instructionRouteTarget("packages/api/src/router.go", files); got != "packages/api/AGENTS.md" {
		t.Fatalf("nearest instruction route=%q", got)
	}
	if got := instructionRouteTarget("cmd/main.go", files); got != "AGENTS.md" {
		t.Fatalf("root instruction route=%q", got)
	}
}

func TestBuildEntrypointTargetRecognizesCanonicalFiles(t *testing.T) {
	for _, target := range []string{"Makefile", "tools/Justfile", "Taskfile.yml"} {
		if !buildEntrypointTarget(target) {
			t.Errorf("%s was not recognized as a build entry point", target)
		}
	}
	if buildEntrypointTarget("build.go") {
		t.Fatal("ordinary source was recognized as a build entry point")
	}
}
