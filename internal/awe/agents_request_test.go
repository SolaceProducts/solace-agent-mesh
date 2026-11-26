// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package awe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
)

func agentPayload(name string) map[string]any {
	return map[string]any{"kind": "agent", "name": name}
}

func running(exe *Executor) map[string]string {
	out := map[string]string{}
	exe.mu.RLock()
	defer exe.mu.RUnlock()
	for _, mi := range exe.instances {
		if mi.getStatus() == StatusRunning {
			out[mi.inst.Name()] = mi.deploymentID
		}
	}
	return out
}

func reported(exe *Executor) map[string]control.AgentStatus {
	out := map[string]control.AgentStatus{}
	for _, s := range exe.platform.report() {
		out[s.Name] = s
	}
	return out
}

type removals struct {
	mu   sync.Mutex
	seen []string
}

func (r *removals) add(what string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen = append(r.seen, what)
}

func (r *removals) sorted() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := slices.Clone(r.seen)
	slices.Sort(out)
	return out
}

type removalRecordingInstance struct {
	*mockInstance
	removals *removals
}

func (r *removalRecordingInstance) Remove(ctx context.Context) error {
	r.removals.add("remove " + r.name)
	return r.mockInstance.Stop(ctx)
}

func (r *removalRecordingInstance) StopForReplace(ctx context.Context) error {
	r.removals.add("replace " + r.name)
	return r.mockInstance.Stop(ctx)
}

func (r *removalRecordingInstance) Stop(ctx context.Context) error {
	r.removals.add("stop " + r.name)
	return r.mockInstance.Stop(ctx)
}

func TestPlatformAgentsFollowTheListedDeployments(t *testing.T) {
	ctx := t.Context()
	exe := NewExecutor(newTestServices())
	recorded := &removals{}
	exe.RegisterKind("agent", func(cfg config.Config) (Instance, error) {
		return &removalRecordingInstance{mockInstance: newMockInstance(cfg.GetString("name"), "agent"), removals: recorded}, nil
	})
	if _, err := exe.AddAndStart(ctx, config.FromMap(agentPayload("from_yaml"))); err != nil {
		t.Fatalf("start the YAML agent: %v", err)
	}
	steps := []struct {
		name        string
		desired     []control.AgentApply
		wantRunning map[string]string
		wantReport  map[string]control.AgentStatus
	}{
		{
			name:        "two agents deployed",
			desired:     []control.AgentApply{{Name: "a", DeploymentID: "d1", Payloads: []map[string]any{agentPayload("a")}}, {Name: "b", DeploymentID: "d1", Payloads: []map[string]any{agentPayload("b")}}},
			wantRunning: map[string]string{"from_yaml": "", "a": "d1", "b": "d1"},
			wantReport:  map[string]control.AgentStatus{"a": {Name: "a", DeploymentID: "d1"}, "b": {Name: "b", DeploymentID: "d1"}},
		},
		{
			name:        "a changed and b no longer listed",
			desired:     []control.AgentApply{{Name: "a", DeploymentID: "d2", Payloads: []map[string]any{agentPayload("a")}}},
			wantRunning: map[string]string{"from_yaml": "", "a": "d2"},
			wantReport:  map[string]control.AgentStatus{"a": {Name: "a", DeploymentID: "d2"}},
		},
		{
			name:        "a listed with a config that cannot be built",
			desired:     []control.AgentApply{{Name: "a", DeploymentID: "d3"}},
			wantRunning: map[string]string{"from_yaml": "", "a": "d2"},
			wantReport:  map[string]control.AgentStatus{"a": {Name: "a", DeploymentID: "d2"}},
		},
		{
			name:        "a undeployed",
			desired:     []control.AgentApply{{Name: "a", DeploymentID: "u1", Undeploy: true}},
			wantRunning: map[string]string{"from_yaml": ""},
			wantReport:  map[string]control.AgentStatus{"a": {Name: "a", DeploymentID: "u1"}},
		},
	}
	for _, step := range steps {
		exe.applyPlatformAgents(ctx, step.desired).Wait()
		if diff := cmp.Diff(step.wantRunning, running(exe)); diff != "" {
			t.Fatalf("%s: running (-want +got):\n%s", step.name, diff)
		}
		if diff := cmp.Diff(step.wantReport, reported(exe)); diff != "" {
			t.Fatalf("%s: report (-want +got):\n%s", step.name, diff)
		}
	}
	want := []string{"replace a", "stop a", "stop b"}
	if got := recorded.sorted(); !slices.Equal(got, want) {
		t.Fatalf("removals %v, want %v: a changed agent is replaced, an agent no longer deployed is stopped with its tasks ended, and only the leader deletes a queue", got, want)
	}
}

