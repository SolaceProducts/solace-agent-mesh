// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	"github.com/SolaceProducts/solace-agent-mesh/internal/identity"
)

type slowIdentityService struct {
	delay time.Duration
	calls atomic.Int64
}

func (s *slowIdentityService) Resolve(_ context.Context, _ map[string]any) (*identity.Resolution, error) {
	s.calls.Add(1)
	time.Sleep(s.delay)
	return &identity.Resolution{EnrichmentStatus: identity.EnrichmentOK}, nil
}

func (s *slowIdentityService) SearchUsers(context.Context, string, int) ([]*identity.Profile, error) {
	return nil, nil
}
func (s *slowIdentityService) GetUserProfile(context.Context, string) (map[string]any, error) {
	return nil, nil
}
func (s *slowIdentityService) GetTimeOff(context.Context, string, string, string) ([]identity.TimeOffEntry, error) {
	return nil, nil
}
func (s *slowIdentityService) GetUserProfilePicture(context.Context, string) ([]byte, string, error) {
	return nil, "", nil
}

func TestAuthzLookupHandler_WorkerPoolParallelism(t *testing.T) {
	const (
		ns         = "rbac-workers"
		concurrent = 16
		workers    = 8
		stubDelay  = 200 * time.Millisecond
	)

	brk := newTestBroker(t)
	composer := newAuthzTestComposer(t)
	slowID := &slowIdentityService{delay: stubDelay}

	h := NewAuthzLookupHandler(composer, slowID, brk, AuthzLookupConfig{
		Namespace: ns,
		CacheTTL:  time.Minute,
		Workers:   workers,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := h.Start(context.Background()); err != nil {
		t.Fatalf("start handler: %v", err)
	}
	t.Cleanup(func() { _ = h.Stop(context.Background()) })
	time.Sleep(50 * time.Millisecond)

	start := time.Now()
	var wg sync.WaitGroup
	errs := make(chan error, concurrent)

	for i := range concurrent {
		wg.Go(func() {
			gwID := fmt.Sprintf("webui-%d", i)
			payload, _ := json.Marshal(lookupRequest{
				UserID:         fmt.Sprintf("user-%d@example.org", i),
				GatewayContext: map[string]any{"gateway_id": gwID},
			})
			reqCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			reply, err := brk.Request(reqCtx, a2a.AuthzLookupTopic(ns, gwID), &broker.Message{
				Payload: payload,
				ReplyTo: a2a.AuthzResponseTopic(ns, gwID),
			}, 10*time.Second)
			if err != nil {
				errs <- fmt.Errorf("request %d: %w", i, err)
				return
			}
			resp, err := control.UnmarshalResponse(reply.Payload)
			if err != nil {
				errs <- fmt.Errorf("unmarshal %d: %w", i, err)
				return
			}
			if resp.Error != nil {
				errs <- fmt.Errorf("response %d: %+v", i, resp.Error)
				return
			}
		})
	}
	wg.Wait()
	elapsed := time.Since(start)
	close(errs)

	for err := range errs {
		t.Errorf("lookup error: %v", err)
	}
	if t.Failed() {
		return
	}

	parallelBudget := time.Duration(concurrent/workers+1) * stubDelay
	serialBound := time.Duration(concurrent) * stubDelay
	t.Logf("elapsed=%s parallel-budget=%s serial-bound=%s stub_calls=%d",
		elapsed, parallelBudget, serialBound, slowID.calls.Load())
	if elapsed >= serialBound {
		t.Fatalf("elapsed %s >= serial bound %s — worker pool not running in parallel", elapsed, serialBound)
	}
	if elapsed > 2*parallelBudget {
		t.Fatalf("elapsed %s > 2*parallel-budget %s — worker pool underperformed expected concurrency", elapsed, 2*parallelBudget)
	}
	if got := slowID.calls.Load(); got != concurrent {
		t.Fatalf("stub call count = %d, want %d (one per unique user)", got, concurrent)
	}
}

func TestAuthzLookupHandler_StopWaitsForInFlightHandler(t *testing.T) {
	const (
		ns        = "rbac-stop-drain"
		stubDelay = 500 * time.Millisecond
	)
	brk := newTestBroker(t)
	composer := newAuthzTestComposer(t)
	slowID := &slowIdentityService{delay: stubDelay}

	h := NewAuthzLookupHandler(composer, slowID, brk, AuthzLookupConfig{
		Namespace: ns,
		CacheTTL:  time.Minute,
		Workers:   1,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := h.Start(t.Context()); err != nil {
		t.Fatalf("start handler: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	payload, err := json.Marshal(lookupRequest{
		UserID:         "drain-user@example.org",
		GatewayContext: map[string]any{"gateway_id": "webui"},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := brk.Publish(t.Context(), a2a.AuthzLookupTopic(ns, "webui"), &broker.Message{
		Payload: payload,
		ReplyTo: a2a.AuthzResponseTopic(ns, "webui"),
	}); err != nil {
		t.Fatalf("publish: %v", err)
	}

	deadline := time.Now().Add(stubDelay)
	for time.Now().Before(deadline) && slowID.calls.Load() == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	if slowID.calls.Load() == 0 {
		t.Fatal("worker never entered Resolve — broker or queue wiring is wrong")
	}

	stopStart := time.Now()
	if err := h.Stop(context.Background()); err != nil {
		t.Fatalf("stop: %v", err)
	}
	stopElapsed := time.Since(stopStart)

	minDrain := 6 * stubDelay / 10
	if stopElapsed < minDrain {
		t.Fatalf("Stop returned in %s; expected ≥%s while a handler was mid-Resolve", stopElapsed, minDrain)
	}
}
