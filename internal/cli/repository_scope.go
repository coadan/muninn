package cli

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
)

var codexWorkdirStartPattern = regexp.MustCompile(
	`(?:^|[,{]\s*)(?:"workdir"|'workdir'|workdir)\s*:\s*`,
)

func codexToolWorkingDirectories(toolName, arguments, input string) []string {
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "exec_command":
		var decoded struct {
			Workdir string `json:"workdir"`
		}
		if json.Unmarshal([]byte(arguments), &decoded) == nil {
			return appendWorkingDirectory(nil, decoded.Workdir)
		}
	case "exec":
		var directories []string
		for _, location := range codexWorkdirStartPattern.FindAllStringIndex(input, -1) {
			value, _, ok := codexJavaScriptString(input, location[1])
			if ok {
				directories = appendWorkingDirectory(directories, value)
			}
		}
		return directories
	}
	return nil
}

func appendWorkingDirectory(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return values
	}
	value = filepath.Clean(value)
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func eventRepositoryScope(
	event normalizedSessionEvent,
	sessionCWD,
	repositoryRoot string,
) (inside, explicit bool) {
	if len(event.WorkingDirectories) > 0 {
		for _, directory := range event.WorkingDirectories {
			if !filepath.IsAbs(directory) {
				directory = filepath.Join(sessionCWD, directory)
			}
			if matches, err := pathInsideRoot(repositoryRoot, directory); err == nil && matches {
				return true, true
			}
		}
		return false, true
	}
	if event.Kind != sessionEventToolCall {
		return false, false
	}
	if event.ToolName == "apply_patch" && len(event.TargetCandidates) > 0 {
		sawAbsolute := false
		for _, target := range event.TargetCandidates {
			if !filepath.IsAbs(target) {
				continue
			}
			sawAbsolute = true
			if matches, err := pathInsideRoot(repositoryRoot, target); err == nil && matches {
				return true, true
			}
		}
		if sawAbsolute {
			return false, true
		}
	}
	if event.ToolName == "exec" || event.ToolName == "exec_command" {
		matches, err := pathInsideRoot(repositoryRoot, sessionCWD)
		return err == nil && matches, true
	}
	return false, false
}

func normalizedSessionTouchesRepository(session normalizedSession, repositoryRoot string) bool {
	inside, err := pathInsideRoot(repositoryRoot, session.CWD)
	if err != nil {
		return false
	}
	inRepositoryScope := inside
	for _, event := range session.Events {
		if eventInside, explicit := eventRepositoryScope(event, session.CWD, repositoryRoot); explicit {
			inRepositoryScope = eventInside
		}
		if inRepositoryScope {
			return true
		}
	}
	return false
}

func markRepositoryEventScope(session *normalizedSession, repositoryRoot string) {
	inside, err := pathInsideRoot(repositoryRoot, session.CWD)
	if err != nil {
		inside = false
	}
	for index := range session.Events {
		event := &session.Events[index]
		if eventInside, explicit := eventRepositoryScope(*event, session.CWD, repositoryRoot); explicit {
			inside = eventInside
		}
		event.InRepositoryScope = inside
	}
}

func eventRepositoryWorkingDirectory(
	event normalizedSessionEvent,
	sessionCWD,
	repositoryRoot string,
) string {
	for _, directory := range event.WorkingDirectories {
		if !filepath.IsAbs(directory) {
			directory = filepath.Join(sessionCWD, directory)
		}
		if inside, err := pathInsideRoot(repositoryRoot, directory); err == nil && inside {
			return filepath.Clean(directory)
		}
	}
	if inside, err := pathInsideRoot(repositoryRoot, sessionCWD); err == nil && inside {
		return sessionCWD
	}
	return repositoryRoot
}
