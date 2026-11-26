// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build product_analytics

package entrypoint

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	gwauth "github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/productanalytics"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

type captureSink struct {
	mu     sync.Mutex
	events []productanalytics.Event
}

func (s *captureSink) Send(_ context.Context, events []productanalytics.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, events...)
	return nil
}

func (s *captureSink) all() []productanalytics.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]productanalytics.Event(nil), s.events...)
}

type runAnalyticsHarness struct {
	core      *GatewayCore
	analytics *productanalytics.Service
	sink      *captureSink
}

type harnessOptions struct {
	adapter GatewayAdapter
	consent productanalytics.ConsentProvider
}

type harnessOption func(*harnessOptions)

func withAdapter(adapter GatewayAdapter) harnessOption {
	return func(o *harnessOptions) { o.adapter = adapter }
}

func withConsent(consent productanalytics.ConsentProvider) harnessOption {
	return func(o *harnessOptions) { o.consent = consent }
}

func newRunAnalyticsHarness(t *testing.T, opts ...harnessOption) *runAnalyticsHarness {
	t.Helper()
	cfg := harnessOptions{
		adapter: &recordingAdapter{},
		consent: productanalytics.ConsentFunc(func(context.Context) bool { return true }),
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	sink := &captureSink{}
	analytics := productanalytics.NewService(productanalytics.Options{
		Sink:     sink,
		Consent:  cfg.consent,
		Identity: testAnalyticsIdentity,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	core := NewGatewayCore(cfg.adapter, map[string]any{
		"namespace":  "test-ns",
		"gateway_id": "test-gw",
	})
	core.SetServices(runtime.NewRuntime(brk, nil, runtime.WithUsageReporter(analytics)).Services())

	ctx := context.Background()
	if err := core.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := core.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = core.Stop(context.Background()) })
	return &runAnalyticsHarness{core: core, analytics: analytics, sink: sink}
}

func (h *runAnalyticsHarness) awaitReports(t *testing.T) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for range cap(h.core.analytics.slots) {
		select {
		case h.core.analytics.slots <- struct{}{}:
		case <-deadline:
			t.Fatal("run reporters did not finish within 5s")
		}
	}
}

