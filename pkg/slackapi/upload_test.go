// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package slackapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	goslack "github.com/slack-go/slack"
)

type uploadMockServer struct {
	mu             sync.Mutex
	methods        []string
	fileCounter    int
	uploadBytes    []byte
	uploadFilename string
	uploadStatus   int
}

func newUploadMockServer() (*uploadMockServer, *httptest.Server) {
	m := &uploadMockServer{}
	mux := http.NewServeMux()

	mux.HandleFunc("/files.getUploadURLExternal", m.handleGetUploadURL)
	mux.HandleFunc("/upload/", m.handleUpload)
	mux.HandleFunc("/files.completeUploadExternal", m.handleComplete)
	mux.HandleFunc("/files.info", m.handleFilesInfo)
	mux.HandleFunc("/auth.test", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "user_id": "U-bot", "team_id": "T-test"})
	})

	server := httptest.NewServer(mux)
	return m, server
}

func (m *uploadMockServer) record(method string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.methods = append(m.methods, method)
}

func (m *uploadMockServer) getMethods() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]string, len(m.methods))
	copy(result, m.methods)
	return result
}

func (m *uploadMockServer) handleGetUploadURL(w http.ResponseWriter, r *http.Request) {
	m.record("files.getUploadURLExternal")
	m.mu.Lock()
	m.fileCounter++
	n := m.fileCounter
	m.mu.Unlock()

	uploadURL := fmt.Sprintf("http://%s/upload/%d", r.Host, n)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":         true,
		"upload_url": uploadURL,
		"file_id":    fmt.Sprintf("F-mock-%d", n),
	})
}

func (m *uploadMockServer) handleUpload(w http.ResponseWriter, r *http.Request) {
	m.record("file_upload")

	m.mu.Lock()
	forcedStatus := m.uploadStatus
	m.mu.Unlock()
	if forcedStatus != 0 {
		w.WriteHeader(forcedStatus)
		_, _ = io.WriteString(w, "simulated slack edge failure")
		return
	}

	contentType := r.Header.Get("Content-Type")
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil || mediaType != "multipart/form-data" {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, fmt.Sprintf("expected multipart/form-data, got Content-Type=%q (parse err: %v)", contentType, err))
		return
	}
	boundary := params["boundary"]
	if boundary == "" {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, "missing multipart boundary")
		return
	}

	body, filename, parseErr := readMultipartFile(r.Body, boundary)
	if parseErr != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, parseErr.Error())
		return
	}

	m.mu.Lock()
	m.uploadBytes = body
	m.uploadFilename = filename
	m.mu.Unlock()
	w.WriteHeader(http.StatusOK)
}

func readMultipartFile(r io.Reader, boundary string) (content []byte, filename string, err error) {
	reader := multipart.NewReader(r, boundary)
	for {
		part, partErr := reader.NextPart()
		if partErr == io.EOF {
			break
		}
		if partErr != nil {
			return nil, "", fmt.Errorf("read multipart part: %w", partErr)
		}
		if part.FormName() != "file" {
			_ = part.Close()
			continue
		}
		filename = part.FileName()
		data, readErr := io.ReadAll(part)
		_ = part.Close()
		if readErr != nil {
			return nil, "", fmt.Errorf("read file part: %w", readErr)
		}
		content = data
	}
	if content == nil && filename == "" {
		return nil, "", fmt.Errorf("no \"file\" part in multipart body")
	}
	return content, filename, nil
}

func (m *uploadMockServer) setUploadStatus(status int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.uploadStatus = status
}

func (m *uploadMockServer) handleComplete(w http.ResponseWriter, r *http.Request) {
	m.record("files.completeUploadExternal")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":    true,
		"files": []map[string]any{{"id": "F-mock-1"}},
	})
}

func (m *uploadMockServer) handleFilesInfo(w http.ResponseWriter, r *http.Request) {
	m.record("files.info")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok": true,
		"file": map[string]any{
			"id": "F-mock-1",
			"shares": map[string]any{
				"public": map[string]any{
					"C-test": []map[string]any{{}},
				},
			},
		},
	})
}

