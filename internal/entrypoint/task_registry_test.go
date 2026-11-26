// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
	"github.com/SolaceProducts/solace-agent-mesh/internal/usagefacts"
)

func TestTaskRegistry_RegisterAndGet(t *testing.T) {
	r := NewMemoryTaskRegistry()

	_ = r.Register("task-1", "agent-a", "user-1", TaskIdentity{})

	if !r.IsRunning("task-1") {
		t.Error("expected task-1 to be running")
	}
	if r.AgentName("task-1") != "agent-a" {
		t.Errorf("AgentName = %q, want %q", r.AgentName("task-1"), "agent-a")
	}

	info := r.Get("task-1")
	if info == nil {
		t.Fatal("Get returned nil")
	}
	if info.AgentName != "agent-a" {
		t.Errorf("info.AgentName = %q, want %q", info.AgentName, "agent-a")
	}
	if info.Metadata != nil {
		t.Errorf("info.Metadata = %v, want nil", info.Metadata)
	}
}

func TestTaskRegistry_Count(t *testing.T) {
	r := NewMemoryTaskRegistry().(*memoryTaskRegistry)

	if got := r.Count(); got != 0 {
		t.Fatalf("empty Count = %d, want 0", got)
	}
	_ = r.Register("t1", "agent-a", "user-1", TaskIdentity{})
	_ = r.Register("t2", "agent-a", "user-1", TaskIdentity{})
	_ = r.Register("t1", "agent-a", "user-1", TaskIdentity{})
	if got := r.Count(); got != 2 {
		t.Fatalf("Count after 2 distinct registers = %d, want 2", got)
	}
	r.Remove("t1")
	if got := r.Count(); got != 1 {
		t.Fatalf("Count after remove = %d, want 1", got)
	}
}

func TestTaskRegistry_SetMetadata(t *testing.T) {
	r := NewMemoryTaskRegistry()
	_ = r.Register("task-1", "agent-a", "user-1", TaskIdentity{})

	type slackCtx struct {
		ChannelID string
		ThreadTS  string
	}
	meta := slackCtx{ChannelID: "C123", ThreadTS: "1234.5678"}
	r.SetMetadata("task-1", meta)

	info := r.Get("task-1")
	if info == nil {
		t.Fatal("Get returned nil")
	}
	got, ok := info.Metadata.(slackCtx)
	if !ok {
		t.Fatalf("Metadata type = %T, want slackCtx", info.Metadata)
	}
	if got.ChannelID != "C123" {
		t.Errorf("ChannelID = %q, want %q", got.ChannelID, "C123")
	}
}

func TestTaskRegistry_SetMetadataUntracked(t *testing.T) {
	r := NewMemoryTaskRegistry()
	r.SetMetadata("nonexistent", "data")
	if r.Get("nonexistent") != nil {
		t.Error("SetMetadata should not create a task entry")
	}
}

func TestTaskRegistry_GetReturnsCopy(t *testing.T) {
	r := NewMemoryTaskRegistry()
	_ = r.Register("task-1", "agent-a", "user-1", TaskIdentity{})

	info := r.Get("task-1")
	info.AgentName = "mutated"

	if r.AgentName("task-1") != "agent-a" {
		t.Error("Get should return a copy; mutation should not affect registry")
	}
}

func TestTaskRegistry_Remove(t *testing.T) {
	r := NewMemoryTaskRegistry()
	_ = r.Register("task-1", "agent-a", "user-1", TaskIdentity{})
	r.Remove("task-1")

	if r.IsRunning("task-1") {
		t.Error("expected task-1 to not be running after Remove")
	}
	if r.AgentName("task-1") != "" {
		t.Errorf("AgentName after Remove = %q, want empty", r.AgentName("task-1"))
	}
	if r.Get("task-1") != nil {
		t.Error("Get after Remove should return nil")
	}
}

func TestTaskRegistry_RemoveNonexistent(t *testing.T) {
	r := NewMemoryTaskRegistry()
	r.Remove("nonexistent")
}

