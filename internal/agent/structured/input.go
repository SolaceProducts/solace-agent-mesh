// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package structured

import (
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
)

func ExtractInputData(msg a2a.Message, schema map[string]any) (map[string]any, error) {
	if IsSingleTextSchema(schema) {
		return extractTextInput(msg), nil
	}
	return extractFileInput(msg)
}

func IsSingleTextSchema(schema map[string]any) bool {
	if schema["type"] != "object" {
		return false
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok || len(props) != 1 {
		return false
	}
	textProp, ok := props["text"].(map[string]any)
	if !ok {
		return false
	}
	return textProp["type"] == "string"
}

func extractTextInput(msg a2a.Message) map[string]any {
	var text string
	for _, p := range msg.Parts {
		if p.Kind == "text" {
			if text != "" {
				text += "\n"
			}
			text += p.Text
		}
	}
	return map[string]any{"text": text}
}

func InputFilePartIndex(msg a2a.Message, schema map[string]any) int {
	if IsSingleTextSchema(schema) {
		return -1
	}
	return inputFilePartIndex(msg)
}

func inputFilePartIndex(msg a2a.Message) int {
	for i, p := range msg.Parts {
		if p.Kind == "file" && p.File != nil && (p.File.Bytes != "" || p.File.URI != "") {
			return i
		}
	}
	return -1
}

func extractFileInput(msg a2a.Message) (map[string]any, error) {
	i := inputFilePartIndex(msg)
	if i < 0 {
		return nil, fmt.Errorf("no file part found in message for structured input extraction")
	}
	p := msg.Parts[i]

	if p.File.Bytes == "" {
		return nil, fmt.Errorf("file part with URI %q: artifact URI loading not yet implemented", p.File.URI)
	}

	var data map[string]any
	if err := json.Unmarshal(DecodeFileBytes(p.File.Bytes), &data); err != nil {
		return nil, fmt.Errorf("decode file part content: %w", err)
	}
	return data, nil
}

func DecodeFileBytes(s string) []byte {
	decoded, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return []byte(s)
	}
	return decoded
}