func (h *runAnalyticsHarness) drain(t *testing.T) []productanalytics.Event {
	t.Helper()
	h.awaitReports(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := h.analytics.Stop(ctx); err != nil {
		t.Fatalf("stop analytics: %v", err)
	}
	return h.sink.all()
}

func (h *runAnalyticsHarness) submit(t *testing.T, agentName, taskID string, metadata map[string]any) string {
	t.Helper()
	msg := a2a.Message{
		Kind:      "message",
		MessageID: "msg-" + taskID,
		Role:      a2a.RoleUser,
		Parts:     []a2a.Part{a2a.NewTextPart("hello")},
		Metadata:  metadata,
	}
	if err := h.core.SubmitTask(context.Background(), agentName, taskID, msg, gwauth.NewTestSubject(gwauth.SubjectFields{UserID: "user-1"})); err != nil {
		t.Fatalf("SubmitTask: %v", err)
	}
	return taskID
}

func testAgentCard(name string, exts ...a2a.AgentExtension) a2a.AgentCard {
	card := a2a.AgentCard{Name: name}
	card.Capabilities.Extensions = exts
	return card
}

func displayNameExt(displayName string) a2a.AgentExtension {
	return a2a.AgentExtension{
		URI:    a2a.ExtensionURIDisplayName,
		Params: map[string]any{"display_name": displayName},
	}
}

func completedTask(taskID string) *a2a.Task {
	return &a2a.Task{ID: taskID, Status: a2a.TaskStatus{State: a2a.TaskStateCompleted}}
}

func onlyEvent(t *testing.T, events []productanalytics.Event) productanalytics.Event {
	t.Helper()
	var reports []productanalytics.Event
	for _, event := range events {
		if event.Name != productanalytics.EventModelUsed {
			reports = append(reports, event)
		}
	}
	if len(reports) != 1 {
		t.Fatalf("emitted %d events besides %s, want exactly 1: %v",
			len(reports), productanalytics.EventModelUsed, eventNames(reports))
	}
	return reports[0]
}

func eventNames(events []productanalytics.Event) []string {
	names := make([]string, 0, len(events))
	for _, event := range events {
		names = append(names, event.Name)
	}
	return names
}

func TestRunCompletedOutcomeMapping(t *testing.T) {
	timeoutErr := &a2a.Error{Code: TaskTimeoutErrorCode, Message: "The request timed out."}
	cases := []struct {
		name    string
		task    *a2a.Task
		a2aErr  *a2a.Error
		outcome string
	}{
		{"completed is a success", completedTask("t"), nil, productanalytics.OutcomeSuccess},
		{"canceled is cancelled", &a2a.Task{Status: a2a.TaskStatus{State: a2a.TaskStateCancelled}}, nil, productanalytics.OutcomeCancelled},
		{"failed is an error", &a2a.Task{Status: a2a.TaskStatus{State: a2a.TaskStateFailed}}, nil, productanalytics.OutcomeError},
		{"rejected is an error", &a2a.Task{Status: a2a.TaskStatus{State: a2a.TaskStateRejected}}, nil, productanalytics.OutcomeError},
		{"an agent error is an error", nil, &a2a.Error{Code: -32000, Message: "boom"}, productanalytics.OutcomeError},
		{"a gateway timeout is a timeout, not an error", nil, timeoutErr, productanalytics.OutcomeTimeout},
		{"a timeout flagged only in data is still a timeout", nil, &a2a.Error{
			Code: -32000,
			Data: map[string]any{"taskStatus": "timeout"},
		}, productanalytics.OutcomeTimeout},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newRunAnalyticsHarness(t)
			h.core.agents.AddOrUpdate(testAgentCard("my-agent"))
			taskID := h.submit(t, "my-agent", "task-outcome", nil)
			h.core.fireTaskCompleteHooks(taskID, c.task, c.a2aErr)

			ev := onlyEvent(t, h.drain(t))
			if ev.Name != productanalytics.EventAgentRunCompleted {
				t.Fatalf("event name = %q, want %q", ev.Name, productanalytics.EventAgentRunCompleted)
			}
			if got := ev.Properties[productanalytics.PropOutcome]; got != c.outcome {
				t.Errorf("outcome = %v, want %q", got, c.outcome)
			}
		})
	}
}

func TestRunCompletedAgentClassification(t *testing.T) {
	cases := []struct {
		name      string
		card      a2a.AgentCard
		agentType string
	}{
		{"orchestrator is builtin", testAgentCard("Orchestrator"), productanalytics.AgentTypeBuiltin},
		{"observability agent is builtin", testAgentCard("Observability Agent"), productanalytics.AgentTypeBuiltin},
		{"activity monitor is builtin", testAgentCard("Activity Monitor"), productanalytics.AgentTypeBuiltin},
		{
			"a builtin deployed under a uuid name is matched on its display name",
			testAgentCard("agent_0199aaaa-bbbb-7ccc-8ddd-eeeeffff0000", displayNameExt(platform.BuiltinOrchestratorName)),
			productanalytics.AgentTypeBuiltin,
		},
		{"an operator's agent is user_created", testAgentCard("my-support-bot"), productanalytics.AgentTypeUserCreated},
		{
			"an operator's deployed agent is user_created",
			testAgentCard("agent_0199bbbb-cccc-7ddd-8eee-ffff00001111", displayNameExt("Support Bot")),
			productanalytics.AgentTypeUserCreated,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newRunAnalyticsHarness(t)
			h.core.agents.AddOrUpdate(c.card)
			taskID := h.submit(t, c.card.Name, "task-class", nil)
			h.core.fireTaskCompleteHooks(taskID, completedTask(taskID), nil)

			ev := onlyEvent(t, h.drain(t))
			if got := ev.Properties[productanalytics.PropAgentType]; got != c.agentType {
				t.Errorf("agentType = %v, want %q", got, c.agentType)
			}
		})
	}
}

