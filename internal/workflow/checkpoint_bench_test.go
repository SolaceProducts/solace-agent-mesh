// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	wfstore "github.com/SolaceProducts/solace-agent-mesh/internal/workflow/store"
)

func BenchmarkCheckpointWrite(b *testing.B) {
	for _, tc := range []struct {
		nodes, outputBytes int
	}{
		{4, 1 << 10},
		{20, 1 << 10},
		{20, 16 << 10},
	} {
		b.Run(fmt.Sprintf("nodes=%d/output=%dKiB", tc.nodes, tc.outputBytes>>10), func(b *testing.B) {
			db, err := wfstore.Open("sqlite:///"+filepath.Join(b.TempDir(), "bench.db"), slog.Default())
			if err != nil {
				b.Fatalf("open: %v", err)
			}
			b.Cleanup(func() { _ = db.Close() })
			if err := db.Migrate(context.Background()); err != nil {
				b.Fatalf("migrate: %v", err)
			}
			w := &WorkflowInstance{checkpointRepo: wfstore.NewRunRepo(db), logger: slog.Default()}
			state := NewExecutionState("bench", "0190a1b2-0000-7000-8000-0000000be4c0")
			output := strings.Repeat("x", tc.outputBytes)
			for i := range tc.nodes {
				id := fmt.Sprintf("n%d", i)
				state.NodeOutputs[id] = map[string]any{"output": map[string]any{"text": output}}
				state.CompletedNodes[id] = "sub-" + id
			}
			execCtx := NewExecutionContext(context.Background(), "bench-task", "ctx", nil, a2a.UserProperties{UserID: "alice"}, false)
			defer execCtx.Cancel()
			e := &DAGExecutor{cfg: &WorkflowConfig{Name: "bench"}, state: state, execCtx: execCtx, instance: w, logger: slog.Default()}

			b.ResetTimer()
			for i := range b.N {
				state.NodeOutputs["workflow"]["step"] = i
				e.writeCheckpointLocked()
			}
			b.StopTimer()
			b.ReportMetric(float64(len(e.checkpoint.last)), "snapshot-bytes")
			if e.checkpoint.last == nil {
				b.Fatal("checkpoint writes did not land")
			}
		})
	}
}
