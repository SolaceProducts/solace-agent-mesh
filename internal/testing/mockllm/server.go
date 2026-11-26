// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mockllm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type MockLLMServer struct {
	addr        string
	delay       time.Duration
	streamDelay time.Duration
	logger      *slog.Logger
	server      *http.Server
	listener    net.Listener

	scenarios map[string]*Scenario

	scenariosList []*Scenario

	requestCount atomic.Int64

	captureMu      sync.Mutex
	capturedBodies [][]byte
	captureLimit   int
}

const requestCaptureDefaultLimit = 50

func NewMockLLMServer(addr string, delay, streamDelay time.Duration, logger *slog.Logger) *MockLLMServer {
	return &MockLLMServer{
		addr:         addr,
		delay:        delay,
		streamDelay:  streamDelay,
		logger:       logger,
		scenarios:    make(map[string]*Scenario),
		captureLimit: requestCaptureDefaultLimit,
	}
}

func (s *MockLLMServer) captureRequest(body []byte) {
	if s.captureLimit <= 0 {
		return
	}
	copied := make([]byte, len(body))
	copy(copied, body)

	s.captureMu.Lock()
	defer s.captureMu.Unlock()
	s.capturedBodies = append(s.capturedBodies, copied)
	if overflow := len(s.capturedBodies) - s.captureLimit; overflow > 0 {
		s.capturedBodies = s.capturedBodies[overflow:]
	}
}

func (s *MockLLMServer) CapturedRequests() [][]byte {
	s.captureMu.Lock()
	defer s.captureMu.Unlock()
	out := make([][]byte, len(s.capturedBodies))
	for i, b := range s.capturedBodies {
		out[i] = append([]byte(nil), b...)
	}
	return out
}

func (s *MockLLMServer) ResetCapturedRequests() {
	s.captureMu.Lock()
	defer s.captureMu.Unlock()
	s.capturedBodies = nil
}

func (s *MockLLMServer) handleCapturedRequests(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	bodies := s.CapturedRequests()
	decoded := make([]json.RawMessage, 0, len(bodies))
	for _, b := range bodies {
		if !json.Valid(b) {
			decoded = append(decoded, json.RawMessage("null"))
			continue
		}
		decoded = append(decoded, json.RawMessage(b))
	}
	out := struct {
		Count    int               `json:"count"`
		Requests []json.RawMessage `json:"requests"`
	}{Count: len(decoded), Requests: decoded}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func (s *MockLLMServer) LoadScenarios(scenarios ...*Scenario) {
	for _, sc := range scenarios {
		s.scenarios[sc.ID] = sc
		s.scenariosList = append(s.scenariosList, sc)
		s.logger.Info("loaded scenario", "id", sc.ID, "responses", len(sc.Responses), "match", sc.Match)
	}
}

func (s *MockLLMServer) RequestCount() int64 {
	return s.requestCount.Load()
}

func (s *MockLLMServer) Start() error {
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", s.addr, err)
	}
	s.listener = ln

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", s.handleChatCompletions)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("/__requests", s.handleCapturedRequests)
	mux.HandleFunc("/__requests/reset", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.ResetCapturedRequests()
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/v1/models", s.handleListModels)
	mux.HandleFunc("/models", s.handleListModels)
	mux.HandleFunc("/v1/images/generations", s.handleImageGenerations)
	mux.HandleFunc(DecisionPath, s.handleDecision)
	mux.HandleFunc(OpenAIDecisionPath, s.handleOpenAIDecision)

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		s.logger.Warn("unhandled request", "method", r.Method, "path", r.URL.Path)
		http.NotFound(w, r)
	})

	s.server = &http.Server{Handler: mux}

	go func() {
		if err := s.server.Serve(ln); err != nil && err != http.ErrServerClosed {
			s.logger.Error("mock LLM server serve error", "error", err)
		}
	}()

	s.logger.Info("mock LLM server started", "addr", ln.Addr().String())
	return nil
}

func (s *MockLLMServer) Addr() string {
	if s.listener != nil {
		return s.listener.Addr().String()
	}
	return s.addr
}

func (s *MockLLMServer) Stop() {
	if s.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.server.Shutdown(ctx)
	}
}

