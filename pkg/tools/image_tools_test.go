// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const tinyPNGBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8/5+hHgAHggJ/PchI7wAAAABJRU5ErkJggg=="

func newImageToolContext(t *testing.T) *tool.Context {
	t.Helper()
	return &tool.Context{
		ArtifactService: &artifact.ScopedService{
			Inner: memory.New(),
			Scope: "agent",
		},
		AgentName: "test-agent",
		UserID:    "test-user",
		SessionID: "test-session",
	}
}

func loadImageArtifactMetadata(t *testing.T, toolCtx *tool.Context, filename string, version int) map[string]any {
	t.Helper()
	metaName := filename + artifact.MetadataSuffix
	content, _, err := toolCtx.Facade().LoadArtifact(context.Background(), metaName, &version)
	if err != nil {
		t.Fatalf("failed to load metadata artifact %q: %v", metaName, err)
	}
	var meta map[string]any
	if unmarshalErr := json.Unmarshal(content, &meta); unmarshalErr != nil {
		t.Fatalf("failed to parse metadata JSON: %v", unmarshalErr)
	}
	return meta
}

func newGeminiMockServer(t *testing.T, wantAPIKey string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var callCount atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		if !strings.HasSuffix(r.URL.Path, ":generateContent") {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if got := r.URL.Query().Get("key"); got != wantAPIKey {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprint(w, `{"error": {"code": 400, "message": "API key not valid."}}`)
			return
		}
		geminiResp := map[string]any{
			"candidates": []map[string]any{{
				"content": map[string]any{
					"parts": []map[string]any{
						{"text": "Here is the generated image."},
						{
							"inlineData": map[string]any{
								"mimeType": "image/png",
								"data":     tinyPNGBase64,
							},
						},
					},
				},
			}},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(geminiResp)
	}))
	t.Cleanup(srv.Close)
	return srv, &callCount
}

func TestGenerateImageWithGeminiHappyPath(t *testing.T) {
	srv, calls := newGeminiMockServer(t, "fake-key")

	tl := NewGenerateImageWithGeminiTool(map[string]any{
		"gemini_api_key":  "fake-key",
		"gemini_api_base": srv.URL,
		"model":           "gemini-2.5-flash-image",
	})

	toolCtx := newImageToolContext(t)
	result, err := tl.Execute(context.Background(), map[string]any{
		"image_description": "a red cat on a bicycle",
	}, toolCtx)
	if err != nil {
		t.Fatalf("Execute returned Go error: %v", err)
	}
	if result.Status != tool.StatusSuccess {
		t.Fatalf("status = %q, want success; message = %q", result.Status, result.Message)
	}
	if calls.Load() != 1 {
		t.Errorf("expected 1 Gemini API call, got %d", calls.Load())
	}

	filename, _ := result.Data["output_filename"].(string)
	if filename == "" || !strings.HasSuffix(strings.ToLower(filename), ".png") {
		t.Errorf("output_filename = %q, want non-empty .png", filename)
	}
	if _, ok := result.Data["output_version"].(int); !ok {
		t.Errorf("output_version missing or wrong type: %T", result.Data["output_version"])
	}
	if result.Data["model_used"] != "gemini-2.5-flash-image" {
		t.Errorf("model_used = %v", result.Data["model_used"])
	}
	if result.Data["used_pro_model"] != false {
		t.Errorf("used_pro_model = %v, want false", result.Data["used_pro_model"])
	}
	preview, _ := result.Data["result_preview"].(string)
	if !strings.Contains(preview, "a red cat on a bicycle") {
		t.Errorf("result_preview = %q, want to contain prompt", preview)
	}

	version, _ := result.Data["output_version"].(int)
	meta := loadImageArtifactMetadata(t, toolCtx, filename, version)
	if meta["description"] != "Image generated from prompt: a red cat on a bicycle" {
		t.Errorf("metadata.description = %v", meta["description"])
	}
	if meta["source_prompt"] != "a red cat on a bicycle" {
		t.Errorf("metadata.source_prompt = %v", meta["source_prompt"])
	}
	if meta["generation_tool"] != "gemini" {
		t.Errorf("metadata.generation_tool = %v", meta["generation_tool"])
	}
	if meta["generation_model"] != "gemini-2.5-flash-image" {
		t.Errorf("metadata.generation_model = %v", meta["generation_model"])
	}
	if meta["used_pro_model"] != false {
		t.Errorf("metadata.used_pro_model = %v, want false", meta["used_pro_model"])
	}
	if meta["original_requested_filename"] != "N/A" {
		t.Errorf("metadata.original_requested_filename = %v, want N/A", meta["original_requested_filename"])
	}
	if meta["gemini_response_text"] != "Here is the generated image." {
		t.Errorf("metadata.gemini_response_text missing or wrong: %v", meta["gemini_response_text"])
	}
	ts, _ := meta["request_timestamp"].(string)
	if !strings.HasSuffix(ts, "+00:00") {
		t.Errorf("request_timestamp = %q, want Python isoformat +00:00 offset", ts)
	}
}