func TestRunCompletedSilentCases(t *testing.T) {
	internalCard := testAgentCard("Builder", a2a.AgentExtension{
		URI:    a2a.ExtensionURIInternal,
		Params: map[string]any{"internal": true},
	})
	internalVisibleCard := testAgentCard("Visible Internal", a2a.AgentExtension{
		URI:    a2a.ExtensionURIInternal,
		Params: map[string]any{"internal": true, "show_sessions": true},
	})
	ephemeralCard := testAgentCard("__test__wrapper", a2a.AgentExtension{
		URI:    a2a.EphemeralAgentExtensionURI,
		Params: map[string]any{"ephemeral": true},
	})
	cases := []struct {
		name     string
		card     *a2a.AgentCard
		metadata map[string]any
	}{
		{"an internal agent run", &internalCard, nil},
		{"an internal agent that shows its sessions", &internalVisibleCard, nil},
		{"an ephemeral builder test wrapper", &ephemeralCard, nil},
		{"a scheduled task", ptrCard(testAgentCard("my-agent")), map[string]any{"source": a2a.SourceScheduler}},
		{"an agent with no discovered card", nil, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newRunAnalyticsHarness(t)
			agentName := "my-agent"
			if c.card != nil {
				h.core.agents.AddOrUpdate(*c.card)
				agentName = c.card.Name
			}
			taskID := h.submit(t, agentName, "task-silent", c.metadata)
			h.core.fireTaskCompleteHooks(taskID, completedTask(taskID), nil)

			if events := h.drain(t); len(events) != 0 {
				t.Fatalf("emitted %d events, want none: %+v", len(events), events)
			}
		})
	}
}

func ptrCard(card a2a.AgentCard) *a2a.AgentCard { return &card }