func TestAnUndeployOfAnAgentThisAWEDoesNotRunTouchesNoQueue(t *testing.T) {
	ctx := t.Context()
	svc, brk := newTrackingServices(t)
	exe := NewExecutor(svc)

	exe.applyPlatformAgents(ctx, []control.AgentApply{{Name: "agent_gone", DeploymentID: "u1", Undeploy: true}}).Wait()

	if got := brk.deprovisionedNames(); len(got) != 0 {
		t.Fatalf("deprovisioned %v, want no queue touched", got)
	}
	if diff := cmp.Diff(map[string]control.AgentStatus{"agent_gone": {Name: "agent_gone", DeploymentID: "u1"}}, reported(exe)); diff != "" {
		t.Fatalf("report (-want +got):\n%s", diff)
	}
}

func TestPlatformAgentStartFailureIsReportedAndRetriedWhenDue(t *testing.T) {
	ctx := t.Context()
	exe := NewExecutor(newTestServices())
	var starts atomic.Int32
	exe.RegisterKind("agent", func(cfg config.Config) (Instance, error) {
		inst := newMockInstance(cfg.GetString("name"), "agent")
		if starts.Add(1) == 1 {
			inst.startErr = errors.New("queue create refused")
		}
		return inst, nil
	})
	desired := []control.AgentApply{{Name: "a", DeploymentID: "d1", Payloads: []map[string]any{agentPayload("a")}}}

	exe.applyPlatformAgents(ctx, desired).Wait()
	got := reported(exe)["a"]
	if got.DeploymentID != "d1" || got.Error == "" {
		t.Fatalf("report after a failed start = %+v, want d1 with the error", got)
	}
	exe.applyPlatformAgents(ctx, desired).Wait()
	if n := starts.Load(); n != 1 {
		t.Fatalf("%d starts before the retry is due, want 1", n)
	}

	exe.platform.mu.Lock()
	failed := exe.platform.applied["a"]
	failed.retryAt = time.Now().Add(-time.Second)
	exe.platform.applied["a"] = failed
	exe.platform.mu.Unlock()
	exe.applyPlatformAgents(ctx, desired).Wait()
	if diff := cmp.Diff(map[string]control.AgentStatus{"a": {Name: "a", DeploymentID: "d1"}}, reported(exe)); diff != "" {
		t.Fatalf("report after the due retry (-want +got):\n%s", diff)
	}
	if got := running(exe)["a"]; got != "d1" {
		t.Fatalf("after the retry a runs %q, want d1", got)
	}
}

