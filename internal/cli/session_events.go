package cli

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var nonZeroExitCodePattern = regexp.MustCompile(`(?i)"exit_code"\s*:\s*[1-9][0-9]*`)
var nonZeroDisplayExitCodePattern = regexp.MustCompile(`(?im)^exit code:\s*[1-9][0-9]*`)
var nonZeroProcessExitCodePattern = regexp.MustCompile(`(?i)process exited with code\s+[1-9][0-9]*`)
var searchMissExitCodePattern = regexp.MustCompile(`(?im)(?:"exit_code"\s*:\s*1(?:[^0-9]|$)|^exit code:\s*1(?:[^0-9]|$)|process exited with code\s+1(?:[^0-9]|$))`)
var cliErrorCodePattern = regexp.MustCompile(`(?is)"error"\s*:\s*\{\s*"code"\s*:\s*"([a-z][a-z0-9_-]{0,63})"`)

func addCodexToolMetrics(metrics map[string]codexToolMetrics, key string, calls int, failed, truncated bool, outputBytes int64) {
	if key == "" {
		return
	}
	value := metrics[key]
	value.Calls += calls
	if failed {
		value.FailedCalls++
	}
	if truncated {
		value.TruncatedCalls++
	}
	value.OutputBytes += outputBytes
	value.EstimatedOutputTokens = estimatedTokens(value.OutputBytes)
	metrics[key] = value
}

func sessionActivityKey(kind, target string) string {
	return kind + "\x00" + target
}

func touchSessionActivity(activity map[string]time.Time, kind, target string, occurredAt time.Time) {
	if occurredAt.IsZero() {
		return
	}
	key := sessionActivityKey(kind, target)
	if current := activity[key]; current.IsZero() || occurredAt.After(current) {
		activity[key] = occurredAt
	}
}

func mergeSessionActivity(target, additions map[string]time.Time) {
	for key, occurredAt := range additions {
		if current := target[key]; current.IsZero() || occurredAt.After(current) {
			target[key] = occurredAt
		}
	}
}

func pathInsideRoot(root, path string) (bool, error) {
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return false, err
	}
	relative, err := filepath.Rel(root, absolutePath)
	if err != nil {
		return false, err
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))), nil
}

func codexTaskName(workspaceRoot, cwd string) string {
	relative, err := filepath.Rel(workspaceRoot, cwd)
	if err != nil || relative == "." {
		return "(root)"
	}
	parts := strings.Split(filepath.Clean(relative), string(filepath.Separator))
	if len(parts) >= 2 && parts[0] == ".worktrees" {
		return parts[1]
	}
	if len(parts) >= 3 && parts[0] == ".workbench" && parts[1] == "worktrees" {
		return parts[2]
	}
	if len(parts) >= 3 && parts[0] == ".workbench" && parts[1] == "repos" {
		return "(cached)/" + parts[2]
	}
	return "(root)"
}

func codexToolOutputText(raw json.RawMessage) (string, string, int64) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", "", 0
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", "", int64(len(raw))
	}
	var texts []string
	var collect func(any)
	collect = func(item any) {
		switch typed := item.(type) {
		case string:
			texts = append(texts, typed)
		case []any:
			for _, child := range typed {
				collect(child)
			}
		case map[string]any:
			if text, ok := typed["text"].(string); ok {
				texts = append(texts, text)
				return
			}
			if output, ok := typed["output"]; ok {
				collect(output)
			}
		}
	}
	collect(value)
	text := strings.Join(texts, "\n")
	statusText := ""
	if len(texts) > 0 {
		statusText = texts[0]
	}
	return text, statusText, int64(len([]byte(text)))
}

func codexToolOutputTruncated(text string) bool {
	lower := strings.ToLower(text)
	return strings.Contains(lower, "warning: truncated output") ||
		strings.Contains(lower, "output truncated") ||
		strings.Contains(lower, "…truncated")
}

func codexToolOutputFailed(statusText, toolName string) bool {
	preview := statusText
	if len(preview) > 8192 {
		preview = preview[:8192]
	}
	lower := strings.ToLower(preview)
	if strings.HasPrefix(strings.TrimSpace(lower), "error:") ||
		strings.HasPrefix(strings.TrimSpace(lower), "failed:") ||
		nonZeroDisplayExitCodePattern.MatchString(preview) {
		return true
	}
	execLike := strings.Contains(strings.ToLower(toolName), "exec") ||
		strings.EqualFold(toolName, "write_stdin") ||
		strings.EqualFold(toolName, "wait")
	if !execLike {
		return false
	}
	if strings.Contains(lower, "script failed") ||
		nonZeroProcessExitCodePattern.MatchString(preview) ||
		strings.Contains(lower, "command timed out") ||
		strings.Contains(lower, "timed out after") {
		return true
	}
	if nonZeroExitCodePattern.MatchString(preview) {
		return true
	}
	return false
}