func TestRunCompletedSkipsSentinelInvestigation(t *testing.T) {
	h := newRunAnalyticsHarness(t)
	h.core.agents.AddOrUpdate(testAgentCard(platform.BuiltinSentinelName))

	monitor := NewSentinelMonitorService(
		SentinelMonitorConfig{
			Enabled:       true,
			TargetAgent:   platform.BuiltinSentinelName,
			OnTaskFailure: true,
			MaxConcurrent: 1,
			SystemUser:    "sentinel-monitor",
		},
		h.core,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	ctx := context.Background()
	monitor.Start(ctx)
	t.Cleanup(monitor.Stop)

	investigationCtx := gwauth.ContextWithSubject(ctx, monitor.systemIdentity())
	msg := a2a.Message{
		Kind:      "message",
		MessageID: "msg-investigation",
		Role:      a2a.RoleUser,
		Parts:     []a2a.Part{a2a.NewTextPart("Automated alert")},
		Metadata:  map[string]any{"agent_name": platform.BuiltinSentinelName, "sessionBehavior": "RUN_BASED"},
	}
	if err := h.core.SubmitTaskAttributedTo(investigationCtx, platform.BuiltinSentinelName, "task-investigation", msg, stagedCaller(investigationCtx), "alice"); err != nil {
		t.Fatalf("SubmitTask: %v", err)
	}
	h.core.fireTaskCompleteHooks("task-investigation", completedTask("task-investigation"), nil)

	if events := h.drain(t); len(events) != 0 {
		t.Fatalf("a Sentinel investigation was counted as a user run: %+v", events)
	}
}

func TestRunCompletedCountsUserChatWithSentinelAgent(t *testing.T) {
	h := newRunAnalyticsHarness(t)
	h.core.agents.AddOrUpdate(testAgentCard(platform.BuiltinSentinelName))
	taskID := h.submit(t, platform.BuiltinSentinelName, "task-user-chat", nil)
	h.core.fireTaskCompleteHooks(taskID, completedTask(taskID), nil)

	ev := onlyEvent(t, h.drain(t))
	if got := ev.Properties[productanalytics.PropAgentType]; got != productanalytics.AgentTypeBuiltin {
		t.Errorf("agentType = %v, want %q", got, productanalytics.AgentTypeBuiltin)
	}
}

func TestRunCompletedRequiresARegisteredTask(t *testing.T) {
	h := newRunAnalyticsHarness(t)
	h.core.agents.AddOrUpdate(testAgentCard("my-agent"))
	taskID := h.submit(t, "my-agent", "task-forgotten", nil)
	h.core.ForgetTask(taskID)
	h.core.fireTaskCompleteHooks(taskID, completedTask(taskID), nil)
	h.core.fireTaskCompleteHooks("sub-task-never-registered", completedTask("sub-task-never-registered"), nil)

	if events := h.drain(t); len(events) != 0 {
		t.Fatalf("an unregistered task was reported as a run: %+v", events)
	}
}

func TestRunCompletedDoesNotDoubleEmitOnRedelivery(t *testing.T) {
	h := newRunAnalyticsHarness(t)
	h.core.agents.AddOrUpdate(testAgentCard("my-agent"))
	taskID := h.submit(t, "my-agent", "task-redelivered", nil)

	h.core.fireTaskCompleteHooks(taskID, completedTask(taskID), nil)
	h.core.ForgetTask(taskID)
	h.core.fireTaskCompleteHooks(taskID, completedTask(taskID), nil)

	if events := h.drain(t); len(events) != 1 {
		t.Fatalf("emitted %d events for one run, want 1", len(events))
	}
}

func TestRunReportPropertiesAreOnlyTheApprovedThree(t *testing.T) {
	const agentName = "acme-payroll-assistant"
	info := &TaskInfo{
		AgentName: agentName,
		TraceID:   "0199cccc-dddd-7eee-8fff-000011112222",
		TaskOrigin: TaskOrigin{
			Trigger:   a2a.TriggerInteractive,
			AgentType: productanalytics.AgentTypeUserCreated,
		},
	}
	props := runReportProperties(info, completedTask("task-privacy"), nil, usageFacts{})

	want := map[string]any{
		productanalytics.PropOutcome:   productanalytics.OutcomeSuccess,
		productanalytics.PropAgentType: productanalytics.AgentTypeUserCreated,
		productanalytics.PropTraceID:   info.TraceID,
	}
	if !maps.Equal(props, want) {
		t.Fatalf("emitted properties = %+v, want %+v", props, want)
	}
}

func TestRunCompletedCarriesNoAgentName(t *testing.T) {
	h := newRunAnalyticsHarness(t)
	const agentName = "acme-payroll-assistant"
	const displayName = "Acme Payroll Assistant"
	h.core.agents.AddOrUpdate(testAgentCard(agentName, displayNameExt(displayName)))
	taskID := h.submit(t, agentName, "task-privacy", nil)
	h.core.fireTaskCompleteHooks(taskID, completedTask(taskID), nil)

	ev := onlyEvent(t, h.drain(t))
	for key, value := range ev.Properties {
		text, ok := value.(string)
		if !ok {
			continue
		}
		for _, forbidden := range []string{agentName, displayName, "acme", "payroll"} {
			if strings.Contains(strings.ToLower(text), forbidden) {
				t.Errorf("property %q = %q leaks the agent name", key, text)
			}
		}
	}
	if got := ev.Properties[productanalytics.PropInterface]; got != productanalytics.SurfaceServer {
		t.Errorf("interface = %v, want %q", got, productanalytics.SurfaceServer)
	}
}

func TestRunCompletedCarriesABoundTraceIDRatherThanTheRawOne(t *testing.T) {
	h := newRunAnalyticsHarness(t)
	h.core.agents.AddOrUpdate(testAgentCard("my-agent"))
	taskID := h.submit(t, "my-agent", "task-trace", nil)
	info := h.core.tasks.Get(taskID)
	if info == nil || info.TraceID == "" {
		t.Fatal("SubmitTask did not record a trace id")
	}
	h.core.fireTaskCompleteHooks(taskID, completedTask(taskID), nil)

	ev := onlyEvent(t, h.drain(t))
	got, ok := ev.Properties[productanalytics.PropTraceID].(string)
	if !ok {
		t.Fatalf("the delivered event carries no trace id, so a run no longer correlates: %+v", ev.Properties)
	}
	if got == info.TraceID {
		t.Errorf("traceId = %q, the raw id this gateway logs; it must be bound to the install marker first", got)
	}
}

func TestOneRunsEventsShareOneBoundTraceID(t *testing.T) {
	h := newRunAnalyticsHarness(t)
	h.core.agents.AddOrUpdate(testAgentCard("acme-payroll-assistant"))
	taskID := h.submit(t, "acme-payroll-assistant", "task-trace-group", nil)
	info := h.core.tasks.Get(taskID)
	if info == nil || info.TraceID == "" {
		t.Fatal("SubmitTask did not record a trace id")
	}
	h.publishAgentTask(t, taskID, usageMetadata(
		[]string{productanalytics.ToolCategoryBuiltin},
		nil,
		true,
	))

	events := h.drain(t)
	if len(events) < 3 {
		t.Fatalf("emitted %d events, want the run plus its tool and delegation facts", len(events))
	}
	first, ok := events[0].Properties[productanalytics.PropTraceID].(string)
	if !ok {
		t.Fatalf("event %q carries no trace id", events[0].Name)
	}
	for _, ev := range events {
		if got := ev.Properties[productanalytics.PropTraceID]; got != first {
			t.Errorf("event %q has trace id %v, want %q shared with the rest of the run", ev.Name, got, first)
		}
		if got := ev.Properties[productanalytics.PropTraceID]; got == info.TraceID {
			t.Errorf("event %q carries the raw trace id this gateway logs", ev.Name)
		}
	}
}

func TestRunAnalyticsNotRegisteredWithoutAService(t *testing.T) {
	core, _, _ := setupTestGateway(t)
	ctx := context.Background()
	if err := core.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if hasRunAnalyticsHook(core) {
		t.Fatal("registered the run reporter without an analytics service")
	}
	core.agents.AddOrUpdate(testAgentCard("my-agent"))
	_ = core.tasks.Register("task-noservice", "my-agent", "user-1", TaskIdentity{TraceID: "trace-1"})
	core.fireTaskCompleteHooks("task-noservice", completedTask("task-noservice"), nil)
}

func hasRunAnalyticsHook(core *GatewayCore) bool {
	want := reflect.ValueOf(core.reportRunCompleted).Pointer()
	for _, fn := range core.taskCompleteHooks {
		if reflect.ValueOf(fn).Pointer() == want {
			return true
		}
	}
	return false
}

func TestRunAnalyticsRegisteredWithAService(t *testing.T) {
	h := newRunAnalyticsHarness(t)
	if !hasRunAnalyticsHook(h.core) {
		t.Fatal("the run reporter was not registered alongside an analytics service")
	}
}

type blockingConsent struct {
	entered   chan context.Context
	release   chan struct{}
	releaseAt sync.Once
	deadline  chan time.Duration
}

const consentBlockFor = 3 * time.Second

func newBlockingConsent(t *testing.T) *blockingConsent {
	t.Helper()
	c := &blockingConsent{
		entered:  make(chan context.Context, 1),
		release:  make(chan struct{}),
		deadline: make(chan time.Duration, 1),
	}
	timer := time.AfterFunc(consentBlockFor, c.unblock)
	t.Cleanup(func() {
		timer.Stop()
		c.unblock()
	})
	return c
}

func (c *blockingConsent) unblock() {
	c.releaseAt.Do(func() { close(c.release) })
}

func (c *blockingConsent) Enabled(ctx context.Context) bool {
	if deadline, ok := ctx.Deadline(); ok {
		select {
		case c.deadline <- time.Until(deadline):
		default:
		}
	}
	select {
	case c.entered <- ctx:
	default:
	}
	select {
	case <-c.release:
	case <-ctx.Done():
	}
	return true
}

func TestRunReportDoesNotStallTheCompletionPath(t *testing.T) {
	consent := newBlockingConsent(t)
	h := newRunAnalyticsHarness(t, withConsent(consent))
	h.core.agents.AddOrUpdate(testAgentCard("my-agent"))
	taskID := h.submit(t, "my-agent", "task-slow-store", nil)

	started := time.Now()
	h.core.fireTaskCompleteHooks(taskID, completedTask(taskID), nil)
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("the completion path waited %v on the analytics store", elapsed)
	}

	select {
	case <-consent.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the report never reached the analytics store")
	}
	select {
	case deadline := <-consent.deadline:
		if deadline <= 0 || deadline > runReportTimeout {
			t.Errorf("report context deadline = %v, want a bound within %v", deadline, runReportTimeout)
		}
	default:
		t.Error("the report was enqueued with no deadline, so a hung store would park it forever")
	}
	consent.unblock()
}

