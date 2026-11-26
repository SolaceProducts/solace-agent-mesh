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
	"strings"
	"sync"
	"time"
)

type CapturedTeamsCall struct {
	Method         string
	Path           string
	ConversationID string
	ActivityID     string
	Body           []byte
	ActivityType   string
	Text           string
	Attachments    int
	Timestamp      time.Time
	ResponseID     string
}

type MockTeamsServer struct {
	server    *httptest.Server
	mu        sync.Mutex
	calls     []CapturedTeamsCall
	idCounter int
	files     map[string]MockFile
}

type MockFile struct {
	Content     string
	ContentType string
}

func NewMockTeamsServer() *MockTeamsServer {
	m := &MockTeamsServer{
		files: make(map[string]MockFile),
	}

	mux := http.NewServeMux()

	mux.HandleFunc("/v3/conversations/", m.handleConversationActivity)

	mux.HandleFunc("/botframework.com/oauth2/v2.0/token", m.handleOAuthToken)

	mux.HandleFunc("/files/", m.handleFileDownload)

	m.server = httptest.NewServer(mux)
	return m
}

func (m *MockTeamsServer) AddFile(name, content, contentType string) string {
	path := "/files/" + name
	m.files[path] = MockFile{Content: content, ContentType: contentType}
	return m.server.URL + path
}

func (m *MockTeamsServer) handleFileDownload(w http.ResponseWriter, r *http.Request) {
	f, ok := m.files[r.URL.Path]
	if !ok {
		http.NotFound(w, r)
		return
	}
	if f.ContentType != "" {
		w.Header().Set("Content-Type", f.ContentType)
	}
	w.Write([]byte(f.Content)) //nolint:errcheck
}

func (m *MockTeamsServer) URL() string {
	return m.server.URL
}

func (m *MockTeamsServer) OAuthTokenURL() string {
	return m.server.URL + "/botframework.com/oauth2/v2.0/token"
}

func (m *MockTeamsServer) Calls() []CapturedTeamsCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]CapturedTeamsCall, len(m.calls))
	copy(out, m.calls)
	return out
}

func (m *MockTeamsServer) CallsByText(substr string) []CapturedTeamsCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []CapturedTeamsCall
	for _, c := range m.calls {
		if strings.Contains(c.Text, substr) {
			out = append(out, c)
		}
	}
	return out
}

func (m *MockTeamsServer) WaitForCallCount(n int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		m.mu.Lock()
		count := len(m.calls)
		m.mu.Unlock()
		if count >= n {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

func (m *MockTeamsServer) Close() {
	if m.server != nil {
		m.server.Close()
	}
}

func (m *MockTeamsServer) nextID() string {
	m.idCounter++
	return fmt.Sprintf("mock-activity-%d", m.idCounter)
}

func (m *MockTeamsServer) handleConversationActivity(w http.ResponseWriter, r *http.Request) {
	remainder := strings.TrimPrefix(r.URL.Path, "/v3/conversations/")
	if r.Method == http.MethodGet && strings.Contains(remainder, "/members/") {
		m.handleMemberLookup(w, r)
		return
	}

	body, _ := io.ReadAll(r.Body)
	defer func() { _ = r.Body.Close() }()

	parts := strings.Split(remainder, "/")
	convID := ""
	activityID := ""
	if len(parts) >= 1 {
		convID = parts[0]
	}
	if len(parts) >= 3 {
		activityID = parts[2]
	}

	var activity struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		Attachments []any  `json:"attachments"`
	}
	json.Unmarshal(body, &activity) //nolint:errcheck

	m.mu.Lock()
	respID := m.nextID()
	m.calls = append(m.calls, CapturedTeamsCall{
		Method:         r.Method,
		Path:           r.URL.Path,
		ConversationID: convID,
		ActivityID:     activityID,
		Body:           body,
		ActivityType:   activity.Type,
		Text:           activity.Text,
		Attachments:    len(activity.Attachments),
		Timestamp:      time.Now(),
		ResponseID:     respID,
	})
	m.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"id": respID}) //nolint:errcheck
}

func (m *MockTeamsServer) handleMemberLookup(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
		"id":                "teams-test-user",
		"name":              "Test User",
		"email":             "testuser@example.com",
		"userPrincipalName": "testuser@example.com",
		"aadObjectId":       "aad-test-object-id",
	})
}

func (m *MockTeamsServer) handleOAuthToken(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
		"access_token": "mock-bot-token",
		"token_type":   "Bearer",
		"expires_in":   3600,
	})
}
