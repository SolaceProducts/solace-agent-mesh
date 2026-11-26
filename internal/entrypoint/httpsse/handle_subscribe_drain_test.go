// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/eventlog"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

func TestSubscribe_StreamEndsWhenProxyStops(t *testing.T) {
	el := eventlog.NewMemoryEventLog()
	mgr := NewSSEManager(100, nil)
	taskID := idgen.New()
	mgr.RegisterTask(taskID, "s", "u")
	if _, err := el.Append(t.Context(), taskID, "", "", "status_update", statusPayload(1)); err != nil {
		t.Fatal(err)
	}
	sub := &subscribeHandler{sseManager: mgr, eventLog: el}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/sse/subscribe/{taskId}", sub.handleSubscribe)
	proxy := entrypoint.NewGatewayProxy("127.0.0.1:0", slog.Default())
	proxy.RegisterPrefixHandler("/", mux)
	if err := proxy.Start(t.Context()); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	frames, ended := liveFeed(ctx, t, "http://"+proxy.Addr()+"/api/v1/sse/subscribe/"+taskID)
	nextFrame(t, frames, 2*time.Second)

	start := time.Now()
	if err := proxy.Stop(t.Context()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	recvWithin(t, ended, "the stream did not end when the proxy stopped")
	if elapsed := time.Since(start); elapsed >= time.Second {
		t.Errorf("Stop took %s, want the stream to end at once instead of waiting the shutdown timeout", elapsed)
	}
}
