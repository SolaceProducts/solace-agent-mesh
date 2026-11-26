// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"context"
	"errors"
	"log/slog"
	"os/exec"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
)

const testLimit = 1000 << 20

type fakeGuard struct {
	*memoryGuard
	limit  atomic.Int64
	used   atomic.Int64
	clock  time.Time
	mem    map[int]int64
	mu     sync.Mutex
	killed []int
	scans  int
}

func newFakeGuard(t *testing.T) *fakeGuard {
	t.Helper()
	f := &fakeGuard{mem: map[int]int64{}, clock: time.Unix(1_000_000, 0)}
	f.memoryGuard = newMemoryGuard(testLimit, 85, 95, slog.Default(), nil)
	f.limit.Store(testLimit)
	f.readLimit = f.limit.Load
	f.readUsage = func() (int64, bool) { return f.used.Load(), true }
	f.now = func() time.Time { return f.clock }
	f.callMemory = func(roots []int) (map[int]int64, bool) {
		f.scans++
		out := map[int]int64{}
		for _, r := range roots {
			if s, ok := f.mem[r]; ok {
				out[r] = s
			}
		}
		return out, true
	}
	f.kill = func(pid int) error {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.killed = append(f.killed, pid)
		return nil
	}
	return f
}

func (f *fakeGuard) kills() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int(nil), f.killed...)
}

func (f *fakeGuard) tick() {
	f.clock = f.clock.Add(memoryGuardTick)
	f.step()
}

func TestNewMemoryGuard_OffWithoutALimitOrWithBadThresholds(t *testing.T) {
	cases := []struct {
		name       string
		limit      int64
		soft, hard int
	}{
		{"no limit", 0, 85, 95},
		{"soft above hard", testLimit, 96, 95},
		{"hard above 100", testLimit, 85, 101},
		{"no soft", testLimit, 0, 95},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if g := newMemoryGuard(tc.limit, tc.soft, tc.hard, nil, nil); g != nil {
				t.Fatal("guard must be off")
			}
		})
	}
	var off *memoryGuard
	done, err := off.admit(t.Context())
	if err != nil {
		t.Fatalf("an off guard must admit: %v", err)
	}
	done()
	if off.track(1, "t", nil) != nil {
		t.Fatal("an off guard must not track calls")
	}
	off.untrack(1)
}

func TestMemoryGuard_StopsTheLargestCallAboveTheHardThreshold(t *testing.T) {
	g := newFakeGuard(t)
	small := g.track(100, "small", nil)
	big := g.track(200, "big", nil)
	g.mem[100], g.mem[200] = 50<<20, 800<<20

	g.used.Store(testLimit * 94 / 100)
	g.tick()
	if len(g.kills()) != 0 {
		t.Fatalf("killed %v below the hard threshold", g.kills())
	}

	g.used.Store(testLimit * 96 / 100)
	g.tick()
	if got := g.kills(); len(got) != 1 || got[0] != 200 {
		t.Fatalf("killed %v, want the largest call 200", got)
	}
	if !big.killed.Load() || small.killed.Load() {
		t.Fatal("only the largest call must be marked as stopped")
	}

	g.tick()
	if len(g.kills()) != 1 {
		t.Fatal("a second call was stopped while the first one still frees its memory")
	}
	g.untrack(200)
	g.tick()
	if got := g.kills(); len(got) != 2 || got[1] != 100 {
		t.Fatalf("killed %v, want 100 next once the first call exited and memory is still high", got)
	}
}

func TestMemoryGuard_StopsEnoughCallsInOnePassToGetUnderTheSoftThreshold(t *testing.T) {
	g := newFakeGuard(t)
	for i := range 12 {
		pid := 100 + i
		g.track(pid, "parallel", nil)
		g.mem[pid] = int64(60+5*i) << 20
	}
	g.used.Store(testLimit * 99 / 100)
	g.tick()
	got := g.kills()
	if len(got) != 2 || got[0] != 111 || got[1] != 110 {
		t.Fatalf("killed %v, want the two largest calls 111 and 110", got)
	}
}

func TestMemoryGuard_StopsACallBeforeFastGrowthCrossesTheHardThreshold(t *testing.T) {
	g := newFakeGuard(t)
	g.track(100, "big", nil)
	g.track(200, "small", nil)
	g.mem[100], g.mem[200] = 500<<20, 300<<20

	g.used.Store(testLimit * 80 / 100)
	g.tick()
	g.used.Store(testLimit * 92 / 100)
	g.tick()
	if got := g.kills(); len(got) != 1 || got[0] != 100 {
		t.Fatalf("killed %v, want 100: memory grew 12%% in one tick, so the next sample would pass the limit", got)
	}
}