func TestAFailedAgentListedWithoutAConfigWaitsForItsNextRetry(t *testing.T) {
	ctx := t.Context()
	exe := NewExecutor(newTestServices())
	exe.RegisterKind("agent", func(cfg config.Config) (Instance, error) {
		inst := newMockInstance(cfg.GetString("name"), "agent")
		inst.startErr = errors.New("queue create refused")
		return inst, nil
	})
	exe.applyPlatformAgents(ctx, []control.AgentApply{{Name: "a", DeploymentID: "d2", Payloads: []map[string]any{agentPayload("a")}}}).Wait()
	exe.platform.mu.Lock()
	failed := exe.platform.applied["a"]
	failed.retryAt = time.Now().Add(-time.Second)
	exe.platform.applied["a"] = failed
	exe.platform.mu.Unlock()

	exe.applyPlatformAgents(ctx, []control.AgentApply{{Name: "a", DeploymentID: "d3"}}).Wait()
	if exe.platform.anyRetryDue(time.Now()) {
		t.Fatal("a retry is still due after the GWE listed the agent without a config, so the AWE asks every 5 s")
	}
	if !exe.platform.anyRetryDue(time.Now().Add(time.Hour)) {
		t.Fatal("the failed agent is never retried again")
	}
}

func TestAgentRetryDelayDoublesUpToTheCap(t *testing.T) {
	cases := []struct {
		failures int
		base     time.Duration
	}{
		{1, agentRetryFirst},
		{2, 2 * agentRetryFirst},
		{4, 8 * agentRetryFirst},
		{8, 128 * agentRetryFirst},
		{9, agentRetryMax},
		{19, agentRetryMax},
		{20, agentRetryMax},
		{1000, agentRetryMax},
	}
	for _, tc := range cases {
		for range 50 {
			got := agentRetryDelay(tc.failures)
			low := time.Duration(float64(tc.base) * (1 - agentRetryJitter))
			high := time.Duration(float64(tc.base) * (1 + agentRetryJitter))
			if got < low || got > high {
				t.Fatalf("agentRetryDelay(%d) = %s, want %s within %.0f%%", tc.failures, got, tc.base, agentRetryJitter*100)
			}
		}
	}
}

func TestAnAgentAnotherWriterStartedAfterAFailedStartIsReportedAsApplied(t *testing.T) {
	ctx := t.Context()
	exe := NewExecutor(newTestServices())
	var starts atomic.Int32
	exe.RegisterKind("agent", func(cfg config.Config) (Instance, error) {
		inst := newMockInstance(cfg.GetString("name"), "agent")
		if starts.Add(1) == 1 {
			inst.startErr = errors.New("instance already exists")
		}
		return inst, nil
	})
	desired := []control.AgentApply{{Name: "a", DeploymentID: "d1", Payloads: []map[string]any{agentPayload("a")}}}
	exe.applyPlatformAgents(ctx, desired).Wait()
	if got := reported(exe)["a"]; got.Error == "" {
		t.Fatalf("report after the lost start = %+v, want the error", got)
	}

	body, _ := json.Marshal(map[string]any{"kind": "agent", "name": "a", control.DeploymentIDField: "d1"})
	if _, err := exe.handleAddInstance(ctx, body); err != nil {
		t.Fatalf("start by the other writer: %v", err)
	}
	exe.applyPlatformAgents(ctx, desired).Wait()
	if diff := cmp.Diff(map[string]control.AgentStatus{"a": {Name: "a", DeploymentID: "d1"}}, reported(exe)); diff != "" {
		t.Fatalf("report once d1 runs (-want +got):\n%s", diff)
	}
	if n := starts.Load(); n != 2 {
		t.Fatalf("%d starts, want 2: the follower starts nothing once d1 runs", n)
	}
}

