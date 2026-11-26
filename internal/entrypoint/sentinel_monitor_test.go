// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

type fakeMonitorGateway struct {
	reg TaskRegistry

	mu        sync.Mutex
	submits   []submitCall
	submitErr error
	notifies  []notifyCall
	notifyErr error
}

type submitCall struct {
	agent    string
	taskID   string
	prompt   string
	user     string
	identity *auth.Subject
}

type notifyCall struct {
	recipient string
	req       a2a.NotifyRequest
	ctxErr    error
}

func newFakeMonitorGateway() *fakeMonitorGateway {
	return &fakeMonitorGateway{reg: NewMemoryTaskRegistry()}
}

func (f *fakeMonitorGateway) SubmitTaskAttributedTo(ctx context.Context, agentName, taskID string, message a2a.Message, caller *auth.Subject, attributedUserID string, opts ...SubmitOption) error {
	userID := attributedUserID
	if userID == "" && caller != nil {
		userID = caller.UserID()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.submitErr != nil {
		return f.submitErr
	}
	prompt := ""
	if len(message.Parts) > 0 {
		prompt = message.Parts[0].Text
	}
	f.submits = append(f.submits, submitCall{agent: agentName, taskID: taskID, prompt: prompt, user: userID, identity: caller})
	return f.reg.Register(taskID, agentName, userID, TaskIdentity{Origin: TaskOrigin{
		Trigger: "system", SystemInitiated: caller != nil && caller.SystemInitiated(),
	}})
}

func (f *fakeMonitorGateway) Tasks() TaskRegistry { return f.reg }

func (f *fakeMonitorGateway) PublishNotification(ctx context.Context, recipientUserID string, req a2a.NotifyRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.notifyErr != nil {
		return f.notifyErr
	}
	f.notifies = append(f.notifies, notifyCall{recipient: recipientUserID, req: req, ctxErr: ctx.Err()})
	return nil
}

func (f *fakeMonitorGateway) calls() []submitCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]submitCall, len(f.submits))
	copy(out, f.submits)
	return out
}

func (f *fakeMonitorGateway) notifications() []notifyCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]notifyCall, len(f.notifies))
	copy(out, f.notifies)
	return out
}

func newTestMonitor(t *testing.T, cfg SentinelMonitorConfig, gw monitorGateway) *SentinelMonitorService {
	t.Helper()
	s := NewSentinelMonitorService(cfg, gw, testLogger())
	s.Start(t.Context())
	t.Cleanup(s.Stop)
	return s
}

func defaultMonitorCfg() SentinelMonitorConfig {
	return ParseSentinelMonitorConfig(map[string]any{"enabled": true})
}

func TestParseSentinelMonitorConfig_Defaults(t *testing.T) {
	cfg := ParseSentinelMonitorConfig(nil)
	if cfg.Enabled {
		t.Errorf("Enabled should default to false")
	}
	if cfg.TargetAgent != "Observability Agent" {
		t.Errorf("TargetAgent = %q, want Observability Agent", cfg.TargetAgent)
	}
	if !cfg.OnTaskFailure || !cfg.OnNegativeFeedback {
		t.Errorf("triggers should default on: failure=%v feedback=%v", cfg.OnTaskFailure, cfg.OnNegativeFeedback)
	}
	if cfg.CooldownSeconds != 300 {
		t.Errorf("CooldownSeconds = %d, want 300", cfg.CooldownSeconds)
	}
	if cfg.MaxConcurrent != 1 {
		t.Errorf("MaxConcurrent = %d, want 1", cfg.MaxConcurrent)
	}
	if cfg.SystemUser != "sentinel-monitor" {
		t.Errorf("SystemUser = %q, want sentinel-monitor", cfg.SystemUser)
	}
}

func TestParseSentinelMonitorConfig_Overrides(t *testing.T) {
	cfg := ParseSentinelMonitorConfig(map[string]any{
		"enabled":              true,
		"target_agent":         "Watcher",
		"on_task_failure":      false,
		"on_negative_feedback": false,
		"cooldown_seconds":     60,
		"max_concurrent":       3,
		"system_user":          "robot",
	})
	if !cfg.Enabled || cfg.TargetAgent != "Watcher" || cfg.OnTaskFailure || cfg.OnNegativeFeedback ||
		cfg.CooldownSeconds != 60 || cfg.MaxConcurrent != 3 || cfg.SystemUser != "robot" {
		t.Errorf("overrides not applied: %+v", cfg)
	}
}

