// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mockllm

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"testing"
)

func TestCaptureEndpoints(t *testing.T) {
	sc, err := ParseScenario([]byte(`{
		"id": "capture-test",
		"responses": [
			{"choices": [{"index": 0, "message": {"role": "assistant", "content": "ok"}, "finish_reason": "stop"}]}
		]
	}`))
	if err != nil {
		t.Fatalf("parse scenario: %v", err)
	}

	srv := NewMockLLMServer("127.0.0.1:0", 0, 0, slog.Default())
	srv.LoadScenarios(sc)
	if startErr := srv.Start(); startErr != nil {
		t.Fatalf("start server: %v", startErr)
	}
	defer srv.Stop()

	base := "http://" + srv.Addr()

	if got := fetchRequests(t, base); got.Count != 0 {
		t.Fatalf("initial capture should be empty, got count=%d", got.Count)
	}

	chatBody := `{"model":"m","messages":[{"role":"user","content":"hello"}]}`
	chatResp, err := http.Post(base+"/v1/chat/completions", "application/json", bytes.NewBufferString(chatBody))
	if err != nil {
		t.Fatalf("post chat: %v", err)
	}
	_, _ = io.Copy(io.Discard, chatResp.Body)
	_ = chatResp.Body.Close()

	got := fetchRequests(t, base)
	if got.Count != 1 {
		t.Fatalf("after one chat call, expected count=1, got %d", got.Count)
	}
	if len(got.Requests) != 1 || !json.Valid(got.Requests[0]) {
		t.Fatalf("captured request should be valid JSON, got %s", string(got.Requests[0]))
	}

	badResp, err := http.Post(base+"/v1/chat/completions", "application/json", bytes.NewBufferString(`{not-json`))
	if err != nil {
		t.Fatalf("post malformed: %v", err)
	}
	_, _ = io.Copy(io.Discard, badResp.Body)
	_ = badResp.Body.Close()

	got = fetchRequests(t, base)
	if got.Count != 2 {
		t.Fatalf("after malformed call, expected count=2, got %d", got.Count)
	}
	if string(got.Requests[1]) != "null" {
		t.Fatalf("malformed request body should round-trip as null, got %s", string(got.Requests[1]))
	}

	resetReq, _ := http.NewRequest(http.MethodPost, base+"/__requests/reset", nil)
	resetResp, err := http.DefaultClient.Do(resetReq)
	if err != nil {
		t.Fatalf("reset: %v", err)
	}
	_ = resetResp.Body.Close()
	if resetResp.StatusCode != http.StatusNoContent {
		t.Fatalf("reset status: want 204, got %d", resetResp.StatusCode)
	}
	if got := fetchRequests(t, base); got.Count != 0 {
		t.Fatalf("after reset, expected count=0, got %d", got.Count)
	}

	badGetReset, err := http.Get(base + "/__requests/reset")
	if err != nil {
		t.Fatalf("get reset: %v", err)
	}
	_ = badGetReset.Body.Close()
	if badGetReset.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET /__requests/reset: want 405, got %d", badGetReset.StatusCode)
	}

	badPostReq, _ := http.NewRequest(http.MethodPost, base+"/__requests", nil)
	badPostResp, err := http.DefaultClient.Do(badPostReq)
	if err != nil {
		t.Fatalf("post __requests: %v", err)
	}
	_ = badPostResp.Body.Close()
	if badPostResp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST /__requests: want 405, got %d", badPostResp.StatusCode)
	}
}

type capturedEnvelope struct {
	Count    int               `json:"count"`
	Requests []json.RawMessage `json:"requests"`
}

func fetchRequests(t *testing.T, base string) capturedEnvelope {
	t.Helper()
	resp, err := http.Get(base + "/__requests")
	if err != nil {
		t.Fatalf("get __requests: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /__requests: want 200, got %d", resp.StatusCode)
	}
	var env capturedEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return env
}

func TestCaptureLimit(t *testing.T) {
	srv := NewMockLLMServer("127.0.0.1:0", 0, 0, slog.Default())
	srv.captureLimit = 3

	for i := range 5 {
		srv.captureRequest([]byte(`{"i":` + string(rune('0'+i)) + `}`))
	}

	got := srv.CapturedRequests()
	if len(got) != 3 {
		t.Fatalf("expected 3 entries after overflow, got %d", len(got))
	}
	for i, want := range []string{`{"i":2}`, `{"i":3}`, `{"i":4}`} {
		if string(got[i]) != want {
			t.Fatalf("entry %d: want %s, got %s", i, want, string(got[i]))
		}
	}
}
