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

type CapturedSlackCall struct {
	Method     string
	Channel    string
	ThreadTS   string
	Text       string
	Blocks     string
	Timestamp  time.Time
	RawForm    map[string]string
	Body       []byte
	ResponseTS string
}

type MockSlackServer struct {
	server      *httptest.Server
	mu          sync.Mutex
	calls       []CapturedSlackCall
	tsCounter   int
	fileCounter int
	userEmails  map[string]string

	files map[string][]byte
}

func NewMockSlackServer() *MockSlackServer {
	m := &MockSlackServer{
		userEmails: map[string]string{
			"U-test-user": "testuser@example.com",
		},
		files: map[string][]byte{},
	}

	mux := http.NewServeMux()

	mux.HandleFunc("/chat.postMessage", m.handleChatPostMessage)
	mux.HandleFunc("/chat.update", m.handleChatUpdate)
	mux.HandleFunc("/chat.delete", m.handleChatDelete)
	mux.HandleFunc("/chat.postEphemeral", m.handleChatPostMessage)
	mux.HandleFunc("/files.getUploadURLExternal", m.handleFilesGetUploadURL)
	mux.HandleFunc("/files.completeUploadExternal", m.handleFilesCompleteUpload)
	mux.HandleFunc("/files.info", m.handleFilesInfo)
	mux.HandleFunc("/users.profile.get", m.handleUsersProfileGet)
	mux.HandleFunc("/users.info", m.handleUsersInfo)
	mux.HandleFunc("/reactions.add", m.handleGenericOK)
	mux.HandleFunc("/reactions.remove", m.handleGenericOK)
	mux.HandleFunc("/conversations.info", m.handleConversationsInfo)
	mux.HandleFunc("/conversations.list", m.handleConversationsList)
	mux.HandleFunc("/auth.test", m.handleAuthTest)

	mux.HandleFunc("/upload/", m.handleFileUpload)
	mux.HandleFunc("/files/get/", m.handleFileDownload)

	m.server = httptest.NewServer(mux)
	return m
}

func (m *MockSlackServer) RegisterFile(fileID string, content []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.files[fileID] = append([]byte(nil), content...)
}

func (m *MockSlackServer) FileDownloadURL(fileID string) string {
	return m.server.URL + "/files/get/" + fileID
}

func (m *MockSlackServer) handleFileDownload(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/files/get/")
	m.mu.Lock()
	content, ok := m.files[id]
	if ok {
		m.record(CapturedSlackCall{Method: "files.download", RawForm: map[string]string{"file_id": id}, Body: content})
	}
	m.mu.Unlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	_, _ = w.Write(content)
}

func (m *MockSlackServer) URL() string {
	return m.server.URL + "/"
}

func (m *MockSlackServer) Calls() []CapturedSlackCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]CapturedSlackCall, len(m.calls))
	copy(out, m.calls)
	return out
}

func (m *MockSlackServer) CallsByMethod(method string) []CapturedSlackCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []CapturedSlackCall
	for _, c := range m.calls {
		if c.Method == method {
			out = append(out, c)
		}
	}
	return out
}

func (m *MockSlackServer) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = nil
	m.tsCounter = 0
	m.fileCounter = 0
}

func (m *MockSlackServer) Close() {
	if m.server != nil {
		m.server.Close()
	}
}

func (m *MockSlackServer) SetUserEmail(userID, email string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.userEmails[userID] = email
}

func (m *MockSlackServer) nextTS() string {
	m.tsCounter++
	return fmt.Sprintf("1700000000.%06d", m.tsCounter)
}

func (m *MockSlackServer) record(call CapturedSlackCall) {
	call.Timestamp = time.Now()
	m.calls = append(m.calls, call)
}

func parseForm(r *http.Request) map[string]string {
	_ = r.ParseForm()
	result := make(map[string]string)
	for k, v := range r.Form {
		if len(v) > 0 {
			result[k] = v[0]
		}
	}
	return result
}

func writeJSON(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data) //nolint:errcheck
}

func (m *MockSlackServer) handleChatPostMessage(w http.ResponseWriter, r *http.Request) {
	form := parseForm(r)
	m.mu.Lock()
	ts := m.nextTS()
	m.record(CapturedSlackCall{
		Method:     "chat.postMessage",
		Channel:    form["channel"],
		ThreadTS:   form["thread_ts"],
		Text:       form["text"],
		Blocks:     form["blocks"],
		RawForm:    form,
		ResponseTS: ts,
	})
	m.mu.Unlock()

	writeJSON(w, map[string]any{
		"ok":      true,
		"ts":      ts,
		"channel": form["channel"],
	})
}

func (m *MockSlackServer) handleChatUpdate(w http.ResponseWriter, r *http.Request) {
	form := parseForm(r)
	m.mu.Lock()
	m.record(CapturedSlackCall{
		Method:     "chat.update",
		Channel:    form["channel"],
		ThreadTS:   form["ts"],
		Text:       form["text"],
		Blocks:     form["blocks"],
		RawForm:    form,
		ResponseTS: form["ts"],
	})
	m.mu.Unlock()

	writeJSON(w, map[string]any{
		"ok":      true,
		"ts":      form["ts"],
		"channel": form["channel"],
	})
}