func TestMonitor_TaskFailure_Triggers(t *testing.T) {
	gw := newFakeMonitorGateway()
	_ = gw.reg.Register("task-1", "WorkerAgent", "alice", TaskIdentity{})
	s := newTestMonitor(t, defaultMonitorCfg(), gw)

	s.HandleTaskComplete("task-1", nil, &a2a.Error{Message: "boom"})
	s.wg.Wait()

	calls := gw.calls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 investigation, got %d", len(calls))
	}
	c := calls[0]
	if c.agent != "Observability Agent" {
		t.Errorf("investigation agent = %q, want Observability Agent", c.agent)
	}
	if c.user != "alice" {
		t.Errorf("investigation user = %q, want alice (caller carried through)", c.user)
	}
	if !strings.Contains(c.prompt, "task-1") || !strings.Contains(c.prompt, "WorkerAgent") || !strings.Contains(c.prompt, "boom") {
		t.Errorf("prompt missing context: %q", c.prompt)
	}
}

func TestMonitor_TaskSuccess_NoTrigger(t *testing.T) {
	gw := newFakeMonitorGateway()
	_ = gw.reg.Register("task-1", "WorkerAgent", "alice", TaskIdentity{})
	s := newTestMonitor(t, defaultMonitorCfg(), gw)

	s.HandleTaskComplete("task-1", nil, nil)
	s.wg.Wait()

	if n := len(gw.calls()); n != 0 {
		t.Errorf("success should not trigger; got %d investigations", n)
	}
}

func TestMonitor_LoopGuard_SkipsSystemInitiatedFailures(t *testing.T) {
	gw := newFakeMonitorGateway()
	_ = gw.reg.Register("task-1", "Observability Agent", "alice", TaskIdentity{Origin: TaskOrigin{Trigger: "system", SystemInitiated: true}})
	s := newTestMonitor(t, defaultMonitorCfg(), gw)

	s.HandleTaskComplete("task-1", nil, &a2a.Error{Message: "boom"})
	s.wg.Wait()

	if n := len(gw.calls()); n != 0 {
		t.Errorf("loop guard failed: the observability agent's own failure triggered %d investigations", n)
	}
}

func TestMonitor_LoopGuard_SkipsTargetAgentFailures(t *testing.T) {
	gw := newFakeMonitorGateway()
	_ = gw.reg.Register("task-1", "Observability Agent", "alice", TaskIdentity{})
	s := newTestMonitor(t, defaultMonitorCfg(), gw)

	s.HandleTaskComplete("task-1", nil, &a2a.Error{Message: "boom"})
	s.wg.Wait()

	if n := len(gw.calls()); n != 0 {
		t.Errorf("a failed task on the observability agent triggered %d investigations", n)
	}
}

func TestMonitor_TaskFailure_Disabled(t *testing.T) {
	gw := newFakeMonitorGateway()
	_ = gw.reg.Register("task-1", "WorkerAgent", "alice", TaskIdentity{})
	cfg := defaultMonitorCfg()
	cfg.OnTaskFailure = false
	s := newTestMonitor(t, cfg, gw)

	s.HandleTaskComplete("task-1", nil, &a2a.Error{Message: "boom"})
	s.wg.Wait()

	if n := len(gw.calls()); n != 0 {
		t.Errorf("OnTaskFailure=false should suppress; got %d", n)
	}
}

func TestMonitor_NegativeFeedback_Triggers(t *testing.T) {
	gw := newFakeMonitorGateway()
	s := newTestMonitor(t, defaultMonitorCfg(), gw)

	s.HandleFeedback("task-9", "down", "unhelpful answer", "bob")
	s.wg.Wait()

	calls := gw.calls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 investigation, got %d", len(calls))
	}
	c := calls[0]
	if c.user != "bob" {
		t.Errorf("user = %q, want bob", c.user)
	}
	if !strings.Contains(c.prompt, "task-9") || !strings.Contains(c.prompt, "unhelpful answer") {
		t.Errorf("prompt missing context: %q", c.prompt)
	}
}