func TestGenerateImageWithGeminiHonoursOutputFilename(t *testing.T) {
	srv, _ := newGeminiMockServer(t, "fake-key")
	tl := NewGenerateImageWithGeminiTool(map[string]any{
		"gemini_api_key":  "fake-key",
		"gemini_api_base": srv.URL,
	})

	toolCtx := newImageToolContext(t)
	result, err := tl.Execute(context.Background(), map[string]any{
		"image_description": "a blue sky",
		"output_filename":   "sunset",
	}, toolCtx)
	if err != nil || result.Status != tool.StatusSuccess {
		t.Fatalf("unexpected: err=%v status=%s message=%s", err, result.Status, result.Message)
	}
	if result.Data["output_filename"] != "sunset.png" {
		t.Errorf("output_filename = %v, want sunset.png", result.Data["output_filename"])
	}
	version, _ := result.Data["output_version"].(int)
	meta := loadImageArtifactMetadata(t, toolCtx, "sunset.png", version)
	if meta["original_requested_filename"] != "sunset" {
		t.Errorf("original_requested_filename = %v, want sunset", meta["original_requested_filename"])
	}
}

func TestGenerateImageWithGeminiProModel(t *testing.T) {
	var gotModel string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const prefix = "/v1beta/models/"
		const suffix = ":generateContent"
		p := r.URL.Path
		if strings.HasPrefix(p, prefix) && strings.HasSuffix(p, suffix) {
			gotModel = p[len(prefix) : len(p)-len(suffix)]
		}
		geminiResp := map[string]any{
			"candidates": []map[string]any{{
				"content": map[string]any{
					"parts": []map[string]any{{
						"inlineData": map[string]any{
							"mimeType": "image/png",
							"data":     tinyPNGBase64,
						},
					}},
				},
			}},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(geminiResp)
	}))
	t.Cleanup(srv.Close)

	tl := NewGenerateImageWithGeminiTool(map[string]any{
		"gemini_api_key":  "fake-key",
		"gemini_api_base": srv.URL,
		"model":           "flash-fast",
		"pro_model":       "pro-strong",
	})
	toolCtx := newImageToolContext(t)
	result, err := tl.Execute(context.Background(), map[string]any{
		"image_description": "diagram of a neural net",
		"use_pro_model":     true,
	}, toolCtx)
	if err != nil || result.Status != tool.StatusSuccess {
		t.Fatalf("unexpected: err=%v status=%s message=%s", err, result.Status, result.Message)
	}
	if gotModel != "pro-strong" {
		t.Errorf("server saw model = %q, want pro-strong", gotModel)
	}
	if result.Data["model_used"] != "pro-strong" {
		t.Errorf("model_used = %v, want pro-strong", result.Data["model_used"])
	}
	if result.Data["used_pro_model"] != true {
		t.Errorf("used_pro_model = %v, want true", result.Data["used_pro_model"])
	}
}

