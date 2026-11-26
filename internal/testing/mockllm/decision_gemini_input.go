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

	"github.com/SolaceProducts/solace-agent-mesh/internal/decision"
)

const GeminiDecisionPath = "/v1beta/decisions"

type geminiDecisionBlock struct {
	Type       string  `json:"type"`
	Text       *string `json:"text"`
	Data       string  `json:"data"`
	URI        string  `json:"uri"`
	MIMEType   string  `json:"mime_type"`
	Resolution string  `json:"resolution"`
}

type geminiDecisionQuestion struct {
	Type         string          `json:"type"`
	Instructions string          `json:"instructions"`
	Criteria     json.RawMessage `json:"criteria"`
}

type GeminiMediaBlock struct {
	Kind       decision.MediaKind
	MIMEType   string
	Resolution string
}

func (b GeminiMediaBlock) String() string {
	s := string(b.Kind) + ":" + b.MIMEType
	if b.Resolution != "" {
		s += ":" + b.Resolution
	}
	return s
}

var geminiResolutions = map[decision.MediaKind][]string{
	decision.MediaImage: {"low", "medium", "high", "ultra_high"},
	decision.MediaVideo: {"low", "medium", "high"},
}

func GeminiDecisionInputMedia(body []byte) ([]GeminiMediaBlock, error) {
	var req struct {
		Model     string                            `json:"model"`
		Input     json.RawMessage                   `json:"input"`
		Questions map[string]geminiDecisionQuestion `json:"questions"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("request body: %w", err)
	}
	if req.Model == "" {
		return nil, errors.New("request names no model")
	}
	if len(req.Questions) == 0 {
		return nil, errors.New("request asks no questions")
	}
	for key, q := range req.Questions {
		if err := checkGeminiDecisionQuestion(q); err != nil {
			return nil, fmt.Errorf("questions[%s]: %w", key, err)
		}
	}
	var text string
	if json.Unmarshal(req.Input, &text) == nil {
		if text == "" {
			return nil, errors.New("input is empty")
		}
		return nil, nil
	}
	var blocks []geminiDecisionBlock
	if err := json.Unmarshal(req.Input, &blocks); err != nil {
		return nil, errors.New("input is neither a string nor a list of content blocks")
	}
	var media []GeminiMediaBlock
	for i, block := range blocks {
		if block.Type == "text" {
			if block.Text == nil {
				return nil, fmt.Errorf("input[%d]: text block has no text", i)
			}
			continue
		}
		m, err := checkGeminiMediaBlock(block)
		if err != nil {
			return nil, fmt.Errorf("input[%d]: %w", i, err)
		}
		media = append(media, m)
	}
	return media, nil
}

func GeminiImageMIMETypes(media []GeminiMediaBlock) []string {
	var images []string
	for _, m := range media {
		if m.Kind == decision.MediaImage {
			images = append(images, m.MIMEType)
		}
	}
	return images
}

func checkGeminiDecisionQuestion(q geminiDecisionQuestion) error {
	if q.Instructions == "" {
		return errors.New("no instructions")
	}
	switch q.Type {
	case "choice":
		var options map[string]*string
		if err := json.Unmarshal(q.Criteria, &options); err != nil || len(options) < 2 {
			return errors.New("choice criteria are not a map of at least two options")
		}
		for option, description := range options {
			if description == nil {
				return fmt.Errorf("choice option %q has a null description", option)
			}
		}
	case "score":
		var levels []string
		if err := json.Unmarshal(q.Criteria, &levels); err != nil || len(levels) < 2 || slices.Contains(levels, "") {
			return errors.New("score criteria are not a list of at least two non-empty levels")
		}
	case "noul":
		if len(q.Criteria) == 0 {
			return nil
		}
		var sides map[string]string
		if err := json.Unmarshal(q.Criteria, &sides); err != nil {
			return errors.New("noul criteria are not an object")
		}
		for side := range sides {
			if side != "true_description" && side != "false_description" {
				return fmt.Errorf("noul criteria name %q", side)
			}
		}
	default:
		return fmt.Errorf("unsupported question type %q", q.Type)
	}
	return nil
}

func checkGeminiMediaBlock(block geminiDecisionBlock) (GeminiMediaBlock, error) {
	kind := decision.MediaKind(block.Type)
	if !slices.Contains([]decision.MediaKind{decision.MediaImage, decision.MediaAudio, decision.MediaVideo, decision.MediaDocument}, kind) {
		return GeminiMediaBlock{}, fmt.Errorf("unsupported block type %q", block.Type)
	}
	if !slices.Contains(decision.MediaMIMETypes(kind), block.MIMEType) {
		return GeminiMediaBlock{}, fmt.Errorf("%s type %q is not accepted", kind, block.MIMEType)
	}
	if block.URI != "" {
		return GeminiMediaBlock{}, fmt.Errorf("%s block carries a uri; the client sends data inline", kind)
	}
	if _, err := base64.StdEncoding.DecodeString(block.Data); err != nil || block.Data == "" {
		return GeminiMediaBlock{}, fmt.Errorf("%s data is not valid base64", kind)
	}
	if block.Resolution != "" && !slices.Contains(geminiResolutions[kind], block.Resolution) {
		return GeminiMediaBlock{}, fmt.Errorf("%s resolution %q is not accepted", kind, block.Resolution)
	}
	return GeminiMediaBlock{Kind: kind, MIMEType: block.MIMEType, Resolution: block.Resolution}, nil
}
