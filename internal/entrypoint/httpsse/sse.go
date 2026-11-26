// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"log/slog"
	"sync"
	"time"
)

const taskShardCount = 16

const defaultOrphanGrace = 60 * time.Second

type SSEEvent struct {
	EventType string
	Data      []byte
	Final     bool
	Sequence  int64
}

type SSEManager struct {
	shards       [taskShardCount]taskShard
	maxQueueSize int
	orphanGrace  time.Duration
	logger       *slog.Logger

	onForget func(taskID string)

	userNotif *userNotifState
}

type taskShard struct {
	mu    sync.Mutex
	tasks map[string]*taskEntry
}

type taskEntry struct {
	subscribers map[uint64]chan *SSEEvent
	nextConnID  uint64
	closed      bool
	held        bool
	sheds       int
	sessionID   string
	userID      string
}

func (m *SSEManager) shardFor(taskID string) *taskShard {
	return &m.shards[shardIndex(taskID)]
}

func shardIndex(taskID string) uint32 {
	const (
		offset32 = 2166136261
		prime32  = 16777619
	)
	h := uint32(offset32)
	for i := range len(taskID) {
		h ^= uint32(taskID[i])
		h *= prime32
	}
	return h % taskShardCount
}

func NewSSEManager(maxQueueSize int, logger *slog.Logger) *SSEManager {
	if maxQueueSize <= 0 {
		maxQueueSize = 200
	}
	if logger == nil {
		logger = slog.Default()
	}
	m := &SSEManager{
		maxQueueSize: maxQueueSize,
		orphanGrace:  defaultOrphanGrace,
		logger:       logger,
		userNotif:    newUserNotifState(),
	}
	for i := range m.shards {
		m.shards[i].tasks = make(map[string]*taskEntry)
	}
	return m
}

func (m *SSEManager) SetForgetHook(fn func(taskID string)) {
	m.onForget = fn
}

func (m *SSEManager) notifyForget(taskID string) {
	if m.onForget != nil {
		m.onForget(taskID)
	}
}

func (m *SSEManager) RegisterTask(taskID, sessionID, userID string) {
	sh := m.shardFor(taskID)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	if _, ok := sh.tasks[taskID]; !ok {
		sh.tasks[taskID] = &taskEntry{
			subscribers: make(map[uint64]chan *SSEEvent),
			sessionID:   sessionID,
			userID:      userID,
		}
	}
}

func (m *SSEManager) SubscribeHeld(taskID string) (<-chan *SSEEvent, uint64, bool) {
	return m.subscribeOpen(taskID, true)
}

func (m *SSEManager) SubscribeOpen(taskID string) (<-chan *SSEEvent, uint64, bool) {
	return m.subscribeOpen(taskID, false)
}

func (m *SSEManager) subscribeOpen(taskID string, hold bool) (<-chan *SSEEvent, uint64, bool) {
	sh := m.shardFor(taskID)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	entry, ok := sh.tasks[taskID]
	if !ok {
		if !hold {
			return nil, 0, false
		}
		entry = &taskEntry{subscribers: make(map[uint64]chan *SSEEvent), held: true}
		sh.tasks[taskID] = entry
	}
	if entry.closed {
		return nil, 0, false
	}
	c := make(chan *SSEEvent, m.maxQueueSize)
	connID := entry.nextConnID
	entry.nextConnID++
	entry.subscribers[connID] = c
	return c, connID, true
}

func (m *SSEManager) TaskContext(taskID string) (sessionID, userID string) {
	sh := m.shardFor(taskID)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	if entry, ok := sh.tasks[taskID]; ok {
		return entry.sessionID, entry.userID
	}
	return "", ""
}

func (m *SSEManager) Subscribe(taskID string) (ch <-chan *SSEEvent, connID uint64, ok bool) {
	sh := m.shardFor(taskID)
	sh.mu.Lock()
	defer sh.mu.Unlock()

	entry, exists := sh.tasks[taskID]
	if !exists {
		return nil, 0, false
	}

	c := make(chan *SSEEvent, m.maxQueueSize)
	connID = entry.nextConnID
	entry.nextConnID++
	entry.subscribers[connID] = c

	if entry.closed {
		close(c)
	}

	return c, connID, true
}

