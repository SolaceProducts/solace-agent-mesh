// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/usagefacts"
)

func twoDBRegistries(t *testing.T) (TaskRegistry, TaskRegistry) {
	t.Helper()
	db := store.TestDB(t)
	repo := store.NewTaskRecordRepo(db, "gw-test")
	return NewDBTaskRegistry(repo, silentLogger()), NewDBTaskRegistry(repo, silentLogger())
}

func TestDBTaskRegistry_OtherInstanceSeesTheRow(t *testing.T) {
	submitting, other := twoDBRegistries(t)
	id := idgen.New()

	if err := submitting.Register(id, "agent-a", "user-1", TaskIdentity{}); err != nil {
		t.Fatal(err)
	}
	_ = submitting.Register(id, "agent-a", "user-1", TaskIdentity{TraceID: "trace-1", SessionID: "session-1"})

	if running, known := other.RunState(id); !running || !known {
		t.Fatalf("other instance: running=%v known=%v, want true, true", running, known)
	}
	if !other.IsRunning(id) {
		t.Fatal("other instance should see the task running")
	}
	if got := other.AgentName(id); got != "agent-a" {
		t.Fatalf("agent name: want agent-a, got %q", got)
	}
	info := other.Get(id)
	if info == nil || info.TraceID != "trace-1" || info.SessionID != "session-1" {
		t.Fatalf("get from other instance: %+v", info)
	}
}

func TestDBTaskRegistry_RemoveOnOtherInstanceEndsTheTask(t *testing.T) {
	submitting, other := twoDBRegistries(t)
	id := idgen.New()

	if err := submitting.Register(id, "agent-a", "user-1", TaskIdentity{TraceID: "trace-1"}); err != nil {
		t.Fatal(err)
	}
	other.Remove(id)

	if running, known := submitting.RunState(id); running || !known {
		t.Fatalf("submitting instance: running=%v known=%v, want false, true", running, known)
	}
	if submitting.IsRunning(id) {
		t.Fatal("submitting instance must not report a removed task as running")
	}
	if got := submitting.AgentName(id); got != "agent-a" {
		t.Fatalf("identity stays readable locally after removal: got %q", got)
	}
}

func TestDBTaskRegistry_MetadataStaysLocal(t *testing.T) {
	submitting, other := twoDBRegistries(t)
	id := idgen.New()

	if err := submitting.Register(id, "agent-a", "user-1", TaskIdentity{TraceID: "trace-1"}); err != nil {
		t.Fatal(err)
	}
	submitting.SetMetadata(id, "channel-42")

	if got := submitting.Metadata(id); got != "channel-42" {
		t.Fatalf("local metadata: got %v", got)
	}
	if got := other.Metadata(id); got != nil {
		t.Fatalf("metadata must not cross instances, got %v", got)
	}
	if info := other.Get(id); info == nil || info.Metadata != nil {
		t.Fatalf("get on other instance must carry no metadata: %+v", info)
	}
}

func TestDBTaskRegistry_ClaimRunReportOnce(t *testing.T) {
	submitting, other := twoDBRegistries(t)
	id := idgen.New()

	if err := submitting.Register(id, "agent-a", "user-1", TaskIdentity{TraceID: "trace-1"}); err != nil {
		t.Fatal(err)
	}
	first := other.ClaimRunReport(id)
	second := submitting.ClaimRunReport(id)

	if first == nil || first.AgentName != "agent-a" {
		t.Fatalf("first claim should win: %+v", first)
	}
	if second != nil {
		t.Fatalf("second claim must get nil, got %+v", second)
	}
}

func TestDBTaskRegistry_RegisterKeepsFirstAndAdoptsLocally(t *testing.T) {
	submitting, other := twoDBRegistries(t)
	id := idgen.New()

	if err := submitting.Register(id, "agent-a", "user-1", TaskIdentity{}); err != nil {
		t.Fatal(err)
	}
	if err := other.Register(id, "agent-b", "user-2", TaskIdentity{TraceID: "trace-x"}); err != nil {
		t.Fatal(err)
	}
	if got := other.AgentName(id); got != "agent-a" {
		t.Fatalf("second register must not overwrite the row: got %q", got)
	}
	if ids := other.AllByAgent("agent-a"); len(ids) != 1 || ids[0] != id {
		t.Fatalf("the adopting instance must list the task under the row's agent, got %v", ids)
	}
	if ids := other.AllByAgent("agent-b"); ids != nil {
		t.Fatalf("the local copy must come from the row, not the arguments, got %v", ids)
	}
	if n := other.(interface{ Count() int }).Count(); n != 1 {
		t.Fatalf("count: want 1, got %d", n)
	}
}