func TestMonitor_Investigation_RunsAsScopedSystemIdentity(t *testing.T) {
	gw := newFakeMonitorGateway()
	s := newTestMonitor(t, defaultMonitorCfg(), gw)

	s.HandleFeedback("task-9", "down", "unhelpful answer", "bob")
	s.wg.Wait()

	calls := gw.calls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 investigation, got %d", len(calls))
	}
	id := calls[0].identity
	if id == nil {
		t.Fatal("investigation submitted with no caller (uncredentialed → denied once RBAC is on)")
	}
	if !id.Authenticated() {
		t.Error("system identity must be Authenticated")
	}
	for _, want := range []string{rbac.ScopeAnalyticsRead, rbac.ScopeAnalyticsReadContent} {
		if !slices.Contains(id.Scopes(), want) {
			t.Errorf("system identity missing scope %q; got %v", want, id.Scopes())
		}
	}
	if slices.Contains(id.Scopes(), "*") {
		t.Errorf("system identity should not hold the * admin scope; got %v", id.Scopes())
	}
}

func TestMonitor_PositiveFeedback_NoTrigger(t *testing.T) {
	gw := newFakeMonitorGateway()
	s := newTestMonitor(t, defaultMonitorCfg(), gw)

	s.HandleFeedback("task-9", "up", "", "bob")
	s.wg.Wait()

	if n := len(gw.calls()); n != 0 {
		t.Errorf("positive feedback should not trigger; got %d", n)
	}
}

func TestMonitor_NegativeFeedback_Disabled(t *testing.T) {
	gw := newFakeMonitorGateway()
	cfg := defaultMonitorCfg()
	cfg.OnNegativeFeedback = false
	s := newTestMonitor(t, cfg, gw)

	s.HandleFeedback("task-9", "down", "", "bob")
	s.wg.Wait()

	if n := len(gw.calls()); n != 0 {
		t.Errorf("OnNegativeFeedback=false should suppress; got %d", n)
	}
}

func TestMonitor_NegativeFeedback_EmptyComment(t *testing.T) {
	gw := newFakeMonitorGateway()
	s := newTestMonitor(t, defaultMonitorCfg(), gw)

	s.HandleFeedback("task-9", "down", "", "bob")
	s.wg.Wait()

	calls := gw.calls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 investigation, got %d", len(calls))
	}
	if !strings.Contains(calls[0].prompt, "task-9") {
		t.Errorf("prompt missing task id: %q", calls[0].prompt)
	}
	if strings.Contains(calls[0].prompt, "comment:") {
		t.Errorf("empty comment should not produce a comment clause: %q", calls[0].prompt)
	}
}

func TestMonitor_SystemUserFallback(t *testing.T) {
	gw := newFakeMonitorGateway()
	_ = gw.reg.Register("task-1", "WorkerAgent", "", TaskIdentity{})
	s := newTestMonitor(t, defaultMonitorCfg(), gw)

	s.HandleTaskComplete("task-1", nil, &a2a.Error{Message: "boom"})
	s.wg.Wait()

	calls := gw.calls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 investigation, got %d", len(calls))
	}
	if calls[0].user != "sentinel-monitor" {
		t.Errorf("empty caller should fall back to system_user; got %q", calls[0].user)
	}
}

func TestMonitor_UnregisteredTaskFailure_StillInvestigates(t *testing.T) {
	gw := newFakeMonitorGateway()
	s := newTestMonitor(t, defaultMonitorCfg(), gw)

	s.HandleTaskComplete("task-1", nil, &a2a.Error{Message: "boom"})
	s.wg.Wait()

	calls := gw.calls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 investigation for an unregistered failed task, got %d", len(calls))
	}
	if calls[0].user != "sentinel-monitor" {
		t.Errorf("unknown caller should fall back to system_user; got %q", calls[0].user)
	}
}

func TestMonitor_SubmitError_DoesNotCrash(t *testing.T) {
	gw := newFakeMonitorGateway()
	gw.submitErr = context.DeadlineExceeded
	_ = gw.reg.Register("task-1", "WorkerAgent", "alice", TaskIdentity{})
	s := newTestMonitor(t, defaultMonitorCfg(), gw)

	s.HandleTaskComplete("task-1", nil, &a2a.Error{Message: "boom"})
	s.wg.Wait()

	s.mu.Lock()
	inFlight := s.inFlight
	s.mu.Unlock()
	if inFlight != 0 {
		t.Errorf("inFlight should return to 0 after a failed submit; got %d", inFlight)
	}
}

