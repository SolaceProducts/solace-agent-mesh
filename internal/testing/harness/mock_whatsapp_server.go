// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"time"
)

type CapturedWhatsAppCall struct {
	Method     string
	Path       string
	To         string
	Recipient  string
	Type       string
	Text       string
	Body       []byte
	Timestamp  time.Time
	Status     int
	Delivered  bool
	ResponseID string
}

type MockWhatsAppServer struct {
	server    *httptest.Server
	mu        sync.Mutex
	calls     []CapturedWhatsAppCall
	idCounter int
	media     map[string]mockMedia

	statusCalls []CapturedWhatsAppCall

	scripted map[string][]ScriptedResponse

	requireAuth string

	mediaCalls []CapturedWhatsAppCall
}

type ScriptedResponse struct {
	Status       int
	GraphCode    int
	GraphMessage string
	Body         string
	Headers      map[string]string
}

type mockMedia struct {
	content []byte
	mime    string
}

func NewMockWhatsAppServer() *MockWhatsAppServer {
	m := &MockWhatsAppServer{media: make(map[string]mockMedia), scripted: make(map[string][]ScriptedResponse)}
	mux := http.NewServeMux()
	mux.HandleFunc("/__captured", m.handleCaptured)
	mux.HandleFunc("/media-bytes/", m.handleMediaBytes)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			m.handleMediaMetadata(w, r)
			return
		}
		m.handleMessages(w, r)
	})
	m.server = httptest.NewServer(mux)
	return m
}

const (
	EndpointSend          = "send"
	EndpointMediaUpload   = "media_upload"
	EndpointMediaMetadata = "media_metadata"
	EndpointMediaDownload = "media_download"
)

func (m *MockWhatsAppServer) ScriptResponses(endpoint string, responses ...ScriptedResponse) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.scripted[endpoint] = append(m.scripted[endpoint], responses...)
}

func (m *MockWhatsAppServer) RequireBearerToken(token string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requireAuth = token
}

func RateLimited() ScriptedResponse {
	return ScriptedResponse{
		Status:       http.StatusTooManyRequests,
		GraphCode:    130429,
		GraphMessage: "message throughput reached",
	}
}

func WindowClosed() ScriptedResponse {
	return ScriptedResponse{Status: http.StatusBadRequest, GraphCode: 131047, GraphMessage: "Re-engagement message required"}
}

func TokenExpired() ScriptedResponse {
	return ScriptedResponse{Status: http.StatusUnauthorized, GraphCode: 190, GraphMessage: "Error validating access token"}
}

func ServerError() ScriptedResponse {
	return ScriptedResponse{Status: http.StatusServiceUnavailable, GraphMessage: "Service temporarily unavailable"}
}

func endpointKind(method, path string) string {
	switch {
	case strings.HasPrefix(path, "/media-bytes/"):
		return EndpointMediaDownload
	case strings.HasSuffix(path, "/media"):
		return EndpointMediaUpload
	case isMessagesPath(path):
		return EndpointSend
	case method == http.MethodGet:
		return EndpointMediaMetadata
	}
	return ""
}

func (m *MockWhatsAppServer) nextScripted(path string) (ScriptedResponse, bool) {
	return m.nextScriptedFor(http.MethodPost, path)
}

func (m *MockWhatsAppServer) nextScriptedFor(method, path string) (ScriptedResponse, bool) {
	kind := endpointKind(method, path)
	if kind == "" {
		return ScriptedResponse{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	queue := m.scripted[kind]
	if len(queue) == 0 {
		return ScriptedResponse{}, false
	}
	m.scripted[kind] = queue[1:]
	return queue[0], true
}

func (r ScriptedResponse) httpStatus() int {
	if r.Status == 0 {
		return http.StatusInternalServerError
	}
	return r.Status
}

func (m *MockWhatsAppServer) writeScripted(w http.ResponseWriter, resp ScriptedResponse) {
	for k, v := range resp.Headers {
		w.Header().Set(k, v)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.httpStatus())
	if resp.Body != "" {
		_, _ = w.Write([]byte(resp.Body))
		return
	}
	json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
		"error": map[string]any{"code": resp.GraphCode, "message": resp.GraphMessage},
	})
}

func (m *MockWhatsAppServer) rejected(w http.ResponseWriter, r *http.Request) bool {
	if !m.authorized(w, r) {
		return true
	}
	if resp, ok := m.nextScriptedFor(r.Method, r.URL.Path); ok {
		m.writeScripted(w, resp)
		return true
	}
	return false
}

func (m *MockWhatsAppServer) authorized(w http.ResponseWriter, r *http.Request) bool {
	m.mu.Lock()
	want := m.requireAuth
	m.mu.Unlock()
	if want == "" {
		return true
	}
	if r.Header.Get("Authorization") == "Bearer "+want {
		return true
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
		"error": map[string]any{"code": 190, "message": "Error validating access token"},
	})
	return false
}

func (m *MockWhatsAppServer) MediaCalls() []CapturedWhatsAppCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]CapturedWhatsAppCall, len(m.mediaCalls))
	copy(out, m.mediaCalls)
	return out
}

func (m *MockWhatsAppServer) recordMediaCall(r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.mediaCalls = append(m.mediaCalls, CapturedWhatsAppCall{
		Method:    r.Method,
		Path:      r.URL.Path,
		Type:      endpointKind(r.Method, r.URL.Path),
		Timestamp: time.Now(),
	})
}

func (m *MockWhatsAppServer) StatusCalls() []CapturedWhatsAppCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]CapturedWhatsAppCall, len(m.statusCalls))
	copy(out, m.statusCalls)
	return out
}

