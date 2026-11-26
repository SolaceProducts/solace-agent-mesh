// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import (
	"cmp"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/testing/mockllm"
)

type PrimedDecisionResponse struct {
	Response               string   `yaml:"response"`
	StatusCode             int      `yaml:"status_code"`
	ExpectedImageMIMETypes []string `yaml:"expected_image_mime_types"`
	ExpectedMedia          []string `yaml:"expected_media"`
}

const typeSafeDecisionPath = "/v1/systemone"

type servedDecision struct {
	primed   PrimedDecisionResponse
	gemini   bool
	images   []string
	media    []string
	inputErr error
}

type MockDecisionServer struct {
	server *httptest.Server

	mu        sync.Mutex
	responses []PrimedDecisionResponse
	served    []servedDecision
}

func NewMockDecisionServer(responses []PrimedDecisionResponse) *MockDecisionServer {
	m := &MockDecisionServer{responses: responses}
	mux := http.NewServeMux()
	mux.HandleFunc(typeSafeDecisionPath, m.handleEvaluate)
	mux.HandleFunc(mockllm.OpenAIDecisionPath, m.handleEvaluate)
	mux.HandleFunc(mockllm.GeminiDecisionPath, m.handleEvaluate)
	m.server = httptest.NewServer(mux)
	return m
}

func (m *MockDecisionServer) URL() string { return m.server.URL }

func (m *MockDecisionServer) Close() {
	if m.server != nil {
		m.server.Close()
	}
}

func (m *MockDecisionServer) handleEvaluate(w http.ResponseWriter, r *http.Request) {
	body, readErr := io.ReadAll(r.Body)
	var images, media []string
	inputErr := readErr
	switch {
	case readErr != nil:
	case r.URL.Path == mockllm.OpenAIDecisionPath:
		images, inputErr = mockllm.OpenAIDecisionInputImages(body)
	case r.URL.Path == typeSafeDecisionPath:
		images, inputErr = mockllm.TypeSafeDecisionStateImages(body)
	case r.URL.Path == mockllm.GeminiDecisionPath:
		var blocks []mockllm.GeminiMediaBlock
		blocks, inputErr = mockllm.GeminiDecisionInputMedia(body)
		images = mockllm.GeminiImageMIMETypes(blocks)
		for _, b := range blocks {
			media = append(media, b.String())
		}
		inputErr = cmp.Or(inputErr, geminiKeyHeaderError(r.Header))
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.responses) == 0 {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = fmt.Fprint(w, `{"error":"no primed decision response available"}`)
		return
	}
	resp := m.responses[0]
	m.responses = m.responses[1:]
	m.served = append(m.served, servedDecision{primed: resp, gemini: r.URL.Path == mockllm.GeminiDecisionPath, images: images, media: media, inputErr: inputErr})

	w.Header().Set("Content-Type", "application/json")
	if resp.StatusCode != 0 {
		w.WriteHeader(resp.StatusCode)
	}
	_, _ = fmt.Fprint(w, resp.Response)
}

func geminiKeyHeaderError(header http.Header) error {
	if header.Get("x-goog-api-key") == "" || header.Get("Authorization") != "" {
		return errors.New("a Gemini request carries its key in x-goog-api-key alone")
	}
	return nil
}

func (m *MockDecisionServer) servedCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.served)
}

func (m *MockDecisionServer) assertRequests(t *testing.T) {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, s := range m.served {
		if s.inputErr != nil {
			t.Errorf("decision request %d: malformed input: %v", i, s.inputErr)
		}
		if want := s.primed.ExpectedImageMIMETypes; want != nil && !slices.Equal(s.images, want) {
			t.Errorf("decision request %d carried images %v, want %v", i, s.images, want)
		}
		if s.primed.ExpectedMedia != nil && !s.gemini {
			t.Errorf("decision request %d sets expected_media, which only a Gemini decision request carries", i)
		}
		if want := s.primed.ExpectedMedia; want != nil && !slices.Equal(s.media, want) {
			t.Errorf("decision request %d carried media %v, want %v", i, s.media, want)
		}
	}
	for i, r := range m.responses {
		if r.ExpectedImageMIMETypes != nil {
			t.Errorf("primed decision response %d expects images %v but no request reached it", len(m.served)+i, r.ExpectedImageMIMETypes)
		}
		if r.ExpectedMedia != nil {
			t.Errorf("primed decision response %d expects media %v but no request reached it", len(m.served)+i, r.ExpectedMedia)
		}
	}
}

func (h *TestHarness) AssertDecisionRequests(t *testing.T) {
	t.Helper()
	if h.decisionServer != nil {
		h.decisionServer.assertRequests(t)
	}
}