func TestAWEAsksForItsAgentsUntilAGWEAnswersAndIsReadyAfter(t *testing.T) {
	ctx := t.Context()
	svc, brk := newTrackingServices(t)
	exe := NewExecutor(svc)
	exe.RegisterKind("agent", func(cfg config.Config) (Instance, error) {
		return newMockInstance(cfg.GetString("name"), "agent"), nil
	})

	gweQueue, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("create the GWE queue: %v", err)
	}
	if err := brk.Subscribe(gweQueue, a2a.AWEAgentsRequestTopic("ns")); err != nil {
		t.Fatalf("subscribe the GWE queue to AWE requests: %v", err)
	}
	firstAsks := make(chan string, 8)
	var asks atomic.Int32
	go func() {
		for {
			msg, err := brk.Receive(ctx, gweQueue)
			if err != nil {
				return
			}
			firstAsks <- msg.UserProperties[control.FirstAskProperty]
			payload := []byte(`{}`)
			if asks.Add(1) > 1 {
				payload, _ = json.Marshal(control.AgentsReply{Agents: []control.AgentApply{{Name: "a", DeploymentID: "d1", Payloads: []map[string]any{agentPayload("a")}}}})
			}
			_ = brk.Publish(ctx, msg.ReplyTo, &broker.Message{Payload: payload, CorrelationID: msg.CorrelationID})
		}
	}()

	exe.FollowPlatformAgents(ctx, "ns")
	t.Cleanup(func() { exe.stopFollowingPlatform(context.Background()) })
	if exe.ReadinessOpen() {
		t.Fatal("ready before any GWE answered")
	}
	deadline := time.Now().Add(3 * agentsAskInterval)
	for !exe.ReadinessOpen() {
		if time.Now().After(deadline) {
			t.Fatal("not ready after a GWE answered done")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := running(exe)["a"]; got != "d1" {
		t.Fatalf("after the answer a runs %q, want d1", got)
	}
	if got := []string{<-firstAsks, <-firstAsks}; !slices.Equal(got, []string{"true", "true"}) {
		t.Fatalf("first-ask marks %v, want both asks before the answer marked first", got)
	}
}

func TestAnAgentChangeCutsShortAFirstAskThatGotNoAnswer(t *testing.T) {
	ctx := t.Context()
	svc, brk := newTrackingServices(t)
	exe := NewExecutor(svc)
	exe.RegisterKind("agent", func(cfg config.Config) (Instance, error) {
		return newMockInstance(cfg.GetString("name"), "agent"), nil
	})
	gweQueue, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("create the GWE queue: %v", err)
	}
	if err := brk.Subscribe(gweQueue, a2a.AWEAgentsRequestTopic("ns")); err != nil {
		t.Fatalf("subscribe the GWE queue to AWE requests: %v", err)
	}
	firstAskDropped := make(chan struct{})
	go func() {
		for asks := 1; ; asks++ {
			msg, err := brk.Receive(ctx, gweQueue)
			if err != nil {
				return
			}
			if asks == 1 {
				close(firstAskDropped)
				continue
			}
			payload, _ := json.Marshal(control.AgentsReply{Agents: []control.AgentApply{{Name: "a", DeploymentID: "d1", Payloads: []map[string]any{agentPayload("a")}}}})
			_ = brk.Publish(ctx, msg.ReplyTo, &broker.Message{Payload: payload, CorrelationID: msg.CorrelationID})
		}
	}()
	exe.FollowPlatformAgents(ctx, "ns")
	t.Cleanup(func() { exe.stopFollowingPlatform(context.Background()) })
	<-firstAskDropped

	body, _ := json.Marshal(control.AgentsChanged{Name: "a"})
	if err := brk.Publish(ctx, a2a.AWEAgentsChangedTopic("ns"), &broker.Message{Payload: body}); err != nil {
		t.Fatalf("publish the change of a: %v", err)
	}

	deadline := time.Now().Add(control.AWEAgentsAnswerWait / 2)
	for running(exe)["a"] != "d1" {
		if time.Now().After(deadline) {
			t.Fatalf("a runs %q %v after the change, want d1 before the unanswered first ask times out", running(exe)["a"], control.AWEAgentsAnswerWait/2)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestAWEReadinessBeforeAnyGWEAnswers(t *testing.T) {
	cases := []struct {
		name      string
		askWait   bool
		wait      bool
		waitedFor time.Duration
		want      bool
	}{
		{name: "an AWE that does not follow a platform", want: true},
		{name: "told not to wait", askWait: true, wait: false, want: true},
		{name: "waiting, before following starts", askWait: true, wait: true, want: false},
		{name: "waiting, just past the cap", askWait: true, wait: true, waitedFor: agentsReadyWaitMax + time.Second, want: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			exe := NewExecutor(newTestServices())
			if c.askWait {
				exe.WaitForPlatformBeforeReady(c.wait)
			}
			if c.waitedFor > 0 {
				exe.platform.since.Store(time.Now().Add(-c.waitedFor).UnixNano())
			}
			if got := exe.ReadinessOpen(); got != c.want {
				t.Fatalf("ReadinessOpen() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestAWEReportsStatusBeforeItsFirstAsk(t *testing.T) {
	ctx := t.Context()
	svc, brk := newTrackingServices(t)
	exe := NewExecutor(svc)
	gwe, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("create the GWE queue: %v", err)
	}
	for _, topic := range []string{a2a.AWEStatusTopic("ns"), a2a.AWEAgentsRequestTopic("ns")} {
		if err = brk.Subscribe(gwe, topic); err != nil {
			t.Fatalf("subscribe the GWE queue to %s: %v", topic, err)
		}
	}

	exe.FollowPlatformAgents(ctx, "ns")
	t.Cleanup(func() { exe.stopFollowingPlatform(context.Background()) })
	first, err := brk.Receive(ctx, gwe)
	if err != nil {
		t.Fatalf("receive the AWE's first message: %v", err)
	}
	if first.Topic != a2a.AWEStatusTopic("ns") {
		t.Fatalf("first message on %s, want the status report on %s before any ask", first.Topic, a2a.AWEStatusTopic("ns"))
	}
}

func TestAppliesBeyondTheSlotLimitWaitForASlot(t *testing.T) {
	ctx := t.Context()
	exe := NewExecutor(newTestServices())
	var blocked sync.Map
	release := make(chan struct{})
	exe.RegisterKind("agent", func(cfg config.Config) (Instance, error) {
		b := &blockedStartInstance{mockInstance: newMockInstance(cfg.GetString("name"), "agent"), started: make(chan struct{}), release: release}
		blocked.Store(b.name, b)
		return b, nil
	})
	desired := make([]control.AgentApply, 0, agentsApplyConcurrent+1)
	for i := range agentsApplyConcurrent + 1 {
		name := fmt.Sprintf("a%02d", i)
		desired = append(desired, control.AgentApply{Name: name, DeploymentID: "d1", Payloads: []map[string]any{agentPayload(name)}})
	}

	applying := exe.applyPlatformAgents(ctx, desired)
	startedNow := func() int {
		n := 0
		blocked.Range(func(_, v any) bool {
			select {
			case <-v.(*blockedStartInstance).started:
				n++
			default:
			}
			return true
		})
		return n
	}
	deadline := time.Now().Add(5 * time.Second)
	for startedNow() < agentsApplyConcurrent {
		if time.Now().After(deadline) {
			t.Fatalf("%d starts began, want %d", startedNow(), agentsApplyConcurrent)
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond)
	if got := startedNow(); got != agentsApplyConcurrent {
		t.Fatalf("%d starts began while every slot was held, want %d", got, agentsApplyConcurrent)
	}
	close(release)
	applying.Wait()
	if got := len(running(exe)); got != agentsApplyConcurrent+1 {
		t.Fatalf("%d agents running after release, want %d", got, agentsApplyConcurrent+1)
	}
}

func TestAnOlderGWEAnswerIsNotTakenAsAnEmptyList(t *testing.T) {
	ctx := t.Context()
	svc, brk := newTrackingServices(t)
	exe := NewExecutor(svc)
	exe.RegisterKind("agent", func(cfg config.Config) (Instance, error) {
		return newMockInstance(cfg.GetString("name"), "agent"), nil
	})
	exe.applyPlatformAgents(ctx, []control.AgentApply{{Name: "a", DeploymentID: "d1", Payloads: []map[string]any{agentPayload("a")}}}).Wait()

	gweQueue, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("create the GWE queue: %v", err)
	}
	if err := brk.Subscribe(gweQueue, a2a.AWEAgentsRequestTopic("ns")); err != nil {
		t.Fatalf("subscribe the GWE queue to AWE requests: %v", err)
	}
	go func() {
		for {
			msg, err := brk.Receive(ctx, gweQueue)
			if err != nil {
				return
			}
			_ = brk.Publish(ctx, msg.ReplyTo, &broker.Message{Payload: []byte(`{"done":true,"started":["a"]}`), CorrelationID: msg.CorrelationID})
		}
	}()

	if _, err := exe.checkOnce(ctx, brk, "ns", false); err == nil {
		t.Fatal("an answer with no agent list was taken as an answer")
	}
	if got := running(exe)["a"]; got != "d1" {
		t.Fatalf("after an older GWE's answer a runs %q, want d1 kept", got)
	}
}

type blockedStartInstance struct {
	*mockInstance
	started chan struct{}
	release chan struct{}
}

func (b *blockedStartInstance) Start(ctx context.Context) error {
	close(b.started)
	select {
	case <-b.release:
	case <-ctx.Done():
	}
	return b.mockInstance.Start(ctx)
}

func TestABlockedStartHoldsOnlyItsAgentAndItsChangeAppliesWhenItEnds(t *testing.T) {
	ctx := t.Context()
	svc, brk := newTrackingServices(t)
	exe := NewExecutor(svc)
	blocked := &blockedStartInstance{mockInstance: newMockInstance("slow", "agent"), started: make(chan struct{}), release: make(chan struct{})}
	releaseOnce := sync.OnceFunc(func() { close(blocked.release) })
	defer releaseOnce()
	var slowBuilt atomic.Int32
	exe.RegisterKind("agent", func(cfg config.Config) (Instance, error) {
		if cfg.GetString("name") == "slow" && slowBuilt.Add(1) == 1 {
			return blocked, nil
		}
		return newMockInstance(cfg.GetString("name"), "agent"), nil
	})

	var desired atomic.Pointer[[]control.AgentApply]
	desired.Store(&[]control.AgentApply{})
	gweQueue, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("create the GWE queue: %v", err)
	}
	if err := brk.Subscribe(gweQueue, a2a.AWEAgentsRequestTopic("ns")); err != nil {
		t.Fatalf("subscribe the GWE queue to AWE requests: %v", err)
	}
	go func() {
		for {
			msg, err := brk.Receive(ctx, gweQueue)
			if err != nil {
				return
			}
			payload, _ := json.Marshal(control.AgentsReply{Agents: *desired.Load()})
			_ = brk.Publish(ctx, msg.ReplyTo, &broker.Message{Payload: payload, CorrelationID: msg.CorrelationID})
		}
	}()

	exe.FollowPlatformAgents(ctx, "ns")
	t.Cleanup(func() { exe.stopFollowingPlatform(context.Background()) })
	deadline := time.Now().Add(3 * agentsAskInterval)
	for !exe.ReadinessOpen() {
		if time.Now().After(deadline) {
			t.Fatal("not ready after a GWE answered an empty list")
		}
		time.Sleep(20 * time.Millisecond)
	}

	entry := func(name, deployment string) control.AgentApply {
		return control.AgentApply{Name: name, DeploymentID: deployment, Payloads: []map[string]any{agentPayload(name)}}
	}
	changed := func(name string, list ...control.AgentApply) {
		desired.Store(&list)
		body, _ := json.Marshal(control.AgentsChanged{Name: name})
		if err := brk.Publish(ctx, a2a.AWEAgentsChangedTopic("ns"), &broker.Message{Payload: body}); err != nil {
			t.Fatalf("publish the change of %s: %v", name, err)
		}
	}
	changed("slow", entry("slow", "d1"))
	select {
	case <-blocked.started:
	case <-time.After(3 * agentsAskInterval):
		t.Fatal("the start of slow never began")
	}
	changed("fast", entry("slow", "d1"), entry("fast", "d1"))
	deadline = time.Now().Add(3 * agentsAskInterval)
	for running(exe)["fast"] != "d1" {
		if time.Now().After(deadline) {
			t.Fatalf("running %v while the start of slow is blocked, want fast running d1", running(exe))
		}
		time.Sleep(20 * time.Millisecond)
	}

	exe.platform.checking.Lock()
	exe.platform.takeSkipped("slow")
	exe.platform.checking.Unlock()
	changed("slow", entry("slow", "d2"), entry("fast", "d1"))
	deadline = time.Now().Add(3 * agentsAskInterval)
	for !exe.platform.takeSkipped("slow") {
		if time.Now().After(deadline) {
			t.Fatal("no check reached slow while its start was blocked")
		}
		time.Sleep(20 * time.Millisecond)
	}
	exe.platform.markSkipped("slow")
	releaseOnce()
	deadline = time.Now().Add(agentsCheckInterval / 6)
	for running(exe)["slow"] != "d2" {
		if time.Now().After(deadline) {
			t.Fatalf("running %v after the blocked start ended, want slow on d2 before the next periodic check", running(exe))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

type startContextInstance struct {
	*mockInstance
	runCtx         chan context.Context
	cancelledAtEnd chan error
}

func (s *startContextInstance) Start(ctx context.Context) error {
	s.runCtx <- ctx
	return s.mockInstance.Start(ctx)
}

func (s *startContextInstance) StopForProcessExit(_ context.Context) error {
	ctx := <-s.runCtx
	s.cancelledAtEnd <- ctx.Err()
	return nil
}

func TestAPlatformAgentKeepsItsTasksRunningUntilTheProcessExitStop(t *testing.T) {
	ctx := t.Context()
	svc, brk := newTrackingServices(t)
	exe := NewExecutor(svc)
	inst := &startContextInstance{mockInstance: newMockInstance("a", "agent"), runCtx: make(chan context.Context, 1), cancelledAtEnd: make(chan error, 1)}
	exe.RegisterKind("agent", func(cfg config.Config) (Instance, error) { return inst, nil })

	gweQueue, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("create the GWE queue: %v", err)
	}
	if err := brk.Subscribe(gweQueue, a2a.AWEAgentsRequestTopic("ns")); err != nil {
		t.Fatalf("subscribe the GWE queue to AWE requests: %v", err)
	}
	go func() {
		for {
			msg, err := brk.Receive(ctx, gweQueue)
			if err != nil {
				return
			}
			payload, _ := json.Marshal(control.AgentsReply{Agents: []control.AgentApply{{Name: "a", DeploymentID: "d1", Payloads: []map[string]any{agentPayload("a")}}}})
			_ = brk.Publish(ctx, msg.ReplyTo, &broker.Message{Payload: payload, CorrelationID: msg.CorrelationID})
		}
	}()

	exe.FollowPlatformAgents(ctx, "ns")
	deadline := time.Now().Add(3 * agentsAskInterval)
	for running(exe)["a"] != "d1" {
		if time.Now().After(deadline) {
			t.Fatal("a never started from the answer")
		}
		time.Sleep(20 * time.Millisecond)
	}

	if err := exe.Stop(ctx); err != nil {
		t.Fatalf("stop the executor: %v", err)
	}
	if err := <-inst.cancelledAtEnd; err != nil {
		t.Fatalf("a's tasks were cancelled (%v) before its process-exit stop, so they end with a cancelled reply instead of staying in the queue for another AWE", err)
	}
}

func answerWithOneAgent(t *testing.T, brk broker.Broker, name string) {
	t.Helper()
	ctx := t.Context()
	gweQueue, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("create the GWE queue: %v", err)
	}
	if err := brk.Subscribe(gweQueue, a2a.AWEAgentsRequestTopic("ns")); err != nil {
		t.Fatalf("subscribe the GWE queue to AWE requests: %v", err)
	}
	go func() {
		for {
			msg, err := brk.Receive(ctx, gweQueue)
			if err != nil {
				return
			}
			payload, _ := json.Marshal(control.AgentsReply{Agents: []control.AgentApply{{Name: name, DeploymentID: "d1", Payloads: []map[string]any{agentPayload(name)}}}})
			_ = brk.Publish(ctx, msg.ReplyTo, &broker.Message{Payload: payload, CorrelationID: msg.CorrelationID})
		}
	}()
}

func TestStopEndsAPlatformAgentStartThatIsStillRunning(t *testing.T) {
	ctx := t.Context()
	svc, brk := newTrackingServices(t)
	exe := NewExecutor(svc)
	blocked := &blockedStartInstance{mockInstance: newMockInstance("slow", "agent"), started: make(chan struct{}), release: make(chan struct{})}
	exe.RegisterKind("agent", func(cfg config.Config) (Instance, error) { return blocked, nil })

	answerWithOneAgent(t, brk, "slow")
	exe.FollowPlatformAgents(ctx, "ns")
	select {
	case <-blocked.started:
	case <-time.After(3 * agentsAskInterval):
		t.Fatal("the start of slow never began")
	}

	stopped := make(chan error, 1)
	go func() { stopped <- exe.Stop(ctx) }()

	select {
	case err := <-stopped:
		if err != nil {
			t.Fatalf("stop the executor: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Stop still waits on a start that was running when it began, want the start cancelled")
	}
	if got := running(exe)["slow"]; got != "" {
		t.Fatalf("slow runs %q after a start cut by Stop, want it not running", got)
	}
}

type deafStartInstance struct {
	*mockInstance
	started chan struct{}
	release chan struct{}
}

func (d *deafStartInstance) Start(ctx context.Context) error {
	close(d.started)
	<-d.release
	return d.mockInstance.Start(ctx)
}

func TestStopReturnsByItsDeadlineWhileAPlatformAgentStartIgnoresCancellation(t *testing.T) {
	svc, brk := newTrackingServices(t)
	exe := NewExecutor(svc)
	deaf := &deafStartInstance{mockInstance: newMockInstance("deaf", "agent"), started: make(chan struct{}), release: make(chan struct{})}
	t.Cleanup(func() { close(deaf.release) })
	exe.RegisterKind("agent", func(cfg config.Config) (Instance, error) { return deaf, nil })
	answerWithOneAgent(t, brk, "deaf")
	exe.FollowPlatformAgents(t.Context(), "ns")
	select {
	case <-deaf.started:
	case <-time.After(3 * agentsAskInterval):
		t.Fatal("the start of deaf never began")
	}

	stopCtx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	stopped := make(chan struct{})
	go func() {
		_ = exe.Stop(stopCtx)
		close(stopped)
	}()

	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop still waits on a start that ignores cancellation, want it to return by its 200 ms deadline")
	}
}

type cancelOnStartInstance struct {
	*mockInstance
	cancel context.CancelFunc
}

func (c *cancelOnStartInstance) Start(ctx context.Context) error {
	c.cancel()
	return c.mockInstance.Start(ctx)
}

func TestAPlatformAgentWhoseStartEndsAsTheAWEStopsIsNotKept(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	exe := NewExecutor(newTestServices())
	inst := &cancelOnStartInstance{mockInstance: newMockInstance("a", "agent"), cancel: cancel}
	exe.RegisterKind("agent", func(config.Config) (Instance, error) { return inst, nil })

	_, err := exe.addAndStart(ctx, config.FromMap(agentPayload("a")), "d1", true)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("addAndStart = %v, want the start reported cancelled when the AWE stopped as it returned", err)
	}
	if inst.stopCalled.Load() != 1 {
		t.Fatalf("stop called %d times, want 1: a start the AWE's stop cut is not kept running", inst.stopCalled.Load())
	}
	if _, err := exe.GetInstance("a"); err == nil {
		t.Fatal("a is still listed after its start was cut, want it removed")
	}
}
