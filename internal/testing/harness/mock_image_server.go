// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
)

type PrimedImageResponse struct {
	Response   string `yaml:"response"`
	StatusCode int    `yaml:"status_code"`
}

type MockImageServer struct {
	server          *httptest.Server
	mu              sync.Mutex
	imageResponses  []PrimedImageResponse
	geminiAPIKey    string
	geminiCallCount int
	geminiMode      string
}

func NewMockImageServer(imageResponses []PrimedImageResponse) *MockImageServer {
	m := &MockImageServer{
		imageResponses: imageResponses,
		geminiAPIKey:   "fake-gemini-api-key",
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/images/generations", m.handleImageGeneration)
	mux.HandleFunc("/v1beta/models/", m.handleGemini)

	m.server = httptest.NewServer(mux)
	return m
}

func (m *MockImageServer) SetGeminiMode(mode string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.geminiMode = mode
}

func (m *MockImageServer) URL() string {
	return m.server.URL
}

func (m *MockImageServer) Close() {
	if m.server != nil {
		m.server.Close()
	}
}

func (m *MockImageServer) handleImageGeneration(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.imageResponses) == 0 {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = fmt.Fprint(w, `{"error": "No primed image generation response available"}`)
		return
	}

	resp := m.imageResponses[0]
	m.imageResponses = m.imageResponses[1:]

	statusCode := resp.StatusCode
	if statusCode == 0 {
		statusCode = http.StatusOK
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_, _ = fmt.Fprint(w, resp.Response)
}

func (m *MockImageServer) handleGemini(w http.ResponseWriter, r *http.Request) {
	if !strings.HasSuffix(r.URL.Path, ":generateContent") {
		w.WriteHeader(http.StatusNotFound)
		_, _ = fmt.Fprint(w, `{"error": {"message": "Not found"}}`)
		return
	}

	m.mu.Lock()
	mode := m.geminiMode
	m.geminiCallCount++
	m.mu.Unlock()

	if mode == "error" {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprint(w, `{"error": {"code": 400, "message": "API key not valid. Please pass a valid API key.", "status": "INVALID_ARGUMENT"}}`)
		return
	}

	if mode == "text_only" {
		geminiResp := map[string]any{
			"candidates": []map[string]any{
				{
					"content": map[string]any{
						"parts": []map[string]any{
							{"text": "I can describe the image but cannot generate one."},
						},
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(geminiResp)
		return
	}

	apiKey := r.URL.Query().Get("key")
	if apiKey != m.geminiAPIKey {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprint(w, `{"error": {"code": 400, "message": "API key not valid. Please pass a valid API key.", "status": "INVALID_ARGUMENT"}}`)
		return
	}

	dummyJPEG := "/9j/4AAQSkZJRgABAQAAAQABAAD/2wBDAAgGBgcGBQgHBwcJCQgKDBQNDAsLDBkSEw8UHRofHh0aHBwgJC4nICIsIxwcKDcpLDAxNDQ0Hyc5PTgyPC4zNDL/2wBDAQkJCQwLDBgNDRgyIRwhMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjL/wAARCAABAAEDASIAAhEBAxEB/8QAHwAAAQUBAQEBAQEAAAAAAAAAAAECAwQFBgcICQoL/8QAtRAAAgEDAwIEAwUFBAQAAAF9AQIDAAQRBRIhMUEGE1FhByJxFDKBkaEII0KxwRVS0fAkM2JyggkKFhcYGRolJicoKSo0NTY3ODk6Q0RFRkdISUpTVFVWV1hZWmNkZWZnaGlqc3R1dnd4eXqDhIWGh4iJipKTlJWWl5iZmqKjpKWmp6ipqrKztLW2t7i5usLDxMXGx8jJytLT1NXW19jZ2uHi4+Tl5ufo6erx8vP09fb3+Pn6/8QAHwEAAwEBAQEBAQEBAQAAAAAAAAECAwQFBgcICQoL/8QAtREAAgECBAQDBAcFBAQAAQJ3AAECAxEEBSExBhJBUQdhcRMiMoEIFEKRobHBCSMzUvAVYnLRChYkNOEl8RcYI4Q/SFhSRJEQVl3EVA2VqIogIm7SiY4K/9oADAMBAAIRAxEAPwD3+gD/2Q=="
	dummyJPEGBytes, _ := base64.StdEncoding.DecodeString(dummyJPEG)
	b64Image := base64.StdEncoding.EncodeToString(dummyJPEGBytes)

	geminiResp := map[string]any{
		"candidates": []map[string]any{
			{
				"content": map[string]any{
					"parts": []map[string]any{
						{
							"inlineData": map[string]any{
								"mimeType": "image/jpeg",
								"data":     b64Image,
							},
						},
					},
				},
			},
		},
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(geminiResp)
}
