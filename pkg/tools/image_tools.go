// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"

	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"net/http"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/strutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/visionimage"

	"github.com/SolaceProducts/solace-agent-mesh/internal/logging"
)

func configString(cfg map[string]any, key string) string {
	if cfg == nil {
		return ""
	}
	if v, ok := cfg[key].(string); ok {
		return v
	}
	return ""
}

func isSupportedImageFormat(filename string) bool {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".png", ".jpg", ".jpeg", ".webp", ".gif":
		return true
	}
	return false
}

func isSupportedAudioFormat(filename string) bool {
	ext := strings.ToLower(filepath.Ext(filename))
	return ext == ".wav" || ext == ".mp3"
}

func imageMIMEType(filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	case ".gif":
		return "image/gif"
	}
	return "application/octet-stream"
}

func audioFormat(filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".mp3":
		return "mp3"
	}
	return "wav"
}

func stripProviderPrefix(model string) string {
	if idx := strings.Index(model, "/"); idx >= 0 {
		return model[idx+1:]
	}
	return model
}

func sanitizeImageFilename(name string) string {
	return path.Base(filepath.Base(name))
}

type CreateImageFromDescriptionTool struct {
	tool.SideEffectingTool
	authoredSchemas
	artifactToolBase
	config map[string]any
}

func NewCreateImageFromDescriptionTool(config map[string]any) *CreateImageFromDescriptionTool {
	return &CreateImageFromDescriptionTool{config: config}
}

func (t *CreateImageFromDescriptionTool) Name() string { return "create_image_from_description" }

func (t *CreateImageFromDescriptionTool) Description() string {
	return "Generates an image based on a textual description using a configured image generation model and saves it as a PNG artifact."
}

func (t *CreateImageFromDescriptionTool) Parameters() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type: "object",
		Properties: map[string]any{
			"image_description": map[string]any{
				"type":        "string",
				"description": "The textual prompt to use for image generation.",
			},
			"output_filename": map[string]any{
				"type":        "string",
				"description": "Optional. The desired filename for the output PNG image.",
			},
		},
		Required: []string{"image_description"},
	}
}

func (t *CreateImageFromDescriptionTool) ResultSchema() *tool.JSONSchema {
	return emptyResultSchema()
}

