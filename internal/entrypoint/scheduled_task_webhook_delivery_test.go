// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

type recordedRequest struct {
	method, path, apiKey, contentType string
	body                              map[string]any
}

func TestSendWebhook_DeliversThroughTheProductionClientToAnAllowedAddress(t *testing.T) {
	var mu sync.Mutex
	var got recordedRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		mu.Lock()
		got = recordedRequest{
			method: r.Method, path: r.URL.Path, apiKey: r.Header.Get("X-Api-Key"),
			contentType: r.Header.Get("Content-Type"), body: body,
		}
		mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(srv.Close)

	err := newWebhookTestService(t, srv).sendWebhook(t.Context(),
		map[string]any{"url": webhookTestURL, "headers": map[string]any{"X-Api-Key": "k1"}},
		map[string]any{"task_name": "nightly"})
	if err != nil {
		t.Fatalf("sendWebhook: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if got.method != http.MethodPost || got.path != "/hook" || got.apiKey != "k1" || got.contentType != "application/json" {
		t.Fatalf("request = %+v, want POST /hook with the custom header and a JSON body", got)
	}
	if got.body["task_name"] != "nightly" {
		t.Fatalf("body = %v, want the payload", got.body)
	}
}