func TestRunCompletedReportsATimeoutAfterTheAgentIsLost(t *testing.T) {
	h := newRunAnalyticsHarness(t)
	h.core.agents.AddOrUpdate(testAgentCard("my-agent"))
	taskID := h.submit(t, "my-agent", "task-agent-lost", nil)

	h.core.agents.Remove("my-agent")
	h.core.onTaskHardTimeout(t.Context(), taskID, 30)

	ev := onlyEvent(t, h.drain(t))
	if got := ev.Properties[productanalytics.PropOutcome]; got != productanalytics.OutcomeTimeout {
		t.Errorf("outcome = %v, want %q", got, productanalytics.OutcomeTimeout)
	}
	if got := ev.Properties[productanalytics.PropAgentType]; got != productanalytics.AgentTypeUserCreated {
		t.Errorf("agentType = %v, want %q", got, productanalytics.AgentTypeUserCreated)
	}
}

func TestRunCompletedStaysSilentForALostInternalAgent(t *testing.T) {
	h := newRunAnalyticsHarness(t)
	h.core.agents.AddOrUpdate(testAgentCard("Builder", a2a.AgentExtension{
		URI:    a2a.ExtensionURIInternal,
		Params: map[string]any{"internal": true},
	}))
	taskID := h.submit(t, "Builder", "task-internal-lost", nil)

	h.core.agents.Remove("Builder")
	h.core.onTaskHardTimeout(t.Context(), taskID, 30)

	if events := h.drain(t); len(events) != 0 {
		t.Fatalf("an internal agent's run was reported once its card expired: %+v", events)
	}
}