func TestDBTaskRegistry_SecondRegisterFillsTheLocalCopy(t *testing.T) {
	submitting, _ := twoDBRegistries(t)
	id := idgen.New()
	if err := submitting.Register(id, "agent-a", "user-1", TaskIdentity{}); err != nil {
		t.Fatal(err)
	}
	identity := TaskIdentity{TraceID: "trace-1", SessionID: "session-1", Origin: TaskOrigin{Trigger: "interactive", Surface: "slack"}}
	if err := submitting.Register(id, "agent-a", "user-1", identity); err != nil {
		t.Fatal(err)
	}
	info := submitting.Get(id)
	if info == nil || info.TraceID != "trace-1" || info.SessionID != "session-1" || info.Trigger != "interactive" || info.Surface != "slack" {
		t.Fatalf("the local copy must carry the identity the second call brought: %+v", info)
	}
	if err := submitting.Register(id, "agent-a", "user-1", TaskIdentity{TraceID: "trace-2"}); err != nil {
		t.Fatal(err)
	}
	if got := submitting.Get(id).TraceID; got != "trace-1" {
		t.Fatalf("each identity part is written once: got %q", got)
	}
}

func TestDBTaskRegistry_OriginModelAndAgentLookup(t *testing.T) {
	submitting, other := twoDBRegistries(t)
	id := idgen.New()

	if err := submitting.Register(id, "agent-a", "user-1", TaskIdentity{TraceID: "trace-1"}); err != nil {
		t.Fatal(err)
	}
	_ = submitting.Register(id, "agent-a", "user-1", TaskIdentity{Origin: TaskOrigin{Trigger: "interactive", Surface: "ui"}})
	_ = submitting.Register(id, "agent-a", "user-1", TaskIdentity{Origin: TaskOrigin{Trigger: "scheduled"}})
	submitting.SetRunModel(id, usagefacts.Model{Family: "claude-opus", Provider: "anthropic", ServedFrom: "hosted"})
	submitting.SetRunModel(id, usagefacts.Model{})

	info := other.Get(id)
	if info == nil || info.Trigger != "interactive" || info.Surface != "ui" {
		t.Fatalf("origin from the other instance: %+v", info)
	}
	claimed := other.ClaimRunReport(id)
	if claimed == nil || claimed.Model.Family != "claude-opus" {
		t.Fatalf("model must be settled on the row: %+v", claimed)
	}
	if ids := submitting.AllByAgent("agent-a"); len(ids) != 1 || ids[0] != id {
		t.Fatalf("all by agent on the submitting instance: %v", ids)
	}
	if ids := other.AllByAgent("agent-a"); ids != nil {
		t.Fatalf("all by agent must cover only this instance's tasks, got %v", ids)
	}
}

func TestDBTaskRegistry_ClaimRunReportFallsBackToTheLocalOrigin(t *testing.T) {
	submitting, _ := twoDBRegistries(t)
	id := idgen.New()

	if err := submitting.Register(id, "agent-a", "user-1", TaskIdentity{TraceID: "trace-1"}); err != nil {
		t.Fatal(err)
	}
	reg := submitting.(*dbTaskRegistry)
	reg.mu.Lock()
	reg.own[id].TaskOrigin = TaskOrigin{Trigger: "interactive", AgentType: "user_created"}
	reg.mu.Unlock()

	claimed := submitting.ClaimRunReport(id)
	if claimed == nil || claimed.Trigger != "interactive" || claimed.AgentType != "user_created" {
		t.Fatalf("origin must come from the local copy when the row has none: %+v", claimed)
	}
}