func codexToolFailureReason(statusText string) string {
	preview := strings.ToLower(statusText)
	if len(preview) > 32768 {
		preview = preview[:32768]
	}
	if reason := codexCLIErrorReason(preview); reason != "" {
		return reason
	}
	switch {
	case strings.Contains(preview, "--api override is disabled"):
		return "local CLI targeting"
	case strings.Contains(preview, "missing test result sentinel") ||
		strings.Contains(preview, "failed before reporting test results"):
		return "test harness protocol"
	case strings.Contains(preview, "verification changed files after staging"):
		return "verification changed staged state"
	case strings.Contains(preview, "head sha can't be blank") ||
		strings.Contains(preview, "base sha can't be blank") ||
		strings.Contains(preview, "no commits between"):
		return "PR branch state"
	case strings.Contains(preview, "unknown nrepl target") ||
		strings.Contains(preview, "unsupported nrepl target"):
		return "unsupported command target"
	case strings.Contains(preview, "unknown option") ||
		strings.Contains(preview, "unknown flag") ||
		strings.Contains(preview, `"code":"invalid-option"`) ||
		strings.Contains(preview, "unrecognized option"):
		return "unsupported command option"
	case strings.Contains(preview, "cannot combine --table with named views"):
		return "unsupported command combination"
	case strings.Contains(preview, "specify --playthrough latest or an exact id"):
		return "ambiguous playthrough selection"
	case strings.Contains(preview, "no diagnostic snapshot"):
		return "missing diagnostic evidence"
	case strings.Contains(preview, "address already in use") ||
		strings.Contains(preview, "port already in use") ||
		strings.Contains(preview, "bind: address in use"):
		return "port collision"
	case strings.Contains(preview, "connection refused") ||
		strings.Contains(preview, "emulator unavailable") ||
		strings.Contains(preview, "spacetimedb is not running") ||
		strings.Contains(preview, "no tracked void development runtime") ||
		strings.Contains(preview, "failed to find database"):
		return "local service unavailable"
	case strings.Contains(preview, "http 502") ||
		strings.Contains(preview, "http 503") ||
		strings.Contains(preview, "http 504") ||
		strings.Contains(preview, "bad gateway") ||
		strings.Contains(preview, "gateway timeout") ||
		strings.Contains(preview, "service unavailable") ||
		strings.Contains(preview, "couldn't respond to github"):
		return "transient service failure"
	case strings.Contains(preview, "command not found") ||
		strings.Contains(preview, "executable file not found"):
		return "missing executable"
	case strings.Contains(preview, "no such file or directory") ||
		strings.Contains(preview, "file not found") ||
		strings.Contains(preview, "missing fixture"):
		return "missing path or fixture"
	case strings.Contains(preview, "command timed out") ||
		strings.Contains(preview, "timed out after"):
		return "timeout"
	case strings.Contains(preview, "process exited with code 130") ||
		strings.Contains(preview, "process exited with code 137") ||
		strings.Contains(preview, "terminated by signal"):
		return "interrupted process"
	case strings.Contains(preview, "lint failed") ||
		strings.Contains(preview, "linting took") ||
		strings.Contains(preview, "clj-kondo found"):
		return "lint failure"
	case strings.Contains(preview, "fail in (") ||
		strings.Contains(preview, "tests failed") ||
		strings.Contains(preview, "test failed"):
		return "test failure"
	default:
		return "other non-zero exit"
	}
}

func codexCLIErrorReason(text string) string {
	matches := cliErrorCodePattern.FindStringSubmatch(text)
	if len(matches) != 2 {
		return ""
	}
	// The whitelist keeps normalization privacy-safe: only fixed command error
	// codes are retained, never error messages or suggested actions.
	switch matches[1] {
	case "invalid-option":
		return "unsupported command option"
	case "unknown-command":
		return "unknown command"
	case "invalid-arguments":
		return "invalid command arguments"
	case "operation-failed":
		return "operation failure"
	case "error-report-failed":
		return "error reporting failure"
	case "flows_push_save_failed":
		return "flows push save failure"
	case "flows_push_validation_failed":
		return "flows push validation failure"
	default:
		return ""
	}
}

func codexToolFailureReasonForDescriptor(statusText string, descriptor codexToolCallDescriptor) string {
	reason := codexToolFailureReason(statusText)
	if reason != "other non-zero exit" || !searchMissExitCodePattern.MatchString(statusText) {
		return reason
	}
	if descriptor.Family == "search" || strings.Contains(descriptor.Shape, "search") {
		return "search no match"
	}
	return reason
}

func codexFailureContextLabel(descriptor codexToolCallDescriptor) string {
	if descriptor.Shape != "" {
		return descriptor.Shape
	}
	if descriptor.Family != "" {
		return descriptor.Family
	}
	if descriptor.Name != "" {
		return "tool " + descriptor.Name
	}
	return "(unknown)"
}

func addCodexFailureContext(contexts map[string]map[string]int, reason, context string) {
	if reason == "" {
		return
	}
	if context == "" {
		context = "(unknown)"
	}
	if contexts[reason] == nil {
		contexts[reason] = map[string]int{}
	}
	contexts[reason][context]++
}
