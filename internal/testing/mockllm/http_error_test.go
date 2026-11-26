// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mockllm

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"testing"
)

func TestHTTPErrorResponseOverridesTheCompletion(t *testing.T) {
	sc, err := ParseScenario([]byte(`{
		"id": "http-error-test",
		"responses": [
			{"httpError": {"status": 500, "headers": {"Retry-After": "30"}, "body": {"error": {"message": "invalid_grant: Invalid grant: account not found"}}}}
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

	for _, stream := range []bool{false, true} {
		body := `{"model":"m","messages":[{"role":"user","content":"hello"}],"stream":` + boolStr(stream) + `}`
		resp, postErr := http.Post(base+"/v1/chat/completions", "application/json", bytes.NewBufferString(body))
		if postErr != nil {
			t.Fatalf("stream=%v: post chat: %v", stream, postErr)
		}
		got, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			t.Fatalf("stream=%v: read body: %v", stream, readErr)
		}

		if resp.StatusCode != 500 {
			t.Errorf("stream=%v: status = %d, want 500", stream, resp.StatusCode)
		}
		if got := resp.Header.Get("Retry-After"); got != "30" {
			t.Errorf("stream=%v: Retry-After = %q, want the scenario's 30", stream, got)
		}
		const want = `{"error": {"message": "invalid_grant: Invalid grant: account not found"}}`
		if string(got) != want {
			t.Errorf("stream=%v: body = %s, want %s", stream, got, want)
		}
	}
}

func TestHTTPErrorRejectsAnOutOfRangeStatus(t *testing.T) {
	_, err := PrecomputeResponse([]byte(`{"httpError": {"status": 200, "body": {}}}`), 0)
	if err == nil {
		t.Fatal("PrecomputeResponse accepted httpError.status = 200, want an error")
	}
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
