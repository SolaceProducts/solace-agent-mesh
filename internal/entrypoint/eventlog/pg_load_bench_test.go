// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eventlog

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

func benchPGStore(b *testing.B) *store.DB {
	b.Helper()
	url := os.Getenv("SAM_TEST_GATEWAY_DB_URL")
	if !strings.HasPrefix(url, "postgres") {
		b.Skip("set SAM_TEST_GATEWAY_DB_URL to a postgres URL to run the #3 load harness")
	}
	db, err := store.Open(url, slog.New(slog.DiscardHandler))
	if err != nil {
		b.Fatalf("open: %v", err)
	}
	if err := db.Migrate(context.Background()); err != nil {
		b.Fatalf("migrate: %v", err)
	}
	if _, err := db.ExecContext(context.Background(), "DELETE FROM sse_event_buffer"); err != nil {
		b.Fatalf("clear: %v", err)
	}
	b.Cleanup(func() { _ = db.Close() })
	return db
}

var benchPayload = []byte(`{"jsonrpc":"2.0","id":"t","result":{"kind":"status-update","contextId":"c","status":{"state":"working","message":{"kind":"message","role":"agent","parts":[{"kind":"text","text":"a typical streaming token delta forwarded to the client during a long answer"}]}}}}`)

func eventsPerSec(b *testing.B) {
	b.ReportMetric(1e9/float64(b.Elapsed().Nanoseconds()/int64(b.N)), "events/sec")
}

func BenchmarkEventLogAppend_Postgres_Serial(b *testing.B) {
	db := benchPGStore(b)
	el := NewPersistentEventLog(store.NewSSEEventRepo(db), nil, slog.New(slog.DiscardHandler))
	ctx := context.Background()
	taskID, sessID := idgen.New(), idgen.New()

	b.ResetTimer()
	for range b.N {
		if _, err := el.Append(ctx, taskID, sessID, "user", "status_update", benchPayload); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	eventsPerSec(b)
}

func BenchmarkEventLogAppend_Postgres_Parallel(b *testing.B) {
	db := benchPGStore(b)
	el := NewPersistentEventLog(store.NewSSEEventRepo(db), nil, slog.New(slog.DiscardHandler))
	ctx := context.Background()
	var n atomic.Int64

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		_ = n.Add(1)
		taskID, sessID := idgen.New(), idgen.New()
		for pb.Next() {
			if _, err := el.Append(ctx, taskID, sessID, "user", "status_update", benchPayload); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.StopTimer()
	eventsPerSec(b)
}

func BenchmarkEventLogAppend_Postgres_Batched(b *testing.B) {
	db := benchPGStore(b)
	ctx := context.Background()
	taskID, sessID := idgen.New(), idgen.New()
	const batch = 100

	b.ResetTimer()
	inserted := 0
	for inserted < b.N {
		n := min(batch, b.N-inserted)
		var sb strings.Builder
		sb.WriteString("INSERT INTO sse_event_buffer (task_id, session_id, user_id, event_sequence, event_type, event_data, created_at) VALUES ")
		args := make([]any, 0, n*7)
		for i := range n {
			if i > 0 {
				sb.WriteByte(',')
			}
			sb.WriteString("(?, ?, ?, ?, ?, ?, ?)")
			args = append(args, taskID, sessID, "user", inserted+i+1, "status_update", string(benchPayload), int64(inserted+i))
		}
		if _, err := db.ExecContext(ctx, sb.String(), args...); err != nil {
			b.Fatal(err)
		}
		inserted += n
	}
	b.StopTimer()
	eventsPerSec(b)
}

func BenchmarkEventLogAppend_Memory_Serial(b *testing.B) {
	el := NewMemoryEventLog()
	ctx := context.Background()
	b.ResetTimer()
	for i := range b.N {
		taskID := fmt.Sprintf("mem-task-%d", i/2000)
		if _, err := el.Append(ctx, taskID, "sess", "user", "status_update", benchPayload); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	eventsPerSec(b)
}
