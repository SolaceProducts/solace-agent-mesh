// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mockllm

import (
	"encoding/json"
	"fmt"
)

type typeSafeStatePart struct {
	Type     string          `json:"type"`
	ImageURL json.RawMessage `json:"image_url"`
}

func TypeSafeDecisionStateImages(body []byte) ([]string, error) {
	var req struct {
		State json.RawMessage `json:"state"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("request body: %w", err)
	}
	var entries []json.RawMessage
	if json.Unmarshal(req.State, &entries) != nil {
		return nil, nil
	}
	var images []string
	for i, entry := range entries {
		var part typeSafeStatePart
		if json.Unmarshal(entry, &part) != nil || part.Type != "image_url" {
			continue
		}
		var imageURL struct {
			URL string `json:"url"`
		}
		if json.Unmarshal(part.ImageURL, &imageURL) != nil {
			return nil, fmt.Errorf("state[%d]: image_url part has no image_url object", i)
		}
		mimeType, err := dataURLImageType(imageURL.URL)
		if err != nil {
			return nil, fmt.Errorf("state[%d]: image_url: %w", i, err)
		}
		images = append(images, mimeType)
	}
	return images, nil
}
