// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

func TestExtractSyncCategory(t *testing.T) {
	cases := []struct {
		name  string
		topic string
		want  string
	}{
		{"tools topic", "ns/sam/v1/sync/tools", "tools"},
		{"skills topic", "ns/sam/v1/sync/skills", "skills"},
		{"trailing slash", "ns/sam/v1/sync/", ""},
		{"empty", "", ""},
		{"no slashes", "tools", ""},
		{"unexpected segment", "ns/sam/v1/sync/something", "something"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := extractSyncCategory(tc.topic); got != tc.want {
				t.Errorf("extractSyncCategory(%q) = %q, want %q", tc.topic, got, tc.want)
			}
		})
	}
}

type recordingMirror struct {
	fetches  atomic.Int64
	removes  atomic.Int64
	fetchAll atomic.Int64
	lastName atomic.Value
}

func (m *recordingMirror) Fetch(_ context.Context, name string) error {
	m.fetches.Add(1)
	m.lastName.Store(name)
	return nil
}
func (m *recordingMirror) Remove(_ context.Context, name string) error {
	m.removes.Add(1)
	m.lastName.Store(name)
	return nil
}
func (m *recordingMirror) FetchAll(_ context.Context) error {
	m.fetchAll.Add(1)
	return nil
}
func (m *recordingMirror) RemoteNames(_ context.Context) ([]string, error) { return nil, nil }

