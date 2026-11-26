// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
)

func TestUnattributedAdoptedCallWarnNamesTheCauseOnlyWhenThereIsOne(t *testing.T) {
	const (
		goneLog       = "the paused task of an adopted remote tool call is gone; its audit event carries no caller"
		unreadableLog = "could not read the paused task to attribute an adopted remote tool call; its audit event carries no caller"
	)
	tests := []struct {
		name    string
		cause   error
		wantLog string
		want    map[string]any
	}{
		{name: "the task is gone", wantLog: goneLog, want: map[string]any{"traceID": "trace-1", "error": nil}},
		{name: "the read failed", cause: errors.New("store down"), wantLog: unreadableLog, want: map[string]any{"traceID": "trace-1", "error": "store down"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logs := &syncLogBuffer{}
			agent := checkpointedAgent(t, session.NewMemoryStore())
			capturingLogs(logs)(agent)

			agent.warnAdoptedCallUnattributed(&session.PendingCall{TraceID: "trace-1", TaskID: "task-1", CorrID: "corr-1"}, tt.cause)

			records := logs.records(tt.wantLog)
			if len(records) != 1 {
				t.Fatalf("records %q = %v, want exactly one", tt.wantLog, records)
			}
			got := map[string]any{"traceID": records[0]["traceID"], "error": records[0]["error"]}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("record mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
