package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func writeWorkbenchFixture(t *testing.T, root, task, name, content string) string {
	t.Helper()
	path := filepath.Join(root, ".workbench", "tasks", task, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestWorkbenchReviewsCountRetainedSnapshotsWithoutPrivateFields(t *testing.T) {
	root := t.TempDir()
	since := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	until := since.Add(24 * time.Hour)
	writeWorkbenchFixture(t, root, "PRIVATE-task-A", "receipt.json", `{"tree":"PRIVATE-current-tree"}`)
	writeWorkbenchFixture(t, root, "PRIVATE-task-A", "reviews/code.json", `{"ok":true,"verdict":"pass","ms":1200,"at":"2026-11-09T01:00:00Z","tree":"PRIVATE-current-tree","key":"PRIVATE-key","summary":"PRIVATE-prompt","findings":[]}`)
	writeWorkbenchFixture(t, root, "PRIVATE-task-A", "reviews/qa.json", `{"ok":true,"verdict":"fail","ms":1800,"at":"2026-11-09T02:00:00Z","tree":"PRIVATE-previous-tree","findings":[{"severity":"blocking","escalate":true,"title":"PRIVATE-title","detail":"/Users/PRIVATE-path","evidence":"PRIVATE-secret"},{"severity":"blocking"},{"severity":"recommendation"}]}`)
	writeWorkbenchFixture(t, root, "PRIVATE-task-B", "reviews/private-reviewer.json", `{"ok":false,"ms":400,"at":"2026-11-09T03:00:00Z","tree":"PRIVATE-tree","error":"PRIVATE-command"}`)
	writeWorkbenchFixture(t, root, "PRIVATE-task-C", "reviews/custom.json", `{"ok":true,"verdict":"pass","ms":600,"at":"2026-11-09T04:00:00Z","tree":"PRIVATE-tree"}`)
	for name, at := range map[string]string{"old": "2026-11-08T23:59:59Z", "future": "2026-11-10T00:00:00Z"} {
		writeWorkbenchFixture(t, root, "PRIVATE-task-C", "reviews/"+name+".json", `{"ok":true,"verdict":"fail","ms":999,"at":"`+at+`","tree":"PRIVATE-tree"}`)
	}
	writeWorkbenchFixture(t, root, "PRIVATE-task-A", "reviews/code.out.json", `{"verdict":"pass","summary":"PRIVATE-output"}`)
	writeWorkbenchFixture(t, root, "PRIVATE-task-A", "reviews/code.out.json.runner.json", `{"backend":"PRIVATE-runner"}`)
	writeWorkbenchFixture(t, root, "PRIVATE-task-A", "reviews/code.tier.json", `{"model":"PRIVATE-runner"}`)
	got := inspectWorkbenchReviews(root, since, until)
	// Independent worked example: 1200 + 1800 + 400 + 600 = 4000 ms.
	want := workbenchReviewMetrics{Results: 4, Passed: 2, Failed: 1, RunnerErrors: 1,
		DurationMs: 4000, MaxDurationMs: 1800, BlockingFindings: 2, RecommendationFindings: 1,
		Escalations: 1, ReceiptMatchedResults: 1, ReceiptMismatchedResults: 1, ReceiptUnavailableResults: 2}
	if got == nil || got.Tasks != 3 || got.Coverage != "latest-per-task-reviewer" || got.Scope != "repository" || !reflect.DeepEqual(got.workbenchReviewMetrics, want) {
		t.Fatalf("summary=%+v want=%+v", got, want)
	}
	if got.SkippedResults != 0 || got.UnreadableTasks != 0 || len(got.ByRole) != 3 || got.ByRole["other"].Results != 2 {
		t.Fatalf("coverage or roles=%+v", got)
	}
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "PRIVATE") {
		t.Fatalf("private data in summary: %s", raw)
	}
	// Overwriting is replacement, never another observed review round.
	writeWorkbenchFixture(t, root, "PRIVATE-task-A", "reviews/code.json", `{"ok":true,"verdict":"fail","ms":1200,"at":"2026-11-09T05:00:00Z","tree":"PRIVATE-current-tree"}`)
	after := inspectWorkbenchReviews(root, since, until)
	if after.Results != 4 || after.Passed != 1 || after.Failed != 2 {
		t.Fatalf("overwrite=%+v", after)
	}
}

func TestWorkbenchReviewsExposePartialCoverageAndRejectUnsafeInputs(t *testing.T) {
	root := t.TempDir()
	valid := `{"ok":true,"verdict":"pass","ms":50,"at":"2026-11-09T01:00:00Z","tree":"tree"}`
	path := writeWorkbenchFixture(t, root, "task", "reviews/code.json", valid)
	for name, content := range map[string]string{
		"malformed": `{`, "missing": `{}`, "date": strings.Replace(valid, "2026-11-09T01:00:00Z", "unknown", 1),
		"negative":  strings.Replace(valid, `"ms":50`, `"ms":-1`, 1),
		"severity":  strings.TrimSuffix(valid, "}") + `,"findings":[{"severity":"PRIVATE"}]}`,
		"oversized": strings.Repeat(" ", (1<<20)+1),
	} {
		writeWorkbenchFixture(t, root, "task", "reviews/"+name+".json", content)
	}
	if err := os.Symlink(path, filepath.Join(filepath.Dir(path), "linked.json")); err != nil {
		t.Fatal(err)
	}
	writeWorkbenchFixture(t, root, "task", "receipt.json", `{"tree":"tree"} trailing-data`)
	// A symlinked task folder is not followed and counts as unreadable.
	outside := writeWorkbenchFixture(t, t.TempDir(), "elsewhere", "reviews/code.json", valid)
	if err := os.Symlink(filepath.Dir(filepath.Dir(outside)), filepath.Join(root, ".workbench", "tasks", "linked-task")); err != nil {
		t.Fatal(err)
	}
	got := inspectWorkbenchReviews(root, time.Time{}, time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC))
	if got.Results != 1 || got.SkippedResults != 7 || got.ReceiptUnavailableResults != 1 || got.UnreadableTasks != 1 {
		t.Fatalf("partial coverage=%+v", got)
	}
	if inspectWorkbenchReviews(t.TempDir(), time.Time{}, time.Now()) != nil {
		t.Fatal("missing Workbench should be absent")
	}
}

