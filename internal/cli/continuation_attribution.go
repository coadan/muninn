package cli

// Continuation calls are real provider tool calls, but they do not start a new
// shell command or owned operation. The parser intentionally carries the
// original descriptor onto their output so later output, failures, and waits
// remain attributable to the command that is still running.
func withoutContinuationCallAttribution(event normalizedSessionEvent) normalizedSessionEvent {
	if event.Kind != sessionEventToolCall ||
		event.Family == "" ||
		event.FirstFamily != "" ||
		event.LastFamily != "" ||
		(event.ToolName != "write_stdin" && event.ToolName != "wait" && event.ToolName != "exec") {
		return event
	}
	event.Family = ""
	event.Shape = ""
	event.SelectorDigests = nil
	event.CommandCandidates = nil
	event.OwnedOperations = nil
	event.OperationAttributionAmbiguous = false
	return event
}