func TestMonitor_Cooldown_CoalescesSameSignature(t *testing.T) {
	gw := newFakeMonitorGateway()
	_ = gw.reg.Register("task-1", "FlakyAgent", "alice", TaskIdentity{})
	_ = gw.reg.Register("task-2", "FlakyAgent", "alice", TaskIdentity{})
	cfg := defaultMonitorCfg()
	cfg.MaxConcurrent = 10
	s := newTestMonitor(t, cfg, gw)

	base := time.Unix(1_700_000_000, 0)
	s.now = func() time.Time { return base }

	s.HandleTaskComplete("task-1", nil, &a2a.Error{Message: "boom"})
	s.HandleTaskComplete("task-2", nil, &a2a.Error{Message: "boom again"})
	s.wg.Wait()

	if n := len(gw.calls()); n != 1 {
		t.Errorf("cooldown should coalesce same-signature burst into 1; got %d", n)
	}

	s.now = func() time.Time { return base.Add(time.Duration(cfg.CooldownSeconds+1) * time.Second) }
	_ = gw.reg.Register("task-3", "FlakyAgent", "alice", TaskIdentity{})
	s.HandleTaskComplete("task-3", nil, &a2a.Error{Message: "boom"})
	s.wg.Wait()

	if n := len(gw.calls()); n != 2 {
		t.Errorf("after cooldown a new investigation should fire; got %d total", n)
	}
}

func TestMonitor_DistinctSignatures_NotCoalesced(t *testing.T) {
	gw := newFakeMonitorGateway()
	_ = gw.reg.Register("task-1", "AgentA", "alice", TaskIdentity{})
	_ = gw.reg.Register("task-2", "AgentB", "alice", TaskIdentity{})
	cfg := defaultMonitorCfg()
	cfg.MaxConcurrent = 10
	s := newTestMonitor(t, cfg, gw)
	base := time.Unix(1_700_000_000, 0)
	s.now = func() time.Time { return base }

	s.HandleTaskComplete("task-1", nil, &a2a.Error{Message: "boom"})
	s.HandleTaskComplete("task-2", nil, &a2a.Error{Message: "boom"})
	s.wg.Wait()

	if n := len(gw.calls()); n != 2 {
		t.Errorf("distinct agents should each trigger; got %d", n)
	}
}

func TestMonitor_SingleFlight_BoundsConcurrency(t *testing.T) {
	base := newFakeMonitorGateway()
	gw := &blockingGateway{
		fakeMonitorGateway: base,
		release:            make(chan struct{}),
		entered:            make(chan struct{}, 1),
	}
	cfg := defaultMonitorCfg()
	cfg.CooldownSeconds = 0
	s := NewSentinelMonitorService(cfg, gw, testLogger())

	s.Start(t.Context())
	t.Cleanup(s.Stop)

	s.maybeInvestigate("sig-a", "prompt a", "alice")
	<-gw.entered
	s.maybeInvestigate("sig-b", "prompt b", "alice")

	close(gw.release)
	s.wg.Wait()

	if n := len(base.calls()); n != 1 {
		t.Errorf("single-flight should bound to 1 concurrent; got %d", n)
	}
}

type blockingGateway struct {
	*fakeMonitorGateway
	release chan struct{}
	entered chan struct{}
}

func (b *blockingGateway) SubmitTaskAttributedTo(ctx context.Context, agentName, taskID string, message a2a.Message, caller *auth.Subject, attributedUserID string, opts ...SubmitOption) error {
	b.entered <- struct{}{}
	<-b.release
	return b.fakeMonitorGateway.SubmitTaskAttributedTo(ctx, agentName, taskID, message, caller, attributedUserID, opts...)
}

func TestMonitor_StoppedRejectsNewInvestigations(t *testing.T) {
	gw := newFakeMonitorGateway()
	_ = gw.reg.Register("task-1", "WorkerAgent", "alice", TaskIdentity{})
	s := NewSentinelMonitorService(defaultMonitorCfg(), gw, testLogger())
	s.Start(t.Context())
	s.Stop()

	s.HandleTaskComplete("task-1", nil, &a2a.Error{Message: "boom"})

	if n := len(gw.calls()); n != 0 {
		t.Errorf("stopped monitor should reject new investigations; got %d", n)
	}
}