func (m *MockSlackServer) handleChatDelete(w http.ResponseWriter, r *http.Request) {
	form := parseForm(r)
	m.mu.Lock()
	m.record(CapturedSlackCall{
		Method:  "chat.delete",
		Channel: form["channel"],
		RawForm: form,
	})
	m.mu.Unlock()

	writeJSON(w, map[string]any{"ok": true})
}

func (m *MockSlackServer) handleFilesGetUploadURL(w http.ResponseWriter, r *http.Request) {
	form := parseForm(r)
	m.mu.Lock()
	m.fileCounter++
	fileID := fmt.Sprintf("F-mock-%d", m.fileCounter)
	uploadURL := fmt.Sprintf("%s/upload/%d", m.server.URL, m.fileCounter)
	m.record(CapturedSlackCall{
		Method:  "files.getUploadURLExternal",
		RawForm: form,
	})
	m.mu.Unlock()

	writeJSON(w, map[string]any{
		"ok":         true,
		"upload_url": uploadURL,
		"file_id":    fileID,
	})
}

func (m *MockSlackServer) handleFileUpload(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	m.mu.Lock()
	m.record(CapturedSlackCall{
		Method: "file_upload",
		Body:   body,
	})
	m.mu.Unlock()

	w.WriteHeader(http.StatusOK)
}

func (m *MockSlackServer) handleFilesCompleteUpload(w http.ResponseWriter, r *http.Request) {
	form := parseForm(r)
	m.mu.Lock()
	m.record(CapturedSlackCall{
		Method:  "files.completeUploadExternal",
		RawForm: form,
	})
	m.mu.Unlock()

	writeJSON(w, map[string]any{
		"ok": true,
		"files": []map[string]any{
			{"id": "F-mock-complete", "name": "uploaded-file"},
		},
	})
}

func (m *MockSlackServer) handleFilesInfo(w http.ResponseWriter, r *http.Request) {
	form := parseForm(r)
	m.mu.Lock()
	m.record(CapturedSlackCall{
		Method:  "files.info",
		RawForm: form,
	})
	m.mu.Unlock()

	writeJSON(w, map[string]any{
		"ok": true,
		"file": map[string]any{
			"id":   form["file"],
			"name": "mock-file",
			"shares": map[string]any{
				"public": map[string]any{
					"C-test-channel": []map[string]any{{}},
				},
			},
		},
	})
}

func (m *MockSlackServer) handleUsersProfileGet(w http.ResponseWriter, r *http.Request) {
	form := parseForm(r)
	userID := form["user"]
	m.mu.Lock()
	email := m.userEmails[userID]
	m.record(CapturedSlackCall{
		Method:  "users.profile.get",
		RawForm: form,
	})
	m.mu.Unlock()

	if email == "" {
		email = "unknown@example.com"
	}

	writeJSON(w, map[string]any{
		"ok": true,
		"profile": map[string]any{
			"email":                email,
			"real_name_normalized": "Test User",
		},
	})
}

func (m *MockSlackServer) handleUsersInfo(w http.ResponseWriter, r *http.Request) {
	form := parseForm(r)
	userID := form["user"]
	m.mu.Lock()
	email := m.userEmails[userID]
	m.record(CapturedSlackCall{
		Method:  "users.info",
		RawForm: form,
	})
	m.mu.Unlock()

	if email == "" {
		email = "unknown@example.com"
	}

	writeJSON(w, map[string]any{
		"ok": true,
		"user": map[string]any{
			"id":   userID,
			"name": "testuser",
			"profile": map[string]any{
				"email":                email,
				"real_name_normalized": "Test User",
			},
		},
	})
}

func (m *MockSlackServer) handleConversationsInfo(w http.ResponseWriter, r *http.Request) {
	form := parseForm(r)
	m.mu.Lock()
	m.record(CapturedSlackCall{
		Method:  "conversations.info",
		RawForm: form,
	})
	m.mu.Unlock()

	writeJSON(w, map[string]any{
		"ok": true,
		"channel": map[string]any{
			"id":   form["channel"],
			"name": "test-channel",
		},
	})
}

func (m *MockSlackServer) handleConversationsList(w http.ResponseWriter, r *http.Request) {
	form := parseForm(r)
	m.mu.Lock()
	m.record(CapturedSlackCall{
		Method:  "conversations.list",
		RawForm: form,
	})
	m.mu.Unlock()

	writeJSON(w, map[string]any{
		"ok":       true,
		"channels": []any{},
	})
}

func (m *MockSlackServer) handleAuthTest(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{
		"ok":      true,
		"url":     "https://test-workspace.slack.com/",
		"team":    "Test Workspace",
		"user":    "bot",
		"team_id": "T-test-team",
		"user_id": "U-bot",
		"bot_id":  "B-bot",
	})
}

func (m *MockSlackServer) handleGenericOK(w http.ResponseWriter, r *http.Request) {
	form := parseForm(r)
	m.mu.Lock()
	method := r.URL.Path
	if len(method) > 1 && method[0] == '/' {
		method = method[1:]
	}
	m.record(CapturedSlackCall{
		Method:  method,
		RawForm: form,
	})
	m.mu.Unlock()

	writeJSON(w, map[string]any{"ok": true})
}
