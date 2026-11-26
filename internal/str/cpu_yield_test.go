// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"testing"
	"time"
)

func TestParseProcStat(t *testing.T) {
	cases := []struct {
		name      string
		line      string
		wantPPID  int
		wantRun   bool
		wantTicks uint64
		wantOK    bool
	}{
		{"sleeping", "1234 (python3) S 1 1234 1234 0 -1 4194560 100 0 0 0 37 5 0 0 20 0 1 0 100 0 0 0", 1, false, 42, true},
		{"runnable, comm with spaces and parens", "77 (my tool (v2)) R 42 900 900 0 -1 0 0 0 0 0 10 2 0 0 20 0 1 0 5 0 0 0", 42, true, 12, true},
		{"truncated", "1 (init) S 0 1", 0, false, 0, false},
		{"garbage", "not a stat line", 0, false, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ppid, run, ticks, ok := parseProcStat(tc.line)
			if ppid != tc.wantPPID || run != tc.wantRun || ticks != tc.wantTicks || ok != tc.wantOK {
				t.Errorf("parseProcStat = (%d, %v, %d, %v), want (%d, %v, %d, %v)", ppid, run, ticks, ok, tc.wantPPID, tc.wantRun, tc.wantTicks, tc.wantOK)
			}
		})
	}
}

func TestSumTreeCPU_FollowsParentChainNotProcessGroup(t *testing.T) {
	procs := map[int]procCPU{
		1:   {ppid: 0, ticks: 999, runnable: true},
		100: {ppid: 1, ticks: 1},
		101: {ppid: 100, ticks: 2},
		102: {ppid: 101, ticks: 40, runnable: true},
		103: {ppid: 102, ticks: 7},
		200: {ppid: 1, ticks: 5},
		201: {ppid: 200, ticks: 3},
	}
	got := sumTreeCPU(procs, []int{100, 200, 300})
	if got[100].ticks != 50 || !got[100].runnable {
		t.Errorf("tree 100 = %+v, want 50 ticks, runnable", got[100])
	}
	if got[200].ticks != 8 || got[200].runnable {
		t.Errorf("tree 200 = %+v, want 8 ticks, parked", got[200])
	}
	if _, alive := got[300]; alive {
		t.Error("exited root 300 must be absent")
	}
}

type scriptedCPU struct {
	steps []map[int]treeSample
	i     int
}

func parked(ticks uint64) treeSample { return treeSample{ticks: ticks} }

func busy(ticks uint64) treeSample { return treeSample{ticks: ticks, runnable: true} }

func (f *scriptedCPU) sample(roots []int) (map[int]treeSample, bool) {
	if roots == nil {
		return nil, true
	}
	if f.i < len(f.steps) {
		f.i++
		return f.steps[f.i-1], true
	}
	return f.steps[len(f.steps)-1], true
}

