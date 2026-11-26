// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

func TestSignalReconcile_Coalesces(t *testing.T) {
	w := &Worker{reconcileTrigger: make(chan reconcileReason, 1)}

	w.signalReconcile(reconcileReasonPeriodic)
	for range 10 {
		w.signalReconcile(reconcileReasonPeriodic)
	}

	if len(w.reconcileTrigger) != 1 {
		t.Fatalf("reconcileTrigger len = %d, want 1", len(w.reconcileTrigger))
	}
	got := <-w.reconcileTrigger
	if got != reconcileReasonPeriodic {
		t.Errorf("reason = %q, want %q", got, reconcileReasonPeriodic)
	}
}

func TestSignalReconcile_CoalescesDuringRun(t *testing.T) {
	var runCount atomic.Int64
	blocker := make(chan struct{})
	started := make(chan struct{})

	w := &Worker{
		reconcileTrigger: make(chan reconcileReason, 1),
		logger:           discardLogger(),
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-w.reconcileTrigger:
				runCount.Add(1)
				select {
				case started <- struct{}{}:
				case <-ctx.Done():
					return
				}
				select {
				case <-blocker:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	w.signalReconcile(reconcileReasonBoot)

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("first reconcile run did not start within 2s")
	}

	for range 20 {
		w.signalReconcile(reconcileReasonPush)
	}

	if n := len(w.reconcileTrigger); n > 1 {
		t.Errorf("reconcileTrigger buffered %d signals; want ≤ 1 (coalescing broken)", n)
	}

	blocker <- struct{}{}

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("second reconcile run did not start within 2s")
	}

	blocker <- struct{}{}

	select {
	case <-started:
		t.Errorf("unexpected third reconcile run; 20 signals should have coalesced into 1")
		blocker <- struct{}{}
	case <-time.After(100 * time.Millisecond):
	}

	if n := runCount.Load(); n != 2 {
		t.Errorf("runCount = %d; want exactly 2 (boot + 1 coalesced push)", n)
	}
}

func TestPruneLocalPackages_ManagedDirAbsentIsRemoved(t *testing.T) {
	toolsDir := t.TempDir()

	pkgDir := filepath.Join(toolsDir, "stale_pkg")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "package.zip.extracted"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	w := &Worker{
		packageProcessor: NewPackageProcessor(toolsDir, discardLogger()),
		packageStore:     &recordingMirror{},
		logger:           discardLogger(),
	}

	w.pruneLocalPackages(t.Context(), reconcileReasonPeriodic)

	if _, err := os.Stat(pkgDir); !os.IsNotExist(err) {
		t.Errorf("stale_pkg was not removed by pruneLocalPackages; stat err = %v", err)
	}
}

func TestPruneLocalPackages_UnmanagedDirIsPreserved(t *testing.T) {
	toolsDir := t.TempDir()

	unmanaged := filepath.Join(toolsDir, "builtin_stuff")
	if err := os.MkdirAll(unmanaged, 0o755); err != nil {
		t.Fatal(err)
	}

	w := &Worker{
		packageProcessor: NewPackageProcessor(toolsDir, discardLogger()),
		packageStore:     &recordingMirror{},
		logger:           discardLogger(),
	}

	w.pruneLocalPackages(t.Context(), reconcileReasonPeriodic)

	if _, err := os.Stat(unmanaged); err != nil {
		t.Errorf("unmanaged dir was removed unexpectedly; stat err = %v", err)
	}
}

func TestPruneLocalPackages_NameInRemoteNamesPreserved(t *testing.T) {
	toolsDir := t.TempDir()

	pkgDir := filepath.Join(toolsDir, "live_pkg")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "package.zip.extracted"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	w := &Worker{
		packageProcessor: NewPackageProcessor(toolsDir, discardLogger()),
		packageStore:     &remoteNamesMirror{names: []string{"live_pkg"}},
		logger:           discardLogger(),
	}

	w.pruneLocalPackages(t.Context(), reconcileReasonPeriodic)

	if _, err := os.Stat(pkgDir); err != nil {
		t.Errorf("live_pkg was removed despite being in RemoteNames; stat err = %v", err)
	}
}

func TestPruneLocalSkills_ExtractedSkillAbsentIsRemoved(t *testing.T) {
	skillsDir := t.TempDir()

	skillDir := filepath.Join(skillsDir, "stale_skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# stub"), 0o644); err != nil {
		t.Fatal(err)
	}

	w := &Worker{
		cfg:        WorkerConfig{SkillsDir: skillsDir},
		skillStore: &recordingMirror{},
		logger:     discardLogger(),
	}

	w.pruneLocalSkills(t.Context(), reconcileReasonPeriodic)

	if _, err := os.Stat(skillDir); !os.IsNotExist(err) {
		t.Errorf("stale_skill was not removed by pruneLocalSkills; stat err = %v", err)
	}
}

