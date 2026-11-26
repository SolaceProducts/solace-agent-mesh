// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"context"
	"encoding/json"
	"maps"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	"github.com/SolaceProducts/solace-agent-mesh/internal/discovery"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

type testAWEFollower struct {
	brk       broker.Broker
	namespace string
	handle    func(method string, path []string, body json.RawMessage) (any, error)

	mu      sync.Mutex
	applied map[string]control.AgentStatus
}

func followAsTestAWE(t *testing.T, brk broker.Broker, namespace string, handle func(method string, path []string, body json.RawMessage) (any, error)) {
	t.Helper()
	f := &testAWEFollower{brk: brk, namespace: namespace, handle: handle, applied: map[string]control.AgentStatus{}}
	queue, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("create test AWE subscription: %v", err)
	}
	if err := brk.Subscribe(queue, a2a.AWEAgentsChangedTopic(namespace)); err != nil {
		t.Fatalf("subscribe test AWE: %v", err)
	}
	ctx := t.Context()
	var loops sync.WaitGroup
	t.Cleanup(func() {
		loops.Wait()
		_ = brk.DeleteQueue(queue.Name())
	})
	wake := make(chan struct{}, 1)
	loops.Go(func() {
		for {
			msg, err := brk.Receive(ctx, queue)
			if ctx.Err() != nil {
				return
			}
			if err != nil || msg == nil {
				continue
			}
			msg.Ack()
			select {
			case wake <- struct{}{}:
			default:
			}
		}
	})
	loops.Go(func() {
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-wake:
				f.check(ctx)
			case <-ticker.C:
			}
			f.report(ctx)
		}
	})
}

func (f *testAWEFollower) check(ctx context.Context) {
	topic := a2a.AWEAgentsRequestTopic(f.namespace)
	resp, err := f.brk.Request(ctx, topic, &broker.Message{
		ReplyTo:        a2a.AWEAgentsReplyTopic(f.namespace, idgen.New()),
		UserProperties: map[string]string{discovery.ProcessIDProperty: "test-awe"},
	}, 5*time.Second)
	if err != nil {
		return
	}
	var reply control.AgentsReply
	if json.Unmarshal(resp.Payload, &reply) != nil {
		return
	}
	for _, d := range reply.Agents {
		f.mu.Lock()
		cur, known := f.applied[d.Name]
		f.mu.Unlock()
		if known && cur.DeploymentID == d.DeploymentID {
			continue
		}
		if !d.Undeploy && len(d.Payloads) == 0 {
			continue
		}
		status := control.AgentStatus{Name: d.Name, DeploymentID: d.DeploymentID}
		if err := f.apply(d); err != nil {
			status.Error = err.Error()
		}
		f.mu.Lock()
		f.applied[d.Name] = status
		f.mu.Unlock()
	}
}

func (f *testAWEFollower) apply(d control.AgentApply) error {
	if d.Undeploy {
		_, err := f.handle("delete", []string{"instances", d.Name}, nil)
		return err
	}
	for _, payload := range d.Payloads {
		body := maps.Clone(payload)
		body[control.DeploymentIDField] = d.DeploymentID
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		if _, err := f.handle("post", []string{"instances"}, raw); err != nil {
			return err
		}
	}
	return nil
}

func (f *testAWEFollower) report(ctx context.Context) {
	f.mu.Lock()
	status := control.AWEStatus{ProcessID: "test-awe"}
	for _, a := range f.applied {
		status.Agents = append(status.Agents, a)
	}
	f.mu.Unlock()
	payload, err := json.Marshal(status)
	if err != nil {
		return
	}
	_ = f.brk.Publish(ctx, a2a.AWEStatusTopic(f.namespace), &broker.Message{Payload: payload})
}
