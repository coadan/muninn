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
			action: "AGENTS.md is normally injected into the session; clarify the relevant rule or tooling entry point, then avoid rereading the file.",
		},
		{
			target: "README.md",
			title:  "documentation entry point is repeatedly reopened",
			action: "Route agents to the exact heading through a concise documentation index or heading-aware inspection surface instead of reopening the whole document.",
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
			action: config.Actions.SourceContext,
		},
	}

	for _, test := range tests {
		title, action := ownerRediscoveryPolicy(test.target, config)
		if title != test.title || action != test.action {
			t.Errorf("%s policy=(%q, %q) want (%q, %q)", test.target, title, action, test.title, test.action)
		}
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