type machineIngressAdapter struct {
	recordingAdapter
	entrypointType string
}

func (a *machineIngressAdapter) MachineIngress() bool { return true }

func (a *machineIngressAdapter) EntrypointType() string {
	if a.entrypointType == "" {
		return "webhook"
	}
	return a.entrypointType
}

func TestRunCompletedSkipsMachineIngress(t *testing.T) {
	h := newRunAnalyticsHarness(t, withAdapter(&machineIngressAdapter{}))
	h.core.agents.AddOrUpdate(testAgentCard("my-agent"))
	taskID := h.submit(t, "my-agent", "task-machine", nil)
	h.core.fireTaskCompleteHooks(taskID, completedTask(taskID), nil)

	events := h.drain(t)
	if got := len(eventsNamed(events, productanalytics.EventAgentRunCompleted)); got != 0 {
		t.Fatalf("machine ingress was counted as a user run: %+v", events)
	}
	if got := len(eventsNamed(events, productanalytics.EventEntrypointUsed)); got != 1 {
		t.Fatalf("entrypoint:used emitted %d times, want 1: %+v", got, events)
	}
}

func TestMachineIngressIsNotForgeableFromMessageMetadata(t *testing.T) {
	h := newRunAnalyticsHarness(t)
	h.core.agents.AddOrUpdate(testAgentCard("my-agent"))
	taskID := h.submit(t, "my-agent", "task-forged", map[string]any{
		"machineIngress":  true,
		"machine_ingress": true,
		"systemInitiated": true,
		"source":          "webhook",
	})
	h.core.fireTaskCompleteHooks(taskID, completedTask(taskID), nil)

	if events := h.drain(t); len(events) != 1 {
		t.Fatalf("emitted %d events, want 1: a client silenced its own run via metadata", len(events))
	}
}