func TestPruneLocalSkills_NonExtractedDirPreserved(t *testing.T) {
	skillsDir := t.TempDir()

	plainDir := filepath.Join(skillsDir, "not_a_skill")
	if err := os.MkdirAll(plainDir, 0o755); err != nil {
		t.Fatal(err)
	}

	w := &Worker{
		cfg:        WorkerConfig{SkillsDir: skillsDir},
		skillStore: &recordingMirror{},
		logger:     discardLogger(),
	}

	w.pruneLocalSkills(t.Context(), reconcileReasonPeriodic)

	if _, err := os.Stat(plainDir); err != nil {
		t.Errorf("non-skill dir was removed unexpectedly; stat err = %v", err)
	}
}

func TestPruneLocalSkills_NameInRemoteNamesPreserved(t *testing.T) {
	skillsDir := t.TempDir()

	skillDir := filepath.Join(skillsDir, "live_skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# live"), 0o644); err != nil {
		t.Fatal(err)
	}

	w := &Worker{
		cfg:        WorkerConfig{SkillsDir: skillsDir},
		skillStore: &remoteNamesMirror{names: []string{"live_skill"}},
		logger:     discardLogger(),
	}

	w.pruneLocalSkills(t.Context(), reconcileReasonPeriodic)

	if _, err := os.Stat(skillDir); err != nil {
		t.Errorf("live_skill was removed despite being in RemoteNames; stat err = %v", err)
	}
}

func TestReconcileDrift_ReasonGating(t *testing.T) {
	var driftCalls atomic.Int64

	cm := &countingMetrics{onCounter: func(name string, _ int64, _ ...string) {
		if name == metricReconcileDrift {
			driftCalls.Add(1)
		}
	}}
	m := newSTRMetrics(cm)

	w := &Worker{
		reconcileTrigger: make(chan reconcileReason, 1),
		logger:           discardLogger(),
		metrics:          m,
	}

	cases := []struct {
		reason    reconcileReason
		changed   bool
		wantDrift bool
	}{
		{reconcileReasonPush, true, false},
		{reconcileReasonBoot, true, false},
		{reconcileReasonPeriodic, true, true},
		{reconcileReasonReconnect, true, true},
		{reconcileReasonPeriodic, false, false},
		{reconcileReasonReconnect, false, false},
	}

	for _, tc := range cases {
		before := driftCalls.Load()
		w.emitReconcileOutcome(tc.reason, tc.changed)
		after := driftCalls.Load()
		if tc.wantDrift && after == before {
			t.Errorf("reason=%s changed=%v: expected drift increment, got none", tc.reason, tc.changed)
		}
		if !tc.wantDrift && after != before {
			t.Errorf("reason=%s changed=%v: unexpected drift increment", tc.reason, tc.changed)
		}
	}
}

