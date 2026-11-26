// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"cmp"
	"context"
	"errors"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

const (
	defaultMemorySoftPercent = 85
	defaultMemoryHardPercent = 95

	memoryGuardTick = 50 * time.Millisecond

	memoryPressureClearMarginPercent = 2

	memoryScanInterval      = time.Second
	memoryScanGrowthPercent = 1

	memoryFreeingGrace = 500 * time.Millisecond
)

type memoryCall struct {
	tool     string
	log      *slog.Logger
	killed   atomic.Bool
	freeing  int64
	killedAt time.Time
}

type memoryGuard struct {
	limit       int64
	soft        int64
	hard        int64
	softPercent int64
	hardPercent int64

	readLimit  func() int64
	readUsage  func() (int64, bool)
	callMemory func(roots []int) (map[int]int64, bool)
	kill       func(pid int) error
	now        func() time.Time
	logger     *slog.Logger
	metrics    *strMetrics

	lastUsed         int64
	lastScan         time.Time
	lastScanUsed     int64
	toolsNotTheCause bool

	mu           sync.Mutex
	running      map[int]*memoryCall
	pending      int
	pressure     bool
	outsideTools bool
	relief       chan struct{}
}

func newMemoryGuard(limit int64, softPercent, hardPercent int, logger *slog.Logger, m *strMetrics) *memoryGuard {
	if limit <= 0 || !validMemoryThresholds(softPercent, hardPercent) {
		return nil
	}
	if logger == nil {
		logger = slog.Default()
	}
	g := &memoryGuard{
		softPercent: int64(softPercent),
		hardPercent: int64(hardPercent),
		readLimit:   cgroupMemoryLimitBytes,
		readUsage:   cgroupMemoryUnreclaimableBytes,
		callMemory:  procCallMemory,
		kill:        killToolTree,
		now:         time.Now,
		logger:      logger,
		metrics:     m,
		running:     make(map[int]*memoryCall),
		relief:      make(chan struct{}),
	}
	g.setLimit(limit)
	return g
}

func validMemoryThresholds(softPercent, hardPercent int) bool {
	return softPercent > 0 && softPercent <= hardPercent && hardPercent <= 100
}

func (g *memoryGuard) setLimit(limit int64) {
	g.limit = limit
	g.soft = limit * g.softPercent / 100
	g.hard = limit * g.hardPercent / 100
}

func (g *memoryGuard) refreshLimit() {
	if limit := g.readLimit(); limit > 0 && limit != g.limit {
		g.logger.Info("memory guard: container memory limit changed", "fromMB", g.limit>>20, "toMB", limit>>20)
		g.setLimit(limit)
	}
}

func killToolTree(pid int) error {
	descendants := processDescendants(pid)
	err := killProcessGroup(pid)
	killProcesses(descendants)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

func (g *memoryGuard) track(pid int, tool string, log *slog.Logger) *memoryCall {
	if g == nil {
		return nil
	}
	if log == nil {
		log = g.logger
	}
	c := &memoryCall{tool: tool, log: log}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.running[pid] = c
	return c
}

func (g *memoryGuard) untrack(pid int) {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.running, pid)
	g.wakeLocked()
}

func (g *memoryGuard) wakeLocked() {
	close(g.relief)
	g.relief = make(chan struct{})
}