func TestMonitor_StopDrainsInFlight(t *testing.T) {
	base := newFakeMonitorGateway()
	gw := &blockingGateway{
		fakeMonitorGateway: base,
		release:            make(chan struct{}),
		entered:            make(chan struct{}, 1),
	}
	s := NewSentinelMonitorService(defaultMonitorCfg(), gw, testLogger())
	s.Start(t.Context())

	s.maybeInvestigate("sig-a", "prompt a", "alice")
	<-gw.entered

	stopped := make(chan struct{})
	go func() { s.Stop(); close(stopped) }()

	select {
	case <-stopped:
		t.Fatal("Stop returned before draining the in-flight submission")
	case <-time.After(50 * time.Millisecond):
	}

	close(gw.release)
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("Stop did not return after the in-flight submission drained")
	}

	if n := len(base.calls()); n != 1 {
		t.Errorf("the in-flight submission should have completed; got %d", n)
	}
}

func TestMonitor_LoopGuard_ByTaskID_ResolvedNameMismatch(t *testing.T) {
	gw := newFakeMonitorGateway()
	_ = gw.reg.Register("task-1", "WorkerAgent", "alice", TaskIdentity{})
	s := newTestMonitor(t, defaultMonitorCfg(), gw)

	s.HandleTaskComplete("task-1", nil, &a2a.Error{Message: "boom"})
	s.wg.Wait()
	calls := gw.calls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 investigation, got %d", len(calls))
	}
	investigationTaskID := calls[0].taskID

	_ = gw.reg.Register(investigationTaskID, "agent_2f9c-resolved", "sentinel-monitor", TaskIdentity{})
	s.HandleTaskComplete(investigationTaskID, nil, &a2a.Error{Message: "sentinel itself crashed"})
	s.wg.Wait()

	if n := len(gw.calls()); n != 1 {
		t.Errorf("loop guard must skip our own investigation by taskID even when its registered name != target; got %d investigations", n)
	}
}

func TestMonitor_Feedback_OnInvestigationTask_NoTrigger(t *testing.T) {
	gw := newFakeMonitorGateway()
	_ = gw.reg.Register("task-1", "WorkerAgent", "alice", TaskIdentity{})
	s := newTestMonitor(t, defaultMonitorCfg(), gw)

	s.HandleTaskComplete("task-1", nil, &a2a.Error{Message: "boom"})
	s.wg.Wait()
	calls := gw.calls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 investigation, got %d", len(calls))
	}
	investigationTaskID := calls[0].taskID

	s.HandleFeedback(investigationTaskID, "down", "unhelpful", "alice")
	s.wg.Wait()
	if n := len(gw.calls()); n != 1 {
		t.Errorf("feedback on our own investigation task must be ignored; got %d", n)
	}
}

func TestMonitor_ConcurrentFireVsStop_NoPanic(t *testing.T) {
	gw := newFakeMonitorGateway()
	cfg := defaultMonitorCfg()
	cfg.MaxConcurrent = 64
	cfg.CooldownSeconds = 0
	s := NewSentinelMonitorService(cfg, gw, testLogger())
	s.Start(t.Context())

	var fan sync.WaitGroup
	for i := range 50 {
		fan.Go(func() {
			s.maybeInvestigate(fmt.Sprintf("sig-%d", i), "p", "u")
		})
	}
	stopDone := make(chan struct{})
	go func() {
		s.Stop()
		close(stopDone)
	}()
	fan.Wait()
	<-stopDone
}