func (t *CreateImageFromDescriptionTool) Execute(ctx context.Context, args map[string]any, toolCtx *tool.Context) (*tool.Result, error) {
	description, _ := args["image_description"].(string)
	if description == "" {
		return tool.Error("image_description is required"), nil
	}

	outputFilename, _ := args["output_filename"].(string)

	cfg := t.config
	modelName := configString(cfg, "model")
	apiKey := configString(cfg, "api_key")
	apiBase := configString(cfg, "api_base")

	if modelName == "" {
		return tool.Error("'model' configuration is missing in tool_config."), nil
	}
	if apiKey == "" {
		return tool.Error("'api_key' configuration is missing in tool_config."), nil
	}
	if apiBase == "" {
		return tool.Error("'api_base' configuration is missing in tool_config."), nil
	}

	modelName = stripProviderPrefix(modelName)

	apiURL := strings.TrimRight(apiBase, "/") + "/v1/images/generations"
	payload := map[string]any{
		"model":  modelName,
		"prompt": description,
	}
	if extra, ok := cfg["extra_params"].(map[string]any); ok {
		for k, v := range extra {
			payload[k] = v
		}
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return tool.Error(fmt.Sprintf("failed to marshal request: %v", err)), nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(body))
	if err != nil {
		return tool.Error(fmt.Sprintf("failed to create request: %v", err)), nil
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return tool.Error(fmt.Sprintf("Request error generating image: %v", err)), nil
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 50*1024*1024))
	if err != nil {
		return tool.Error(fmt.Sprintf("failed to read image API response: %v", err)), nil
	}

	if resp.StatusCode != http.StatusOK {
		msg := fmt.Sprintf("API error generating image: HTTP %d", resp.StatusCode)
		if text := providerErrorText(respBody); text != "" {
			msg += ": " + text
		}
		return tool.Error(msg, tool.WithErrorCode(imageAPIErrorCode(resp.StatusCode, respBody))), nil
	}

	var respData struct {
		Data []struct {
			B64JSON string `json:"b64_json"`
			URL     string `json:"url"`
		} `json:"data"`
	}
	if unmarshalErr := json.Unmarshal(respBody, &respData); unmarshalErr != nil || len(respData.Data) == 0 {
		return tool.Error("Image generation API did not return valid image data."), nil
	}

	var imageBytes []byte
	if respData.Data[0].B64JSON != "" {
		var decodeErr error
		imageBytes, decodeErr = base64.StdEncoding.DecodeString(respData.Data[0].B64JSON)
		if decodeErr != nil {
			return tool.Error(fmt.Sprintf("Failed to decode base64 image data: %v", decodeErr)), nil
		}
	} else if respData.Data[0].URL != "" {
		if !IsSafeURL(respData.Data[0].URL, false) {
			return tool.Error("image URL returned by API points to a non-public address"), nil
		}
		imgReq, err := http.NewRequestWithContext(ctx, http.MethodGet, respData.Data[0].URL, nil)
		if err != nil {
			return tool.Error(fmt.Sprintf("failed to create image fetch request: %v", err)), nil
		}
		imgResp, err := newSSRFSafeClient(client.Timeout, false).Do(imgReq)
		if err != nil {
			return tool.Error(fmt.Sprintf("Request error fetching image: %v", err)), nil
		}
		defer func() { _ = imgResp.Body.Close() }()
		if imgResp.StatusCode != http.StatusOK {
			return tool.Error(fmt.Sprintf("HTTP error fetching image: %d", imgResp.StatusCode),
				tool.WithErrorCode(tool.HTTPFailureCode(imgResp.StatusCode))), nil
		}
		imageBytes, err = io.ReadAll(io.LimitReader(imgResp.Body, 50*1024*1024))
		if err != nil {
			return tool.Error(fmt.Sprintf("failed to read image response body: %v", err)), nil
		}
	} else {
		return tool.Error("No valid image data (URL or b64_json) found in response."), nil
	}

	if len(imageBytes) == 0 {
		return tool.Error("Failed to retrieve image bytes."), nil
	}

	finalFilename := outputFilename
	if finalFilename == "" {
		finalFilename = fmt.Sprintf("generated_image_%s.png", idgen.New())
	} else if !strings.HasSuffix(strings.ToLower(finalFilename), ".png") {
		finalFilename += ".png"
	}

	artifactDescription := fmt.Sprintf("Image generated from prompt: %s", description)
	metadata := map[string]any{
		"description":       artifactDescription,
		"source_prompt":     description,
		"generation_tool":   "direct_api",
		"generation_model":  modelName,
		"request_timestamp": time.Now().UTC().Format("2006-01-02T15:04:05.000000-07:00"),
	}

	return tool.OK("Image generated successfully.", tool.WithDataObjects(tool.DataObject{
		Name:        finalFilename,
		Content:     imageBytes,
		MIMEType:    "image/png",
		Disposition: tool.DispositionArtifact,
		Description: artifactDescription,
		Metadata:    metadata,
	})), nil
}

type DescribeImageTool struct {
	tool.ReadOnlyTool
	authoredSchemas
	artifactToolBase
	config map[string]any
}

func NewDescribeImageTool(config map[string]any) *DescribeImageTool {
	return &DescribeImageTool{config: config}
}

func (t *DescribeImageTool) Name() string { return "describe_image" }

func (t *DescribeImageTool) Description() string {
	return "Describes an image using an OpenAI-compatible vision API."
}

func (t *DescribeImageTool) Parameters() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type: "object",
		Properties: map[string]any{
			"input_image": map[string]any{
				"type":        "string",
				"description": "The filename (and optional :version) of the input image artifact.",
			},
			"prompt": map[string]any{
				"type":        "string",
				"description": "Custom prompt for image analysis.",
			},
		},
		Required: []string{"input_image"},
	}
}