func TestMemoryGuard_StopsNothingWhenToolCallsDoNotHoldTheMemory(t *testing.T) {
	g := newFakeGuard(t)
	g.track(100, "small", nil)
	g.mem[100] = 10 << 20
	g.used.Store(testLimit * 99 / 100)
	for range 3 {
		g.tick()
	}
	if got := g.kills(); len(got) != 0 {
		t.Fatalf("killed %v; stopping a 10 MB call cannot free memory the STR itself holds", got)
	}
}

func TestMemoryGuard_ScansProcLessOftenWhileToolCallsDoNotHoldTheMemory(t *testing.T) {
	g := newFakeGuard(t)
	g.track(100, "small", nil)
	g.mem[100] = 10 << 20
	g.used.Store(testLimit * 99 / 100)
	g.tick()
	scans := g.scans
	for range int(memoryScanInterval/memoryGuardTick) - 1 {
		g.tick()
	}
	if g.scans != scans {
		t.Fatalf("scanned /proc %d more times within %s at a steady use", g.scans-scans, memoryScanInterval)
	}
	g.tick()
	if g.scans != scans+1 {
		t.Fatalf("scans = %d, want one more once %s passed", g.scans, memoryScanInterval)
	}
}

func TestMemoryGuard_StopsAgainAndMovesOnWhenAStoppedCallDoesNotExit(t *testing.T) {
	g := newFakeGuard(t)
	g.track(100, "stuck", nil)
	g.track(200, "next", nil)
	g.mem[100], g.mem[200] = 600<<20, 300<<20
	g.used.Store(testLimit * 99 / 100)
	g.tick()
	if got := g.kills(); len(got) != 1 || got[0] != 100 {
		t.Fatalf("killed %v, want 100 first", got)
	}
	for range int(memoryFreeingGrace/memoryGuardTick) - 1 {
		g.tick()
	}
	if got := g.kills(); len(got) != 1 {
		t.Fatalf("killed %v while the first stop was still in its grace", got)
	}
	g.tick()
	got := g.kills()
	if len(got) != 3 || got[1] != 100 || got[2] != 200 {
		t.Fatalf("killed %v, want 100 stopped again and then 200, since 100 never freed its memory", got)
	}
}

func TestMemoryGuard_LogsAStopThatFails(t *testing.T) {
	g := newFakeGuard(t)
	var logged atomic.Bool
	g.logger = slog.New(&recordingHandler{onWarn: func(msg string) {
		if msg == "memory guard could not stop a tool call" {
			logged.Store(true)
		}
	}})
	g.kill = func(int) error { return errors.New("operation not permitted") }
	g.track(100, "big", nil)
	g.mem[100] = 900 << 20
	g.used.Store(testLimit * 99 / 100)
	g.tick()
	if !logged.Load() {
		t.Fatal("a failed stop must be logged")
	}
}

func TestMemoryGuard_FollowsAnInPlaceResizeOfTheMemoryLimit(t *testing.T) {
	g := newFakeGuard(t)
	g.track(100, "big", nil)
	g.mem[100] = 800 << 20
	g.used.Store(testLimit * 90 / 100)
	g.tick()
	if len(g.kills()) != 0 {
		t.Fatalf("killed %v at 90%% of the limit", g.kills())
	}

	g.limit.Store(testLimit * 90 / 100)
	g.tick()
	if got := g.kills(); len(got) != 1 || got[0] != 100 {
		t.Fatalf("killed %v, want 100: the pod was resized down and now uses all of its limit", got)
	}
}

func TestMemoryGuard_PressureClearsOnlyAMarginBelowTheSoftThreshold(t *testing.T) {
	g := newFakeGuard(t)
	g.used.Store(testLimit * 85 / 100)
	g.tick()
	if !g.pressure {
		t.Fatal("no pressure at the soft threshold")
	}
	g.used.Store(testLimit*85/100 - 1)
	g.tick()
	if !g.pressure {
		t.Fatal("pressure cleared just below the soft threshold; it must hold until the margin")
	}
	g.used.Store(testLimit * 82 / 100)
	g.tick()
	if g.pressure {
		t.Fatal("pressure still set below the margin")
	}
}

