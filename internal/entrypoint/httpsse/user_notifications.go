// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"encoding/json"
	"slices"
	"sync"
)

type UserNotification struct {
	Event string `json:"event"`
	Data  string `json:"data"`
}

const userNotifQueueSize = 64

type userNotifState struct {
	mu     sync.Mutex
	queues map[string][]chan UserNotification
}

func newUserNotifState() *userNotifState {
	return &userNotifState{queues: make(map[string][]chan UserNotification)}
}

func (m *SSEManager) AddUserNotificationQueue(userID string) (<-chan UserNotification, func()) {
	return m.AddUserNotificationQueueForKeys(userID)
}

func (m *SSEManager) AddUserNotificationQueueForKeys(keys ...string) (<-chan UserNotification, func()) {
	unique := make([]string, 0, len(keys))
	seen := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		if k == "" {
			continue
		}
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		unique = append(unique, k)
	}

	ch := make(chan UserNotification, userNotifQueueSize)
	m.userNotif.mu.Lock()
	for _, k := range unique {
		m.userNotif.queues[k] = append(m.userNotif.queues[k], ch)
	}
	m.userNotif.mu.Unlock()
	if m.logger != nil {
		m.logger.Debug("added user notification queue", "keys", len(unique))
	}
	remove := func() {
		for _, k := range unique {
			m.removeUserNotificationQueue(k, ch)
		}
	}
	return ch, remove
}

func (m *SSEManager) removeUserNotificationQueue(userID string, ch chan UserNotification) {
	m.userNotif.mu.Lock()
	queues := m.userNotif.queues[userID]
	for i, q := range queues {
		if q == ch {
			m.userNotif.queues[userID] = append(queues[:i], queues[i+1:]...)
			if len(m.userNotif.queues[userID]) == 0 {
				delete(m.userNotif.queues, userID)
			}
			break
		}
	}
	m.userNotif.mu.Unlock()
	if m.logger != nil {
		m.logger.Debug("removed user notification queue", "userID", userID)
	}
}

func (m *SSEManager) SendUserNotification(userID, eventType string, data any) error {
	return m.SendUserNotificationToKeys([]string{userID}, eventType, data)
}

func (m *SSEManager) SendUserNotificationToKeys(keys []string, eventType string, data any) error {
	serialized, err := json.Marshal(data)
	if err != nil {
		return err
	}
	m.sendUserNotificationJSON(keys, eventType, string(serialized))
	return nil
}

func (m *SSEManager) sendUserNotificationJSON(keys []string, eventType, data string) {
	targets, delivered, evicted := m.userNotif.send(keys, UserNotification{Event: eventType, Data: data})
	if m.logger == nil {
		return
	}
	switch {
	case targets == 0:
		m.logger.Debug("no notification queues for user, event dropped",
			"keyCount", len(keys), "eventType", eventType)
	case evicted > 0:
		m.logger.Warn("closed full notification queues",
			"eventType", eventType, "closed", evicted, "delivered", delivered)
	}
}

func (s *userNotifState) send(keys []string, payload UserNotification) (targets, delivered, evicted int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var queues []chan UserNotification
	for _, k := range keys {
		for _, q := range s.queues[k] {
			if !slices.Contains(queues, q) {
				queues = append(queues, q)
			}
		}
	}
	for _, q := range queues {
		select {
		case q <- payload:
			delivered++
		default:
			s.evict(q)
			evicted++
		}
	}
	return len(queues), delivered, evicted
}

func (s *userNotifState) evict(q chan UserNotification) {
	for k, queues := range s.queues {
		remaining := slices.DeleteFunc(queues, func(c chan UserNotification) bool { return c == q })
		if len(remaining) == 0 {
			delete(s.queues, k)
		} else {
			s.queues[k] = remaining
		}
	}
	close(q)
}