func (m *SSEManager) Unsubscribe(taskID string, connID uint64) {
	sh := m.shardFor(taskID)
	sh.mu.Lock()

	entry, exists := sh.tasks[taskID]
	if !exists {
		sh.mu.Unlock()
		return
	}

	delete(entry.subscribers, connID)

	forget := entry.closed && len(entry.subscribers) == 0
	if forget || (entry.held && len(entry.subscribers) == 0) {
		delete(sh.tasks, taskID)
	}
	sh.mu.Unlock()

	if forget {
		m.notifyForget(taskID)
	}
}

func (m *SSEManager) Publish(taskID string, event *SSEEvent) {
	sh := m.shardFor(taskID)
	sh.mu.Lock()
	defer sh.mu.Unlock()

	entry, exists := sh.tasks[taskID]
	if !exists {
		entry = &taskEntry{
			subscribers: make(map[uint64]chan *SSEEvent),
		}
		sh.tasks[taskID] = entry
	}
	m.publishLocked(taskID, entry, event)
}

func (m *SSEManager) PublishIfPresent(taskID string, event *SSEEvent) {
	sh := m.shardFor(taskID)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	if entry, ok := sh.tasks[taskID]; ok {
		m.publishLocked(taskID, entry, event)
	}
}

func (m *SSEManager) publishLocked(taskID string, entry *taskEntry, event *SSEEvent) {
	if entry.closed {
		return
	}

	var behind []uint64
	for id, ch := range entry.subscribers {
		select {
		case ch <- event:
		default:
			behind = append(behind, id)
		}
	}
	for _, id := range behind {
		close(entry.subscribers[id])
		delete(entry.subscribers, id)
		entry.sheds++
		log := m.logger.Debug
		if entry.sheds == 1 {
			log = m.logger.Warn
		}
		log("SSE subscriber fell behind; closing its live channel, the stream subscribes again",
			"taskID", taskID, "connID", id, "eventType", event.EventType, "sheds", entry.sheds)
	}

	if event.Final {
		m.closeEntryLocked(taskID, entry)
	}
}

func (m *SSEManager) closeEntryLocked(taskID string, entry *taskEntry) {
	for _, ch := range entry.subscribers {
		close(ch)
	}
	entry.closed = true
	if len(entry.subscribers) == 0 {
		m.scheduleOrphanSweep(taskID)
	}
}

func (m *SSEManager) Finish(taskID string) {
	sh := m.shardFor(taskID)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	if entry, exists := sh.tasks[taskID]; exists && !entry.closed {
		m.closeEntryLocked(taskID, entry)
	}
}

func (m *SSEManager) scheduleOrphanSweep(taskID string) {
	if m.orphanGrace <= 0 {
		return
	}
	sh := m.shardFor(taskID)
	time.AfterFunc(m.orphanGrace, func() {
		sh.mu.Lock()
		swept := false
		if e, ok := sh.tasks[taskID]; ok && e.closed && len(e.subscribers) == 0 {
			delete(sh.tasks, taskID)
			swept = true
		}
		sh.mu.Unlock()

		if swept {
			m.logger.Debug("swept orphaned finalized SSE task", "taskID", taskID)
			m.notifyForget(taskID)
		}
	})
}

func (m *SSEManager) CleanupTask(taskID string) {
	sh := m.shardFor(taskID)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	delete(sh.tasks, taskID)
}

func (m *SSEManager) HasSubscribers(taskID string) bool {
	sh := m.shardFor(taskID)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	entry, ok := sh.tasks[taskID]
	return ok && len(entry.subscribers) > 0
}

func (m *SSEManager) HeldTasksWithSubscribers() []string {
	var ids []string
	for i := range m.shards {
		sh := &m.shards[i]
		sh.mu.Lock()
		for taskID, entry := range sh.tasks {
			if entry.held && len(entry.subscribers) > 0 {
				ids = append(ids, taskID)
			}
		}
		sh.mu.Unlock()
	}
	return ids
}

func (m *SSEManager) HasTask(taskID string) bool {
	sh := m.shardFor(taskID)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	_, ok := sh.tasks[taskID]
	return ok
}