func TestSyncNotifyDispatch_FetchesOnToolUpload(t *testing.T) {
	brk := newTestBroker(t)
	manifestPath := writeTestManifest(t)
	svc := &testServices{brk: brk}
	w := newTestWorker(t, svc, manifestPath)

	mirror := &recordingMirror{}
	if err := w.Init(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	w.packageStore = mirror

	if err := w.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Stop(context.Background()) })

	if err := brk.PublishGuaranteed(t.Context(),
		a2a.SyncNotifyTopic("test-ns", "tools"),
		&broker.Message{Payload: []byte(`{"category":"tools","reason":"tool.uploaded","name":"my_pkg"}`)},
	); err != nil {
		t.Fatalf("publish sync notify: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if mirror.fetches.Load() > 0 {
			if got, _ := mirror.lastName.Load().(string); got != "my_pkg" {
				t.Errorf("Fetch name = %q, want my_pkg", got)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf("sync notification did not call Fetch within 2s (fetches=%d, removes=%d)",
		mirror.fetches.Load(), mirror.removes.Load())
}

func TestSyncNotifyDispatch_RemovesOnToolDelete(t *testing.T) {
	brk := newTestBroker(t)
	manifestPath := writeTestManifest(t)
	svc := &testServices{brk: brk}
	w := newTestWorker(t, svc, manifestPath)

	mirror := &recordingMirror{}
	if err := w.Init(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	w.packageStore = mirror

	if err := w.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Stop(context.Background()) })

	if err := brk.PublishGuaranteed(t.Context(),
		a2a.SyncNotifyTopic("test-ns", "tools"),
		&broker.Message{Payload: []byte(`{"category":"tools","reason":"tool.deleted","name":"old_pkg"}`)},
	); err != nil {
		t.Fatalf("publish sync notify: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if mirror.removes.Load() > 0 {
			if got, _ := mirror.lastName.Load().(string); got != "old_pkg" {
				t.Errorf("Remove name = %q, want old_pkg", got)
			}
			if fetches := mirror.fetches.Load(); fetches > 0 {
				t.Errorf("Fetch should not run on tool.deleted; got fetches=%d", fetches)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf("delete notification did not call Remove within 2s (removes=%d)", mirror.removes.Load())
}

func TestSyncNotifyDispatch_FetchesOnSkillUpload(t *testing.T) {
	brk := newTestBroker(t)
	manifestPath := writeTestManifest(t)
	svc := &testServices{brk: brk}
	w := newTestWorker(t, svc, manifestPath)

	mirror := &recordingMirror{}
	if err := w.Init(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	w.skillStore = mirror

	if err := w.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Stop(context.Background()) })

	if err := brk.PublishGuaranteed(t.Context(),
		a2a.SyncNotifyTopic("test-ns", "skills"),
		&broker.Message{Payload: []byte(`{"category":"skills","reason":"skill.deployed","name":"my_skill"}`)},
	); err != nil {
		t.Fatalf("publish sync notify: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if mirror.fetches.Load() > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf("skill notification did not call Fetch within 2s (fetches=%d)", mirror.fetches.Load())
}

func TestSyncNotifyDispatch_SetsHasSeenSyncEvent(t *testing.T) {
	brk := newTestBroker(t)
	manifestPath := writeTestManifest(t)
	svc := &testServices{brk: brk}
	w := newTestWorker(t, svc, manifestPath)

	if err := w.Init(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	if err := w.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Stop(context.Background()) })

	if w.hasSeenSyncEvent.Load() {
		t.Fatal("hasSeenSyncEvent should be false before any event arrives")
	}

	if err := brk.PublishGuaranteed(t.Context(),
		a2a.SyncNotifyTopic("test-ns", "tools"),
		&broker.Message{Payload: []byte(`{"category":"tools","reason":"tool.exists","name":"any"}`)},
	); err != nil {
		t.Fatalf("publish: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if w.hasSeenSyncEvent.Load() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Error("hasSeenSyncEvent never flipped to true after a sync event")
}

func TestSyncNotifyDispatch_DeleteTearsDownPackageDir(t *testing.T) {
	brk := newTestBroker(t)
	manifestPath := writeTestManifest(t)
	svc := &testServices{brk: brk}
	w := newTestWorker(t, svc, manifestPath)

	toolsDir := t.TempDir()
	pkgDir := filepath.Join(toolsDir, "to_remove")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatalf("mkdir pkg: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "package.zip.extracted"), []byte{}, 0o644); err != nil {
		t.Fatalf("write extracted marker: %v", err)
	}

	mirror := &recordingMirror{}
	if err := w.Init(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	w.packageStore = mirror
	w.packageProcessor = NewPackageProcessor(toolsDir, slog.New(slog.DiscardHandler))

	if err := w.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Stop(context.Background()) })

	if err := brk.PublishGuaranteed(t.Context(),
		a2a.SyncNotifyTopic("test-ns", "tools"),
		&broker.Message{Payload: []byte(`{"category":"tools","reason":"tool.deleted","name":"to_remove"}`)},
	); err != nil {
		t.Fatalf("publish: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(pkgDir); os.IsNotExist(err) {
			if mirror.removes.Load() != 1 {
				t.Errorf("Mirror.Remove call count = %d, want 1", mirror.removes.Load())
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf("package dir %q was not removed within 2s", pkgDir)
}

func TestSyncNotifyDispatch_DeleteRemovesExtractedSkillTree(t *testing.T) {
	brk := newTestBroker(t)
	manifestPath := writeTestManifest(t)
	svc := &testServices{brk: brk}
	w := newTestWorker(t, svc, manifestPath)

	skillsDir := t.TempDir()
	skillName := "my_skill"
	extractedTree := filepath.Join(skillsDir, skillName)
	if err := os.MkdirAll(filepath.Join(extractedTree, "tools"), 0o755); err != nil {
		t.Fatalf("mkdir extracted skill tree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(extractedTree, "SKILL.md"), []byte("# stub"), 0o644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}

	mirror := &recordingMirror{}
	w.cfg.SkillsDir = skillsDir
	if err := w.Init(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	w.skillStore = mirror

	if err := w.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Stop(context.Background()) })

	if err := brk.PublishGuaranteed(t.Context(),
		a2a.SyncNotifyTopic("test-ns", "skills"),
		&broker.Message{Payload: []byte(`{"category":"skills","reason":"skill.deleted","name":"` + skillName + `"}`)},
	); err != nil {
		t.Fatalf("publish skill.deleted: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(extractedTree); os.IsNotExist(err) {
			if mirror.removes.Load() != 1 {
				t.Errorf("Mirror.Remove call count = %d, want 1", mirror.removes.Load())
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf("extracted skill tree %q was not removed within 2s", extractedTree)
}

func TestReconcileLoop_RetryAndExhaust(t *testing.T) {
	prevAttempts := reconcileMaxAttempts
	prevInterval := reconcileInterval
	prevFallback := reconcileFallbackInterval
	reconcileMaxAttempts = 3
	reconcileInterval = 20 * time.Millisecond
	reconcileFallbackInterval = 30 * time.Millisecond
	t.Cleanup(func() {
		reconcileMaxAttempts = prevAttempts
		reconcileInterval = prevInterval
		reconcileFallbackInterval = prevFallback
	})

	brk := newTestBroker(t)
	manifestPath := writeTestManifest(t)
	svc := &testServices{brk: brk}
	w := newTestWorker(t, svc, manifestPath)

	listener, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("subscription: %v", err)
	}
	topic := a2a.LifecycleTopic("test-ns", "str", "started")
	if err := brk.Subscribe(listener, topic); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	if err := w.Init(t.Context(), nil); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		w.reconcileLoop(ctx)
		close(done)
	}()

	received := 0
	deadline := time.Now().Add(2 * time.Second)
	for received < reconcileMaxAttempts+1 && time.Now().Before(deadline) {
		rxCtx, rxCancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
		msg, err := brk.Receive(rxCtx, listener)
		rxCancel()
		if err != nil || msg == nil {
			continue
		}
		received++
		msg.Ack()
	}
	if received < reconcileMaxAttempts+1 {
		t.Errorf("received %d publishes; want > %d (startup burst + at least one long-interval retry)",
			received, reconcileMaxAttempts)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("reconcileLoop did not exit on ctx cancel within 2s")
	}
}

type flakyMirror struct {
	failCount int
	release   chan struct{}
	succeeded chan string

	callsMu sync.Mutex
	calls   map[string]int
}

func newFlakyMirror(failCount int) *flakyMirror {
	return &flakyMirror{
		failCount: failCount,
		release:   make(chan struct{}),
		succeeded: make(chan string, 1),
		calls:     make(map[string]int),
	}
}

func (m *flakyMirror) Fetch(ctx context.Context, name string) error {
	m.callsMu.Lock()
	m.calls[name]++
	n := m.calls[name]
	m.callsMu.Unlock()
	if n <= m.failCount {
		return fmt.Errorf("simulated S3 error (attempt %d)", n)
	}
	select {
	case <-m.release:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case m.succeeded <- name:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *flakyMirror) awaitSuccess(t *testing.T) string {
	t.Helper()
	close(m.release)
	select {
	case name := <-m.succeeded:
		return name
	case <-time.After(2 * time.Second):
		t.Fatal("mirrorRetryLoop did not retry the failed fetch within 2s; no recovery without a manual restart")
		return ""
	}
}

func awaitPending(t *testing.T, w *Worker, pending func(*Worker) map[string]struct{}, name string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		w.pendingMu.Lock()
		_, ok := pending(w)[name]
		w.pendingMu.Unlock()
		if ok {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s never queued for retry after a failed mirror fetch", name)
}

func pendingPackages(w *Worker) map[string]struct{}                    { return w.pendingPackageFetches }
func pendingSkills(w *Worker) map[string]struct{}                      { return w.pendingSkillFetches }
func (m *flakyMirror) Remove(_ context.Context, _ string) error        { return nil }
func (m *flakyMirror) FetchAll(_ context.Context) error                { return nil }
func (m *flakyMirror) RemoteNames(_ context.Context) ([]string, error) { return nil, nil }

func TestMirrorRetryLoop_RecoversAfterBootFailure(t *testing.T) {
	prevRetry := mirrorRetryInterval
	mirrorRetryInterval = 30 * time.Millisecond
	t.Cleanup(func() { mirrorRetryInterval = prevRetry })

	brk := newTestBroker(t)
	manifestPath := writeTestManifest(t)
	svc := &testServices{brk: brk}
	w := newTestWorker(t, svc, manifestPath)

	mirror := newFlakyMirror(1)
	if err := w.Init(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	w.packageStore = mirror

	if err := w.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Stop(context.Background()) })

	if err := brk.PublishGuaranteed(t.Context(),
		a2a.SyncNotifyTopic("test-ns", "tools"),
		&broker.Message{Payload: []byte(`{"category":"tools","reason":"tool.uploaded","name":"custom_pkg"}`)},
	); err != nil {
		t.Fatalf("publish sync notify: %v", err)
	}

	awaitPending(t, w, pendingPackages, "custom_pkg")
	if err := w.Health(); err == nil {
		t.Fatal("F-2 regression: Health() returned nil after a failed mirror fetch; " +
			"the worker is silently degraded but claims healthy")
	}

	if got := mirror.awaitSuccess(t); got != "custom_pkg" {
		t.Errorf("retry succeeded for %q, want custom_pkg", got)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if w.Health() == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := w.Health(); err != nil {
		t.Fatalf("Health() still degraded after mirrorRetryLoop recovered the package: %v", err)
	}
}

func TestMirrorRetryLoop_HealthClearsOnDelete(t *testing.T) {
	brk := newTestBroker(t)
	manifestPath := writeTestManifest(t)
	svc := &testServices{brk: brk}
	w := newTestWorker(t, svc, manifestPath)

	mirror := newFlakyMirror(9999)
	if err := w.Init(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	w.packageStore = mirror

	if err := w.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Stop(context.Background()) })

	if err := brk.PublishGuaranteed(t.Context(),
		a2a.SyncNotifyTopic("test-ns", "tools"),
		&broker.Message{Payload: []byte(`{"category":"tools","reason":"tool.uploaded","name":"doomed_pkg"}`)},
	); err != nil {
		t.Fatalf("publish upload: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		w.pendingMu.Lock()
		_, ok := w.pendingPackageFetches["doomed_pkg"]
		w.pendingMu.Unlock()
		if ok {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	w.pendingMu.Lock()
	_, stillPending := w.pendingPackageFetches["doomed_pkg"]
	w.pendingMu.Unlock()
	if !stillPending {
		t.Fatal("expected doomed_pkg in pendingPackageFetches after failed fetch")
	}
	if w.Health() == nil {
		t.Fatal("expected Health() to be degraded while fetch is pending")
	}

	if err := brk.PublishGuaranteed(t.Context(),
		a2a.SyncNotifyTopic("test-ns", "tools"),
		&broker.Message{Payload: []byte(`{"category":"tools","reason":"tool.deleted","name":"doomed_pkg"}`)},
	); err != nil {
		t.Fatalf("publish delete: %v", err)
	}

	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		w.pendingMu.Lock()
		_, ok := w.pendingPackageFetches["doomed_pkg"]
		w.pendingMu.Unlock()
		if !ok {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	w.pendingMu.Lock()
	_, afterDelete := w.pendingPackageFetches["doomed_pkg"]
	w.pendingMu.Unlock()
	if afterDelete {
		t.Fatal("doomed_pkg still in pendingPackageFetches after tool.deleted; Health() will never recover")
	}
	if err := w.Health(); err != nil {
		t.Fatalf("Health() still degraded after delete cleared the pending fetch: %v", err)
	}
}

func TestMirrorRetryLoop_SkillRecoversAfterBootFailure(t *testing.T) {
	prevRetry := mirrorRetryInterval
	mirrorRetryInterval = 30 * time.Millisecond
	t.Cleanup(func() { mirrorRetryInterval = prevRetry })

	brk := newTestBroker(t)
	manifestPath := writeTestManifest(t)
	svc := &testServices{brk: brk}
	w := newTestWorker(t, svc, manifestPath)

	skillMirror := newFlakyMirror(1)
	if err := w.Init(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	w.skillStore = skillMirror

	if err := w.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Stop(context.Background()) })

	if err := brk.PublishGuaranteed(t.Context(),
		a2a.SyncNotifyTopic("test-ns", "skills"),
		&broker.Message{Payload: []byte(`{"category":"skills","reason":"skill.deployed","name":"custom_skill"}`)},
	); err != nil {
		t.Fatalf("publish skill sync notify: %v", err)
	}

	awaitPending(t, w, pendingSkills, "custom_skill")
	if err := w.Health(); err == nil {
		t.Fatal("Health() returned nil after failed skill mirror fetch; expected degraded")
	}

	if got := skillMirror.awaitSuccess(t); got != "custom_skill" {
		t.Errorf("retry succeeded for %q, want custom_skill", got)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if w.Health() == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := w.Health(); err != nil {
		t.Fatalf("Health() still degraded after skill mirrorRetryLoop recovered: %v", err)
	}
}

func TestMirrorRetryLoop_PersistentFailureKeepsPending(t *testing.T) {
	prevRetry := mirrorRetryInterval
	mirrorRetryInterval = 20 * time.Millisecond
	t.Cleanup(func() { mirrorRetryInterval = prevRetry })

	brk := newTestBroker(t)
	manifestPath := writeTestManifest(t)
	svc := &testServices{brk: brk}
	w := newTestWorker(t, svc, manifestPath)

	pkgMirror := newFlakyMirror(9999)
	skillMirror := newFlakyMirror(9999)
	if err := w.Init(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	w.packageStore = pkgMirror
	w.skillStore = skillMirror

	if err := w.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Stop(context.Background()) })

	if err := brk.PublishGuaranteed(t.Context(),
		a2a.SyncNotifyTopic("test-ns", "tools"),
		&broker.Message{Payload: []byte(`{"category":"tools","reason":"tool.uploaded","name":"stuck_pkg"}`)},
	); err != nil {
		t.Fatalf("publish tool sync: %v", err)
	}
	if err := brk.PublishGuaranteed(t.Context(),
		a2a.SyncNotifyTopic("test-ns", "skills"),
		&broker.Message{Payload: []byte(`{"category":"skills","reason":"skill.deployed","name":"stuck_skill"}`)},
	); err != nil {
		t.Fatalf("publish skill sync: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		w.pendingMu.Lock()
		pkgPending := len(w.pendingPackageFetches) > 0
		skillPending := len(w.pendingSkillFetches) > 0
		w.pendingMu.Unlock()
		if pkgPending && skillPending {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	time.Sleep(100 * time.Millisecond)

	w.pendingMu.Lock()
	_, pkgStillPending := w.pendingPackageFetches["stuck_pkg"]
	_, skillStillPending := w.pendingSkillFetches["stuck_skill"]
	w.pendingMu.Unlock()
	if !pkgStillPending {
		t.Error("stuck_pkg was cleared from pendingPackageFetches despite persistent mirror failure")
	}
	if !skillStillPending {
		t.Error("stuck_skill was cleared from pendingSkillFetches despite persistent mirror failure")
	}
	if err := w.Health(); err == nil {
		t.Fatal("Health() returned nil while packages/skills are still pending")
	}

	pkgMirror.callsMu.Lock()
	pkgCalls := pkgMirror.calls["stuck_pkg"]
	pkgMirror.callsMu.Unlock()
	skillMirror.callsMu.Lock()
	skillCalls := skillMirror.calls["stuck_skill"]
	skillMirror.callsMu.Unlock()
	if pkgCalls < 2 {
		t.Errorf("expected at least 2 Fetch calls for stuck_pkg (initial + retry), got %d", pkgCalls)
	}
	if skillCalls < 2 {
		t.Errorf("expected at least 2 Fetch calls for stuck_skill (initial + retry), got %d", skillCalls)
	}
}

func TestReconcileLoop_PublishesLifecycleStarted(t *testing.T) {
	brk := newTestBroker(t)
	manifestPath := writeTestManifest(t)
	svc := &testServices{brk: brk}
	w := newTestWorker(t, svc, manifestPath)

	listener, subErr := brk.CreateDirectSubscription()
	if subErr != nil {
		t.Fatalf("create direct sub: %v", subErr)
	}
	topic := a2a.LifecycleTopic("test-ns", "str", "started")
	if subErr := brk.Subscribe(listener, topic); subErr != nil {
		t.Fatalf("subscribe lifecycle: %v", subErr)
	}

	if initErr := w.Init(t.Context(), nil); initErr != nil {
		t.Fatal(initErr)
	}
	if startErr := w.Start(t.Context()); startErr != nil {
		t.Fatal(startErr)
	}
	t.Cleanup(func() { _ = w.Stop(context.Background()) })

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	msg, err := brk.Receive(ctx, listener)
	if err != nil {
		t.Fatalf("receive lifecycle: %v", err)
	}
	if msg == nil {
		t.Fatal("expected lifecycle/str/started message; got nil")
	}
	if msg.Topic != topic {
		t.Errorf("topic = %q, want %q", msg.Topic, topic)
	}
}