func TestTaskRegistry_NotTracked(t *testing.T) {
	r := NewMemoryTaskRegistry()

	if r.IsRunning("nope") {
		t.Error("untracked task should not be running")
	}
	if r.AgentName("nope") != "" {
		t.Error("untracked task should return empty agent name")
	}
	if r.Get("nope") != nil {
		t.Error("untracked task should return nil")
	}
}

func TestTaskRegistry_RegisterWithTraceID(t *testing.T) {
	r := NewMemoryTaskRegistry()
	_ = r.Register("task-1", "agent-a", "user-1", TaskIdentity{TraceID: "trace-xyz"})

	info := r.Get("task-1")
	if info == nil {
		t.Fatal("Get returned nil")
	}
	if info.TraceID != "trace-xyz" {
		t.Errorf("TraceID = %q, want %q", info.TraceID, "trace-xyz")
	}
}

func TestTaskRegistry_RegisterFillsTheTraceIDLater(t *testing.T) {
	r := NewMemoryTaskRegistry()
	_ = r.Register("task-1", "agent-a", "user-1", TaskIdentity{})
	_ = r.Register("task-1", "agent-a", "user-1", TaskIdentity{TraceID: "trace-xyz"})

	if got := r.Get("task-1").TraceID; got != "trace-xyz" {
		t.Errorf("TraceID after a second register = %q, want %q", got, "trace-xyz")
	}
}

func TestTaskRegistry_RegisterDoesNotOverwriteTheTraceID(t *testing.T) {
	r := NewMemoryTaskRegistry()
	_ = r.Register("task-1", "agent-a", "user-1", TaskIdentity{TraceID: "original"})
	_ = r.Register("task-1", "agent-a", "user-1", TaskIdentity{TraceID: "different"})

	if got := r.Get("task-1").TraceID; got != "original" {
		t.Errorf("TraceID = %q, want %q (a second register must not overwrite a non-empty value)", got, "original")
	}
}

func TestTaskRegistry_RegisterWithAnEmptyIdentityChangesNothing(t *testing.T) {
	r := NewMemoryTaskRegistry()
	_ = r.Register("task-1", "agent-a", "user-1", TaskIdentity{})
	_ = r.Register("task-1", "agent-a", "user-1", TaskIdentity{})

	info := r.Get("task-1")
	if info.TraceID != "" || info.SessionID != "" || info.Trigger != "" {
		t.Errorf("an empty identity must be a no-op, got %+v", info)
	}
}

func TestTaskRegistry_RunStateIsAlwaysKnown(t *testing.T) {
	r := NewMemoryTaskRegistry()
	_ = r.Register("task-1", "agent-a", "user-1", TaskIdentity{})

	if running, known := r.RunState("task-1"); !running || !known {
		t.Errorf("tracked task: running=%v known=%v, want true, true", running, known)
	}
	if running, known := r.RunState("nope"); running || !known {
		t.Errorf("untracked task: running=%v known=%v, want false, true", running, known)
	}
}

func TestTaskRegistry_Concurrent(t *testing.T) {
	r := NewMemoryTaskRegistry()
	var wg sync.WaitGroup

	for i := range 100 {
		wg.Add(2)
		taskID := "task-" + string(rune('a'+i%26))
		go func() {
			defer wg.Done()
			_ = r.Register(taskID, "agent", "user", TaskIdentity{})
			r.SetMetadata(taskID, "meta")
			_ = r.Register(taskID, "agent-a", "user-1", TaskIdentity{SessionID: "sess"})
			_ = r.Get(taskID)
		}()
		go func() {
			defer wg.Done()
			_ = r.IsRunning(taskID)
			_ = r.AgentName(taskID)
			_ = r.AllByAgent("agent")
			r.Remove(taskID)
		}()
	}
	wg.Wait()
}

func TestTaskRegistry_SessionIsSetOnce(t *testing.T) {
	r := NewMemoryTaskRegistry()
	_ = r.Register("t1", "AgentA", "u1", TaskIdentity{})
	_ = r.Register("t1", "agent-a", "user-1", TaskIdentity{SessionID: "sessX"})
	_ = r.Register("t1", "agent-a", "user-1", TaskIdentity{SessionID: "sessZ"})
	_ = r.Register("t1", "agent-a", "user-1", TaskIdentity{SessionID: ""})

	if got := r.Get("t1").SessionID; got != "sessX" {
		t.Errorf("SessionID = %q, want sessX (the first non-empty session wins)", got)
	}
}

