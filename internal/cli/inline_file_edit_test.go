package cli

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// padInline makes a command large enough to count as inline orchestration
// (2,048 bytes for exec_command) without changing what it does.
func padInline(command string) string {
	return command + "\n# " + strings.Repeat("x", 2100) + "\n"
}

func execCommandArguments(t *testing.T, command string) string {
	t.Helper()
	encoded, err := json.Marshal(map[string]string{"cmd": command})
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func TestCodexInlineFileEditClassifiesWritesNotReads(t *testing.T) {
	cases := []struct {
		name    string
		command string
		want    bool
	}{
		{"python heredoc rewrites a file", "python3 - <<'EOF'\nfrom pathlib import Path\np = Path('a.go')\np.write_text(p.read_text().replace('x', 'y'))\nEOF", true},
		{"python open for writing", "python3 -c \"open('out.txt', 'w').write('z')\"", true},
		{"node writes a file", "node -e \"require('fs').writeFileSync('a.json', '{}')\"", true},
		{"heredoc redirected into a file", "cat > notes/plan.md <<'EOF'\n# plan\nEOF", true},
		{"heredoc with redirect after the marker", "cat <<'EOF' >> src/app.tsx\nexport {}\nEOF", true},
		{"heredoc through tee", "tee docs/a.md <<'EOF' >/dev/null\nbody\nEOF", true},
		{"in-place sed", "sed -i '' 's/a/b/' main.go", true},
		{"read-only python script", "python3 - <<'EOF'\nimport json, sys\nprint(len(json.load(open('a.json'))))\nEOF", false},
		{"commit message heredoc", "git commit -m \"$(cat <<'EOF'\nfix: thing\nEOF\n)\"", false},
		{"heredoc piped into a program", "cat <<'EOF' | python3 -\nprint(1)\nEOF", false},
		{"heredoc to stderr", "cat <<'EOF' >&2\nwarning\nEOF", false},
		{"heredoc to /dev/null", "cat <<'EOF' > /dev/null\nnothing\nEOF", false},
		{"heredoc write across a line continuation", "cat > a.txt \\\n<<'EOF'\nbody\nEOF", true},
		{"read-only script mentioning write syntax in a string", "python3 - <<'EOF'\nfrom pathlib import Path\np = Path('a.py')\nprint('.write_text(' in p.read_text(), \"open(f, 'w')\")\nEOF", false},
		{"python open with a mode keyword", "python3 - <<'EOF'\nwith open('out.txt', mode='w') as f:\n    f.write('body')\nEOF", true},
		{"pathlib open for writing", "python3 - <<'EOF'\nfrom pathlib import Path\nwith Path('out.txt').open('w') as f:\n    f.write('body')\nEOF", true},
		{"read-only script with a commented heredoc write", "python3 - <<'EOF'\n# cat > example.txt <<EOF\nprint(1)\nEOF", false},
		{"data heredoc mentioning write syntax", "cat <<'EOF' | wc -l\np.write_text(x)\ncat > a.txt <<X\nEOF", false},
		{"python open with a nested filename expression", "python3 - <<'EOF'\nfrom pathlib import Path\nwith open(Path('out.txt'), 'w') as f:\n    f.write('body')\nEOF", true},
		{"python -c after interpreter flags", "python3 -I -c \"from pathlib import Path; Path('a').write_text('b')\"", true},
		{"read-only script with a commented python -c write", "python3 - <<'EOF'\n# Example: python3 -c \"p.write_text('x')\"\nprint(1)\nEOF", false},
		{"search pattern passed with -e", "rg -F -e 'p.write_text(' .", false},
		{"read-only script with write syntax in a triple-quoted string", "python3 - <<'EOF'\nexample = \"\"\"\np.write_text(value)\n\"\"\"\nprint(example)\nEOF", false},
		{"heredoc with an explicit stdout descriptor", "cat 1> out.txt <<'EOF'\nbody\nEOF", true},
		{"heredoc with stdout and stderr redirected", "cat &> out.txt <<'EOF'\nbody\nEOF", true},
		{"heredoc teed with append flag", "tee -a out.txt <<'EOF'\nbody\nEOF", true},
		{"read-only script teed to /dev/null", "python3 - <<'EOF' | tee /dev/null\nprint(1)\nEOF", false},
		{"read-only script mentioning write syntax in a comment", "python3 - <<'EOF'\n# later: p.write_text(x)\nprint(open('a.py').read())\nEOF", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			arguments := execCommandArguments(t, padInline(tc.command))
			if got := codexInlineFileEdit("exec_command", arguments, ""); got != tc.want {
				t.Fatalf("codexInlineFileEdit=%v want %v", got, tc.want)
			}
		})
	}
	nested := "const r = await tools.exec_command({cmd: \"cat > a.md <<'EOF'\\nbody\\nEOF\"});\n" + strings.Repeat("// pad\n", 700)
	if !codexInlineFileEdit("exec", "", nested) {
		t.Fatal("nested code-mode heredoc write was not classified as a file edit")
	}
}

const shellFileEditMarker = "secretmarker"

