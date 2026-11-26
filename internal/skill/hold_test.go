// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package skill

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func builtinEntry(name string) map[string]any {
	return map[string]any{"tool_type": "builtin", "tool_name": name}
}

func TestManager_Hold_FirstCopyWins(t *testing.T) {
	m := NewManager()
	registrar := &countingRegistrar{}
	first := &LoadedSkill{Name: "documents", InstructionContent: "v1", Tools: []map[string]any{builtinEntry("pdf_ops")}}
	second := &LoadedSkill{Name: "documents", InstructionContent: "v2", Tools: []map[string]any{builtinEntry("pdf_ops")}}

	if held, err := m.Hold(t.Context(), "documents", first, registrar); err != nil || held != first {
		t.Fatalf("first Hold returned %+v, %v; want the copy it was given", held, err)
	}
	if held, err := m.Hold(t.Context(), "documents", second, registrar); err != nil || held != first {
		t.Fatalf("second Hold returned %+v, %v; want the copy already held", held, err)
	}
	if registrar.count() != 1 {
		t.Errorf("RegisterSkillTools calls = %d, want 1", registrar.count())
	}
	if owners := m.SkillToolOwners("pdf_ops"); !slices.Equal(owners, []string{"documents"}) {
		t.Errorf("SkillToolOwners(pdf_ops) = %v, want [documents]", owners)
	}
}

func blockFirstRegistration(registrar *countingRegistrar) (entered, release chan struct{}) {
	entered, release = make(chan struct{}), make(chan struct{})
	registrar.onRegister = func(call int) {
		if call == 1 {
			close(entered)
			<-release
		}
	}
	return entered, release
}

type holdResult struct {
	held *LoadedSkill
	err  error
}

func holdAsync(ctx context.Context, m *Manager, ls *LoadedSkill, registrar ToolRegistrar) chan holdResult {
	out := make(chan holdResult, 1)
	go func() {
		held, err := m.Hold(ctx, ls.Name, ls, registrar)
		out <- holdResult{held, err}
	}()
	return out
}

func TestManager_Hold_ASecondCallerWaitsForTheFirstRegistration(t *testing.T) {
	m := NewManager()
	registrar := &countingRegistrar{}
	entered, release := blockFirstRegistration(registrar)
	first := &LoadedSkill{Name: "documents", Tools: []map[string]any{builtinEntry("pdf_ops")}}
	second := &LoadedSkill{Name: "documents", Tools: []map[string]any{builtinEntry("pdf_ops")}}

	firstDone := holdAsync(t.Context(), m, first, registrar)
	<-entered
	secondDone := holdAsync(t.Context(), m, second, registrar)
	if m.IsHeld("documents") {
		t.Error("skill reported held before its tools were registered")
	}
	close(release)

	for _, done := range []chan holdResult{firstDone, secondDone} {
		if r := <-done; r.err != nil || r.held != first {
			t.Errorf("Hold returned %+v, %v; want the first copy", r.held, r.err)
		}
	}
	if got := registrar.count(); got != 1 {
		t.Errorf("RegisterSkillTools calls = %d, want 1", got)
	}
}

func TestManager_Hold_AWaitingCallerStopsWhenItsTaskEnds(t *testing.T) {
	m := NewManager()
	registrar := &countingRegistrar{}
	entered, release := blockFirstRegistration(registrar)
	first := &LoadedSkill{Name: "documents", Tools: []map[string]any{builtinEntry("pdf_ops")}}

	firstDone := holdAsync(t.Context(), m, first, registrar)
	<-entered
	ended, end := context.WithCancel(t.Context())
	end()
	if held, err := m.Hold(ended, "documents", &LoadedSkill{Name: "documents"}, registrar); !errors.Is(err, context.Canceled) || held != nil {
		t.Errorf("Hold for an ended task returned %+v, %v; want context.Canceled", held, err)
	}
	close(release)
	if r := <-firstDone; r.err != nil || r.held != first || !m.IsHeld("documents") {
		t.Errorf("first Hold returned %+v, %v, held=%v; want the first copy held", r.held, r.err, m.IsHeld("documents"))
	}
}

