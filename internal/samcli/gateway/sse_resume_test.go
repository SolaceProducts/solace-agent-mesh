// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSubscribeFromSendsLastSequence(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Query().Get("lastSequence")
		w.Header().Set("Content-Type", "text/event-stream")
	}))
	t.Cleanup(srv.Close)

	stream := NewSSEStream(&Client{BaseURL: srv.URL}, 5*time.Second)
	_ = stream.SubscribeFrom("task-1", 41, func(SSEEvent) error { return nil })

	if got != "41" {
		t.Fatalf("lastSequence = %q, want 41", got)
	}
}