// shellFileEditCommands are two file writes and one read-only inspection that
// mentions write syntax only in comments and a triple-quoted string and tees
// its output to /dev/null.
func shellFileEditCommands() (writes []string, read string) {
	m := shellFileEditMarker
	return []string{
			padInline("python3 - <<'EOF'\nfrom pathlib import Path\nPath('" + m + ".go').write_text('package x')\nEOF"),
			padInline("cat > " + m + ".md <<'EOF'\nbody\nEOF"),
		},
		padInline("python3 - <<'EOF' | tee /dev/null\n# cat > " + m + ".txt <<EOF\n# Example: python3 -c \"p.write_text('x')\"\nnote = \"\"\"\np.write_text(value)\n\"\"\"\nimport json\nprint(json.load(open('" + m + ".json')))\nEOF")
}

func TestClaudeShellFileEditsAreNotInlineOrchestration(t *testing.T) {
	root, sources := t.TempDir(), t.TempDir()
	usage := map[string]any{"input_tokens": 1, "output_tokens": 1}
	writes, read := shellFileEditCommands()
	records := []any{
		claudeFixtureAssistant(1, "m1", "tool_use", usage, claudeFixtureToolUse("e1", "Bash", map[string]any{"command": writes[0]})),
		claudeFixtureToolResult(2, "e1", false, ""),
		claudeFixtureAssistant(3, "m2", "tool_use", usage, claudeFixtureToolUse("e2", "Bash", map[string]any{"command": writes[1]})),
		claudeFixtureToolResult(4, "e2", false, ""),
		claudeFixtureAssistant(5, "m3", "tool_use", usage, claudeFixtureToolUse("r1", "Bash", map[string]any{"command": read})),
		claudeFixtureToolResult(6, "r1", false, "{}"),
	}
	for _, raw := range records {
		raw.(map[string]any)["cwd"] = root
	}
	path := writeClaudeSessionFixture(t, sources, "shell-file-edits", records)
	assertShellFileEditsSplit(t, "claude", root, sources, path, time.Date(2026, 7, 29, 8, 0, 0, 0, time.UTC))
}

func TestCodexShellFileEditsAreNotInlineOrchestration(t *testing.T) {
	root, sources := t.TempDir(), t.TempDir()
	at := time.Date(2026, 7, 24, 8, 0, 0, 0, time.UTC)
	row := func(second int, kind string, payload map[string]any) any {
		return map[string]any{"timestamp": at.Add(time.Duration(second) * time.Second).Format(time.RFC3339), "type": kind, "payload": payload}
	}
	writes, read := shellFileEditCommands()
	events := []any{row(0, "session_meta", map[string]any{"cwd": root})}
	for i, command := range append(writes, read) {
		id := fmt.Sprintf("c%d", i)
		events = append(events,
			row(2*i+1, "response_item", map[string]any{"type": "function_call", "call_id": id, "name": "exec_command", "arguments": execCommandArguments(t, command)}),
			row(2*i+2, "response_item", map[string]any{"type": "function_call_output", "call_id": id, "output": "exit code: 0"}),
		)
	}
	path := writeCodexSessionFixture(t, sources, "shell-file-edits", events)
	assertShellFileEditsSplit(t, "codex", root, sources, path, at)
}

func assertShellFileEditsSplit(t *testing.T, providerName, root, sources, path string, at time.Time) {
	t.Helper()
	provider, err := resolveSessionSource(providerName)
	if err != nil {
		t.Fatal(err)
	}
	discovery := sessionDiscovery{Dirs: []string{sources}, Files: []string{path}}
	store, err := openSessionStore(filepath.Join(t.TempDir(), "index.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	direct, err := analyzeProviderSessions(provider, discovery, root, at, at.Add(time.Hour), "", ownershipCatalog{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	stats, err := store.refresh(t.Context(), provider.Name(), discovery, root, provider, ownershipCatalog{}, false)
	if err != nil {
		t.Fatal(err)
	}
	indexed, err := store.analyze(t.Context(), provider.Name(), discovery.Dirs, root, at, at.Add(time.Hour), "", ownershipCatalog{}, stats)
	if err != nil {
		t.Fatal(err)
	}
	for label, report := range map[string]codexSessionInsightsReport{"direct": direct, "indexed": indexed} {
		summary := report.Summary
		if got := summary.ShellFileEdits["exec_command"].Calls; got != 2 || summary.ShellFileEditSessions != 1 {
			t.Fatalf("%s: shell file edits=%d sessions=%d want 2/1", label, got, summary.ShellFileEditSessions)
		}
		if summary.InlineOrchestrationCalls != 1 {
			t.Fatalf("%s: inline orchestration calls=%d want 1 (the read-only script)", label, summary.InlineOrchestrationCalls)
		}
		var inline *sessionFinding
		findings := buildSessionFindings(report, defaultRepositoryConfig())
		for i := range findings {
			if findings[i].Title == "very long CLI input is rebuilding an inspection workflow" {
				inline = &findings[i]
			}
		}
		// Shell file writes are routine harness behaviour: counted, never a finding.
		if inline == nil || inline.Count != 1 {
			t.Fatalf("%s: inline orchestration finding=%+v", label, inline)
		}
		encoded, err := json.Marshal(struct {
			Report   codexSessionInsightsReport
			Findings []sessionFinding
		}{report, findings})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), shellFileEditMarker) {
			t.Fatalf("%s: report leaks command content", label)
		}
	}
}