func TestTaskRegistry_RegisterWritesTheOriginOnce(t *testing.T) {
	r := NewMemoryTaskRegistry()
	_ = r.Register("t1", "agent-a", "user-1", TaskIdentity{})

	_ = r.Register("t1", "agent-a", "user-1", TaskIdentity{Origin: TaskOrigin{Surface: "ui"}})
	if got := r.Get("t1").TaskOrigin; got != (TaskOrigin{}) {
		t.Errorf("an origin without a trigger must not be written, got %+v", got)
	}

	first := TaskOrigin{Trigger: "interactive", AgentType: "user_created"}
	_ = r.Register("t1", "agent-a", "user-1", TaskIdentity{Origin: first})
	_ = r.Register("t1", "agent-a", "user-1", TaskIdentity{Origin: TaskOrigin{Trigger: "scheduled", SystemInitiated: true, MachineIngress: true, AgentType: "builtin"}})

	if got := r.Get("t1").TaskOrigin; got != first {
		t.Errorf("origin = %+v, want %+v", got, first)
	}

}

func TestTaskRegistry_SetRunModelSettlesTheClassificationAsAUnit(t *testing.T) {
	r := NewMemoryTaskRegistry()
	_ = r.Register("t1", "agent-a", "user-1", TaskIdentity{})

	r.SetRunModel("t1", usagefacts.Model{Family: "gpt-4o", Provider: "openai", ServedFrom: taxonomy.ModelTypeHosted})
	r.SetRunModel("t1", usagefacts.Model{Family: "claude-sonnet"})
	want := usagefacts.Model{Family: "claude-sonnet"}
	if got := r.Get("t1"); got.Model != want {
		t.Errorf("model = %+v, want %+v whole rather than components spliced", got.Model, want)
	}

	r.SetRunModel("t1", usagefacts.Model{})
	if got := r.Get("t1"); got.Model != want {
		t.Errorf("model = %+v, want an empty announcement to change nothing", got.Model)
	}

	r.SetRunModel("untracked", usagefacts.Model{Family: "gpt-4o", Provider: "openai"})
	if r.Get("untracked") != nil {
		t.Error("SetRunModel created an entry for an untracked task")
	}
}

func TestTheAnnouncedModelDoesNotDependOnArrivalOrder(t *testing.T) {
	announcements := []usagefacts.Model{
		{Family: "gpt-4o", Provider: "openai", ServedFrom: taxonomy.ModelTypeHosted},
		{Family: "claude-sonnet", Provider: "anthropic", ServedFrom: taxonomy.ModelTypeHosted},
		{Family: "gemini-flash", Provider: "gemini", ServedFrom: taxonomy.ModelTypeLocal},
	}

	settled := func(order []int) usagefacts.Model {
		r := NewMemoryTaskRegistry()
		_ = r.Register("task-1", "my-agent", "user-1", TaskIdentity{})
		for _, i := range order {
			r.SetRunModel("task-1", announcements[i])
		}
		return r.Get("task-1").Model
	}

	forward := settled([]int{0, 1, 2})
	for _, order := range [][]int{{2, 1, 0}, {1, 2, 0}, {0, 2, 1}} {
		if got := settled(order); got != forward {
			t.Errorf("order %v settled on %+v, where %v settled on %+v", order, got, []int{0, 1, 2}, forward)
		}
	}
}

func TestAPartialClassificationNeverDisplacesACompleteOne(t *testing.T) {
	r := NewMemoryTaskRegistry()
	_ = r.Register("task-1", "my-agent", "user-1", TaskIdentity{})
	complete := usagefacts.Model{Family: "claude-sonnet", Provider: "anthropic", ServedFrom: taxonomy.ModelTypeHosted}
	r.SetRunModel("task-1", complete)
	r.SetRunModel("task-1", usagefacts.Model{Provider: "openai", ServedFrom: taxonomy.ModelTypeHosted})

	if got := r.Get("task-1").Model; got != complete {
		t.Errorf("settled on %+v, want the complete classification kept intact", got)
	}
}