func (t *DescribeImageTool) ArtifactParams() map[string]tool.ArtifactParamInfo {
	return map[string]tool.ArtifactParamInfo{
		"input_image": {IsList: false},
	}
}

func (t *DescribeImageTool) ResultSchema() *tool.JSONSchema {
	return resultSchema(
		resultField{name: "description", typ: "string",
			description: "Model-generated description of the image."},
		resultField{name: "image_filename", typ: "string",
			description: "Artifact that was described."},
		resultField{name: "image_version", typ: "integer",
			description: "Version of the described artifact."},
	)
}

func (t *DescribeImageTool) Execute(ctx context.Context, args map[string]any, toolCtx *tool.Context) (*tool.Result, error) {
	art, ok := args["input_image"].(*tool.Artifact)
	if !ok || art == nil {
		return tool.Error("input_image artifact not found or could not be loaded"), nil
	}

	prompt, _ := args["prompt"].(string)
	if prompt == "" {
		prompt = "What is in this image?"
	}

	if !isSupportedImageFormat(art.Filename) {
		return tool.Error("Unsupported image format. Supported formats: .png, .jpg, .jpeg, .webp, .gif"), nil
	}

	if toolCtx.InternalLLMCallMultimodal == nil {
		return tool.Error("No multimodal LLM configured for image description"), nil
	}

	imageData, imageMIME, fitted := visionimage.Clamp(art.Content, imageMIMEType(art.Filename))
	if !fitted {
		return tool.Error("The image is too large to describe: it cannot be reduced to a size the vision model accepts"), nil
	}

	parts := []tool.MultimodalPart{
		tool.TextPart{Text: prompt},
		tool.ImagePart{
			Data:     imageData,
			MIMEType: imageMIME,
		},
	}

	systemPrompt := "You are an image analysis assistant. Describe the provided image."
	description, err := toolCtx.InternalLLMCallMultimodal(ctx, systemPrompt, parts, tool.InternalLLMCallMultimodalOpts{})
	if err != nil {
		return tool.Error(fmt.Sprintf("Vision API error: %s", llm.SanitizeErrorText(err)), llmErrorCode(err)), nil
	}

	if description == "" {
		return tool.Error("API response does not contain valid message content."), nil
	}

	return tool.OK("Image described successfully", tool.WithData(map[string]any{
		"description":    description,
		"image_filename": art.Filename,
		"image_version":  art.Version,
	})), nil
}

type DescribeAudioTool struct {
	tool.ReadOnlyTool
	authoredSchemas
	artifactToolBase
	config map[string]any
}

func NewDescribeAudioTool(config map[string]any) *DescribeAudioTool {
	return &DescribeAudioTool{config: config}
}

func (t *DescribeAudioTool) Name() string { return "describe_audio" }

func (t *DescribeAudioTool) Description() string {
	return "Describes an audio recording using a multimodal API."
}

func (t *DescribeAudioTool) Parameters() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type: "object",
		Properties: map[string]any{
			"input_audio": map[string]any{
				"type":        "string",
				"description": "The filename (and optional :version) of the input audio artifact.",
			},
			"prompt": map[string]any{
				"type":        "string",
				"description": "Custom prompt for audio analysis.",
			},
		},
		Required: []string{"input_audio"},
	}
}

func (t *DescribeAudioTool) ArtifactParams() map[string]tool.ArtifactParamInfo {
	return map[string]tool.ArtifactParamInfo{
		"input_audio": {IsList: false},
	}
}

func (t *DescribeAudioTool) ResultSchema() *tool.JSONSchema {
	return resultSchema(
		resultField{name: "description", typ: "string",
			description: "Model-generated description of the audio."},
		resultField{name: "audio_filename", typ: "string",
			description: "Artifact that was described."},
		resultField{name: "audio_version", typ: "integer",
			description: "Version of the described artifact."},
	)
}

