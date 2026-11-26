// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/common/embed"
)

func (h *TestHarness) resolveEmbedsInPayload(t *testing.T, payload json.RawMessage) json.RawMessage {
	t.Helper()

	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return payload
	}
	resultRaw, ok := envelope["result"]
	if !ok || len(resultRaw) == 0 {
		return payload
	}

	var result map[string]json.RawMessage
	if err := json.Unmarshal(resultRaw, &result); err != nil {
		return payload
	}
	statusRaw, ok := result["status"]
	if !ok {
		return payload
	}
	var status map[string]json.RawMessage
	if err := json.Unmarshal(statusRaw, &status); err != nil {
		return payload
	}
	messageRaw, ok := status["message"]
	if !ok {
		return payload
	}
	var message map[string]json.RawMessage
	if err := json.Unmarshal(messageRaw, &message); err != nil {
		return payload
	}
	partsRaw, ok := message["parts"]
	if !ok {
		return payload
	}
	var parts []map[string]any
	if err := json.Unmarshal(partsRaw, &parts); err != nil {
		return payload
	}

	changed := false
	var newParts []map[string]any
	for _, part := range parts {
		kind, _ := part["kind"].(string)
		if kind != "text" {
			newParts = append(newParts, part)
			continue
		}
		text, _ := part["text"].(string)
		hasGuillemets := strings.Contains(text, "\u00ab")
		hasCurly := strings.Contains(text, "{artifact_return:")
		if text == "" || (!hasGuillemets && !hasCurly) {
			newParts = append(newParts, part)
			continue
		}

		if hasCurly {
			text = curlyArtifactReturnRe.ReplaceAllStringFunc(text, func(m string) string {
				inner := curlyArtifactReturnRe.FindStringSubmatch(m)
				if len(inner) >= 2 {
					return "\u00ab" + "artifact_return:" + inner[1] + "\u00bb"
				}
				return m
			})
		}

		embeds := embed.FindAll(text)
		if len(embeds) == 0 {
			newParts = append(newParts, part)
			continue
		}

		hasArtifactReturn := false
		for _, e := range embeds {
			if e.Type == "artifact_return" {
				hasArtifactReturn = true
				break
			}
		}

		if !hasArtifactReturn {
			resolved, signals, err := h.embedResolver.ResolveEmbedsInString(context.Background(), text, embed.A2AMessageToUser)
			if err != nil {
				t.Logf("embed resolution error: %v", err)
				newParts = append(newParts, part)
				continue
			}

			var returnSignals []embed.Signal
			for _, sig := range signals {
				if sig.Type == "artifact_return" {
					returnSignals = append(returnSignals, sig)
				}
			}

			if len(returnSignals) > 0 {
				changed = true
				if resolved != "" {
					newParts = append(newParts, map[string]any{"kind": "text", "text": resolved})
				}
				for _, sig := range returnSignals {
					newParts = append(newParts, h.signalToFilePart(sig))
				}
				continue
			}

			if resolved != text {
				part["text"] = resolved
				changed = true
			}
			newParts = append(newParts, part)
			continue
		}

		changed = true
		pos := 0
		for _, e := range embeds {
			if e.Type == "artifact_return" {
				_, signals, err := h.embedResolver.ResolveEmbedsInString(context.Background(), e.Raw, embed.A2AMessageToUser)
				var filePart map[string]any
				if err == nil {
					for _, sig := range signals {
						if sig.Type == "artifact_return" {
							fp := h.signalToFilePart(sig)
							if fp != nil {
								filePart = fp
							}
						}
					}
				}
				if filePart != nil {
					before := text[pos:e.Start]
					if before != "" {
						newParts = append(newParts, map[string]any{"kind": "text", "text": before})
					}
					newParts = append(newParts, filePart)
					pos = e.End
				}
			} else {
				resolved, _, err := h.embedResolver.ResolveEmbedsInString(context.Background(), e.Raw, embed.A2AMessageToUser)
				if err != nil {
					t.Logf("embed resolution error: %v", err)
					continue
				}
				before := text[pos:e.Start]
				newParts = append(newParts, map[string]any{"kind": "text", "text": before + resolved})
				pos = e.End
			}
		}
		if pos < len(text) {
			trailing := text[pos:]
			if trailing != "" {
				newParts = append(newParts, map[string]any{"kind": "text", "text": trailing})
			}
		}
	}

	if !changed {
		return payload
	}

	serializedParts, err := json.Marshal(newParts)
	if err != nil {
		t.Logf("marshal newParts error: %v", err)
		return payload
	}
	message["parts"] = serializedParts
	newMessage, err := json.Marshal(message)
	if err != nil {
		t.Logf("marshal message error: %v", err)
		return payload
	}
	status["message"] = newMessage
	newStatus, err := json.Marshal(status)
	if err != nil {
		t.Logf("marshal status error: %v", err)
		return payload
	}
	result["status"] = newStatus
	newResult, err := json.Marshal(result)
	if err != nil {
		t.Logf("marshal result error: %v", err)
		return payload
	}
	envelope["result"] = newResult
	newPayload, err := json.Marshal(envelope)
	if err != nil {
		t.Logf("marshal envelope error: %v", err)
		return payload
	}
	return newPayload
}

func (h *TestHarness) signalToFilePart(sig embed.Signal) map[string]any {
	sigData, _ := sig.Data.(map[string]any)
	filename, _ := sigData["filename"].(string)

	mimeType := ""
	if h.artSvc != nil && filename != "" {
		agentName := "test_agent"
		if h.scenario.GatewayInput != nil && h.scenario.GatewayInput.TargetAgentName != "" {
			agentName = h.scenario.GatewayInput.TargetAgentName
		}
		userID := "test-user"
		if h.scenario.GatewayInput != nil && h.scenario.GatewayInput.UserIdentity != "" {
			userID = h.scenario.GatewayInput.UserIdentity
		}
		sessionID := "session_" + h.scenario.TestCaseID
		if h.scenario.GatewayInput != nil && h.scenario.GatewayInput.ExternalContext != nil &&
			h.scenario.GatewayInput.ExternalContext.A2ASessionID != "" {
			sessionID = h.scenario.GatewayInput.ExternalContext.A2ASessionID
		}
		var versionPtr *int
		if v, ok := sigData["version"]; ok {
			switch vv := v.(type) {
			case int:
				versionPtr = &vv
			case float64:
				vi := int(vv)
				versionPtr = &vi
			}
		}
		loc := h.artSvc.Resolve(agentName, userID, sessionID, filename)
		meta, metaErr := h.artSvc.Inner.GetVersionMetadata(context.Background(), loc, versionPtr)
		if metaErr != nil || meta == nil {
			return nil
		}
		if meta.MIMEType != "" {
			mimeType = meta.MIMEType
		}
	}

	if mimeType == "" {
		return nil
	}

	return map[string]any{
		"kind":      "file",
		"filename":  filename,
		"mime_type": mimeType,
	}
}
