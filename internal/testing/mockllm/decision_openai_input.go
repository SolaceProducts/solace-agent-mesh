// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mockllm

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/decision"
)

type openAIDecisionPart struct {
	Type     string  `json:"type"`
	Text     *string `json:"text"`
	ImageURL string  `json:"image_url"`
}

type openAIDecisionMessage struct {
	Role    string               `json:"role"`
	Content []openAIDecisionPart `json:"content"`
}

func OpenAIDecisionInputImages(body []byte) ([]string, error) {
	var req struct {
		Input json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("request body: %w", err)
	}
	var text string
	if json.Unmarshal(req.Input, &text) == nil {
		return nil, nil
	}
	var messages []openAIDecisionMessage
	if err := json.Unmarshal(req.Input, &messages); err != nil {
		return nil, errors.New("input is neither a string nor a list of messages")
	}
	var images []string
	for i, m := range messages {
		if m.Role != "user" {
			return nil, fmt.Errorf("input[%d]: role %q, want user", i, m.Role)
		}
		for j, part := range m.Content {
			mimeType, err := checkOpenAIDecisionPart(part)
			if err != nil {
				return nil, fmt.Errorf("input[%d].content[%d]: %w", i, j, err)
			}
			if mimeType != "" {
				images = append(images, mimeType)
			}
		}
	}
	return images, nil
}

func checkOpenAIDecisionPart(part openAIDecisionPart) (string, error) {
	switch part.Type {
	case "input_text":
		if part.Text == nil {
			return "", errors.New("input_text has no text")
		}
		return "", nil
	case "input_image":
		mimeType, err := dataURLImageType(part.ImageURL)
		if err != nil {
			return "", fmt.Errorf("input_image: %w", err)
		}
		return mimeType, nil
	default:
		return "", fmt.Errorf("unsupported part type %q", part.Type)
	}
}

func dataURLImageType(url string) (string, error) {
	rest, ok := strings.CutPrefix(url, "data:")
	mimeType, encoded, isBase64 := strings.Cut(rest, ";base64,")
	if !ok || !isBase64 {
		return "", errors.New("not a base64 data URL")
	}
	if !slices.Contains(decision.ImageMIMETypes(), mimeType) {
		return "", fmt.Errorf("image type %q is not accepted", mimeType)
	}
	if _, err := base64.StdEncoding.DecodeString(encoded); err != nil {
		return "", errors.New("image data is not valid base64")
	}
	return mimeType, nil
}