func (g *memoryGuard) admit(ctx context.Context) (done func(), err error) {
	if g == nil {
		return func() {
		}, nil
	}
	for {
		g.mu.Lock()
		if !g.pressure || g.outsideTools || len(g.running)+g.pending == 0 {
			g.pending++
			g.mu.Unlock()
			var once sync.Once
			return func() { once.Do(g.admitted) }, nil
		}
		relief := g.relief
		g.mu.Unlock()
		select {
		case <-relief:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func (g *memoryGuard) admitted() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.pending--
	g.wakeLocked()
}

func (g *memoryGuard) run(ctx context.Context, tick time.Duration) {
	if _, ok := g.readUsage(); !ok {
		g.logger.Info("memory guard disabled: cgroup memory not readable")
		return
	}
	ticker := time.NewTicker(tick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			g.step()
		}
	}
}

func (g *memoryGuard) step() {
	g.refreshLimit()
	used, ok := g.readUsage()
	if !ok {
		return
	}
	projected := used
	if g.lastUsed > 0 {
		projected += max(used-g.lastUsed, 0)
	}
	g.lastUsed = used
	pressure := g.updatePressure(used)
	if !pressure && projected < g.hard {
		return
	}

	now := g.now()
	freeing, roots, stale := g.killCandidates(now)
	for _, pid := range stale {
		g.stop(pid)
	}
	killPlausible := projected-freeing >= g.hard && len(roots) > 0
	if !g.scanDue(now, used) && (!killPlausible || g.toolsNotTheCause) {
		return
	}
	if len(roots) == 0 && freeing == 0 {
		g.setOutsideTools(used >= g.soft)
		return
	}
	mem, ok := g.callMemory(roots)
	if !ok {
		return
	}
	g.lastScan, g.lastScanUsed = now, used
	held := freeing
	for _, size := range mem {
		held += size
	}
	notTools := used - held
	g.setOutsideTools(notTools >= g.soft)
	if !killPlausible {
		return
	}
	target, stop := g.killTarget(notTools)
	if !stop {
		return
	}
	expected := projected - freeing
	for _, pid := range largestFirst(roots, mem) {
		if expected < target {
			return
		}
		size := mem[pid]
		c := g.markKilled(pid, size, now)
		if c == nil {
			continue
		}
		g.stop(pid)
		expected -= size
		c.log.Warn("memory guard stopped a tool call to keep the STR under its memory limit",
			"tool", c.tool, "toolMemoryMB", size>>20, "usedMB", used>>20, "projectedMB", projected>>20, "limitMB", g.limit>>20)
		g.metrics.recordMemoryKill(c.tool)
	}
}

func (g *memoryGuard) stop(pid int) {
	if err := g.kill(pid); err != nil {
		g.logger.Warn("memory guard could not stop a tool call", "pid", pid, "error", err)
	}
}

func (g *memoryGuard) scanDue(now time.Time, used int64) bool {
	return now.Sub(g.lastScan) >= memoryScanInterval ||
		used-g.lastScanUsed >= g.limit*memoryScanGrowthPercent/100
}

func (g *memoryGuard) killTarget(notTools int64) (target int64, stop bool) {
	if notTools >= g.hard {
		if !g.toolsNotTheCause {
			g.logger.Warn("memory guard: the STR is near its memory limit but tool calls do not hold the memory; no call is stopped",
				"notToolsMB", notTools>>20, "hardMB", g.hard>>20, "limitMB", g.limit>>20)
		}
		g.toolsNotTheCause = true
		return 0, false
	}
	g.toolsNotTheCause = false
	if notTools >= g.soft {
		return g.hard, true
	}
	return g.soft, true
}

func (g *memoryGuard) markKilled(pid int, size int64, now time.Time) *memoryCall {
	g.mu.Lock()
	defer g.mu.Unlock()
	c := g.running[pid]
	if c == nil || c.killed.Load() {
		return nil
	}
	c.killed.Store(true)
	c.freeing = size
	c.killedAt = now
	return c
}

func (g *memoryGuard) setOutsideTools(on bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if on && !g.outsideTools {
		g.wakeLocked()
	}
	g.outsideTools = on
}

func (g *memoryGuard) updatePressure(used int64) bool {
	clearBelow := g.soft - g.limit*memoryPressureClearMarginPercent/100
	g.mu.Lock()
	was := g.pressure
	switch {
	case !was && used >= g.soft:
		g.pressure = true
	case was && used < clearBelow:
		g.pressure = false
		g.outsideTools = false
		g.wakeLocked()
	}
	on := g.pressure
	g.mu.Unlock()
	if on == was {
		return on
	}
	g.logger.Debug("memory guard: pressure changed", "pressure", on,
		"usedMB", used>>20, "softMB", g.soft>>20, "limitMB", g.limit>>20)
	g.metrics.recordMemoryPressure(on)
	return on
}

func (g *memoryGuard) killCandidates(now time.Time) (freeing int64, roots, stale []int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	roots = make([]int, 0, len(g.running))
	for pid, c := range g.running {
		if !c.killed.Load() {
			roots = append(roots, pid)
			continue
		}
		if now.Sub(c.killedAt) >= memoryFreeingGrace {
			stale = append(stale, pid)
			continue
		}
		freeing += c.freeing
	}
	return freeing, roots, stale
}

func largestFirst(roots []int, mem map[int]int64) []int {
	out := make([]int, 0, len(roots))
	for _, root := range roots {
		if _, ok := mem[root]; ok {
			out = append(out, root)
		}
	}
	slices.SortFunc(out, func(a, b int) int { return cmp.Compare(mem[b], mem[a]) })
	return out
}

func callMembers(procs map[int]procCPU, roots []int) map[int][]int {
	out := make(map[int][]int, len(roots))
	for _, root := range roots {
		seen := map[int]bool{}
		if _, alive := procs[root]; alive {
			seen[root] = true
		}
		for _, pid := range descendantsOf(procs, root) {
			seen[pid] = true
		}
		for pid, p := range procs {
			if p.pgrp == root {
				seen[pid] = true
			}
		}
		if len(seen) == 0 {
			continue
		}
		members := make([]int, 0, len(seen))
		for pid := range seen {
			members = append(members, pid)
		}
		out[root] = members
	}
	return out
}

func parseStatusMemory(contents string) int64 {
	var total int64
	for line := range strings.SplitSeq(contents, "\n") {
		name, value, ok := strings.Cut(line, ":")
		if !ok || (name != "RssAnon" && name != "RssShmem") {
			continue
		}
		kb, err := strconv.ParseInt(strings.TrimSuffix(strings.TrimSpace(value), " kB"), 10, 64)
		if err == nil && kb > 0 {
			total += kb << 10
		}
	}
	return total
}

func parseProcStatPgrp(line string) int {
	end := strings.LastIndexByte(line, ')')
	if end < 0 {
		return 0
	}
	f := strings.Fields(line[end+1:])
	const pgrpField = 2
	if len(f) <= pgrpField {
		return 0
	}
	v, err := strconv.Atoi(f[pgrpField])
	if err != nil {
		return 0
	}
	return v
}

func parseCgroupStat(contents, key string) int64 {
	for line := range strings.SplitSeq(contents, "\n") {
		name, value, ok := strings.Cut(line, " ")
		if !ok || name != key {
			continue
		}
		v, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if err != nil || v < 0 {
			return 0
		}
		return v
	}
	return 0
}

func descendantsOf(procs map[int]procCPU, root int) []int {
	children := make(map[int][]int, len(procs))
	for pid, p := range procs {
		children[p.ppid] = append(children[p.ppid], pid)
	}
	var found []int
	stack := append([]int(nil), children[root]...)
	for len(stack) > 0 {
		pid := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		found = append(found, pid)
		stack = append(stack, children[pid]...)
	}
	return found
}

type cgroupStatKeys struct{ file, shmem string }

var (
	cgroupV2StatKeys = cgroupStatKeys{file: "file", shmem: "shmem"}
	cgroupV1StatKeys = cgroupStatKeys{file: "total_cache", shmem: "total_shmem"}
)

func unreclaimableFromStat(usage int64, stat string, keys cgroupStatKeys) int64 {
	file, shmem := parseCgroupStat(stat, keys.file), parseCgroupStat(stat, keys.shmem)
	return max(usage-max(file-shmem, 0), 0)
}
