// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samcli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestIsAlreadyReported(t *testing.T) {
	if !IsAlreadyReported(errTaskFailed) {
		t.Error("errTaskFailed should report as already-reported")
	}
	if !IsAlreadyReported(fmt.Errorf("wrapped: %w", errTaskFailed)) {
		t.Error("a wrapped errTaskFailed should still report as already-reported")
	}
	if !IsAlreadyReported(errChangesPending) {
		t.Error("errChangesPending should report as already-reported: the plan output is the report")
	}
	if IsAlreadyReported(errors.New("some other failure")) {
		t.Error("an unrelated error must NOT be suppressed")
	}
	if IsAlreadyReported(nil) {
		t.Error("nil must not report as already-reported")
	}
}

func TestSendTask_AdoptsTheGatewaysCanonicalSessionID(t *testing.T) {
	t.Setenv("SAM_AUTH_TOKEN", "test-token")

	const canonical = "0192f0c4-6a4e-7b1e-9d3c-5f2a8b7e1c40"
	var artifactPaths []string
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/agentCards":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"name": "orchestrator"}}})
		case r.URL.Path == "/api/v1/message:stream":
			_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"id": "task-abc", "contextId": canonical}})
		case strings.HasPrefix(r.URL.Path, "/api/v1/sse/subscribe/"):
			w.Header().Set("Content-Type", "text/event-stream")
			event, _ := json.Marshal(map[string]any{"result": map[string]any{
				"kind": "status-update", "taskId": "task-abc", "final": true,
				"status": map[string]any{"message": map[string]any{"parts": []any{map[string]any{"kind": "text", "text": "done"}}}},
			}})
			_, _ = fmt.Fprintf(w, "event: status_update\ndata: %s\n\n", event)
		default:
			if strings.HasPrefix(r.URL.Path, "/api/v1/artifacts/") {
				mu.Lock()
				artifactPaths = append(artifactPaths, r.URL.Path)
				mu.Unlock()
			}
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	var out, errOut bytes.Buffer
	err := sendTask("summarize this", sendTaskOpts{
		URL: srv.URL, Agent: "orchestrator", SessionID: strings.ToUpper(canonical),
		Insecure: true, Timeout: 5 * time.Second, NoSTIM: true, Out: &out, ErrOut: &errOut,
	})
	if err != nil {
		t.Fatalf("sendTask: %v\nstderr:\n%s", err, errOut.String())
	}

	mu.Lock()
	defer mu.Unlock()
	if len(artifactPaths) == 0 {
		t.Fatal("the CLI never asked for the session's artifacts")
	}
	for _, p := range artifactPaths {
		if !strings.HasPrefix(p, "/api/v1/artifacts/"+canonical) {
			t.Errorf("artifacts requested at %q, want the canonical session id %q", p, canonical)
		}
	}
	if !strings.Contains(errOut.String(), "Session ID: "+canonical) {
		t.Errorf("run summary does not report the canonical session id:\n%s", errOut.String())
	}
}

func TestSendTask_StreamSeparation(t *testing.T) {
	t.Setenv("SAM_AUTH_TOKEN", "test-token")

	const reply = "Hello from the agent."
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/agentCards":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"name": "orchestrator"}},
			})
		case r.URL.Path == "/api/v1/message:stream":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"result": map[string]any{"id": "task-abc"},
			})
		case strings.HasPrefix(r.URL.Path, "/api/v1/sse/subscribe/"):
			w.Header().Set("Content-Type", "text/event-stream")
			event, _ := json.Marshal(map[string]any{"result": map[string]any{
				"kind":   "status-update",
				"taskId": "task-abc",
				"final":  true,
				"status": map[string]any{"message": map[string]any{
					"parts": []any{map[string]any{"kind": "text", "text": reply}},
				}},
			}})
			_, _ = fmt.Fprintf(w, "event: status_update\ndata: %s\n\n", event)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	var out, errOut bytes.Buffer
	err := sendTask("summarize this", sendTaskOpts{
		URL:      srv.URL,
		Agent:    "orchestrator",
		Insecure: true,
		Timeout:  5 * time.Second,
		NoSTIM:   true,
		Out:      &out,
		ErrOut:   &errOut,
	})
	if err != nil {
		t.Fatalf("sendTask: %v\nstdout:\n%s\nstderr:\n%s", err, out.String(), errOut.String())
	}

	if got := strings.TrimSpace(out.String()); got != reply {
		t.Errorf("stdout = %q; want only the agent reply %q", got, reply)
	}

	for _, diag := range []string{"Sending task to", "Task ID:", "Session ID:", "Task completed successfully."} {
		if strings.Contains(out.String(), diag) {
			t.Errorf("stdout leaked diagnostic %q:\n%s", diag, out.String())
		}
		if !strings.Contains(errOut.String(), diag) {
			t.Errorf("stderr missing diagnostic %q:\n%s", diag, errOut.String())
		}
	}
}
