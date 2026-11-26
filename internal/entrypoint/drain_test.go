// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestGatewayProxy_StopEndsStreamsAtOnce(t *testing.T) {
	done := make(chan struct{}, 1)
	proxy := NewGatewayProxy("127.0.0.1:0", slog.Default())
	proxy.timeouts.ShutdownTimeout = 2 * time.Second
	proxy.RegisterPrefixHandler("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := StreamContext(r)
		defer cancel()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: hello\n\n")
		w.(http.Flusher).Flush()
		<-ctx.Done()
		done <- struct{}{}
	}))
	if err := proxy.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	openStream(t, http.DefaultClient, fmt.Sprintf("http://%s/stream", proxy.Addr()))

	start := time.Now()
	if err := proxy.Stop(t.Context()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if elapsed := time.Since(start); elapsed >= proxy.timeouts.ShutdownTimeout {
		t.Errorf("Stop took %s, want the stream to end before the %s shutdown timeout", elapsed, proxy.timeouts.ShutdownTimeout)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("stream handler still running after Stop")
	}
}

func TestStreamContext_WithoutDrainSignalEndsWithCancel(t *testing.T) {
	ctx, cancel := StreamContext(httptest.NewRequest(http.MethodGet, "/stream", nil))
	if ctx.Err() != nil {
		t.Fatal("context ended before cancel")
	}
	cancel()
	if ctx.Err() == nil {
		t.Fatal("context still live after cancel")
	}
}