func TestMemoryGuard_AdmitWaitsAboveTheSoftThresholdUntilACallEnds(t *testing.T) {
	g := newFakeGuard(t)
	g.track(100, "running", nil)
	g.mem[100] = 800 << 20
	g.used.Store(testLimit * 90 / 100)
	g.tick()

	admitted := make(chan error, 1)
	go func() { _, err := g.admit(t.Context()); admitted <- err }()
	select {
	case err := <-admitted:
		t.Fatalf("admitted above the soft threshold: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	g.untrack(100)
	select {
	case err := <-admitted:
		if err != nil {
			t.Fatalf("admit: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the end of the last call did not admit the waiting one")
	}
}

func TestMemoryGuard_AdmitLetsOneWaiterThroughWhenTheLastCallEnds(t *testing.T) {
	g := newFakeGuard(t)
	g.track(100, "running", nil)
	g.mem[100] = 800 << 20
	g.used.Store(testLimit * 90 / 100)
	g.tick()

	admitted := make(chan func(), 3)
	for range 3 {
		go func() {
			if done, err := g.admit(t.Context()); err == nil {
				admitted <- done
			}
		}()
	}
	time.Sleep(50 * time.Millisecond)
	g.untrack(100)

	first := <-admitted
	select {
	case <-admitted:
		t.Fatal("a second waiter was admitted while the first had not started yet")
	case <-time.After(100 * time.Millisecond):
	}
	g.track(200, "first", nil)
	first()
	select {
	case <-admitted:
		t.Fatal("a waiter was admitted while the first call runs and memory is still high")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestMemoryGuard_AdmitsEveryCallWhenTheMemoryIsHeldOutsideToolCalls(t *testing.T) {
	g := newFakeGuard(t)
	g.track(100, "small", nil)
	g.mem[100] = 10 << 20
	g.used.Store(testLimit * 90 / 100)
	g.tick()

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	for range 3 {
		if _, err := g.admit(ctx); err != nil {
			t.Fatalf("waited although ending the 10 MB call frees nothing: %v", err)
		}
	}
}

func TestMemoryGuard_AdmitResumesWhenMemoryDrops(t *testing.T) {
	g := newFakeGuard(t)
	g.track(100, "running", nil)
	g.mem[100] = 800 << 20
	g.used.Store(testLimit * 90 / 100)
	g.tick()

	admitted := make(chan error, 1)
	go func() { _, err := g.admit(t.Context()); admitted <- err }()
	time.Sleep(50 * time.Millisecond)
	g.used.Store(testLimit / 2)
	g.tick()
	select {
	case err := <-admitted:
		if err != nil {
			t.Fatalf("admit: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("memory dropped below the soft threshold but the call still waits")
	}
}

func TestMemoryGuard_AdmitStopsWaitingWhenTheCallIsCancelled(t *testing.T) {
	g := newFakeGuard(t)
	g.track(100, "running", nil)
	g.mem[100] = 800 << 20
	g.used.Store(testLimit * 90 / 100)
	g.tick()
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if _, err := g.admit(ctx); err == nil {
		t.Fatal("admit returned nil after its context ended")
	}
}

func TestMemoryGuard_AdmitsAtOnceWhenNoCallRuns(t *testing.T) {
	g := newFakeGuard(t)
	g.used.Store(testLimit * 99 / 100)
	g.tick()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if _, err := g.admit(ctx); err != nil {
		t.Fatalf("a full pod with no running call must still admit: %v", err)
	}
}

func TestCallMembers_IncludesTheProcessGroupAndReparentedChildren(t *testing.T) {
	procs := map[int]procCPU{
		1:   {ppid: 0, pgrp: 1},
		100: {ppid: 1, pgrp: 100},
		101: {ppid: 100, pgrp: 100},
		102: {ppid: 1, pgrp: 100},
		200: {ppid: 1, pgrp: 200},
	}
	got := callMembers(procs, []int{100, 300})
	members := map[int]bool{}
	for _, pid := range got[100] {
		members[pid] = true
	}
	if len(members) != 3 || !members[100] || !members[101] || !members[102] {
		t.Fatalf("members of 100 = %v, want 100, its child 101 and the reparented 102", got[100])
	}
	if _, ok := got[300]; ok {
		t.Fatal("a call with no live process must be absent")
	}
}

func TestParseStatusMemory_CountsAnonymousAndSharedMemoryOnly(t *testing.T) {
	status := "Name:\tpython3\nVmRSS:\t  300000 kB\nRssAnon:\t  200000 kB\nRssFile:\t   90000 kB\nRssShmem:\t   10000 kB\n"
	if got := parseStatusMemory(status); got != (200000+10000)<<10 {
		t.Fatalf("memory = %d, want RssAnon + RssShmem", got)
	}
}

func TestParseProcStatPgrp(t *testing.T) {
	line := "1234 (my tool (v2)) S 1 1200 1200 0 -1 4194560 100 0 0 0 37 5"
	if got := parseProcStatPgrp(line); got != 1200 {
		t.Fatalf("pgrp = %d, want 1200", got)
	}
	if got := parseProcStatPgrp("1 (init) S 0"); got != 0 {
		t.Fatalf("truncated line pgrp = %d, want 0", got)
	}
}

func TestUnreclaimableFromStat_ReadsBothCgroupLayouts(t *testing.T) {
	v2 := "anon 4000\nfile 3000\nkernel 100\nshmem 500\nactive_file 2000\ninactive_file 500\n"
	if got := unreclaimableFromStat(10000, v2, cgroupV2StatKeys); got != 7500 {
		t.Fatalf("v2 = %d, want 10000 - (3000 - 500)", got)
	}
	v1 := "cache 99\nrss 4000\nshmem 99\ntotal_cache 3000\ntotal_rss 4000\ntotal_shmem 500\n"
	if got := unreclaimableFromStat(10000, v1, cgroupV1StatKeys); got != 7500 {
		t.Fatalf("v1 = %d, want 10000 - (3000 - 500)", got)
	}
	if got := unreclaimableFromStat(100, v2, cgroupV2StatKeys); got != 0 {
		t.Fatalf("result = %d, must never be negative", got)
	}
}

func TestParseCgroupStat(t *testing.T) {
	stat := "anon 4096\nfile 8192\ninactive_file 2048\nactive_file 6144\n"
	if got := parseCgroupStat(stat, "inactive_file"); got != 2048 {
		t.Fatalf("inactive_file = %d", got)
	}
	if got := parseCgroupStat(stat, "missing"); got != 0 {
		t.Fatalf("missing key = %d", got)
	}
}

func TestSandboxConfig_MemoryThresholdsDefault(t *testing.T) {
	c := SandboxConfig{}
	c.setDefaults()
	if c.MemorySoftPercent != defaultMemorySoftPercent || c.MemoryHardPercent != defaultMemoryHardPercent {
		t.Fatalf("thresholds = %d/%d, want the defaults", c.MemorySoftPercent, c.MemoryHardPercent)
	}
}

func TestExecute_MemoryGuardStopsTheLargestCall(t *testing.T) {
	skipOnWindows(t)
	if _, ok := procCallMemory(nil); !ok {
		t.Skip("needs readable /proc process accounting")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("needs python3")
	}
	dir := t.TempDir()
	big := writeScript(t, dir, "big.sh", python+` -c "b = b'x' * (200 << 20); import time; time.sleep(20)"`)
	small := writeScript(t, dir, "small.sh", `sleep 2; echo '{"result":"small"}' > result.json`)

	r := newComputeQueueRunnerWith(t, 2, 2)
	var stopped atomic.Bool
	g := newMemoryGuard(testLimit, 85, 95, slog.Default(), nil)
	g.readLimit = func() int64 { return testLimit }
	g.readUsage = func() (int64, bool) {
		if stopped.Load() {
			return testLimit / 2, true
		}
		return testLimit * 96 / 100, true
	}
	g.kill = func(pid int) error {
		stopped.Store(true)
		return killToolTree(pid)
	}
	r.memGuard = g
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	bigDone := startExecute(ctx, r, minimalParams(), big)
	smallDone := startExecute(ctx, r, minimalParams(), small)
	waitForTracked(t, g, 2)
	time.Sleep(500 * time.Millisecond)
	go g.run(ctx, memoryGuardTick)

	select {
	case bigErr := <-bigDone:
		if bigErr == nil || bigErr.Code != remotetool.ErrToolMemoryExceeded {
			t.Fatalf("big call error = %+v, want %s", bigErr, remotetool.ErrToolMemoryExceeded)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the guard did not stop the call that holds the most memory")
	}
	if smallErr := <-smallDone; smallErr != nil {
		t.Fatalf("small call failed: code=%s msg=%q", smallErr.Code, smallErr.Message)
	}
}

func waitForTracked(t *testing.T, g *memoryGuard, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		g.mu.Lock()
		got := len(g.running)
		g.mu.Unlock()
		if got >= n {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("fewer than %d tool processes started", n)
}

type recordingHandler struct {
	onWarn func(msg string)
}

func (h *recordingHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *recordingHandler) Handle(_ context.Context, r slog.Record) error {
	if r.Level == slog.LevelWarn {
		h.onWarn(r.Message)
	}
	return nil
}

func (h *recordingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *recordingHandler) WithGroup(string) slog.Handler      { return h }

func TestAcquireInFlightWithin_FailsWithMemoryExceededWhenTheWaitRunsOut(t *testing.T) {
	r := newComputeQueueRunnerWith(t, 2, 2)
	g := newFakeGuard(t)
	g.track(100, "running", nil)
	g.mem[100] = 800 << 20
	g.used.Store(testLimit * 90 / 100)
	g.tick()
	r.memGuard = g.memoryGuard

	lease, err := r.acquireInFlightWithin(t.Context(), "waiting", 50*time.Millisecond)
	if lease != nil || err == nil || err.Code != remotetool.ErrToolMemoryExceeded {
		t.Fatalf("lease=%v err=%+v, want %s once the memory wait runs out", lease, err, remotetool.ErrToolMemoryExceeded)
	}
	if got := len(r.inflightSem); got != 0 {
		t.Fatalf("%d in-flight slots still held after the call gave up", got)
	}
}