func (m *MockWhatsAppServer) IndicatedTaskMessageIDs() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var ids []string
	for _, call := range m.statusCalls {
		var status struct {
			MessageID       string          `json:"message_id"`
			TypingIndicator json.RawMessage `json:"typing_indicator"`
		}
		if json.Unmarshal(call.Body, &status) != nil || status.TypingIndicator == nil {
			continue
		}
		if !slices.Contains(ids, status.MessageID) {
			ids = append(ids, status.MessageID)
		}
	}
	return ids
}

func (m *MockWhatsAppServer) RegisterMedia(id string, content []byte, mime string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.media[id] = mockMedia{content: content, mime: mime}
}

func (m *MockWhatsAppServer) handleMediaMetadata(w http.ResponseWriter, r *http.Request) {
	m.recordMediaCall(r)
	if m.rejected(w, r) {
		return
	}
	id := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
	m.mu.Lock()
	med, ok := m.media[id]
	m.mu.Unlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
		"url":       m.server.URL + "/media-bytes/" + id,
		"mime_type": med.mime,
		"file_size": len(med.content),
		"id":        id,
	})
}

func (m *MockWhatsAppServer) handleMediaBytes(w http.ResponseWriter, r *http.Request) {
	m.recordMediaCall(r)
	if m.rejected(w, r) {
		return
	}
	id := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
	m.mu.Lock()
	med, ok := m.media[id]
	m.mu.Unlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	if med.mime != "" {
		w.Header().Set("Content-Type", med.mime)
	}
	_, _ = w.Write(med.content)
}

func (m *MockWhatsAppServer) handleCaptured(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(m.Calls()) //nolint:errcheck
}

func (m *MockWhatsAppServer) URL() string { return m.server.URL }

func (m *MockWhatsAppServer) Calls() []CapturedWhatsAppCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]CapturedWhatsAppCall, len(m.calls))
	copy(out, m.calls)
	return out
}

func (m *MockWhatsAppServer) DeliveredCalls() []CapturedWhatsAppCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]CapturedWhatsAppCall, 0, len(m.calls))
	for _, c := range m.calls {
		if c.Delivered {
			out = append(out, c)
		}
	}
	return out
}

func (m *MockWhatsAppServer) WaitForCallCount(n int, timeout time.Duration) bool {
	return m.waitForCount(n, timeout, func(c CapturedWhatsAppCall) bool { return true })
}

func (m *MockWhatsAppServer) WaitForDeliveredCount(n int, timeout time.Duration) bool {
	return m.waitForCount(n, timeout, func(c CapturedWhatsAppCall) bool { return c.Delivered })
}

func (m *MockWhatsAppServer) waitForCount(n int, timeout time.Duration, include func(CapturedWhatsAppCall) bool) bool {
	deadline := time.Now().Add(timeout)
	for {
		m.mu.Lock()
		count := 0
		for _, c := range m.calls {
			if include(c) {
				count++
			}
		}
		m.mu.Unlock()
		if count >= n {
			return true
		}
		if !time.Now().Before(deadline) {
			return false
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (m *MockWhatsAppServer) Close() {
	if m.server != nil {
		m.server.Close()
	}
}

func (m *MockWhatsAppServer) nextID() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.idCounter++
	return fmt.Sprintf("wamid.MOCK-%d", m.idCounter)
}

func (m *MockWhatsAppServer) LastSentID() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.calls) == 0 {
		return ""
	}
	return m.calls[len(m.calls)-1].ResponseID
}

func (m *MockWhatsAppServer) handleMessages(w http.ResponseWriter, r *http.Request) {
	if !m.authorized(w, r) {
		return
	}
	if strings.HasSuffix(r.URL.Path, "/media") {
		_ = r.Body.Close()
		if resp, ok := m.nextScripted(r.URL.Path); ok {
			m.writeScripted(w, resp)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"id": "upload-" + m.nextID()}) //nolint:errcheck
		return
	}

	body, _ := io.ReadAll(r.Body)
	defer func() { _ = r.Body.Close() }()

	var msg struct {
		To        string `json:"to"`
		Recipient string `json:"recipient"`
		Type      string `json:"type"`
		Status    string `json:"status"`
		Text      struct {
			Body string `json:"body"`
		} `json:"text"`
	}
	json.Unmarshal(body, &msg) //nolint:errcheck

	isStatusPost := msg.Status != ""

	scripted := ScriptedResponse{}
	failed := false
	if !isStatusPost {
		scripted, failed = m.nextScripted(r.URL.Path)
	}

	status := http.StatusOK
	respID := ""
	if failed {
		status = scripted.httpStatus()
	} else {
		respID = m.nextID()
	}
	delivered := status >= 200 && status < 300

	if isMessagesPath(r.URL.Path) {
		call := CapturedWhatsAppCall{
			Method:     r.Method,
			Path:       r.URL.Path,
			To:         msg.To,
			Recipient:  msg.Recipient,
			Type:       msg.Type,
			Text:       msg.Text.Body,
			Body:       body,
			Timestamp:  time.Now(),
			Status:     status,
			Delivered:  delivered,
			ResponseID: respID,
		}
		m.mu.Lock()
		if isStatusPost {
			m.statusCalls = append(m.statusCalls, call)
		} else {
			m.calls = append(m.calls, call)
		}
		m.mu.Unlock()
	}

	if failed {
		m.writeScripted(w, scripted)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
		"messaging_product": "whatsapp",
		"messages":          []map[string]string{{"id": respID}},
	})
}

func isMessagesPath(path string) bool {
	return strings.HasSuffix(path, "/messages")
}
