// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"encoding/json"
	"log/slog"
	"sync"
	"testing"
	"time"
)

func TestUserNotifications_AddAndSend(t *testing.T) {
	m := NewSSEManager(100, slog.Default())

	ch, remove := m.AddUserNotificationQueue("u1")
	defer remove()

	if err := m.SendUserNotification("u1", "session_created", map[string]any{"sessionId": "s1"}); err != nil {
		t.Fatalf("SendUserNotification: %v", err)
	}

	select {
	case got := <-ch:
		if got.Event != "session_created" {
			t.Errorf("Event = %q, want session_created", got.Event)
		}
		var data map[string]any
		if err := json.Unmarshal([]byte(got.Data), &data); err != nil {
			t.Fatalf("unmarshal data: %v", err)
		}
		if data["sessionId"] != "s1" {
			t.Errorf("session_id = %v, want s1", data["sessionId"])
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for notification")
	}
}

func TestUserNotifications_FanoutToAllQueuesForUser(t *testing.T) {
	m := NewSSEManager(100, slog.Default())

	ch1, remove1 := m.AddUserNotificationQueue("u1")
	defer remove1()
	ch2, remove2 := m.AddUserNotificationQueue("u1")
	defer remove2()

	if err := m.SendUserNotification("u1", "execution_queued", map[string]any{"executionId": "e1"}); err != nil {
		t.Fatalf("SendUserNotification: %v", err)
	}

	for i, ch := range []<-chan UserNotification{ch1, ch2} {
		select {
		case got := <-ch:
			if got.Event != "execution_queued" {
				t.Errorf("queue[%d] event = %q, want execution_queued", i, got.Event)
			}
		case <-time.After(time.Second):
			t.Fatalf("queue[%d] timed out", i)
		}
	}
}

func TestUserNotifications_OnlyDeliversToTargetedUser(t *testing.T) {
	m := NewSSEManager(100, slog.Default())

	chU1, removeU1 := m.AddUserNotificationQueue("u1")
	defer removeU1()
	chU2, removeU2 := m.AddUserNotificationQueue("u2")
	defer removeU2()

	if err := m.SendUserNotification("u1", "execution_started", map[string]any{}); err != nil {
		t.Fatalf("send: %v", err)
	}

	select {
	case <-chU1:
	case <-time.After(time.Second):
		t.Fatal("u1 did not receive event")
	}

	select {
	case got := <-chU2:
		t.Fatalf("u2 should not have received event, got %+v", got)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestUserNotifications_NoQueueIsNoError(t *testing.T) {
	m := NewSSEManager(100, slog.Default())
	if err := m.SendUserNotification("u-nobody", "session_created", map[string]any{}); err != nil {
		t.Errorf("send to user with no queues should not error, got %v", err)
	}
}

func TestUserNotifications_RemoveUnregisters(t *testing.T) {
	m := NewSSEManager(100, slog.Default())

	ch, remove := m.AddUserNotificationQueue("u1")
	remove()

	if err := m.SendUserNotification("u1", "session_created", map[string]any{}); err != nil {
		t.Fatalf("send after remove: %v", err)
	}
	select {
	case got := <-ch:
		t.Fatalf("expected no delivery after remove, got %+v", got)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestUserNotifications_FullQueuePruned(t *testing.T) {
	m := NewSSEManager(100, slog.Default())

	ch, remove := m.AddUserNotificationQueue("u1")
	defer remove()

	for range userNotifQueueSize {
		if err := m.SendUserNotification("u1", "noise", map[string]any{}); err != nil {
			t.Fatalf("send: %v", err)
		}
	}

	if err := m.SendUserNotification("u1", "overflow", map[string]any{}); err != nil {
		t.Fatalf("overflow send: %v", err)
	}

	if err := m.SendUserNotification("u1", "after-prune", map[string]any{}); err != nil {
		t.Fatalf("post-prune send: %v", err)
	}

	drained := 0
	for range ch {
		drained++
	}
	if drained != userNotifQueueSize {
		t.Errorf("expected %d events drained before prune, got %d", userNotifQueueSize, drained)
	}
}

func TestUserNotifications_ConcurrentSendsAndDisconnects(t *testing.T) {
	m := NewSSEManager(100, slog.Default())

	const senders = 8
	const receivers = 16
	const sends = 500

	done := make(chan struct{})
	var wg sync.WaitGroup

	for i := range receivers {
		uid := "u" + string(rune('a'+i%4))
		wg.Go(func() {
			ch, remove := m.AddUserNotificationQueue(uid)
			defer remove()
			for {
				select {
				case <-ch:
				case <-done:
					return
				}
			}
		})
	}

	var senderWg sync.WaitGroup
	for range senders {
		senderWg.Go(func() {
			for j := range sends {
				uid := "u" + string(rune('a'+j%4))
				_ = m.SendUserNotification(uid, "tick", map[string]any{"j": j})
			}
		})
	}
	senderWg.Wait()
	close(done)
	wg.Wait()
}

func TestUserNotifications_UnmarshalableData(t *testing.T) {
	m := NewSSEManager(100, slog.Default())
	ch, remove := m.AddUserNotificationQueue("u1")
	defer remove()

	err := m.SendUserNotification("u1", "session_created", map[string]any{
		"chan": make(chan int),
	})
	if err == nil {
		t.Fatal("expected error for unmarshalable data, got nil")
	}

	select {
	case got := <-ch:
		t.Fatalf("expected no delivery on marshal failure, got %+v", got)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestUserNotifications_RemoveUnregistersTheQueue(t *testing.T) {
	m := NewSSEManager(100, slog.Default())

	ch, remove := m.AddUserNotificationQueue("u1")
	remove()

	if err := m.SendUserNotification("u1", "session_created", map[string]any{}); err != nil {
		t.Fatalf("send: %v", err)
	}
	select {
	case got := <-ch:
		t.Fatalf("expected no delivery after remove, got %+v", got)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestUserNotifications_ConcurrentSendersCloseANeverDrainedQueueOnce(t *testing.T) {
	m := NewSSEManager(0, nil)
	ch, remove := m.AddUserNotificationQueueForKeys("ben", "0192f0a6-3c1b-7d2e-8f4a-5b6c7d8e9f01")
	defer remove()

	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range userNotifQueueSize {
				if err := m.SendUserNotificationToKeys([]string{"ben", "0192f0a6-3c1b-7d2e-8f4a-5b6c7d8e9f01"}, "notification_created", map[string]string{"id": "n"}); err != nil {
					t.Error(err)
				}
			}
		})
	}
	wg.Wait()

	received := 0
	for range ch {
		received++
	}
	if received != userNotifQueueSize {
		t.Errorf("closed queue held %d events, want a full %d", received, userNotifQueueSize)
	}
	m.userNotif.mu.Lock()
	registered := len(m.userNotif.queues)
	m.userNotif.mu.Unlock()
	if registered != 0 {
		t.Errorf("%d keys still registered after the queue was closed", registered)
	}
}

func TestUserNotifications_AQueueUnderSeveralKeysGetsAnEventOnce(t *testing.T) {
	m := NewSSEManager(0, nil)
	ch, remove := m.AddUserNotificationQueueForKeys("ben", "0192f0a6-3c1b-7d2e-8f4a-5b6c7d8e9f01")
	defer remove()
	other, removeOther := m.AddUserNotificationQueue("alice")
	defer removeOther()

	if err := m.SendUserNotificationToKeys([]string{"ben", "0192f0a6-3c1b-7d2e-8f4a-5b6c7d8e9f01", ""}, "notification_created", map[string]string{"id": "n1"}); err != nil {
		t.Fatal(err)
	}
	if got := len(ch); got != 1 {
		t.Errorf("queue got %d events, want 1", got)
	}
	if got := len(other); got != 0 {
		t.Errorf("another user's queue got %d events, want 0", got)
	}
}

func TestUserNotifications_AFullQueueUnderSeveralKeysIsPrunedFromEveryKey(t *testing.T) {
	m := NewSSEManager(0, nil)
	keys := []string{"ben", "0192f0a6-3c1b-7d2e-8f4a-5b6c7d8e9f01"}
	full, removeFull := m.AddUserNotificationQueueForKeys(keys...)
	defer removeFull()
	healthy, removeHealthy := m.AddUserNotificationQueue("ben")
	defer removeHealthy()
	for range userNotifQueueSize {
		if err := m.SendUserNotificationToKeys(keys, "noise", map[string]any{}); err != nil {
			t.Fatal(err)
		}
	}
	for range userNotifQueueSize {
		<-healthy
	}

	if err := m.SendUserNotificationToKeys(keys, "overflow", map[string]any{}); err != nil {
		t.Fatal(err)
	}

	m.userNotif.mu.Lock()
	byPlatformID, byLogin := len(m.userNotif.queues[keys[1]]), len(m.userNotif.queues[keys[0]])
	m.userNotif.mu.Unlock()
	if byPlatformID != 0 || byLogin != 1 {
		t.Errorf("queues after prune: %d under the users.id, %d under the login id; want 0 and the healthy one", byPlatformID, byLogin)
	}
	if got := len(healthy); got != 1 {
		t.Errorf("healthy queue got %d events, want the overflow event", got)
	}
	drained := 0
	for range full {
		drained++
	}
	if drained != userNotifQueueSize {
		t.Errorf("full queue held %d events before it closed, want %d", drained, userNotifQueueSize)
	}
}