func TestGenerateImageWithGeminiDefaultModel(t *testing.T) {
	var gotModel string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const prefix = "/v1beta/models/"
		const suffix = ":generateContent"
		p := r.URL.Path
		if strings.HasPrefix(p, prefix) && strings.HasSuffix(p, suffix) {
			gotModel = p[len(prefix) : len(p)-len(suffix)]
		}
		geminiResp := map[string]any{
			"candidates": []map[string]any{{
				"content": map[string]any{
					"parts": []map[string]any{{
						"inlineData": map[string]any{
							"mimeType": "image/png",
							"data":     tinyPNGBase64,
						},
					}},
				},
			}},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(geminiResp)
	}))
	t.Cleanup(srv.Close)

	tl := NewGenerateImageWithGeminiTool(map[string]any{
		"gemini_api_key":  "fake-key",
		"gemini_api_base": srv.URL,
	})
	toolCtx := newImageToolContext(t)
	_, err := tl.Execute(context.Background(), map[string]any{
		"image_description": "anything",
	}, toolCtx)
	if err != nil {
		t.Fatalf("Execute returned Go error: %v", err)
	}
	if gotModel != "gemini-3.1-flash-image-preview" {
		t.Errorf("default model = %q, want gemini-3.1-flash-image-preview", gotModel)
	}
}

func TestGenerateImageWithGeminiMissingAPIKey(t *testing.T) {
	tl := NewGenerateImageWithGeminiTool(map[string]any{})
	result, err := tl.Execute(context.Background(), map[string]any{
		"image_description": "anything",
	}, newImageToolContext(t))
	if err != nil {
		t.Fatalf("Execute returned Go error: %v", err)
	}
	if result.Status != tool.StatusError {
		t.Fatalf("status = %q, want error", result.Status)
	}
	if !strings.Contains(result.Message, "gemini_api_key") {
		t.Errorf("message = %q, want to mention gemini_api_key", result.Message)
	}
}

func TestGenerateImageWithGeminiMissingAPIBase(t *testing.T) {
	tl := NewGenerateImageWithGeminiTool(map[string]any{
		"gemini_api_key": "fake-key",
	})
	result, err := tl.Execute(context.Background(), map[string]any{
		"image_description": "anything",
	}, newImageToolContext(t))
	if err != nil {
		t.Fatalf("Execute returned Go error: %v", err)
	}
	if result.Status != tool.StatusError {
		t.Fatalf("status = %q, want error", result.Status)
	}
	if !strings.Contains(result.Message, "gemini_api_base") {
		t.Errorf("message = %q, want to mention gemini_api_base", result.Message)
	}
}

func TestGenerateImageWithGeminiMissingPrompt(t *testing.T) {
	tl := NewGenerateImageWithGeminiTool(map[string]any{
		"gemini_api_key": "fake-key",
	})
	result, err := tl.Execute(context.Background(), map[string]any{}, newImageToolContext(t))
	if err != nil {
		t.Fatalf("Execute returned Go error: %v", err)
	}
	if result.Status != tool.StatusError {
		t.Fatalf("status = %q, want error", result.Status)
	}
	if !strings.Contains(result.Message, "image_description is required") {
		t.Errorf("message = %q, want to mention image_description is required", result.Message)
	}
}

func TestGenerateImageWithGeminiNoImageInResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		geminiResp := map[string]any{
			"candidates": []map[string]any{{
				"content": map[string]any{
					"parts": []map[string]any{
						{"text": "I can only describe images; I cannot generate one."},
					},
				},
			}},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(geminiResp)
	}))
	t.Cleanup(srv.Close)

	tl := NewGenerateImageWithGeminiTool(map[string]any{
		"gemini_api_key":  "fake-key",
		"gemini_api_base": srv.URL,
	})
	result, err := tl.Execute(context.Background(), map[string]any{
		"image_description": "a cat",
	}, newImageToolContext(t))
	if err != nil {
		t.Fatalf("Execute returned Go error: %v", err)
	}
	if result.Status != tool.StatusError {
		t.Fatalf("status = %q, want error", result.Status)
	}
	if !strings.Contains(result.Message, "No image data received") {
		t.Errorf("message = %q, want to mention no image data", result.Message)
	}
}

func TestGenerateImageWithGeminiAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprint(w, `{"error": {"code": 400, "message": "Invalid request"}}`)
	}))
	t.Cleanup(srv.Close)

	tl := NewGenerateImageWithGeminiTool(map[string]any{
		"gemini_api_key":  "fake-key",
		"gemini_api_base": srv.URL,
	})
	result, err := tl.Execute(context.Background(), map[string]any{
		"image_description": "a cat",
	}, newImageToolContext(t))
	if err != nil {
		t.Fatalf("Execute returned Go error: %v", err)
	}
	if result.Status != tool.StatusError {
		t.Fatalf("status = %q, want error", result.Status)
	}
	if !strings.Contains(result.Message, "HTTP 400") {
		t.Errorf("message = %q, want to mention HTTP 400", result.Message)
	}
	if want := tool.DispatchErrorCode(taxonomy.DispatchClassBackend4xx); result.ErrorCode != want {
		t.Errorf("ErrorCode = %q, want %q", result.ErrorCode, want)
	}
}

func TestGenerateImageWithGeminiInvalidAPIKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprint(w, `{"error":{"code":400,"message":"API key not valid. Please pass a valid API key.","status":"INVALID_ARGUMENT",`+
			`"details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"API_KEY_INVALID","domain":"googleapis.com"}]}}`)
	}))
	t.Cleanup(srv.Close)

	tl := NewGenerateImageWithGeminiTool(map[string]any{
		"gemini_api_key":  "fake-key",
		"gemini_api_base": srv.URL,
	})
	result, err := tl.Execute(t.Context(), map[string]any{
		"image_description": "a cat",
	}, newImageToolContext(t))
	if err != nil {
		t.Fatalf("Execute returned Go error: %v", err)
	}
	if result.Status != tool.StatusError {
		t.Fatalf("status = %q, want error", result.Status)
	}
	if result.Message != "Gemini API call failed: HTTP 400" {
		t.Errorf("message = %q, want the bare status", result.Message)
	}
	if want := tool.DispatchErrorCode(taxonomy.DispatchClassAuthRequired); result.ErrorCode != want {
		t.Errorf("ErrorCode = %q, want %q", result.ErrorCode, want)
	}
}