func (t *DescribeAudioTool) Execute(ctx context.Context, args map[string]any, toolCtx *tool.Context) (*tool.Result, error) {
	art, ok := args["input_audio"].(*tool.Artifact)
	if !ok || art == nil {
		return tool.Error("input_audio artifact not found or could not be loaded"), nil
	}

	prompt, _ := args["prompt"].(string)
	if prompt == "" {
		prompt = "What is in this recording?"
	}

	if !isSupportedAudioFormat(art.Filename) {
		return tool.Error("Unsupported audio format. Supported formats: .wav, .mp3"), nil
	}

	if toolCtx.InternalLLMCallMultimodal == nil {
		return tool.Error("No multimodal LLM configured for audio description"), nil
	}

	parts := []tool.MultimodalPart{
		tool.TextPart{Text: prompt},
		tool.AudioPart{
			Data:   art.Content,
			Format: audioFormat(art.Filename),
		},
	}

	systemPrompt := "You are an audio analysis assistant. Describe the provided audio."
	description, err := toolCtx.InternalLLMCallMultimodal(ctx, systemPrompt, parts, tool.InternalLLMCallMultimodalOpts{})
	if err != nil {
		return tool.Error(fmt.Sprintf("Audio API error: %s", llm.SanitizeErrorText(err)), llmErrorCode(err)), nil
	}

	if description == "" {
		return tool.Error("API response does not contain valid message content."), nil
	}

	return tool.OK("Audio described successfully", tool.WithData(map[string]any{
		"description":    description,
		"audio_filename": art.Filename,
		"audio_version":  art.Version,
	})), nil
}

type geminiConfig struct {
	apiKey       string
	apiBase      string
	defaultModel string
	proModel     string
}

func resolveGeminiConfig(cfg map[string]any) (*geminiConfig, error) {
	gc := &geminiConfig{
		apiKey:       configString(cfg, "gemini_api_key"),
		apiBase:      configString(cfg, "gemini_api_base"),
		defaultModel: configString(cfg, "model"),
		proModel:     configString(cfg, "pro_model"),
	}
	if gc.apiKey == "" {
		return nil, fmt.Errorf("'gemini_api_key' configuration is missing in tool_config")
	}
	if gc.defaultModel == "" {
		gc.defaultModel = "gemini-3.1-flash-image-preview"
	}
	if gc.proModel == "" {
		gc.proModel = "gemini-3-pro-image-preview"
	}
	if gc.apiBase == "" {
		return nil, fmt.Errorf("'gemini_api_base' configuration is missing in tool_config")
	}
	return gc, nil
}

type geminiImagePart struct {
	MIMEType string
	Data     string
}

type geminiResult struct {
	Text      string
	ImageData []byte
	ImageMIME string
}

var geminiHTTPClient = &http.Client{Timeout: 120 * time.Second}

func ucFirst(s string) string {
	if s == "" {
		return s
	}
	r, size := utf8.DecodeRuneInString(s)
	return string(unicode.ToUpper(r)) + s[size:]
}

func llmErrorCode(err error) tool.ResultOption {
	if llmErr, ok := errors.AsType[*llm.Error](err); ok && llmErr.StatusCode > 0 {
		return tool.WithErrorCode(tool.HTTPFailureCode(llmErr.StatusCode))
	}
	return tool.WithErrorCode("")
}

const maxProviderErrorTextBytes = 256

func providerErrorText(body []byte) string {
	var envelope map[string]any
	if json.Unmarshal(body, &envelope) != nil {
		return ""
	}
	text := strings.Join(strings.Fields(providerEnvelopeMessage(envelope)), " ")
	lower := strings.ToLower(text)
	if strings.Contains(lower, "<html") || strings.Contains(lower, "<!doctype") {
		return ""
	}
	return strutil.Truncate(text, maxProviderErrorTextBytes, "")
}

var imageRequestRejectedTypes = []string{"invalid_request_error", "image_generation_user_error"}

const imageBillingCodePrefix = "billing"

var imageModerationCodes = []string{"content_policy_violation", "moderation_blocked"}