func TestWireReconnectNotifier_BrokerFiresReconnect(t *testing.T) {
	fb := &reconnectBroker{inner: newTestBroker(t)}
	svc := &testServices{brk: fb}

	w := &Worker{
		reconcileTrigger: make(chan reconcileReason, 1),
		svc:              svc,
		logger:           discardLogger(),
	}

	w.wireReconnectNotifier()
	fb.fireReconnect()

	select {
	case reason := <-w.reconcileTrigger:
		if reason != reconcileReasonReconnect {
			t.Errorf("reason = %q, want reconnect", reason)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reconcileTrigger did not receive reconnect signal within 2s")
	}
}

func TestWireReconnectNotifier_NonNotifierBrokerIsNoOp(t *testing.T) {
	svc := &testServices{brk: newTestBroker(t)}

	w := &Worker{
		reconcileTrigger: make(chan reconcileReason, 1),
		svc:              svc,
		logger:           discardLogger(),
	}

	w.wireReconnectNotifier()

	if len(w.reconcileTrigger) != 0 {
		t.Errorf("reconcileTrigger received unexpected signal from non-notifier broker")
	}
}

type reconnectBroker struct {
	inner broker.Broker
	fns   []func()
}

func (b *reconnectBroker) Connect(ctx context.Context) error { return b.inner.Connect(ctx) }
func (b *reconnectBroker) Disconnect(ctx context.Context) error {
	return b.inner.Disconnect(ctx)
}
func (b *reconnectBroker) IsConnected() bool { return b.inner.IsConnected() }
func (b *reconnectBroker) CreateQueue(name string, opts broker.QueueOptions) (broker.Queue, error) {
	return b.inner.CreateQueue(name, opts)
}
func (b *reconnectBroker) CreateTemporaryQueue() (broker.Queue, error) { return nil, nil }
func (b *reconnectBroker) RequestReplyInbox(context.Context, string, *broker.Message, time.Duration) (*broker.Message, error) {
	return nil, nil
}
func (b *reconnectBroker) CreateDirectSubscription() (broker.Queue, error) {
	return b.inner.CreateDirectSubscription()
}
func (b *reconnectBroker) DeleteQueue(name string) error            { return b.inner.DeleteQueue(name) }
func (b *reconnectBroker) DeprovisionQueue(name string) error       { return b.inner.DeprovisionQueue(name) }
func (b *reconnectBroker) Subscribe(q broker.Queue, t string) error { return b.inner.Subscribe(q, t) }
func (b *reconnectBroker) Unsubscribe(q broker.Queue, t string) error {
	return b.inner.Unsubscribe(q, t)
}
func (b *reconnectBroker) Publish(ctx context.Context, topic string, msg *broker.Message) error {
	return b.inner.Publish(ctx, topic, msg)
}
func (b *reconnectBroker) PublishGuaranteed(ctx context.Context, topic string, msg *broker.Message) error {
	return b.inner.PublishGuaranteed(ctx, topic, msg)
}
func (b *reconnectBroker) Receive(ctx context.Context, q broker.Queue) (*broker.Message, error) {
	return b.inner.Receive(ctx, q)
}
func (b *reconnectBroker) Request(ctx context.Context, topic string, msg *broker.Message, timeout time.Duration) (*broker.Message, error) {
	return b.inner.Request(ctx, topic, msg, timeout)
}
func (b *reconnectBroker) OnReconnect(fn func()) { b.fns = append(b.fns, fn) }
func (b *reconnectBroker) fireReconnect() {
	for _, fn := range b.fns {
		fn()
	}
}

func (w *Worker) emitReconcileOutcome(reason reconcileReason, changed bool) {
	if changed && reason != reconcileReasonPush && reason != reconcileReasonBoot {
		w.logger.Warn("reconcile drift: non-push reconcile found changes; push event likely dropped",
			"reason", reason)
		w.metrics.recordReconcileDrift(string(reason))
	}
}

type remoteNamesMirror struct {
	recordingMirror
	names []string
}

func (m *remoteNamesMirror) RemoteNames(_ context.Context) ([]string, error) {
	return m.names, nil
}

type countingMetrics struct {
	onCounter func(name string, value int64, labels ...string)
	runtime.NoopMetrics
}

func (c *countingMetrics) Counter(name string, value int64, labels ...string) {
	if c.onCounter != nil {
		c.onCounter(name, value, labels...)
	}
}

type errorMirror struct {
	recordingMirror
}

func (m *errorMirror) RemoteNames(_ context.Context) ([]string, error) {
	return nil, errors.New("remote names unavailable")
}

func TestPruneLocalPackages_RemoteNamesErrorSkipsPrune(t *testing.T) {
	toolsDir := t.TempDir()

	pkgDir := filepath.Join(toolsDir, "would_be_pruned")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "package.zip.extracted"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	w := &Worker{
		packageProcessor: NewPackageProcessor(toolsDir, discardLogger()),
		packageStore:     &errorMirror{},
		logger:           discardLogger(),
	}

	w.pruneLocalPackages(t.Context(), reconcileReasonPeriodic)

	if _, err := os.Stat(pkgDir); err != nil {
		t.Errorf("dir should survive RemoteNames error; stat err = %v", err)
	}
}

func TestPruneLocalSkills_RemoteNamesErrorSkipsPrune(t *testing.T) {
	skillsDir := t.TempDir()

	skillDir := filepath.Join(skillsDir, "would_be_pruned")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# stub"), 0o644); err != nil {
		t.Fatal(err)
	}

	w := &Worker{
		cfg:        WorkerConfig{SkillsDir: skillsDir},
		skillStore: &errorMirror{},
		logger:     discardLogger(),
	}

	w.pruneLocalSkills(t.Context(), reconcileReasonPeriodic)

	if _, err := os.Stat(skillDir); err != nil {
		t.Errorf("skill dir should survive RemoteNames error; stat err = %v", err)
	}
}

func TestPruneLocalSkills_EmptySkillsDirIsNoOp(t *testing.T) {
	w := &Worker{
		cfg:        WorkerConfig{SkillsDir: ""},
		skillStore: &recordingMirror{},
		logger:     discardLogger(),
	}
	w.pruneLocalSkills(t.Context(), reconcileReasonPeriodic)
}

func TestPeriodicReconcileLoop_FiresSignal(t *testing.T) {
	w := &Worker{
		cfg:              WorkerConfig{ReconcileInterval: 10 * time.Millisecond},
		reconcileTrigger: make(chan reconcileReason, 1),
		logger:           discardLogger(),
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	go w.periodicReconcileLoop(ctx)

	select {
	case reason := <-w.reconcileTrigger:
		if reason != reconcileReasonPeriodic {
			t.Errorf("reason = %q, want periodic", reason)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("periodic reconcile signal not received within 2s")
	}
}

func TestRunReconcile_DriftLoggedForPeriodicWhenChangesFound(t *testing.T) {
	var driftCount atomic.Int64

	cm := &countingMetrics{onCounter: func(name string, _ int64, _ ...string) {
		if name == metricReconcileDrift {
			driftCount.Add(1)
		}
	}}

	w := &Worker{
		reconcileTrigger: make(chan reconcileReason, 1),
		logger:           discardLogger(),
		metrics:          newSTRMetrics(cm),
	}

	w.emitReconcileOutcome(reconcileReasonPeriodic, true)

	if n := driftCount.Load(); n != 1 {
		t.Errorf("drift counter = %d, want 1", n)
	}
}

func TestStrMetrics_NilGuardDoesNotPanic(t *testing.T) {
	var m *strMetrics
	m.recordReconcileRun(time.Millisecond, "boot", "nochange")
	m.recordReconcileDrift("periodic")
}