func TestMonitor_SubmitFailure_ClearsCooldown(t *testing.T) {
	gw := newFakeMonitorGateway()
	gw.submitErr = context.DeadlineExceeded
	_ = gw.reg.Register("task-1", "FlakyAgent", "alice", TaskIdentity{})
	s := newTestMonitor(t, defaultMonitorCfg(), gw)
	base := time.Unix(1_700_000_000, 0)
	s.now = func() time.Time { return base }

	s.HandleTaskComplete("task-1", nil, &a2a.Error{Message: "boom"})
	s.wg.Wait()
	if n := len(gw.calls()); n != 0 {
		t.Fatalf("submit was set to fail; expected 0 recorded calls, got %d", n)
	}

	gw.submitErr = nil
	_ = gw.reg.Register("task-2", "FlakyAgent", "alice", TaskIdentity{})
	s.HandleTaskComplete("task-2", nil, &a2a.Error{Message: "boom again"})
	s.wg.Wait()
	if n := len(gw.calls()); n != 1 {
		t.Errorf("a transient submit failure should not suppress retry within the cooldown window; got %d successful submissions", n)
	}
}

func notifyCfg() SentinelMonitorConfig {
	cfg := defaultMonitorCfg()
	cfg.NotifyOnResult = true
	cfg.NotifyRecipients = []string{"admin@example.com"}
	return cfg
}

func triggerInvestigation(t *testing.T, s *SentinelMonitorService, gw *fakeMonitorGateway) string {
	t.Helper()
	_ = gw.reg.Register("worker-task", "WorkerAgent", "alice", TaskIdentity{})
	s.HandleTaskComplete("worker-task", nil, &a2a.Error{Message: "boom"})
	s.wg.Wait()
	calls := gw.calls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 investigation submitted, got %d", len(calls))
	}
	return calls[0].taskID
}

func TestMonitor_NotifyOnResult_Success(t *testing.T) {
	gw := newFakeMonitorGateway()
	s := newTestMonitor(t, notifyCfg(), gw)
	investigationID := triggerInvestigation(t, s, gw)

	report := &a2a.Task{Status: a2a.TaskStatus{Message: &a2a.Message{
		Parts: []a2a.Part{{Kind: "text", Text: "Root cause: timeout in WorkerAgent."}},
	}}}
	s.HandleTaskComplete(investigationID, report, nil)
	s.wg.Wait()

	notifs := gw.notifications()
	if len(notifs) != 1 {
		t.Fatalf("expected 1 notification, got %d", len(notifs))
	}
	n := notifs[0]
	if n.recipient != "admin@example.com" {
		t.Errorf("recipient = %q, want admin@example.com", n.recipient)
	}
	if !strings.Contains(n.req.Message, "Root cause: timeout") {
		t.Errorf("notification message missing report text: %q", n.req.Message)
	}
	if n.req.Title != "Observability Agent investigation complete" {
		t.Errorf("title = %q", n.req.Title)
	}
	if n.req.Metadata["investigation_task_id"] != investigationID {
		t.Errorf("metadata investigation_task_id = %v, want %q", n.req.Metadata["investigation_task_id"], investigationID)
	}
	if n.req.SenderAgent != "Observability Agent" {
		t.Errorf("senderAgent = %q, want Observability Agent", n.req.SenderAgent)
	}
}

func TestMonitor_NotifyOnResult_PublishesDuringAStop(t *testing.T) {
	gw := newFakeMonitorGateway()
	s := newTestMonitor(t, notifyCfg(), gw)
	investigationID := triggerInvestigation(t, s, gw)
	s.Stop()

	s.HandleTaskComplete(investigationID, nil, nil)

	notifs := gw.notifications()
	if len(notifs) != 1 || notifs[0].ctxErr != nil {
		t.Fatalf("notifications = %+v, want one published on a live context", notifs)
	}
}

func TestMonitor_NotifyOnResult_MultipleRecipients(t *testing.T) {
	gw := newFakeMonitorGateway()
	cfg := notifyCfg()
	cfg.NotifyRecipients = []string{"admin@example.com", "ops@example.com", "oncall@example.com"}
	s := newTestMonitor(t, cfg, gw)
	investigationID := triggerInvestigation(t, s, gw)

	s.HandleTaskComplete(investigationID, &a2a.Task{Status: a2a.TaskStatus{Message: &a2a.Message{
		Parts: []a2a.Part{{Kind: "text", Text: "Root cause: timeout."}},
	}}}, nil)
	s.wg.Wait()

	notifs := gw.notifications()
	if len(notifs) != 3 {
		t.Fatalf("expected one notification per recipient (3), got %d", len(notifs))
	}
	got := make([]string, 0, len(notifs))
	seenReqIDs := map[string]bool{}
	for _, n := range notifs {
		got = append(got, n.recipient)
		if n.recipient != n.req.RecipientUserID {
			t.Errorf("publish recipient %q != req.RecipientUserID %q", n.recipient, n.req.RecipientUserID)
		}
		if seenReqIDs[n.req.RequestID] {
			t.Errorf("duplicate RequestID across recipients: %q", n.req.RequestID)
		}
		seenReqIDs[n.req.RequestID] = true
	}
	slices.Sort(got)
	if !slices.Equal(got, []string{"admin@example.com", "oncall@example.com", "ops@example.com"}) {
		t.Errorf("recipients = %v, want one notification each for all three", got)
	}
}

