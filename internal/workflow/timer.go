// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"container/heap"
	"context"
	"log/slog"
	"sync"
	"time"
)

type TimerManager interface {
	Schedule(executionID string, timers []Timer)

	Cancel(executionID string)

	Start(ctx context.Context)
}

type TimerFireFunc func(executionID string, event Event)

type timerKey struct {
	Kind      TimerKind
	NodeID    string
	SubTaskID string
}

func timerKeyFor(t Timer) timerKey {
	return timerKey{Kind: t.Kind, NodeID: t.NodeID, SubTaskID: t.SubTaskID}
}

type timerEntry struct {
	executionID string
	key         timerKey
	mapNodeID   string
	loopNodeID  string
	itemIndex   int
	firesAt     time.Time
	duration    time.Duration
	index       int
	removed     bool
}

type timerHeap []*timerEntry

func (h timerHeap) Len() int           { return len(h) }
func (h timerHeap) Less(i, j int) bool { return h[i].firesAt.Before(h[j].firesAt) }
func (h timerHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}

func (h *timerHeap) Push(x any) {
	e := x.(*timerEntry)
	e.index = len(*h)
	*h = append(*h, e)
}

func (h *timerHeap) Pop() any {
	old := *h
	n := len(old)
	e := old[n-1]
	old[n-1] = nil
	e.index = -1
	*h = old[:n-1]
	return e
}

type inProcessTimerManager struct {
	logger *slog.Logger
	fire   TimerFireFunc

	mu    sync.Mutex
	heap  timerHeap
	byKey map[string]map[timerKey]*timerEntry
	wake  chan struct{}
}

func NewInProcessTimerManager(logger *slog.Logger, fire TimerFireFunc) *inProcessTimerManager {
	if logger == nil {
		logger = slog.Default()
	}
	return &inProcessTimerManager{
		logger: logger,
		fire:   fire,
		byKey:  make(map[string]map[timerKey]*timerEntry),
		wake:   make(chan struct{}, 1),
	}
}

func (m *inProcessTimerManager) signal() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

func (m *inProcessTimerManager) Schedule(executionID string, timers []Timer) {
	m.mu.Lock()
	defer m.mu.Unlock()

	target := make(map[timerKey]Timer, len(timers))
	for _, t := range timers {
		target[timerKeyFor(t)] = t
	}

	existing := m.byKey[executionID]
	if existing == nil {
		existing = make(map[timerKey]*timerEntry)
		m.byKey[executionID] = existing
	}

	for k, e := range existing {
		if _, ok := target[k]; !ok {
			e.removed = true
			delete(existing, k)
		}
	}

	for k, t := range target {
		cur, ok := existing[k]
		if ok && cur.firesAt.Equal(t.FiresAt) {
			cur.mapNodeID = t.MapNodeID
			cur.loopNodeID = t.LoopNodeID
			cur.itemIndex = t.ItemIndex
			cur.duration = t.Duration
			continue
		}
		if ok {
			cur.removed = true
		}
		ent := &timerEntry{
			executionID: executionID,
			key:         k,
			mapNodeID:   t.MapNodeID,
			loopNodeID:  t.LoopNodeID,
			itemIndex:   t.ItemIndex,
			firesAt:     t.FiresAt,
			duration:    t.Duration,
		}
		existing[k] = ent
		heap.Push(&m.heap, ent)
	}

	if len(existing) == 0 {
		delete(m.byKey, executionID)
	}
	m.signal()
}

func (m *inProcessTimerManager) Cancel(executionID string) {
	m.mu.Lock()
	for _, e := range m.byKey[executionID] {
		e.removed = true
	}
	delete(m.byKey, executionID)
	m.mu.Unlock()
	m.signal()
}

func (m *inProcessTimerManager) Start(ctx context.Context) {
	timer := time.NewTimer(time.Hour)
	if !timer.Stop() {
		<-timer.C
	}
	for {
		m.mu.Lock()
		for m.heap.Len() > 0 && m.heap[0].removed {
			heap.Pop(&m.heap)
		}
		var head *timerEntry
		if m.heap.Len() > 0 {
			head = m.heap[0]
		}
		m.mu.Unlock()

		if head == nil {
			select {
			case <-m.wake:
				continue
			case <-ctx.Done():
				return
			}
		}
		delay := time.Until(head.firesAt)
		if delay <= 0 {
			m.fireHead(ctx, head)
			continue
		}

		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(delay)
		select {
		case <-timer.C:
			m.fireHead(ctx, head)
		case <-m.wake:
		case <-ctx.Done():
			return
		}
	}
}

func (m *inProcessTimerManager) fireHead(ctx context.Context, head *timerEntry) {
	m.mu.Lock()
	for m.heap.Len() > 0 && m.heap[0].removed {
		heap.Pop(&m.heap)
	}
	if m.heap.Len() == 0 || m.heap[0] != head {
		m.mu.Unlock()
		return
	}
	heap.Pop(&m.heap)
	if execMap := m.byKey[head.executionID]; execMap != nil {
		if execMap[head.key] == head {
			delete(execMap, head.key)
		}
		if len(execMap) == 0 {
			delete(m.byKey, head.executionID)
		}
	}
	executionID := head.executionID
	event := eventFromTimer(head)
	m.mu.Unlock()

	if event == nil {
		m.logger.Warn("workflow timer fired with unknown kind",
			"executionID", executionID,
			"kind", head.key.Kind,
		)
		return
	}
	if m.fire != nil && ctx.Err() == nil {
		m.fire(executionID, event)
	}
}

func eventFromTimer(e *timerEntry) Event {
	switch e.key.Kind {
	case TimerKindRetry:
		return &EventRetryDue{NodeID: e.key.NodeID}
	case TimerKindNodeTimeout:
		return &EventNodeTimeout{
			NodeID:     e.key.NodeID,
			SubTaskID:  e.key.SubTaskID,
			Duration:   e.duration,
			MapNodeID:  e.mapNodeID,
			LoopNodeID: e.loopNodeID,
			ItemIndex:  e.itemIndex,
		}
	case TimerKindWorkflowTimeout:
		return &EventWorkflowTimeout{}
	case TimerKindWake:
		return &EventWake{NodeID: e.key.NodeID}
	default:
		return nil
	}
}
