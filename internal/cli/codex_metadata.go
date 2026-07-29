package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
)

func codexSessionMetadata(discovery sessionDiscovery) map[string]normalizedSessionMetadata {
	return loadCodexSessionMetadata(discovery.Dirs)
}

func readCodexRolloutLineage(path string) (string, string) {
	file, err := os.Open(path)
	if err != nil {
		return "", ""
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 16*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if !bytes.Contains(line, []byte(`"session_meta"`)) {
			continue
		}
		var envelope codexRolloutEnvelope
		if json.Unmarshal(line, &envelope) != nil || envelope.Type != "session_meta" {
			continue
		}
		var payload codexRolloutPayload
		if json.Unmarshal(envelope.Payload, &payload) != nil {
			continue
		}
		lineage := ""
		parent := ""
		if payload.ID != "" {
			lineage = ownershipSelectorDigest("provider-thread", payload.ID)
		}
		if payload.ParentThreadID != "" {
			parent = ownershipSelectorDigest("provider-thread", payload.ParentThreadID)
		}
		return lineage, parent
	}
	return "", ""
}