func TestManager_Hold_AFailedRegistrationLetsTheNextCallerRegister(t *testing.T) {
	m := NewManager()
	registrar := &countingRegistrar{onRegister: func(call int) {
		if call == 1 {
			panic("registration failed")
		}
	}}
	func() {
		defer func() { _ = recover() }()
		_, _ = m.Hold(t.Context(), "documents", &LoadedSkill{Name: "documents", Tools: []map[string]any{builtinEntry("pdf_ops")}}, registrar)
	}()
	if m.IsHeld("documents") {
		t.Fatal("a registration that panicked left the skill held")
	}
	second := &LoadedSkill{Name: "documents", Tools: []map[string]any{builtinEntry("pdf_ops")}}
	if held, err := m.Hold(t.Context(), "documents", second, registrar); err != nil || held != second {
		t.Errorf("Hold after a failed registration returned %+v, %v; want the new copy", held, err)
	}
}

func TestManager_AToolSeveralSkillsDeclareBelongsToEach(t *testing.T) {
	m := NewManager()
	m.RegisterSkillTool("skill-a", "get_current_time")
	m.RegisterSkillTool("skill-b", "get_current_time")
	m.RegisterSkillTool("skill-a", "get_current_time")

	if owners := m.SkillToolOwners("get_current_time"); !slices.Equal(owners, []string{"skill-a", "skill-b"}) {
		t.Errorf("owners = %v, want [skill-a skill-b]", owners)
	}
	if names := m.SkillToolNames("skill-a"); !slices.Equal(names, []string{"get_current_time"}) {
		t.Errorf("skill-a tools = %v, want one get_current_time", names)
	}
	if owners := m.SkillToolOwners("web_search"); owners != nil {
		t.Errorf("owners of a tool no skill registered = %v, want nil", owners)
	}
}

func TestManager_RetryTools_RegistersAFailedEntryOnceTheIntervalPassed(t *testing.T) {
	m := NewManager()
	clock := time.Unix(0, 0)
	m.now = func() time.Time { return clock }
	registrar := &countingRegistrar{unavailable: map[string]int{"search_docs": 1}}
	ls := &LoadedSkill{Name: "docs", Tools: []map[string]any{builtinEntry("pdf_ops"), builtinEntry("search_docs")}}
	if _, err := m.Hold(t.Context(), "docs", ls, registrar); err != nil {
		t.Fatal(err)
	}
	if !m.IsHeld("docs") || m.SkillToolOwners("search_docs") != nil {
		t.Fatalf("held=%v owners=%v; want the skill held without the failed tool", m.IsHeld("docs"), m.SkillToolOwners("search_docs"))
	}

	clock = clock.Add(toolRetryInterval - time.Second)
	m.RetryTools("docs", registrar)
	if registrar.count() != 1 {
		t.Fatalf("retried before the interval passed: %d calls", registrar.count())
	}

	clock = clock.Add(time.Second)
	if _, err := m.Hold(t.Context(), "docs", &LoadedSkill{Name: "docs"}, registrar); err != nil {
		t.Fatal(err)
	}
	m.retrying.Wait()
	if registrar.count() != 2 || !slices.Equal(registrar.lastEntries(), []string{"search_docs"}) {
		t.Errorf("calls=%d last=%v; want one retry of search_docs only", registrar.count(), registrar.lastEntries())
	}
	if owners := m.SkillToolOwners("search_docs"); !slices.Equal(owners, []string{"docs"}) {
		t.Errorf("owners(search_docs) = %v after the retry, want [docs]", owners)
	}

	clock = clock.Add(toolRetryInterval)
	m.RetryTools("docs", registrar)
	m.retrying.Wait()
	if registrar.count() != 2 {
		t.Errorf("retried a skill with no failed entries left: %d calls", registrar.count())
	}
}