func TestMonitor_NotifyOnResult_Failure(t *testing.T) {
	gw := newFakeMonitorGateway()
	s := newTestMonitor(t, notifyCfg(), gw)
	investigationID := triggerInvestigation(t, s, gw)

	s.HandleTaskComplete(investigationID, nil, &a2a.Error{Message: "sentinel crashed"})
	s.wg.Wait()

	notifs := gw.notifications()
	if len(notifs) != 1 {
		t.Fatalf("expected 1 notification, got %d", len(notifs))
	}
	if notifs[0].req.Title != "Observability Agent investigation failed" {
		t.Errorf("title = %q, want failure title", notifs[0].req.Title)
	}
	if !strings.Contains(notifs[0].req.Message, "sentinel crashed") {
		t.Errorf("message missing error: %q", notifs[0].req.Message)
	}
}

func TestMonitor_NotifyOnResult_Disabled(t *testing.T) {
	gw := newFakeMonitorGateway()
	s := newTestMonitor(t, defaultMonitorCfg(), gw)
	investigationID := triggerInvestigation(t, s, gw)

	s.HandleTaskComplete(investigationID, &a2a.Task{Status: a2a.TaskStatus{Message: &a2a.Message{
		Parts: []a2a.Part{{Kind: "text", Text: "report"}},
	}}}, nil)
	s.wg.Wait()

	if n := len(gw.notifications()); n != 0 {
		t.Errorf("NotifyOnResult=false should not notify; got %d", n)
	}
}

func TestMonitor_NotifyOnResult_NoRecipient(t *testing.T) {
	gw := newFakeMonitorGateway()
	cfg := defaultMonitorCfg()
	cfg.NotifyOnResult = true
	s := newTestMonitor(t, cfg, gw)
	investigationID := triggerInvestigation(t, s, gw)

	s.HandleTaskComplete(investigationID, &a2a.Task{Status: a2a.TaskStatus{Message: &a2a.Message{
		Parts: []a2a.Part{{Kind: "text", Text: "report"}},
	}}}, nil)
	s.wg.Wait()

	if n := len(gw.notifications()); n != 0 {
		t.Errorf("no recipient configured should skip notification; got %d", n)
	}
}

func TestParseSentinelMonitorConfig_Notify(t *testing.T) {
	cfg := ParseSentinelMonitorConfig(map[string]any{
		"enabled":           true,
		"notify_on_result":  true,
		"notify_recipients": []any{"admin@example.com", "ops@example.com"},
	})
	if !cfg.NotifyOnResult || !slices.Equal(cfg.NotifyRecipients, []string{"admin@example.com", "ops@example.com"}) {
		t.Errorf("notify config not parsed: %+v", cfg)
	}
}

func TestMonitor_NotifyOnResult_PublishError(t *testing.T) {
	gw := newFakeMonitorGateway()
	gw.notifyErr = context.DeadlineExceeded
	s := newTestMonitor(t, notifyCfg(), gw)
	investigationID := triggerInvestigation(t, s, gw)

	s.HandleTaskComplete(investigationID, &a2a.Task{Status: a2a.TaskStatus{Message: &a2a.Message{
		Parts: []a2a.Part{{Kind: "text", Text: "report"}},
	}}}, nil)
	s.wg.Wait()

	if n := len(gw.notifications()); n != 0 {
		t.Errorf("failed publish records nothing; got %d", n)
	}
}

