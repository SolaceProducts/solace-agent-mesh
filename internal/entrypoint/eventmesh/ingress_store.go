// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eventmesh

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

const routingItem = "eventmesh.routing"

type taskRouting struct {
	HandlerName           string         `json:"handlerName"`
	ForwardContext        map[string]any `json:"forwardContext,omitempty"`
	DeferredACK           bool           `json:"deferredAck,omitempty"`
	UserID                string         `json:"userId,omitempty"`
	SentStructuredRequest bool           `json:"sentStructuredRequest,omitempty"`
	InboundID             string         `json:"inboundId,omitempty"`
}

type ingressStore interface {
	store(ctx context.Context, messageID, taskID, payload string) (bool, error)
	stored(ctx context.Context, limit int) ([]store.StoredInbound, error)
	markSubmitted(ctx context.Context, messageID, taskID, outcome string) error
	setOutcome(ctx context.Context, messageID, taskID, outcome string) (bool, error)
	outcomes(ctx context.Context, messageIDs []string) (map[string]string, error)
	forget(ctx context.Context, messageID string) error
	release(messageID string)
	putRouting(ctx context.Context, taskID string, r taskRouting) error
	routing(ctx context.Context, taskID string) (taskRouting, bool, error)
	dropRouting(taskID string)
	shared() bool
}

type dbIngressStore struct {
	gatewayID string
	inbound   *store.EntrypointInboundRepo
	items     *store.EntrypointTaskItemRepo
}

func newDBIngressStore(db *store.DB, gatewayID string) *dbIngressStore {
	return &dbIngressStore{
		gatewayID: gatewayID,
		inbound:   store.NewEntrypointInboundRepo(db),
		items:     store.NewEntrypointTaskItemRepo(db),
	}
}

func (s *dbIngressStore) store(ctx context.Context, messageID, taskID, payload string) (bool, error) {
	return s.inbound.Store(ctx, s.gatewayID, messageID, taskID, payload)
}

func (s *dbIngressStore) stored(ctx context.Context, limit int) ([]store.StoredInbound, error) {
	return s.inbound.Stored(ctx, s.gatewayID, limit)
}

func (s *dbIngressStore) markSubmitted(ctx context.Context, messageID, taskID, outcome string) error {
	return s.inbound.MarkSubmitted(ctx, s.gatewayID, messageID, taskID, outcome)
}

func (s *dbIngressStore) setOutcome(ctx context.Context, messageID, taskID, outcome string) (bool, error) {
	return s.inbound.SetOutcome(ctx, s.gatewayID, messageID, taskID, outcome)
}

func (s *dbIngressStore) outcomes(ctx context.Context, messageIDs []string) (map[string]string, error) {
	out := make(map[string]string, len(messageIDs))
	for start := 0; start < len(messageIDs); start += dbutil.InClauseCap {
		chunk, err := s.inbound.Outcomes(ctx, s.gatewayID, messageIDs[start:min(start+dbutil.InClauseCap, len(messageIDs))])
		if err != nil {
			return nil, err
		}
		for id, outcome := range chunk {
			out[id] = outcome
		}
	}
	return out, nil
}

func (s *dbIngressStore) forget(ctx context.Context, messageID string) error {
	return s.inbound.Forget(ctx, s.gatewayID, messageID)
}

func (s *dbIngressStore) putRouting(ctx context.Context, taskID string, r taskRouting) error {
	value, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("encode task routing: %w", err)
	}
	return s.items.Put(ctx, taskID, routingItem, string(value))
}

func (s *dbIngressStore) routing(ctx context.Context, taskID string) (taskRouting, bool, error) {
	value, found, err := s.items.Get(ctx, taskID, routingItem)
	if err != nil || !found {
		return taskRouting{}, false, err
	}
	var r taskRouting
	if err := json.Unmarshal([]byte(value), &r); err != nil {
		return taskRouting{}, false, fmt.Errorf("decode task routing: %w", err)
	}
	return r, true, nil
}

func (s *dbIngressStore) dropRouting(string) {
}

func (s *dbIngressStore) release(string) {
}

func (s *dbIngressStore) shared() bool { return true }

type memIngressStore struct {
	mu     sync.Mutex
	events map[string]*memEvent
	order  []string
	routes map[string]taskRouting
}

type memEvent struct {
	taskID  string
	payload string
	done    bool
	outcome string
}

func newMemIngressStore() *memIngressStore {
	return &memIngressStore{events: map[string]*memEvent{}, routes: map[string]taskRouting{}}
}

func (s *memIngressStore) store(_ context.Context, messageID, taskID, payload string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.events[messageID]; ok {
		return false, nil
	}
	s.events[messageID] = &memEvent{taskID: taskID, payload: payload}
	s.order = append(s.order, messageID)
	return true, nil
}

func (s *memIngressStore) stored(_ context.Context, limit int) ([]store.StoredInbound, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []store.StoredInbound
	live := s.order[:0]
	for _, id := range s.order {
		e, ok := s.events[id]
		if !ok {
			continue
		}
		live = append(live, id)
		if !e.done && len(out) < limit {
			out = append(out, store.StoredInbound{MessageID: id, TaskID: e.taskID, Payload: e.payload})
		}
	}
	s.order = live
	return out, nil
}

func (s *memIngressStore) markSubmitted(_ context.Context, messageID, taskID, outcome string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.events[messageID]; ok && !e.done && e.taskID == taskID {
		e.done = true
		e.payload = ""
		if outcome != "" {
			e.outcome = outcome
		}
	}
	return nil
}

func (s *memIngressStore) setOutcome(_ context.Context, messageID, taskID, outcome string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.events[messageID]
	if !ok || e.taskID != taskID || e.outcome != "" {
		return false, nil
	}
	e.outcome = outcome
	return true, nil
}

func (s *memIngressStore) outcomes(_ context.Context, messageIDs []string) (map[string]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]string, len(messageIDs))
	for _, id := range messageIDs {
		if e, ok := s.events[id]; ok && e.outcome != "" {
			out[id] = e.outcome
		}
	}
	return out, nil
}

func (s *memIngressStore) forget(_ context.Context, messageID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.events, messageID)
	return nil
}

func (s *memIngressStore) release(messageID string) {
	_ = s.forget(context.Background(), messageID)
}

func (s *memIngressStore) putRouting(_ context.Context, taskID string, r taskRouting) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.routes[taskID] = r
	return nil
}

func (s *memIngressStore) routing(_ context.Context, taskID string) (taskRouting, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.routes[taskID]
	return r, ok, nil
}

func (s *memIngressStore) dropRouting(taskID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.routes, taskID)
}

func (s *memIngressStore) shared() bool { return false }