func imageAPIErrorCode(status int, body []byte) string {
	if status != http.StatusBadRequest {
		return tool.HTTPFailureCode(status)
	}
	switch rejected, moderated := imageRequestRejected(body); {
	case moderated:
		return taxonomy.ToolFailureContentFiltered
	case rejected:
		return taxonomy.ToolFailureInvalidQuery
	}
	return tool.HTTPFailureCode(status)
}

func imageRequestRejected(body []byte) (rejected, moderated bool) {
	var envelope struct {
		Error struct {
			Type string `json:"type"`
			Code any    `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &envelope) != nil {
		return false, false
	}
	code, _ := envelope.Error.Code.(string)
	rejected = slices.Contains(imageRequestRejectedTypes, envelope.Error.Type) && !strings.HasPrefix(code, imageBillingCodePrefix)
	return rejected, rejected && slices.Contains(imageModerationCodes, code)
}

func providerEnvelopeMessage(envelope map[string]any) string {
	if errObj, ok := envelope["error"].(map[string]any); ok {
		if msg, ok := errObj["message"].(string); ok && msg != "" {
			return msg
		}
	}
	if msg, ok := envelope["message"].(string); ok && msg != "" {
		return msg
	}
	msg, _ := envelope["error"].(string)
	return msg
}

const geminiReasonAPIKeyInvalid = "API_KEY_INVALID"

const geminiStatusInvalidArgument = "INVALID_ARGUMENT"

type geminiStatusError struct {
	status     int
	reason     string
	statusName string
}

func (e *geminiStatusError) Error() string {
	return fmt.Sprintf("gemini API call failed: HTTP %d", e.status)
}

func (e *geminiStatusError) errorCode() string {
	switch {
	case e.reason == geminiReasonAPIKeyInvalid:
		return tool.DispatchErrorCode(taxonomy.DispatchClassAuthRequired)
	case e.status == http.StatusBadRequest && e.statusName == geminiStatusInvalidArgument:
		return taxonomy.ToolFailureInvalidQuery
	}
	return tool.HTTPFailureCode(e.status)
}

func geminiErrorCode(err error) tool.ResultOption {
	if statusErr, ok := errors.AsType[*geminiStatusError](err); ok {
		return tool.WithErrorCode(statusErr.errorCode())
	}
	return tool.WithErrorCode("")
}

func geminiErrorReason(body []byte) (reason, statusName string) {
	var envelope struct {
		Error struct {
			Status  string `json:"status"`
			Details []struct {
				Reason string `json:"reason"`
			} `json:"details"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &envelope) != nil {
		return "", ""
	}
	for _, detail := range envelope.Error.Details {
		if detail.Reason != "" {
			return detail.Reason, envelope.Error.Status
		}
	}
	return "", envelope.Error.Status
}

func geminiGenerateContent(
	ctx context.Context,
	apiBase string,
	apiKey string,
	model string,
	textPrompt string,
	image *geminiImagePart,
) (*geminiResult, error) {
	parts := []map[string]any{{"text": textPrompt}}
	if image != nil {
		parts = append(parts, map[string]any{
			"inline_data": map[string]any{
				"mime_type": image.MIMEType,
				"data":      image.Data,
			},
		})
	}

	geminiReq := map[string]any{
		"contents": []map[string]any{{"parts": parts}},
		"generationConfig": map[string]any{
			"responseModalities": []string{"TEXT", "IMAGE"},
		},
	}

	body, err := json.Marshal(geminiReq)
	if err != nil {
		return nil, fmt.Errorf("marshal Gemini request: %w", err)
	}

	apiURL := fmt.Sprintf("%s/v1beta/models/%s:generateContent?key=%s",
		strings.TrimRight(apiBase, "/"), model, apiKey)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create Gemini request: %w", logging.UnwrapURLError(err))
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := geminiHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gemini API call failed: %w", logging.UnwrapURLError(err))
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 50*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("read Gemini API response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		reason, statusName := geminiErrorReason(respBody)
		return nil, &geminiStatusError{status: resp.StatusCode, reason: reason, statusName: statusName}
	}

	var geminiResp struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text       string `json:"text,omitempty"`
					InlineData *struct {
						MIMEType string `json:"mimeType"`
						Data     string `json:"data"`
					} `json:"inlineData,omitempty"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
		Error *struct {
			Message string `json:"message"`
			Code    int    `json:"code"`
		} `json:"error,omitempty"`
	}
	if err := json.Unmarshal(respBody, &geminiResp); err != nil {
		return nil, fmt.Errorf("parse Gemini response: %w", err)
	}
	if geminiResp.Error != nil {
		return nil, fmt.Errorf("gemini API error: %s", geminiResp.Error.Message)
	}
	if len(geminiResp.Candidates) == 0 || len(geminiResp.Candidates[0].Content.Parts) == 0 {
		return nil, fmt.Errorf("gemini API did not return valid content")
	}

	out := &geminiResult{}
	for _, part := range geminiResp.Candidates[0].Content.Parts {
		if part.Text != "" {
			out.Text = part.Text
		}
		if part.InlineData != nil {
			decoded, decodeErr := base64.StdEncoding.DecodeString(part.InlineData.Data)
			if decodeErr != nil {
				return nil, fmt.Errorf("decode Gemini image: %w", decodeErr)
			}
			out.ImageData = decoded
			out.ImageMIME = part.InlineData.MIMEType
		}
	}
	return out, nil
}

type EditImageWithGeminiTool struct {
	tool.SideEffectingTool
	authoredSchemas
	artifactToolBase
	config map[string]any
}

func NewEditImageWithGeminiTool(config map[string]any) *EditImageWithGeminiTool {
	return &EditImageWithGeminiTool{config: config}
}

func (t *EditImageWithGeminiTool) Name() string { return "edit_image_with_gemini" }

func (t *EditImageWithGeminiTool) Description() string {
	return "Edits an existing image based on a text prompt using Google's Gemini image generation models."
}

func (t *EditImageWithGeminiTool) Parameters() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type: "object",
		Properties: map[string]any{
			"input_image": map[string]any{
				"type":        "string",
				"description": "The filename (and optional :version) of the input image artifact.",
			},
			"edit_prompt": map[string]any{
				"type":        "string",
				"description": "Text description of the desired edits to apply to the image.",
			},
			"output_filename": map[string]any{
				"type":        "string",
				"description": "Optional. The desired filename for the output edited image.",
			},
			"use_pro_model": map[string]any{
				"type":        "boolean",
				"description": "Set to true to use the pro model for professional quality output.",
			},
		},
		Required: []string{"input_image", "edit_prompt"},
	}
}

func (t *EditImageWithGeminiTool) ArtifactParams() map[string]tool.ArtifactParamInfo {
	return map[string]tool.ArtifactParamInfo{
		"input_image": {IsList: false},
	}
}

func (t *EditImageWithGeminiTool) ResultSchema() *tool.JSONSchema {
	return resultSchema(
		resultField{name: "original_filename", typ: "string",
			description: "Artifact that was edited, not the artifact produced."},
		resultField{name: "original_version", typ: "integer",
			description: "Version of the source artifact."},
	)
}

func (t *EditImageWithGeminiTool) Execute(ctx context.Context, args map[string]any, toolCtx *tool.Context) (*tool.Result, error) {
	art, ok := args["input_image"].(*tool.Artifact)
	if !ok || art == nil {
		return tool.Error("input_image artifact not found or could not be loaded"), nil
	}

	editPrompt, _ := args["edit_prompt"].(string)
	if editPrompt == "" {
		return tool.Error("edit_prompt is required"), nil
	}

	outputFilename, _ := args["output_filename"].(string)
	useProModel, _ := args["use_pro_model"].(bool)

	gc, err := resolveGeminiConfig(t.config)
	if err != nil {
		return tool.Error(err.Error() + "."), nil
	}

	modelName := gc.defaultModel
	if useProModel {
		modelName = gc.proModel
	}

	if !isSupportedImageFormat(art.Filename) {
		return tool.Error("Unsupported image format. Supported formats: .png, .jpg, .jpeg, .webp, .gif"), nil
	}

	res, err := geminiGenerateContent(
		ctx, gc.apiBase, gc.apiKey, modelName, editPrompt,
		&geminiImagePart{
			MIMEType: imageMIMEType(art.Filename),
			Data:     base64.StdEncoding.EncodeToString(art.Content),
		},
	)
	if err != nil {
		return tool.Error(ucFirst(err.Error()), geminiErrorCode(err)), nil
	}
	if len(res.ImageData) == 0 {
		return tool.Error("No edited image data received from Gemini API."), nil
	}

	finalFilename := outputFilename
	if finalFilename != "" {
		finalFilename = sanitizeImageFilename(finalFilename)
		if !strings.HasSuffix(strings.ToLower(finalFilename), ".jpg") && !strings.HasSuffix(strings.ToLower(finalFilename), ".jpeg") {
			finalFilename += ".jpg"
		}
	} else {
		baseName := strings.TrimSuffix(art.Filename, filepath.Ext(art.Filename))
		finalFilename = fmt.Sprintf("edited_%s_%s.jpg", baseName, idgen.NewRandom()[:8])
	}

	metadata := map[string]any{
		"original_image":    art.Filename,
		"original_version":  art.Version,
		"edit_prompt":       editPrompt,
		"editing_tool":      "gemini",
		"editing_model":     modelName,
		"request_timestamp": time.Now().UTC().Format("2006-01-02T15:04:05.000000-07:00"),
	}
	if outputFilename != "" {
		metadata["original_requested_filename"] = outputFilename
	}
	if res.Text != "" {
		metadata["gemini_response_text"] = res.Text
	}

	return tool.OK("Image edited and saved successfully", tool.WithData(map[string]any{
		"original_filename": art.Filename,
		"original_version":  art.Version,
	}), tool.WithDataObjects(tool.DataObject{
		Name:        finalFilename,
		Content:     res.ImageData,
		MIMEType:    "image/jpeg",
		Disposition: tool.DispositionArtifact,
		Description: fmt.Sprintf("Image edited with prompt: %s", editPrompt),
		Metadata:    metadata,
	})), nil
}

type GenerateImageWithGeminiTool struct {
	tool.SideEffectingTool
	authoredSchemas
	artifactToolBase
	config map[string]any
}

func NewGenerateImageWithGeminiTool(config map[string]any) *GenerateImageWithGeminiTool {
	return &GenerateImageWithGeminiTool{config: config}
}

func (t *GenerateImageWithGeminiTool) Name() string { return "generate_image_with_gemini" }

func (t *GenerateImageWithGeminiTool) Description() string {
	return "Generates an image from a text description using Google's Gemini image generation models. " +
		"Two models are available: a standard model (fast, efficient, and cheaper) and a pro model " +
		"(professional quality but more expensive). Use the pro model only when truly necessary for " +
		"complex tasks like infographics, charts, diagrams, or images requiring precise text placement."
}

func (t *GenerateImageWithGeminiTool) Parameters() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type: "object",
		Properties: map[string]any{
			"image_description": map[string]any{
				"type":        "string",
				"description": "The textual prompt to use for image generation.",
			},
			"output_filename": map[string]any{
				"type":        "string",
				"description": "Optional. The desired filename for the output PNG image.",
				"nullable":    true,
			},
			"use_pro_model": map[string]any{
				"type": "boolean",
				"description": "Set to true to use the pro model for professional quality output with advanced reasoning, " +
					"high-fidelity text rendering, and up to 4K resolution. The pro model is MORE EXPENSIVE, " +
					"so only use it when truly necessary for: infographics, charts, diagrams, technical " +
					"illustrations, or complex visual content requiring precise text placement. " +
					"Set to false (default) to use the standard model which is faster, efficient, and cheaper.",
				"nullable": true,
			},
		},
		Required: []string{"image_description"},
	}
}

func (t *GenerateImageWithGeminiTool) ResultSchema() *tool.JSONSchema {
	return resultSchema(
		resultField{name: "output_filename", typ: "string",
			description: "Name of the saved image artifact."},
		resultField{name: "output_version", typ: "integer",
			description: "Version of the saved image artifact."},
		resultField{name: "result_preview", typ: "string",
			description: "Short human-readable summary of what was generated."},
		resultField{name: "model_used", typ: "string",
			description: "Gemini model that produced the image."},
		resultField{name: "used_pro_model", typ: "boolean",
			description: "True when the pro-tier model was selected."},
	)
}

func (t *GenerateImageWithGeminiTool) Execute(ctx context.Context, args map[string]any, toolCtx *tool.Context) (*tool.Result, error) {
	imageDescription, _ := args["image_description"].(string)
	if imageDescription == "" {
		return tool.Error("image_description is required"), nil
	}

	outputFilename, _ := args["output_filename"].(string)
	useProModel, _ := args["use_pro_model"].(bool)

	gc, err := resolveGeminiConfig(t.config)
	if err != nil {
		return tool.Error(err.Error() + "."), nil
	}

	modelName := gc.defaultModel
	if useProModel {
		modelName = gc.proModel
	}

	res, err := geminiGenerateContent(
		ctx, gc.apiBase, gc.apiKey, modelName, imageDescription, nil,
	)
	if err != nil {
		return tool.Error(ucFirst(err.Error()), geminiErrorCode(err)), nil
	}
	if len(res.ImageData) == 0 {
		return tool.Error("No image data received from Gemini API."), nil
	}

	decoded, _, decodeErr := image.Decode(bytes.NewReader(res.ImageData))
	if decodeErr != nil {
		return tool.Error(fmt.Sprintf("Failed to decode Gemini image: %v", decodeErr)), nil
	}
	var pngBuf bytes.Buffer
	if encErr := png.Encode(&pngBuf, decoded); encErr != nil {
		return tool.Error(fmt.Sprintf("Failed to re-encode image as PNG: %v", encErr)), nil
	}
	imageBytes := pngBuf.Bytes()

	finalFilename := outputFilename
	if finalFilename != "" {
		finalFilename = sanitizeImageFilename(finalFilename)
		if !strings.HasSuffix(strings.ToLower(finalFilename), ".png") {
			finalFilename += ".png"
		}
	} else {
		finalFilename = fmt.Sprintf("generated_image_%s.png", idgen.New())
	}

	requestTimestamp := time.Now().UTC().Format("2006-01-02T15:04:05.000000-07:00")

	originalRequested := "N/A"
	if outputFilename != "" {
		originalRequested = outputFilename
	}

	custom := map[string]any{
		"source_prompt":               imageDescription,
		"generation_tool":             "gemini",
		"generation_model":            modelName,
		"used_pro_model":              useProModel,
		"request_timestamp":           requestTimestamp,
		"original_requested_filename": originalRequested,
	}
	if res.Text != "" {
		custom["gemini_response_text"] = res.Text
	}

	saveResult, saveErr := toolCtx.Facade().SaveArtifactWithMetadata(
		ctx, finalFilename, imageBytes, "image/png",
		artifact.MetadataOpts{
			Description: fmt.Sprintf("Image generated from prompt: %s", imageDescription),
			Custom:      custom,
		},
	)
	if saveErr != nil {
		return tool.Error(fmt.Sprintf("Failed to save generated image artifact: %v", saveErr)), nil
	}

	preview := imageDescription
	if r := []rune(preview); len(r) > 50 {
		preview = string(r[:50])
	}

	return tool.OK(
		"Image generated and saved successfully.",
		tool.WithData(map[string]any{
			"output_filename": finalFilename,
			"output_version":  saveResult.DataVersion,
			"result_preview":  fmt.Sprintf("Image '%s' (v%d) created from prompt: \"%s...\"", finalFilename, saveResult.DataVersion, preview),
			"model_used":      modelName,
			"used_pro_model":  useProModel,
		}),
	), nil
}
