// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/eventlog"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

type publishedRecord struct {
	taskID string
	rec    entrypoint.StreamRecord
}

type recordingPublisher struct {
	mu   sync.Mutex
	recs []publishedRecord
}

func (p *recordingPublisher) PublishTaskStreamRecord(taskID string, rec entrypoint.StreamRecord) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.recs = append(p.recs, publishedRecord{taskID, rec})
}

func (p *recordingPublisher) published() []publishedRecord {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]publishedRecord(nil), p.recs...)
}

func TestInitEventLog_AnnouncesStoredRows(t *testing.T) {
	adapter, core, _, _, _ := setupTestAdapterWithDB(t)
	pub := &recordingPublisher{}
	adapter.streamPub = pub
	adapter.initEventLog(core)
	if pel, ok := adapter.eventLog.(*eventlog.PersistentEventLog); ok {
		t.Cleanup(pel.Stop)
	}
	taskID, sessionID := idgen.New(), idgen.New()
	adapter.sseManager.RegisterTask(taskID, sessionID, "user-1")

	adapter.OnStatusUpdate(t.Context(), taskID, nil, statusPayload(1))

	waitFor(t, "the stored row was never announced", func() bool { return len(pub.published()) == 1 })
	got := pub.published()[0]
	if got.taskID != taskID || got.rec.Sequence != 1 || got.rec.EventType != "status_update" || len(got.rec.Data) != 0 {
		t.Fatalf("announced %+v, want sequence 1, status_update, no data", got)
	}
}

func TestOnActivityEvent_PublishesForATaskWithNoEntry(t *testing.T) {
	adapter, _, _ := setupTestAdapter(t)
	pub := &recordingPublisher{}
	adapter.streamPub = pub
	taskID := idgen.New()

	adapter.OnActivityEvent(t.Context(), taskID, "test/a2a/v1/agent/status/AgentA/"+taskID, statusUpdateFixture(taskID, "AgentA"))

	got := pub.published()
	if len(got) != 1 || got[0].taskID != taskID || got[0].rec.Sequence != 0 || got[0].rec.EventType != "activity_event" || len(got[0].rec.Data) == 0 {
		t.Fatalf("published %+v, want one activity frame with its data and no sequence", got)
	}
	if adapter.sseManager.HasTask(taskID) {
		t.Fatal("publishing the frame created an entry")
	}
}
