// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func benchAdapter(b *testing.B, async bool) (*HTTPSSEAdapter, *entrypoint.GatewayCore) {
	b.Helper()
	url := os.Getenv("SAM_TEST_GATEWAY_DB_URL")
	if !strings.HasPrefix(url, "postgres") {
		b.Skip("set SAM_TEST_GATEWAY_DB_URL to a postgres URL to run the breaking-point re-test")
	}

	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	adapter := NewHTTPSSEAdapter()
	core := entrypoint.NewGatewayCore(adapter, map[string]any{
		"namespace":             "test-ns",
		"gateway_id":            "test-gw",
		"default_user_identity": "sam_dev_user",
		"async_event_log":       async,
		"session_service": map[string]any{
			"type":         "sql",
			"database_url": url,
		},
	})
	core.SetServices(runtime.NewRuntime(brk, nil, runtime.WithLogger(slog.New(slog.DiscardHandler))).Services())
	if err := core.Init(context.Background(), nil); err != nil {
		b.Fatal(err)
	}
	if db := core.Store(); db != nil {
		_, _ = db.ExecContext(context.Background(), "DELETE FROM sse_event_buffer")
	}
	b.Cleanup(func() { _ = adapter.Stop(context.Background()) })
	return adapter, core
}

var benchStatusRaw = []byte(`{"jsonrpc":"2.0","id":"t","result":{"kind":"status-update","contextId":"c","status":{"state":"working","message":{"kind":"message","role":"agent","parts":[{"kind":"text","text":"a typical streaming token delta forwarded to the client during a long answer"}]}}}}`)

func benchRun(b *testing.B, adapter *HTTPSSEAdapter) {
	const taskPool = 100
	taskIDs := make([]string, taskPool)
	for i := range taskIDs {
		taskIDs[i] = idgen.New()
		adapter.sseManager.RegisterTask(taskIDs[i], idgen.New(), "user")
	}
	ctx := context.Background()

	b.ResetTimer()
	for i := range b.N {
		adapter.OnStatusUpdate(ctx, taskIDs[i%taskPool], nil, benchStatusRaw)
	}
	b.StopTimer()
	b.ReportMetric(1e9/float64(b.Elapsed().Nanoseconds()/int64(b.N)), "events/sec")
}

func BenchmarkEventLoopAppend_Sync(b *testing.B) {
	adapter, _ := benchAdapter(b, false)
	benchRun(b, adapter)
}

func BenchmarkEventLoopAppend_Async(b *testing.B) {
	adapter, _ := benchAdapter(b, true)
	benchRun(b, adapter)
}
