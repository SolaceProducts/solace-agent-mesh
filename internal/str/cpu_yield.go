// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	cpuYieldTick = 100 * time.Millisecond

	idleTicksToYield = 1

	idleCPUPerTick = 1

	minAgeBeforeIdleYield = time.Second
)

type treeSample struct {
	ticks    uint64
	runnable bool
}

type procTreeCPUFunc func(roots []int) (samples map[int]treeSample, ok bool)

type cpuWatchEntry struct {
	lease     *slotLease
	started   time.Time
	lastTicks uint64
	haveLast  bool
	sawCPU    bool
	idleTicks int
}

type cpuIdleWatcher struct {
	mu           sync.Mutex
	watched      map[int]*cpuWatchEntry
	sample       procTreeCPUFunc
	onYield      func()
	logger       *slog.Logger
	sampleFailed bool
}

func newCPUIdleWatcher(sample procTreeCPUFunc) *cpuIdleWatcher {
	return &cpuIdleWatcher{watched: make(map[int]*cpuWatchEntry), sample: sample}
}

func (w *cpuIdleWatcher) watch(pid int, lease *slotLease) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.watched[pid] = &cpuWatchEntry{lease: lease, started: time.Now()}
}

func (w *cpuIdleWatcher) unwatch(pid int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.watched, pid)
}

func (w *cpuIdleWatcher) run(ctx context.Context, tick time.Duration) {
	if _, ok := w.sample(nil); !ok {
		return
	}
	ticker := time.NewTicker(tick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			w.step(now)
		}
	}
}

func (w *cpuIdleWatcher) step(now time.Time) {
	w.mu.Lock()
	roots := make([]int, 0, len(w.watched))
	for pid := range w.watched {
		roots = append(roots, pid)
	}
	w.mu.Unlock()
	if len(roots) == 0 {
		return
	}
	samples, ok := w.sample(roots)
	if !ok {
		if !w.sampleFailed && w.logger != nil {
			w.logger.Warn("process accounting became unreadable; running tools keep their compute slot for the whole run")
		}
		w.sampleFailed = true
		return
	}
	if w.sampleFailed && w.logger != nil {
		w.logger.Info("process accounting readable again; compute slots yield on idle")
	}
	w.sampleFailed = false
	for _, lease := range w.advance(roots, samples, now) {
		if lease.YieldCompute() && w.onYield != nil {
			w.onYield()
		}
	}
}

func (w *cpuIdleWatcher) advance(roots []int, samples map[int]treeSample, now time.Time) []*slotLease {
	w.mu.Lock()
	defer w.mu.Unlock()
	var quiet []*slotLease
	for _, pid := range roots {
		e, watched := w.watched[pid]
		if !watched {
			continue
		}
		cur, alive := samples[pid]
		if !alive {
			delete(w.watched, pid)
			continue
		}
		total := cur.ticks
		if e.haveLast {
			delta := total - e.lastTicks
			if total < e.lastTicks {
				delta = 0
			}
			if delta > idleCPUPerTick || cur.runnable {
				e.sawCPU = e.sawCPU || delta > 0
				e.idleTicks = 0
			} else {
				e.idleTicks++
			}
		} else if total > 0 {
			e.sawCPU = true
			if total <= idleCPUPerTick && !cur.runnable {
				e.idleTicks++
			}
		}
		e.lastTicks, e.haveLast = total, true
		if e.idleTicks >= idleTicksToYield && (e.sawCPU || now.Sub(e.started) >= minAgeBeforeIdleYield) {
			quiet = append(quiet, e.lease)
		}
	}
	return quiet
}

type procCPU struct {
	ppid     int
	ticks    uint64
	runnable bool
	pgrp     int
}

func sumTreeCPU(procs map[int]procCPU, roots []int) map[int]treeSample {
	children := make(map[int][]int, len(procs))
	for pid, p := range procs {
		children[p.ppid] = append(children[p.ppid], pid)
	}
	out := make(map[int]treeSample, len(roots))
	for _, root := range roots {
		if _, alive := procs[root]; !alive {
			continue
		}
		var tree treeSample
		stack := []int{root}
		for len(stack) > 0 {
			pid := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			tree.ticks += procs[pid].ticks
			tree.runnable = tree.runnable || procs[pid].runnable
			stack = append(stack, children[pid]...)
		}
		out[root] = tree
	}
	return out
}

func parseProcStat(line string) (ppid int, runnable bool, cpuTicks uint64, ok bool) {
	end := strings.LastIndexByte(line, ')')
	if end < 0 || end+1 >= len(line) {
		return 0, false, 0, false
	}
	f := strings.Fields(line[end+1:])
	if len(f) < 13 {
		return 0, false, 0, false
	}
	ppid, err := strconv.Atoi(f[1])
	if err != nil {
		return 0, false, 0, false
	}
	ut, err1 := strconv.ParseUint(f[11], 10, 64)
	st, err2 := strconv.ParseUint(f[12], 10, 64)
	if err1 != nil || err2 != nil {
		return 0, false, 0, false
	}
	return ppid, f[0] == "R", ut + st, true
}
