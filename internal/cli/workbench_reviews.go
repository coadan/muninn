package cli

import (
	"encoding/json"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Workbench overwrites each task/reviewer's result. These are retained snapshots,
// not review history, session attribution, token costs, or acceptance outcomes.
type workbenchReviewSummary struct {
	Coverage        string `json:"coverage"`
	Scope           string `json:"scope"`
	Tasks           int    `json:"tasks"`
	SkippedResults  int    `json:"skippedResults"`
	UnreadableTasks int    `json:"unreadableTasks"`
	workbenchReviewMetrics
	ByRole map[string]workbenchReviewMetrics `json:"byRole"`
}

type workbenchReviewMetrics struct {
	Results                   int   `json:"results"`
	Passed                    int   `json:"passed"`
	Failed                    int   `json:"failed"`
	RunnerErrors              int   `json:"runnerErrors"`
	DurationMs                int64 `json:"durationMs"`
	MaxDurationMs             int64 `json:"maxDurationMs"`
	BlockingFindings          int   `json:"blockingFindings"`
	RecommendationFindings    int   `json:"recommendationFindings"`
	Escalations               int   `json:"escalations"`
	ReceiptMatchedResults     int   `json:"receiptMatchedResults"`
	ReceiptMismatchedResults  int   `json:"receiptMismatchedResults"`
	ReceiptUnavailableResults int   `json:"receiptUnavailableResults"`
}

// Decode only fields needed for numeric aggregation. Prose, task names, trees
// and result keys never enter the report or the session index.
type retainedWorkbenchReview struct {
	Tree     string `json:"tree"`
	At       string `json:"at"`
	OK       *bool  `json:"ok"`
	Verdict  string `json:"verdict"`
	Ms       *int64 `json:"ms"`
	Findings []struct {
		Severity string `json:"severity"`
		Escalate bool   `json:"escalate"`
	} `json:"findings"`
}

var workbenchResultNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*\.json$`)

func inspectWorkbenchReviews(root string, since, until time.Time) *workbenchReviewSummary {
	// Match Workbench's primary checkout ownership, including linked worktrees.
	// Only a ".git" common directory sits inside its primary checkout; a bare
	// or separate git directory says nothing about where tasks live.
	if common, err := exec.Command("git", "-C", root, "rev-parse", "--path-format=absolute", "--git-common-dir").Output(); err == nil {
		if common := filepath.Clean(strings.TrimSpace(string(common))); filepath.Base(common) == ".git" {
			root = filepath.Dir(common)
		}
	}
	tasksPath := filepath.Join(root, ".workbench", "tasks")
	if _, err := os.Lstat(tasksPath); os.IsNotExist(err) {
		return nil
	}
	summary := &workbenchReviewSummary{
		Coverage: "latest-per-task-reviewer", Scope: "repository",
		ByRole: map[string]workbenchReviewMetrics{},
	}
	if !workbenchDirectory(filepath.Dir(tasksPath)) || !workbenchDirectory(tasksPath) {
		summary.UnreadableTasks++
		return summary
	}
	tasks, err := os.ReadDir(tasksPath)
	if err != nil {
		summary.UnreadableTasks++
		return summary
	}
	for _, task := range tasks {
		if task.Type()&os.ModeSymlink != 0 {
			summary.UnreadableTasks++
			continue
		}
		if !task.IsDir() {
			continue
		}
		taskPath := filepath.Join(tasksPath, task.Name())
		reviewsPath := filepath.Join(taskPath, "reviews")
		if _, err := os.Lstat(reviewsPath); os.IsNotExist(err) {
			continue
		}
		if !workbenchDirectory(reviewsPath) {
			summary.UnreadableTasks++
			continue
		}
		reviews, err := os.ReadDir(reviewsPath)
		if err != nil {
			summary.UnreadableTasks++
			continue
		}
		var receipt struct {
			Tree string `json:"tree"`
		}
		if err := readWorkbenchJSON(filepath.Join(taskPath, "receipt.json"), &receipt); err != nil {
			receipt.Tree = ""
		}
		counted := false
		for _, file := range reviews {
			name := file.Name()
			// Workbench reviewer names contain no dots. This also excludes
			// output, runner and wrapper sidecars without reading their contents.
			if !workbenchResultNamePattern.MatchString(name) {
				continue
			}
			var result retainedWorkbenchReview
			if err := readWorkbenchJSON(filepath.Join(reviewsPath, name), &result); err != nil || !validWorkbenchReview(result) {
				summary.SkippedResults++
				continue
			}
			at, err := time.Parse(time.RFC3339Nano, result.At)
			if err != nil {
				summary.SkippedResults++
				continue
			}
			if at.Before(since) || !at.Before(until) {
				continue
			}
			if *result.Ms > math.MaxInt64-summary.DurationMs {
				summary.SkippedResults++
				continue
			}
			role := strings.TrimSuffix(name, ".json")
			switch role {
			case "code", "qa", "adr", "jira":
			default:
				role = "other"
			}
			metrics := summary.ByRole[role]
			metrics.add(result, receipt.Tree)
			summary.ByRole[role] = metrics
			summary.workbenchReviewMetrics.add(result, receipt.Tree)
			counted = true
		}
		if counted {
			summary.Tasks++
		}
	}
	return summary
}

func validWorkbenchReview(result retainedWorkbenchReview) bool {
	if result.OK == nil || result.Ms == nil || *result.Ms < 0 || result.Tree == "" {
		return false
	}
	if *result.OK && result.Verdict != "pass" && result.Verdict != "fail" {
		return false
	}
	for _, finding := range result.Findings {
		if finding.Severity != "blocking" && finding.Severity != "recommendation" {
			return false
		}
	}
	return true
}

func (metrics *workbenchReviewMetrics) add(result retainedWorkbenchReview, receiptTree string) {
	metrics.Results++
	metrics.DurationMs += *result.Ms
	metrics.MaxDurationMs = max(metrics.MaxDurationMs, *result.Ms)
	if !*result.OK {
		metrics.RunnerErrors++
	} else {
		if result.Verdict == "pass" {
			metrics.Passed++
		} else {
			metrics.Failed++
		}
		for _, finding := range result.Findings {
			if finding.Severity == "blocking" {
				metrics.BlockingFindings++
			} else {
				metrics.RecommendationFindings++
			}
			if finding.Escalate {
				metrics.Escalations++
			}
		}
	}
	switch {
	case receiptTree == "":
		metrics.ReceiptUnavailableResults++
	case result.Tree == receiptTree:
		metrics.ReceiptMatchedResults++
	default:
		metrics.ReceiptMismatchedResults++
	}
}

func workbenchDirectory(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0
}

func readWorkbenchJSON(path string, target any) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return os.ErrInvalid
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil {
		return err
	}
	if len(data) > 1<<20 {
		return os.ErrInvalid
	}
	return json.Unmarshal(data, target)
}