func newWatchedLease(t *testing.T, budget int) (*SandboxRunner, *slotLease) {
	t.Helper()
	r := &SandboxRunner{compute: newComputeBudget(budget), inflightSem: make(chan struct{}, 8)}
	lease, err := acquireSlots(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	return r, lease
}

func TestCPUIdleWatcher_YieldDoesNotWedgeBehindResumeCompute(t *testing.T) {
	r, leaseY := newWatchedLease(t, 1)
	leaseX, err := r.acquireInFlight(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	resumed := make(chan bool, 1)
	go func() { resumed <- leaseX.ResumeCompute(t.Context()) }()
	waitForComputeWaiters(t, r, 1)

	cpu := &scriptedCPU{steps: []map[int]treeSample{{1: parked(5)}, {1: parked(5)}, {1: parked(5)}}}
	w := newCPUIdleWatcher(cpu.sample)
	w.watch(1, leaseX)
	w.watch(2, leaseY)
	stepped := make(chan struct{})
	go func() {
		now := time.Now()
		for i := 0; i < 3; i++ {
			w.step(now.Add(time.Duration(i) * 2 * time.Second))
		}
		close(stepped)
	}()
	select {
	case <-stepped:
	case <-time.After(2 * time.Second):
		t.Fatal("watcher tick wedged behind a lease waiting in ResumeCompute")
	}

	unwatched := make(chan struct{})
	go func() {
		w.unwatch(2)
		leaseY.Release()
		close(unwatched)
	}()
	select {
	case <-unwatched:
	case <-time.After(2 * time.Second):
		t.Fatal("finishing tool could not unwatch and release")
	}
	select {
	case got := <-resumed:
		if !got {
			t.Fatal("ResumeCompute returned without the slot although one was released")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ResumeCompute never got the released slot")
	}
	leaseX.Release()
}

func TestCPUIdleWatcher_YieldsAfterImportBurst(t *testing.T) {
	r, lease := newWatchedLease(t, 1)
	cpu := &scriptedCPU{steps: []map[int]treeSample{{7: busy(0)}, {7: busy(30)}, {7: busy(60)}, {7: parked(61)}, {7: parked(61)}}}
	w := newCPUIdleWatcher(cpu.sample)
	yields := 0
	w.onYield = func() { yields++ }
	w.watch(7, lease)

	now := time.Now()
	for i := 0; i < 3; i++ {
		w.step(now)
	}
	if !lease.holdsCPU {
		t.Fatal("slot yielded while the tool was still burning CPU")
	}
	w.step(now)
	if lease.holdsCPU {
		t.Fatal("slot should be released on the first quiet sample following CPU activity")
	}
	if yields != 1 {
		t.Fatalf("yields = %d, want 1", yields)
	}
	if _, held, _ := r.compute.snapshot(); held != 0 {
		t.Fatalf("compute held = %d after yield, want 0", held)
	}
	w.mu.Lock()
	_, still := w.watched[7]
	w.mu.Unlock()
	if !still {
		t.Fatal("yielded tool must stay watched so a later re-acquired slot yields too")
	}
}

func TestCPUIdleWatcher_YieldsAgainAfterResume(t *testing.T) {
	r, lease := newWatchedLease(t, 1)
	cpu := &scriptedCPU{steps: []map[int]treeSample{
		{7: busy(30)}, {7: parked(31)}, {7: parked(31)},
		{7: busy(80)}, {7: parked(81)}, {7: parked(81)},
	}}
	w := newCPUIdleWatcher(cpu.sample)
	yields := 0
	w.onYield = func() { yields++ }
	w.watch(7, lease)

	now := time.Now()
	w.step(now)
	w.step(now)
	if lease.holdsCPU || yields != 1 {
		t.Fatalf("after first park: holdsCPU=%v yields=%d, want released once", lease.holdsCPU, yields)
	}
	w.step(now)
	if yields != 1 {
		t.Fatalf("a parked tool holding nothing counted as a yield: yields=%d", yields)
	}
	if !lease.ResumeCompute(t.Context()) {
		t.Fatal("ResumeCompute did not re-take the free slot")
	}
	w.step(now)
	if !lease.holdsCPU {
		t.Fatal("slot yielded while the resumed tool was burning CPU")
	}
	w.step(now)
	if lease.holdsCPU || yields != 2 {
		t.Fatalf("after second park: holdsCPU=%v yields=%d, want released twice", lease.holdsCPU, yields)
	}
	if _, held, _ := r.compute.snapshot(); held != 0 {
		t.Fatalf("compute held = %d after second yield, want 0", held)
	}
}

func TestCPUIdleWatcher_FreshProcessNotMistakenForIdle(t *testing.T) {
	_, lease := newWatchedLease(t, 1)
	cpu := &scriptedCPU{steps: []map[int]treeSample{{7: parked(0)}, {7: parked(0)}, {7: parked(0)}, {7: parked(0)}}}
	w := newCPUIdleWatcher(cpu.sample)
	w.watch(7, lease)
	now := time.Now()
	for i := 0; i < 4; i++ {
		w.step(now)
	}
	if !lease.holdsCPU {
		t.Fatal("a young process with no observed CPU must keep its slot")
	}
	w.step(now.Add(minAgeBeforeIdleYield))
	if lease.holdsCPU {
		t.Fatal("after the minimum age a quiet sample should yield even with no CPU ever seen")
	}
}

func TestCPUIdleWatcher_ShortCPUThenParkYieldsOnFirstSample(t *testing.T) {
	_, lease := newWatchedLease(t, 1)
	cpu := &scriptedCPU{steps: []map[int]treeSample{{7: parked(1)}, {7: parked(1)}}}
	w := newCPUIdleWatcher(cpu.sample)
	w.watch(7, lease)
	w.step(time.Now())
	if lease.holdsCPU {
		t.Fatal("a process that has run and is already quiet should yield on its first sample")
	}
}

func TestCPUIdleWatcher_StarvedImportKeepsItsSlot(t *testing.T) {
	_, lease := newWatchedLease(t, 1)
	cpu := &scriptedCPU{steps: []map[int]treeSample{{7: busy(1)}, {7: busy(1)}, {7: busy(2)}, {7: busy(2)}}}
	w := newCPUIdleWatcher(cpu.sample)
	w.watch(7, lease)
	now := time.Now()
	for i := 0; i < 4; i++ {
		w.step(now.Add(time.Duration(i) * 2 * time.Second))
	}
	if !lease.holdsCPU {
		t.Fatal("a runnable tree must keep its compute slot however little CPU it has managed to get")
	}
}

func TestCPUIdleWatcher_ReleaseAfterYieldFreesOnlyInFlight(t *testing.T) {
	r, lease := newWatchedLease(t, 1)
	cpu := &scriptedCPU{steps: []map[int]treeSample{{7: busy(10)}, {7: busy(50)}, {7: parked(50)}, {7: parked(50)}}}
	w := newCPUIdleWatcher(cpu.sample)
	w.watch(7, lease)
	now := time.Now()
	for i := 0; i < 4; i++ {
		w.step(now)
	}
	if lease.holdsCPU {
		t.Fatal("expected yield")
	}
	w.unwatch(7)
	lease.Release()
	if _, held, _ := r.compute.snapshot(); held != 0 {
		t.Fatalf("compute held = %d, want 0", held)
	}
	if len(r.inflightSem) != 0 {
		t.Fatalf("inflight held = %d, want 0", len(r.inflightSem))
	}
}

func waitForComputeWaiters(t *testing.T, r *SandboxRunner, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, _, waiting := r.compute.snapshot(); waiting >= n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("no goroutine reached the compute budget wait within the deadline")
}

func TestCPUIdleWatcher_LateWatchSurvivesTheTickItMissed(t *testing.T) {
	_, leaseA := newWatchedLease(t, 2)
	_, leaseB := newWatchedLease(t, 2)
	w := newCPUIdleWatcher(func([]int) (map[int]treeSample, bool) { return nil, true })
	w.watch(7, leaseA)
	w.watch(8, leaseB)

	w.advance([]int{7}, map[int]treeSample{}, time.Now())

	w.mu.Lock()
	_, aStill := w.watched[7]
	_, bStill := w.watched[8]
	w.mu.Unlock()
	if aStill {
		t.Fatal("root 7 was sampled and found gone, it should have been dropped")
	}
	if !bStill {
		t.Fatal("root 8 was not part of this round and must still be watched")
	}
}
