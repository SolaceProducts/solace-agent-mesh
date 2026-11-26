// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eventlog

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"time"
)

const (
	defaultMaxEventsPerTask = 10000
	defaultMaxPayloadBytes  = 1 << 20
)

type MemoryEventLog struct {
	mu               sync.Mutex
	tasks            map[string]*taskLog
	maxEventsPerTask int
	maxPayloadBytes  int
}

type taskLog struct {
	mu        sync.Mutex
	cond      *sync.Cond
	events    []Event
	nextSeq   int64
	completed bool
}

func NewMemoryEventLog() *MemoryEventLog {
	return &MemoryEventLog{
		tasks:            make(map[string]*taskLog),
		maxEventsPerTask: defaultMaxEventsPerTask,
		maxPayloadBytes:  defaultMaxPayloadBytes,
	}
}

func (m *MemoryEventLog) Append(_ context.Context, taskID, _, _, eventType string, payload []byte) (int64, error) {
	if m.maxPayloadBytes > 0 && len(payload) > m.maxPayloadBytes {
		return 0, fmt.Errorf("event payload too large (%d bytes, max %d)", len(payload), m.maxPayloadBytes)
	}

	tl := m.getOrCreate(taskID)

	tl.mu.Lock()
	defer tl.mu.Unlock()

	if tl.completed {
		return 0, fmt.Errorf("task %s: %w", taskID, ErrTaskCompleted)
	}

	if m.maxEventsPerTask > 0 && len(tl.events) >= m.maxEventsPerTask {
		return 0, fmt.Errorf("task %s exceeded max events (%d)", taskID, m.maxEventsPerTask)
	}

	seq := tl.nextSeq
	tl.nextSeq++

	tl.events = append(tl.events, Event{
		Sequence:  seq,
		TaskID:    taskID,
		EventType: eventType,
		Payload:   payload,
		Timestamp: time.Now().UnixMilli(),
	})

	tl.cond.Broadcast()
	return seq, nil
}

func (m *MemoryEventLog) Completed(_ context.Context, taskID string) (bool, error) {
	tl := m.get(taskID)
	if tl == nil {
		return false, nil
	}
	tl.mu.Lock()
	defer tl.mu.Unlock()
	return tl.completed, nil
}

func (m *MemoryEventLog) Subscribe(ctx context.Context, taskID string, fromSeq int64) (<-chan Event, error) {
	tl := m.get(taskID)
	if tl == nil {
		return nil, fmt.Errorf("%w: %s", ErrTaskNotFound, taskID)
	}

	ch := make(chan Event, 64)

	if fromSeq < 1 {
		fromSeq = 1
	}

	go m.subscriberLoop(ctx, tl, ch, fromSeq)

	return ch, nil
}

func (m *MemoryEventLog) subscriberLoop(ctx context.Context, tl *taskLog, ch chan<- Event, cursor int64) {
	defer close(ch)

	tl.mu.Lock()
	defer tl.mu.Unlock()

	for {
		for {
			idx := m.indexForSeqLocked(tl, cursor)
			if idx < 0 || idx >= len(tl.events) {
				break
			}
			evt := tl.events[idx]

			tl.mu.Unlock()
			select {
			case ch <- evt:
			case <-ctx.Done():
				tl.mu.Lock()
				return
			}
			tl.mu.Lock()
			cursor++
		}

		if tl.completed {
			return
		}

		if ctx.Err() != nil {
			return
		}

		waitDone := make(chan struct{})
		go func() {
			select {
			case <-ctx.Done():
				tl.mu.Lock()
				tl.cond.Broadcast()
				tl.mu.Unlock()
			case <-waitDone:
			}
		}()

		tl.cond.Wait()
		close(waitDone)

		if ctx.Err() != nil {
			return
		}
	}
}

func (m *MemoryEventLog) indexForSeqLocked(tl *taskLog, seq int64) int {
	if len(tl.events) == 0 {
		return -1
	}
	first := tl.events[0].Sequence
	idx := int(seq - first)
	if idx < 0 || idx >= len(tl.events) {
		return -1
	}
	return idx
}

func (m *MemoryEventLog) Replay(_ context.Context, taskID string, fromSeq int64) ([]Event, error) {
	tl := m.get(taskID)
	if tl == nil {
		return nil, nil
	}
	fromSeq = max(fromSeq, 1)

	tl.mu.Lock()
	defer tl.mu.Unlock()

	if len(tl.events) == 0 || fromSeq > tl.events[len(tl.events)-1].Sequence {
		return nil, nil
	}
	idx := max(m.indexForSeqLocked(tl, fromSeq), 0)

	out := make([]Event, 0, len(tl.events)-idx)
	for _, evt := range tl.events[idx:] {
		cp := make([]byte, len(evt.Payload))
		copy(cp, evt.Payload)
		evt.Payload = cp
		out = append(out, evt)
	}
	return out, nil
}

func (m *MemoryEventLog) Complete(_ context.Context, taskID string) error {
	tl := m.get(taskID)
	if tl == nil {
		return nil
	}

	tl.mu.Lock()
	defer tl.mu.Unlock()

	tl.completed = true
	tl.cond.Broadcast()
	return nil
}

func (m *MemoryEventLog) Cleanup(_ context.Context, taskID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.tasks, taskID)
	return nil
}

func (m *MemoryEventLog) Forget(string) {
}

func (m *MemoryEventLog) getOrCreate(taskID string) *taskLog {
	m.mu.Lock()
	defer m.mu.Unlock()

	tl, ok := m.tasks[taskID]
	if !ok {
		tl = &taskLog{nextSeq: 1}
		tl.cond = sync.NewCond(&tl.mu)
		m.tasks[taskID] = tl
	}
	return tl
}

func (m *MemoryEventLog) get(taskID string) *taskLog {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.tasks[taskID]
}

func (m *MemoryEventLog) FindSequenceAfterTimestamp(_ context.Context, taskID string, epochMs int64) (int64, error) {
	tl := m.get(taskID)
	if tl == nil {
		return 0, fmt.Errorf("%w: %s", ErrTaskNotFound, taskID)
	}

	tl.mu.Lock()
	defer tl.mu.Unlock()

	for _, evt := range tl.events {
		if evt.Timestamp > epochMs {
			return evt.Sequence, nil
		}
	}
	return tl.nextSeq, nil
}

func (m *MemoryEventLog) FindLastEventByDataType(_ context.Context, taskID string, dataType string) []byte {
	tl := m.get(taskID)
	if tl == nil {
		return nil
	}

	needle := []byte(`"type":"` + dataType + `"`)

	tl.mu.Lock()
	defer tl.mu.Unlock()

	for i := len(tl.events) - 1; i >= 0; i-- {
		evt := tl.events[i]
		if evt.EventType != "status_update" {
			continue
		}
		if bytes.Contains(evt.Payload, needle) {
			cp := make([]byte, len(evt.Payload))
			copy(cp, evt.Payload)
			return cp
		}
	}
	return nil
}

var _ TaskEventLog = (*MemoryEventLog)(nil)