func TestMonitor_NotifyOnResult_EmptyReport(t *testing.T) {
	gw := newFakeMonitorGateway()
	s := newTestMonitor(t, notifyCfg(), gw)
	investigationID := triggerInvestigation(t, s, gw)

	s.HandleTaskComplete(investigationID, &a2a.Task{Status: a2a.TaskStatus{Message: &a2a.Message{
		Parts: []a2a.Part{{Kind: "data", Data: map[string]any{"k": "v"}}},
	}}}, nil)
	s.wg.Wait()

	notifs := gw.notifications()
	if len(notifs) != 1 {
		t.Fatalf("expected 1 notification, got %d", len(notifs))
	}
	if !strings.Contains(notifs[0].req.Message, "no report text") {
		t.Errorf("expected placeholder body, got %q", notifs[0].req.Message)
	}
}

func TestMonitor_NotifyOnResult_TruncatesOnRuneBoundary(t *testing.T) {
	gw := newFakeMonitorGateway()
	s := newTestMonitor(t, notifyCfg(), gw)
	investigationID := triggerInvestigation(t, s, gw)

	report := strings.Repeat("a", maxNotifyMessageLen-1) + strings.Repeat("世", 50)
	s.HandleTaskComplete(investigationID, &a2a.Task{Status: a2a.TaskStatus{Message: &a2a.Message{
		Parts: []a2a.Part{{Kind: "text", Text: report}},
	}}}, nil)
	s.wg.Wait()

	notifs := gw.notifications()
	if len(notifs) != 1 {
		t.Fatalf("expected 1 notification, got %d", len(notifs))
	}
	msg := notifs[0].req.Message
	if !utf8.ValidString(msg) {
		t.Errorf("truncated message must remain valid UTF-8 (no split rune): %q", msg[len(msg)-8:])
	}
	if !strings.HasSuffix(msg, "…") {
		t.Errorf("truncated message should end with the ellipsis; got tail %q", msg[len(msg)-8:])
	}
	if len(msg) >= len(report) {
		t.Errorf("message should be truncated; got len %d vs report %d", len(msg), len(report))
	}
}

func TestGatewayCore_PublishNotification(t *testing.T) {
	brk := &notifTestBroker{}
	core := NewGatewayCore(&LoggingAdapter{}, map[string]any{"namespace": "test-ns", "gateway_id": "gw"})
	core.SetServices(runtime.NewRuntime(brk, nil).Services())

	req := a2a.NotifyRequest{
		RequestID: "r1", RecipientUserID: "admin@example.com",
		Mode: a2a.NotifyModeNotify, Title: "Investigation", Message: "done",
	}
	if err := core.PublishNotification(t.Context(), "admin@example.com", req); err != nil {
		t.Fatalf("PublishNotification: %v", err)
	}
	if len(brk.published) != 1 {
		t.Fatalf("expected 1 published message, got %d", len(brk.published))
	}
	p := brk.published[0]
	if want := a2a.NotifyRequestTopic("test-ns", "admin@example.com"); p.topic != want {
		t.Errorf("topic = %q, want %q", p.topic, want)
	}
	var got a2a.NotifyRequest
	if err := json.Unmarshal(p.payload, &got); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if got.RecipientUserID != "admin@example.com" || got.Title != "Investigation" {
		t.Errorf("payload mismatch: %+v", got)
	}
}

func TestMonitor_InvestigationCompletingOnAnotherGateway(t *testing.T) {
	submitter := newFakeMonitorGateway()
	completer := &fakeMonitorGateway{reg: submitter.reg}
	onSubmitter := newTestMonitor(t, notifyCfg(), submitter)
	onCompleter := newTestMonitor(t, notifyCfg(), completer)
	investigationID := triggerInvestigation(t, onSubmitter, submitter)

	onCompleter.HandleTaskComplete(investigationID, nil, &a2a.Error{Message: "investigation failed"})
	onCompleter.wg.Wait()

	if n := len(completer.calls()); n != 0 {
		t.Errorf("the completing gateway investigated the investigation %d times", n)
	}
	notifs := completer.notifications()
	if len(notifs) != 1 {
		t.Fatalf("completing gateway sent %d notifications, want 1", len(notifs))
	}
	if want := investigationDedupPrefix + investigationID; notifs[0].req.Dedup != want {
		t.Errorf("Dedup = %q, want %q so a redelivered completion notifies once", notifs[0].req.Dedup, want)
	}
}