func TestDBTaskRegistry_DatabaseDownDegradesToEmptyAnswers(t *testing.T) {
	db := store.TestDB(t)
	reg := NewDBTaskRegistry(store.NewTaskRecordRepo(db, "gw-test"), silentLogger()).(*dbTaskRegistry)
	id, unknown := idgen.New(), idgen.New()
	if err := reg.Register(id, "agent-a", "user-1", TaskIdentity{TraceID: "trace-1"}); err != nil {
		t.Fatal(err)
	}
	reg.SetMetadata(id, "meta")
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	if err := reg.Register(idgen.New(), "agent-a", "user-1", TaskIdentity{}); err == nil {
		t.Fatal("Register must refuse when the database is unreachable")
	}
	_ = reg.Register(id, "agent-a", "user-1", TaskIdentity{SessionID: "session-x", Origin: TaskOrigin{Trigger: "interactive"}})
	reg.SetRunModel(id, usagefacts.Model{Family: "llama"})

	if running, known := reg.RunState(id); running || known {
		t.Errorf("RunState must report unknown, not guess: running=%v known=%v", running, known)
	}
	if !reg.IsRunning(id) {
		t.Error("IsRunning must report true when the row cannot be read, so a timeout is not skipped")
	}
	if reg.ClaimRunReport(id) != nil {
		t.Error("ClaimRunReport must yield nothing when the row cannot be read")
	}
	if got := reg.Get(id); got == nil || got.AgentName != "agent-a" || got.Metadata != "meta" {
		t.Errorf("this instance's own identity and metadata stay readable: %+v", got)
	}
	if reg.Get(unknown) != nil {
		t.Error("an unknown task must read as absent")
	}
	if n := reg.Count(); n != 1 {
		t.Errorf("Count answers from local state, got %d", n)
	}
	if ids := reg.AllByAgent("agent-a"); len(ids) != 1 {
		t.Errorf("AllByAgent answers from local state, got %v", ids)
	}
	reg.sweepLive(t.Context())
	if reg.Get(id) == nil {
		t.Error("a failed liveness sweep must keep every local entry, or an outage costs live tasks their rows")
	}
	reg.Remove(id)
	if reg.Metadata(id) != nil {
		t.Error("Remove must still clear local state")
	}
}

func TestDBTaskRegistry_SweepDropsLocalEntriesOfTasksEndedElsewhere(t *testing.T) {
	submitting, other := twoDBRegistries(t)
	ended, live, metadataOnly := idgen.New(), idgen.New(), idgen.New()

	for _, id := range []string{ended, live} {
		if err := submitting.Register(id, "agent-a", "user-1", TaskIdentity{TraceID: "trace-1"}); err != nil {
			t.Fatal(err)
		}
		submitting.SetMetadata(id, "channel-"+id)
	}
	if err := other.Register(metadataOnly, "agent-a", "user-1", TaskIdentity{TraceID: "trace-1"}); err != nil {
		t.Fatal(err)
	}
	submitting.SetMetadata(metadataOnly, "channel-adopted")
	other.Remove(ended)
	other.Remove(metadataOnly)

	submitting.(*dbTaskRegistry).sweepLive(t.Context())

	if submitting.Get(ended) != nil || submitting.Metadata(ended) != nil {
		t.Error("the entry of a task ended elsewhere must be dropped")
	}
	if submitting.Metadata(metadataOnly) != nil {
		t.Error("metadata attached to a task ended elsewhere must be dropped")
	}
	if info := submitting.Get(live); info == nil || info.Metadata != "channel-"+live {
		t.Errorf("a live task keeps its entry and metadata: %+v", info)
	}
	if n := submitting.(interface{ Count() int }).Count(); n != 1 {
		t.Errorf("count after sweep: want 1, got %d", n)
	}
}