func TestUploadFile_ThreeStepFlow(t *testing.T) {
	mock, server := newUploadMockServer()
	defer server.Close()

	client := goslack.New("xoxb-test", goslack.OptionAPIURL(server.URL+"/"))
	api := NewSlackAPIAdapter(client, "xoxb-test", WithDisabledRateLimiting())
	logger := slog.Default()

	content := []byte("hello world binary content")

	err := UploadFile(
		context.Background(),
		api,
		logger,
		"C-test", "1234567890.000000",
		"test.bin",
		content,
		"Here is the file",
	)
	if err != nil {
		t.Fatalf("UploadFile failed: %v", err)
	}

	methods := mock.getMethods()

	expectedSteps := []string{
		"files.getUploadURLExternal",
		"file_upload",
		"files.completeUploadExternal",
		"files.info",
	}

	if len(methods) < len(expectedSteps) {
		t.Fatalf("expected at least %d API calls, got %d: %v", len(expectedSteps), len(methods), methods)
	}

	for i, step := range expectedSteps {
		if methods[i] != step {
			t.Errorf("step %d: expected %q, got %q (full sequence: %v)", i, step, methods[i], methods)
		}
	}

	mock.mu.Lock()
	uploaded := mock.uploadBytes
	mock.mu.Unlock()

	if string(uploaded) != string(content) {
		t.Errorf("uploaded bytes mismatch: got %d bytes, want %d bytes", len(uploaded), len(content))
	}
}

func TestUploadFile_EmptyComment(t *testing.T) {
	mock, server := newUploadMockServer()
	defer server.Close()

	client := goslack.New("xoxb-test", goslack.OptionAPIURL(server.URL+"/"))
	api := NewSlackAPIAdapter(client, "xoxb-test", WithDisabledRateLimiting())
	logger := slog.Default()

	err := UploadFile(
		context.Background(),
		api,
		logger,
		"C-test", "",
		"data.csv",
		[]byte("a,b\n1,2"),
		"",
	)
	if err != nil {
		t.Fatalf("UploadFile failed: %v", err)
	}

	methods := mock.getMethods()
	if len(methods) < 3 {
		t.Fatalf("expected at least 3 API calls, got %d", len(methods))
	}
}

func TestUploadFile_LargeTextArtifact_NoTruncation(t *testing.T) {
	mock, server := newUploadMockServer()
	defer server.Close()

	client := goslack.New("xoxb-test", goslack.OptionAPIURL(server.URL+"/"))
	api := NewSlackAPIAdapter(client, "xoxb-test", WithDisabledRateLimiting())
	logger := slog.Default()

	const lineCount = 256
	var sb strings.Builder
	for i := 0; i < lineCount; i++ {
		fmt.Fprintf(&sb, "line %04d: the quick brown fox jumps over the lazy dog\n", i)
	}
	content := []byte(sb.String())
	if len(content) <= 1024 {
		t.Fatalf("test fixture too small (%d bytes); the harness requirement is >1 KiB", len(content))
	}

	err := UploadFile(
		context.Background(),
		api,
		logger,
		"C-test", "",
		"weekly-report.txt",
		content,
		"weekly report attached",
	)
	if err != nil {
		t.Fatalf("UploadFile failed: %v", err)
	}

	mock.mu.Lock()
	got := mock.uploadBytes
	gotFilename := mock.uploadFilename
	mock.mu.Unlock()

	if len(got) != len(content) {
		t.Fatalf("uploaded byte count = %d, want %d", len(got), len(content))
	}
	if string(got) != string(content) {
		t.Errorf("uploaded bytes do not match input content")
	}
	if gotFilename != "weekly-report.txt" {
		t.Errorf("filename in multipart Content-Disposition = %q, want %q", gotFilename, "weekly-report.txt")
	}
}

func TestUploadFile_StepTwoError_SurfacesExplicitError(t *testing.T) {
	mock, server := newUploadMockServer()
	defer server.Close()
	mock.setUploadStatus(http.StatusInternalServerError)

	client := goslack.New("xoxb-test", goslack.OptionAPIURL(server.URL+"/"))
	api := NewSlackAPIAdapter(client, "xoxb-test", WithDisabledRateLimiting())
	logger := slog.Default()

	err := UploadFile(
		context.Background(),
		api,
		logger,
		"C-test", "",
		"data.txt",
		[]byte("important contents that must NOT be inlined as a chat message"),
		"file attached",
	)
	if err == nil {
		t.Fatal("expected error from step-2 failure, got nil")
	}

	msg := err.Error()
	if !strings.Contains(msg, "upload to temporary URL") {
		t.Errorf("error should wrap step-2 failure, got: %s", msg)
	}
	if !strings.Contains(msg, "500") {
		t.Errorf("error should include the upstream status code, got: %s", msg)
	}

	for _, m := range mock.getMethods() {
		if m == "files.completeUploadExternal" {
			t.Errorf("step 3 (files.completeUploadExternal) ran despite step-2 failure")
		}
	}
}
