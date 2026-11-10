package cli

import (
	"path/filepath"
	"regexp"
	"strings"
)

// A successful standalone gh pr create result is delivery evidence at the
// creation's position and working directory. Later pr-link records can bind
// existing PRs and must not deliver edits made since creation.
type claudeDeliveryState struct {
	created map[string]bool
}

var claudeCreatedPRPattern = regexp.MustCompile(`https?://[^\s<>"']+/pull/[0-9]+`)

func (state *claudeDeliveryState) observeOutput(block claudeContentBlock, call claudeCall, event normalizedSessionEvent) (normalizedSessionEvent, bool) {
	if !call.PRCreate || event.ToolName != "exec_command" || event.Failed || event.Refusal != "" || event.OperationContinues {
		return normalizedSessionEvent{}, false
	}
	for _, invocation := range event.CommandCandidates {
		if invocation.Executable != "gh" || len(invocation.Args) < 2 || invocation.Args[0] != "pr" || invocation.Args[1] != "create" {
			continue
		}
		if state.created == nil {
			state.created = map[string]bool{}
		}
		text := strings.TrimSpace(claudeToolResultText(block.Content))
		if url := claudeCreatedPRPattern.FindString(text); url != "" && url == text && !state.created[url] {
			state.created[url] = true
			return normalizedSessionEvent{OccurredAt: event.OccurredAt, Kind: sessionEventDelivery,
				WorkingDirectories: call.WorkingDirectories}, true
		}
	}
	return normalizedSessionEvent{}, false
}

// ponytail: only standalone foreground creation with one returned URL is
// evidence. Broaden this only when a provider exposes explicit creation status.
func claudeStandalonePRCreate(command string) bool {
	var quote byte
	escaped := false
	for i := 0; i < len(command); i++ {
		current := command[i]
		if escaped {
			escaped = false
			continue
		}
		if quote == '\'' {
			if current == quote {
				quote = 0
			}
			continue
		}
		if current == '\\' {
			escaped = true
			continue
		}
		if current == '`' || current == '$' && i+1 < len(command) && command[i+1] == '(' {
			return false
		}
		if quote == '"' {
			if current == quote {
				quote = 0
			}
			continue
		}
		if strings.ContainsRune(";&|\n\r<>()", rune(current)) {
			return false
		}
		if current == '\'' || current == '"' {
			quote = current
		}
	}
	if quote != 0 || escaped {
		return false
	}
	segments := codexShellCommandSegments(command)
	if len(segments) != 1 {
		return false
	}
	tokens := segments[0]
	return len(tokens) >= 3 && filepath.Base(tokens[0]) == "gh" && tokens[1] == "pr" && tokens[2] == "create"
}