func TestDBTaskRegistry_SweepReportsTasksEndedElsewhere(t *testing.T) {
	submitting, other := twoDBRegistries(t)
	reg := submitting.(*dbTaskRegistry)
	var reported []string
	reg.onEndedElsewhere = func(ids []string) {
		if reg.mu.TryLock() {
			reg.mu.Unlock()
		} else {
			t.Error("onEndedElsewhere must run outside the registry lock")
		}
		reported = append(reported, ids...)
	}
	endedElsewhere, endedHere, noMetadata, live := idgen.New(), idgen.New(), idgen.New(), idgen.New()
	for _, id := range []string{endedElsewhere, endedHere, noMetadata, live} {
		if err := submitting.Register(id, "agent-a", "user-1", TaskIdentity{TraceID: "trace-1"}); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{endedElsewhere, endedHere, live} {
		submitting.SetMetadata(id, "channel-"+id)
	}
	other.Remove(endedElsewhere)
	other.Remove(noMetadata)
	submitting.Remove(endedHere)

	reg.sweepLive(t.Context())
	reg.sweepLive(t.Context())

	if len(reported) != 1 || reported[0] != endedElsewhere {
		t.Fatalf("reported %v, want only %s once", reported, endedElsewhere)
	}
}

type endedElsewhereAdapter struct {
	LoggingAdapter
	ended []string
}

func (a *endedElsewhereAdapter) OnTaskEndedElsewhere(taskID string) {
	a.ended = append(a.ended, taskID)
}

func TestGatewayCore_PassesTasksEndedElsewhereToTheAdapter(t *testing.T) {
	adapter := &endedElsewhereAdapter{}
	NewGatewayCore(adapter, map[string]any{}).tasksEndedElsewhere([]string{"t1", "t2"})
	if len(adapter.ended) != 2 || adapter.ended[0] != "t1" || adapter.ended[1] != "t2" {
		t.Fatalf("adapter got %v, want [t1 t2]", adapter.ended)
	}
	NewGatewayCore(&LoggingAdapter{}, map[string]any{}).tasksEndedElsewhere([]string{"t1"})
}

func TestDBTaskRegistry_ARemovedTaskStopsBeingStamped(t *testing.T) {
	submitting, _ := twoDBRegistries(t)
	reg := submitting.(*dbTaskRegistry)
	kept, dropped := idgen.New(), idgen.New()
	for _, id := range []string{kept, dropped} {
		if err := submitting.Register(id, "agent-a", "user-1", TaskIdentity{TraceID: "trace-1"}); err != nil {
			t.Fatal(err)
		}
	}
	stampOf := func(id string) int64 {
		t.Helper()
		rec, err := reg.repo.Get(t.Context(), id)
		if err != nil || rec == nil {
			t.Fatalf("read %s: %v", id, err)
		}
		return rec.LastSeenAt
	}
	beforeKept, beforeDropped := stampOf(kept), stampOf(dropped)

	reg.mu.Lock()
	delete(reg.own, dropped)
	reg.mu.Unlock()
	time.Sleep(3 * time.Millisecond)

	reg.sweepLive(t.Context())

	if stampOf(kept) <= beforeKept {
		t.Error("a task this instance still holds must be restamped")
	}
	if got := stampOf(dropped); got != beforeDropped {
		t.Errorf("a task this instance no longer holds must not be restamped, or the reaper never takes it: was %d, now %d", beforeDropped, got)
	}
}

func TestDBTaskRegistry_MetadataOnlyTasksAreNotRestamped(t *testing.T) {
	holder, other := twoDBRegistries(t)
	id := idgen.New()
	if err := holder.Register(id, "agent-a", "user-1", TaskIdentity{TraceID: "trace-1"}); err != nil {
		t.Fatal(err)
	}
	other.SetMetadata(id, "channel-7")
	if other.Metadata(id) == nil {
		t.Fatal("setup: the other instance must hold metadata for the task")
	}
	reg := other.(*dbTaskRegistry)
	stampOf := func() int64 {
		t.Helper()
		rec, err := reg.repo.Get(t.Context(), id)
		if err != nil || rec == nil {
			t.Fatalf("read %s: %v", id, err)
		}
		return rec.LastSeenAt
	}
	before := stampOf()
	time.Sleep(3 * time.Millisecond)

	reg.sweepLive(t.Context())

	if got := stampOf(); got != before {
		t.Errorf("an instance holding only metadata restamped the row: was %d, now %d", before, got)
	}
	if other.Metadata(id) == nil {
		t.Error("metadata of a task whose row still exists must be kept")
	}
}