func TestManager_RetryTools_OneAttemptAtATime(t *testing.T) {
	m := NewManager()
	clock := time.Unix(0, 0)
	m.now = func() time.Time { return clock }
	registrar := &countingRegistrar{unavailable: map[string]int{"search_docs": 2}}
	if _, err := m.Hold(t.Context(), "docs", &LoadedSkill{Name: "docs", Tools: []map[string]any{builtinEntry("search_docs")}}, registrar); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	registrar.setOnRegister(func(call int) {
		if call == 2 {
			close(entered)
			<-release
		}
	})
	clock = clock.Add(toolRetryInterval)
	m.RetryTools("docs", registrar)
	<-entered
	m.RetryTools("docs", registrar)
	close(release)
	m.retrying.Wait()
	if registrar.count() != 2 {
		t.Errorf("RegisterSkillTools calls = %d, want 2: a retry ran while another was running", registrar.count())
	}
}

func TestManager_Hold_DoesNotWaitForARetry(t *testing.T) {
	m := NewManager()
	clock := time.Unix(0, 0)
	m.now = func() time.Time { return clock }
	registrar := &countingRegistrar{unavailable: map[string]int{"search_docs": 2}}
	if _, err := m.Hold(t.Context(), "docs", &LoadedSkill{Name: "docs", Tools: []map[string]any{builtinEntry("search_docs")}}, registrar); err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	t.Cleanup(func() { close(release); m.retrying.Wait() })
	registrar.setOnRegister(func(int) { <-release })
	clock = clock.Add(toolRetryInterval)

	returned := make(chan struct{})
	go func() {
		_, _ = m.Hold(t.Context(), "docs", &LoadedSkill{Name: "docs"}, registrar)
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(5 * time.Second):
		t.Fatal("Hold of a held skill waited for a retry whose connect has not finished")
	}
}

func TestUnloadSkillTool_KeepsToolsForOtherSessions(t *testing.T) {
	mgr := NewManager()
	mgr.AddAvailable(&SkillInfo{Name: "documents"})
	loader := &mockSkillLoader{
		result: map[string]any{
			"instructions": "load me",
			"tools":        []any{builtinEntry("convert_file_to_markdown")},
		},
	}
	lt := NewLoadSkillTool(mgr, loader, &countingRegistrar{})

	stateA := &mockSkillState{loaded: map[string]bool{}}
	ctxA := &tool.Context{SkillState: stateA}
	if res, err := lt.Execute(t.Context(), map[string]any{"name": "documents"}, ctxA); err != nil || res.Status != "success" {
		t.Fatalf("session A load: res=%+v err=%v", res, err)
	}
	res, err := NewUnloadSkillTool().Execute(t.Context(), map[string]any{"name": "documents"}, ctxA)
	if err != nil || res.Status != "success" {
		t.Fatalf("session A unload: res=%+v err=%v", res, err)
	}
	if stateA.IsLoaded("documents") {
		t.Error("session A still lists the skill after unloading it")
	}
	if !mgr.IsHeld("documents") {
		t.Error("the agent released the skill when one session unloaded it")
	}
	if owners := mgr.SkillToolOwners("convert_file_to_markdown"); !slices.Equal(owners, []string{"documents"}) {
		t.Errorf("tool unregistered after session A's unload: owners=%v", owners)
	}
}

type countingRegistrar struct {
	mu          sync.Mutex
	calls       int
	last        []string
	onRegister  func(call int)
	unavailable map[string]int
}

func (c *countingRegistrar) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func (c *countingRegistrar) lastEntries() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.last
}

func (c *countingRegistrar) setOnRegister(fn func(call int)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onRegister = fn
}

func (c *countingRegistrar) RegisterSkillBundledTool(_ remotetool.SkillBundledTool) {}

func (c *countingRegistrar) ToolInstructions(string) string { return "" }

func (c *countingRegistrar) RegisterSkillTools(_ string, configs []map[string]any) (registered []string, retry []map[string]any) {
	c.mu.Lock()
	c.calls++
	call, onRegister := c.calls, c.onRegister
	c.last = nil
	for _, cfg := range configs {
		name, _ := cfg["tool_name"].(string)
		c.last = append(c.last, name)
		if c.unavailable[name] > 0 {
			c.unavailable[name]--
			retry = append(retry, cfg)
			continue
		}
		if tt, _ := cfg["tool_type"].(string); tt == "builtin" && name != "" {
			registered = append(registered, name)
		}
	}
	c.mu.Unlock()
	if onRegister != nil {
		onRegister(call)
	}
	return registered, retry
}