func TestRunReportDropsWhenEverySlotIsBusy(t *testing.T) {
	var entered atomic.Int64
	admitted := make(chan struct{}, runReportConcurrency+1)
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(unblock)

	consent := productanalytics.ConsentFunc(func(ctx context.Context) bool {
		entered.Add(1)
		admitted <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
		}
		return true
	})

	h := newRunAnalyticsHarness(t, withConsent(consent))
	h.core.agents.AddOrUpdate(testAgentCard("my-agent"))

	for i := range runReportConcurrency {
		taskID := h.submit(t, "my-agent", fmt.Sprintf("task-parked-%d", i), nil)
		h.core.fireTaskCompleteHooks(taskID, completedTask(taskID), nil)
	}
	for range runReportConcurrency {
		select {
		case <-admitted:
		case <-time.After(5 * time.Second):
			t.Fatal("the parked reporters never reached the analytics store")
		}
	}

	overflow := h.submit(t, "my-agent", "task-overflow", nil)
	returned := make(chan struct{})
	go func() {
		defer close(returned)
		h.core.fireTaskCompleteHooks(overflow, completedTask(overflow), nil)
	}()
	select {
	case <-returned:
	case <-time.After(2 * time.Second):
		unblock()
		t.Fatal("a saturated reporter blocked the completion path instead of dropping its event")
	}

	select {
	case <-admitted:
		t.Fatal("the overflow report took a slot that was already busy")
	case <-time.After(100 * time.Millisecond):
	}
	if got := entered.Load(); got != runReportConcurrency {
		t.Errorf("%d reports reached the store, want the %d that hold slots", got, runReportConcurrency)
	}
	unblock()
}

func TestRunReportSurvivesAPanickingStore(t *testing.T) {
	consent := productanalytics.ConsentFunc(func(context.Context) bool {
		panic("consent store exploded")
	})
	h := newRunAnalyticsHarness(t, withConsent(consent))
	h.core.agents.AddOrUpdate(testAgentCard("my-agent"))
	taskID := h.submit(t, "my-agent", "task-panicking-store", nil)

	h.core.fireTaskCompleteHooks(taskID, completedTask(taskID), nil)

	deadline := time.After(5 * time.Second)
	for {
		if len(h.core.analytics.slots) == 0 {
			return
		}
		select {
		case <-deadline:
			t.Fatal("the reporter slot was never released, so the panic escaped its recover")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestRunCompletedCountsOnceWhenTwoTerminalPathsRace(t *testing.T) {
	h := newRunAnalyticsHarness(t)
	h.core.agents.AddOrUpdate(testAgentCard("my-agent"))
	taskID := h.submit(t, "my-agent", "task-raced-terminals", nil)

	timeoutErr := &a2a.Error{Code: TaskTimeoutErrorCode, Message: "The request timed out."}
	h.core.fireTaskCompleteHooks(taskID, nil, timeoutErr)
	h.core.fireTaskCompleteHooks(taskID, completedTask(taskID), nil)

	if got := len(h.drain(t)); got != 1 {
		t.Fatalf("emitted %d events for one run, want exactly 1", got)
	}
}

var testAnalyticsIdentity = productanalytics.IdentityFunc(func(context.Context) productanalytics.Identity {
	return productanalytics.Identity{
		Marker:     "11111111-1111-4111-8111-111111111111",
		BindingKey: []byte("0123456789abcdef0123456789abcdef"),
	}
})