func TestAnalyzeIncludesWorkbenchSnapshotsInBothFormatsAndCacheModes(t *testing.T) {
	root, sessions, db := t.TempDir(), t.TempDir(), filepath.Join(t.TempDir(), "index.sqlite")
	at := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano)
	writeWorkbenchFixture(t, root, "PRIVATE-task", "reviews/code.json", `{"ok":true,"verdict":"pass","ms":50,"at":"`+at+`","tree":"PRIVATE-tree","summary":"PRIVATE-message"}`)
	for _, provider := range []string{"codex", "claude"} {
		for _, flags := range [][]string{{"--no-cache"}, {"--no-cache", "--details"}, {"--db", db}, {"--db", db, "--details"}} {
			args := append([]string{"--provider", provider, "--sessions-dir", sessions, "--task", "selected"}, flags...)
			output, err := captureStdout(t, func() error { return cmdAnalyze(root, args) })
			if err != nil {
				t.Fatal(err)
			}
			var report struct {
				Reviews *workbenchReviewSummary `json:"workbenchReviews"`
			}
			if err := json.Unmarshal([]byte(output), &report); err != nil {
				t.Fatal(err)
			}
			if report.Reviews == nil || report.Reviews.Passed != 1 || report.Reviews.Scope != "repository" {
				t.Fatalf("%v: %s", args, output)
			}
			if strings.Contains(output, "PRIVATE") {
				t.Fatalf("private data in CLI output")
			}
		}
	}
	data, err := os.ReadFile(db)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "PRIVATE") {
		t.Fatal("private review data in index")
	}
}

func TestWorkbenchReviewsUsePrimaryCheckoutFromLinkedWorktree(t *testing.T) {
	root, linked := t.TempDir(), filepath.Join(t.TempDir(), "linked")
	for _, args := range [][]string{{"init", "-q"}, {"-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "fixture", "--allow-empty"}, {"worktree", "add", "-qb", "fixture", linked}} {
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git: %v %s", err, out)
		}
	}
	writeWorkbenchFixture(t, root, "task", "reviews/code.json", `{"ok":true,"verdict":"pass","ms":50,"at":"2026-11-09T01:00:00Z","tree":"tree"}`)
	got := inspectWorkbenchReviews(linked, time.Time{}, time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC))
	if got == nil || got.Results != 1 {
		t.Fatalf("linked checkout=%+v", got)
	}
}

// A separate git directory is not the primary checkout's ".git", so the
// analysed checkout itself must own the task folder.
func TestWorkbenchReviewsKeepCheckoutWithSeparateGitDir(t *testing.T) {
	root, gitDir := t.TempDir(), filepath.Join(t.TempDir(), "store.git")
	if out, err := exec.Command("git", "-C", root, "init", "-q", "--separate-git-dir", gitDir).CombinedOutput(); err != nil {
		t.Fatalf("git: %v %s", err, out)
	}
	writeWorkbenchFixture(t, root, "task", "reviews/code.json", `{"ok":true,"verdict":"pass","ms":50,"at":"2026-11-09T01:00:00Z","tree":"tree"}`)
	got := inspectWorkbenchReviews(root, time.Time{}, time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC))
	if got == nil || got.Results != 1 {
		t.Fatalf("separate git dir checkout=%+v", got)
	}
}