func (s *MockLLMServer) handleListModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	resp := map[string]any{
		"object": "list",
		"data": []map[string]any{
			{"id": "e2e-mock", "object": "model", "owned_by": "mock"},
			{"id": "e2e-mock-small", "object": "model", "owned_by": "mock"},
		},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

const mockPNGBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="

type imageGenerationRequest struct {
	Prompt string `json:"prompt"`
}

func (s *MockLLMServer) handleImageGenerations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	s.captureRequest(body)

	var req imageGenerationRequest
	if unmarshalErr := json.Unmarshal(body, &req); unmarshalErr != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if req.Prompt == "" {
		http.Error(w, "prompt is required", http.StatusBadRequest)
		return
	}

	if d := s.imageDelay(req.Prompt); d > 0 {
		time.Sleep(d)
	}

	resp := map[string]any{
		"created": 0,
		"data":    []map[string]any{{"b64_json": mockPNGBase64}},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *MockLLMServer) imageDelay(prompt string) time.Duration {
	for _, sc := range s.scenariosList {
		if sc.matcher == nil || !sc.matcher.MatchString(prompt) {
			continue
		}
		if len(sc.Precomputed) > 0 && sc.Precomputed[0].TTFT != nil {
			return *sc.Precomputed[0].TTFT
		}
		break
	}
	return s.delay
}

func (s *MockLLMServer) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	s.requestCount.Add(1)

	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	s.captureRequest(body)

	var req ChatCompletionRequest
	if unmarshalErr := json.Unmarshal(body, &req); unmarshalErr != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	response, err := s.resolveResponse(&req)
	if err != nil {
		s.logger.Warn("resolve response failed", "error", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	d := s.delay
	if response.TTFT != nil {
		d = *response.TTFT
	}
	if d > 0 {
		time.Sleep(d)
	}

	if response.HTTPError != nil {
		s.writeHTTPError(w, response.HTTPError)
		return
	}

	if req.Stream {
		s.writeStreamResponse(w, response)
	} else {
		s.writeFullResponse(w, response)
	}
}

func (s *MockLLMServer) writeHTTPError(w http.ResponseWriter, httpErr *HTTPErrorResponse) {
	w.Header().Set("Content-Type", "application/json")
	for name, value := range httpErr.Headers {
		w.Header().Set(name, value)
	}
	w.WriteHeader(httpErr.Status)
	if len(httpErr.Body) > 0 {
		_, _ = w.Write(httpErr.Body)
	}
}

func (s *MockLLMServer) resolveResponse(req *ChatCompletionRequest) (*PrecomputedResponse, error) {
	scenarioID, inlineResponses, turnIndex := ExtractScenarioFromMessages(req.Messages)

	if len(inlineResponses) > 0 {
		if turnIndex >= len(inlineResponses) {
			return nil, fmt.Errorf("turn index %d out of range (have %d inline responses)", turnIndex, len(inlineResponses))
		}
		pc, err := PrecomputeResponse(inlineResponses[turnIndex], turnIndex)
		if err != nil {
			return nil, fmt.Errorf("precompute inline response: %w", err)
		}
		return &pc, nil
	}

	if scenarioID != "" {
		sc, ok := s.scenarios[scenarioID]
		if !ok {
			return nil, fmt.Errorf("unknown scenario %q", scenarioID)
		}
		if turnIndex >= len(sc.Precomputed) {
			return nil, fmt.Errorf("turn index %d out of range for scenario %q (have %d responses)", turnIndex, scenarioID, len(sc.Precomputed))
		}
		return &sc.Precomputed[turnIndex], nil
	}

	if len(s.scenarios) == 1 {
		for _, sc := range s.scenarios {
			if turnIndex >= len(sc.Precomputed) {
				return nil, fmt.Errorf("turn index %d out of range for default scenario (have %d responses)", turnIndex, len(sc.Precomputed))
			}
			return &sc.Precomputed[turnIndex], nil
		}
	}

	if len(s.scenariosList) > 1 {
		lastUser := extractLastUserContent(req.Messages)
		for _, sc := range s.scenariosList {
			if sc.matcher == nil {
				continue
			}
			if sc.matcher.MatchString(lastUser) {
				return pickContentResponse(sc, turnIndex), nil
			}
		}
		for _, sc := range s.scenariosList {
			if sc.matcher == nil {
				return pickContentResponse(sc, turnIndex), nil
			}
		}
	}

	return nil, fmt.Errorf("no scenario found: no test_case_id, responses_json, or scenario tag in messages")
}

func extractLastUserContent(msgs []RequestMessage) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != "user" {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(msgs[i].Content), systemReminderPrefix) {
			continue
		}
		return msgs[i].Content
	}
	return ""
}

const systemReminderPrefix = "<system-reminder>"

func pickContentResponse(sc *Scenario, turnIndex int) *PrecomputedResponse {
	if len(sc.Precomputed) == 1 {
		return &sc.Precomputed[0]
	}
	idx := turnIndex
	if idx >= len(sc.Precomputed) {
		idx = len(sc.Precomputed) - 1
	}
	if idx < 0 {
		idx = 0
	}
	return &sc.Precomputed[idx]
}

func (s *MockLLMServer) writeFullResponse(w http.ResponseWriter, pc *PrecomputedResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(pc.FullJSON)
}

func (s *MockLLMServer) writeStreamResponse(w http.ResponseWriter, pc *PrecomputedResponse) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	flusher, ok := w.(http.Flusher)
	if !ok {
		for _, frame := range pc.StreamFrames {
			_, _ = w.Write(frame)
		}
		return
	}

	sd := s.streamDelay
	if pc.StreamDelay != nil {
		sd = *pc.StreamDelay
	}

	for i, frame := range pc.StreamFrames {
		if i > 0 && sd > 0 {
			time.Sleep(sd)
		}
		_, _ = w.Write(frame)
		flusher.Flush()
	}
}
