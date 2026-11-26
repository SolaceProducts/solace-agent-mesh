// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package teams

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
)

func TestTeamsServer_StartDispatchRefusedAfterClose(t *testing.T) {
	var dispatched atomic.Int64
	s := NewTeamsServer(&TeamsConfig{}, nil, func(context.Context, *Activity) *InvokeResponse {
		dispatched.Add(1)
		return nil
	}, teamsNoopLogger())

	if !s.startDispatch(messageWork(s)) {
		t.Fatal("an open server must accept the activity")
	}
	s.Wait()
	if got := dispatched.Load(); got != 1 {
		t.Fatalf("dispatched = %d, want 1", got)
	}

	s.Close()
	if s.startDispatch(messageWork(s)) {
		t.Error("a closed server must refuse the activity rather than ack it")
	}
	s.Wait()
	if got := dispatched.Load(); got != 1 {
		t.Errorf("dispatched = %d after Close, want 1 (nothing new)", got)
	}
}

func TestTeamsServer_NeverAcksWorkDroppedAcrossClose(t *testing.T) {
	var dispatched, accepted, refused atomic.Int64
	s := NewTeamsServer(&TeamsConfig{}, nil, func(context.Context, *Activity) *InvokeResponse {
		dispatched.Add(1)
		return nil
	}, teamsNoopLogger())

	const callers = 50
	var start, done sync.WaitGroup
	start.Add(1)
	done.Add(callers)
	for range callers {
		go func() {
			defer done.Done()
			start.Wait()
			if s.startDispatch(messageWork(s)) {
				accepted.Add(1)
				return
			}
			refused.Add(1)
		}()
	}
	start.Done()
	s.Close()
	done.Wait()
	s.Wait()

	if total := accepted.Load() + refused.Load(); total != callers {
		t.Fatalf("accounted-for calls = %d, want %d", total, callers)
	}
	if got, want := dispatched.Load(), accepted.Load(); got != want {
		t.Errorf("dispatched = %d, accepted = %d — an accepted activity must always be dispatched", got, want)
	}
}

func TestTeamsAdapter_StopRefusesFurtherActivities(t *testing.T) {
	a := newTestAdapter(t)
	a.uploads = NewUploadManager(a.logger)
	if err := a.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := a.Stop(t.Context()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if a.server.startDispatch(messageWork(a.server)) {
		t.Error("after Stop the server must refuse activities")
	}
}

func messageWork(s *TeamsServer) func() {
	return func() { s.activityHandler(context.Background(), &Activity{Type: ActivityTypeMessage}) }
}