func TestCreateImageFromDescriptionRateLimited(t *testing.T) {
	const body = `{"error":{"message":"Rate limit reached for images per minute","type":"requests"}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)

	tl := NewCreateImageFromDescriptionTool(map[string]any{
		"model":    "dall-e-3",
		"api_key":  "fake-key",
		"api_base": srv.URL,
	})
	result, err := tl.Execute(t.Context(), map[string]any{
		"image_description": "a cat",
	}, newImageToolContext(t))
	if err != nil {
		t.Fatalf("Execute returned Go error: %v", err)
	}
	if result.Status != tool.StatusError {
		t.Fatalf("status = %q, want error", result.Status)
	}
	if want := "API error generating image: HTTP 429: Rate limit reached for images per minute"; result.Message != want {
		t.Errorf("message = %q, want %q", result.Message, want)
	}
	if result.ErrorCode != taxonomy.ToolFailureRateLimited {
		t.Errorf("ErrorCode = %q, want %q", result.ErrorCode, taxonomy.ToolFailureRateLimited)
	}
}

func TestCreateImageFromDescriptionBadRequestCode(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"moderation blocked", `{"error":{"code":"moderation_blocked","message":"Your request was rejected.","type":"image_generation_user_error"}}`,
			taxonomy.ToolFailureContentFiltered},
		{"spending limit", `{"error":{"code":"billing_hard_limit_reached","message":"Billing hard limit has been reached","type":"invalid_request_error"}}`,
			tool.DispatchErrorCode(taxonomy.DispatchClassBackend4xx)},
		{"no error type", `{"error":{"message":"Bad request"}}`, tool.DispatchErrorCode(taxonomy.DispatchClassBackend4xx)},
		{"a numeric code", `{"error":{"code":400,"message":"Invalid size","type":"invalid_request_error"}}`, taxonomy.ToolFailureInvalidQuery},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = fmt.Fprint(w, tc.body)
			}))
			t.Cleanup(srv.Close)

			tl := NewCreateImageFromDescriptionTool(map[string]any{
				"model":    "dall-e-3",
				"api_key":  "fake-key",
				"api_base": srv.URL,
			})
			result, err := tl.Execute(t.Context(), map[string]any{
				"image_description": "a cat",
			}, newImageToolContext(t))
			if err != nil {
				t.Fatalf("Execute returned Go error: %v", err)
			}
			if result.Status != tool.StatusError {
				t.Fatalf("status = %q, want error", result.Status)
			}
			if result.ErrorCode != tc.want {
				t.Errorf("ErrorCode = %q, want %q", result.ErrorCode, tc.want)
			}
		})
	}
}

func TestGenerateImageWithGeminiBadRequestCode(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"argument refused", `{"error":{"code":400,"message":"Unable to process input image.","status":"INVALID_ARGUMENT"}}`,
			taxonomy.ToolFailureInvalidQuery},
		{"region not served", `{"error":{"code":400,"message":"User location is not supported for the API use.","status":"FAILED_PRECONDITION"}}`,
			tool.DispatchErrorCode(taxonomy.DispatchClassBackend4xx)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = fmt.Fprint(w, tc.body)
			}))
			t.Cleanup(srv.Close)

			tl := NewGenerateImageWithGeminiTool(map[string]any{
				"gemini_api_key":  "fake-key",
				"gemini_api_base": srv.URL,
			})
			result, err := tl.Execute(t.Context(), map[string]any{
				"image_description": "a cat",
			}, newImageToolContext(t))
			if err != nil {
				t.Fatalf("Execute returned Go error: %v", err)
			}
			if result.ErrorCode != tc.want {
				t.Errorf("ErrorCode = %q, want %q", result.ErrorCode, tc.want)
			}
		})
	}
}

func TestCreateImageFromDescriptionHTMLErrorPageNotSurfaced(t *testing.T) {
	page := "<!DOCTYPE html><html><head><title>502 Bad Gateway</title></head><body>" +
		strings.Repeat("<p>upstream connect error</p>", 3500) + "</body></html>"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = fmt.Fprint(w, page)
	}))
	t.Cleanup(srv.Close)

	tl := NewCreateImageFromDescriptionTool(map[string]any{
		"model":    "dall-e-3",
		"api_key":  "fake-key",
		"api_base": srv.URL,
	})
	result, err := tl.Execute(t.Context(), map[string]any{
		"image_description": "a cat",
	}, newImageToolContext(t))
	if err != nil {
		t.Fatalf("Execute returned Go error: %v", err)
	}
	if result.Status != tool.StatusError {
		t.Fatalf("status = %q, want error", result.Status)
	}
	if strings.Contains(strings.ToLower(result.Message), "<html") {
		t.Errorf("message carries the HTML page: %.200q", result.Message)
	}
	if len(result.Message) >= 1024 {
		t.Errorf("message is %d bytes, want under 1 KB", len(result.Message))
	}
	if want := "API error generating image: HTTP 502"; result.Message != want {
		t.Errorf("message = %q, want %q", result.Message, want)
	}
	if want := tool.DispatchErrorCode(taxonomy.DispatchClassBackend5xx); result.ErrorCode != want {
		t.Errorf("ErrorCode = %q, want %q", result.ErrorCode, want)
	}
}

func TestCreateImageFromDescriptionKeepsProviderJSONMessage(t *testing.T) {
	const providerMsg = "Your request was rejected as a result of our safety system."
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprintf(w, `{"error":{"message":%q,"type":"invalid_request_error","code":"content_policy_violation"}}`, providerMsg)
	}))
	t.Cleanup(srv.Close)

	tl := NewCreateImageFromDescriptionTool(map[string]any{
		"model":    "dall-e-3",
		"api_key":  "fake-key",
		"api_base": srv.URL,
	})
	result, err := tl.Execute(t.Context(), map[string]any{
		"image_description": "a cat",
	}, newImageToolContext(t))
	if err != nil {
		t.Fatalf("Execute returned Go error: %v", err)
	}
	if result.Status != tool.StatusError {
		t.Fatalf("status = %q, want error", result.Status)
	}
	if want := "API error generating image: HTTP 400: " + providerMsg; result.Message != want {
		t.Errorf("message = %q, want %q", result.Message, want)
	}
	if want := taxonomy.ToolFailureContentFiltered; result.ErrorCode != want {
		t.Errorf("ErrorCode = %q, want %q", result.ErrorCode, want)
	}
}

func TestProviderErrorText(t *testing.T) {
	longMsg := strings.Repeat("é", 200)
	cases := []struct {
		name string
		body string
		want string
	}{
		{"nested message", `{"error":{"message":"bad prompt"}}`, "bad prompt"},
		{"top-level message", `{"message":"quota spent"}`, "quota spent"},
		{"string error", `{"error":"Internal Server Error"}`, "Internal Server Error"},
		{"whitespace collapsed", `{"error":{"message":"line one\n\n  line\ttwo "}}`, "line one line two"},
		{"truncated on a rune boundary", `{"message":"` + longMsg + `"}`, strings.Repeat("é", 128)},
		{"html page", `<html><body>502 Bad Gateway</body></html>`, ""},
		{"html wrapped in json", `{"error":{"message":"<!DOCTYPE html><html>502</html>"}}`, ""},
		{"plain text", `upstream connect error`, ""},
		{"json without a message", `{"error":{"code":500}}`, ""},
		{"empty", ``, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := providerErrorText([]byte(c.body)); got != c.want {
				t.Errorf("providerErrorText(%q) = %q, want %q", c.body, got, c.want)
			}
		})
	}
}

func TestEditImageWithGeminiHappyPathAfterRefactor(t *testing.T) {
	srv, _ := newGeminiMockServer(t, "fake-key")
	tl := NewEditImageWithGeminiTool(map[string]any{
		"gemini_api_key":  "fake-key",
		"gemini_api_base": srv.URL,
	})

	imgBytes, _ := base64.StdEncoding.DecodeString(tinyPNGBase64)
	result, err := tl.Execute(context.Background(), map[string]any{
		"input_image": &tool.Artifact{
			Filename: "input.png",
			Version:  1,
			Content:  imgBytes,
		},
		"edit_prompt":     "make it blue",
		"output_filename": "edited",
	}, &tool.Context{})
	if err != nil {
		t.Fatalf("Execute returned Go error: %v", err)
	}
	if result.Status != tool.StatusSuccess {
		t.Fatalf("status = %q, want success; message = %q", result.Status, result.Message)
	}
	if len(result.DataObjects) != 1 {
		t.Fatalf("expected 1 DataObject, got %d", len(result.DataObjects))
	}
	if result.DataObjects[0].Name != "edited.jpg" {
		t.Errorf("filename = %q, want edited.jpg", result.DataObjects[0].Name)
	}
	if result.DataObjects[0].Metadata["editing_tool"] != "gemini" {
		t.Errorf("metadata.editing_tool = %v, want gemini", result.DataObjects[0].Metadata["editing_tool"])
	}
}
