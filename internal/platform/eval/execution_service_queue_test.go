// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

type failingBroker struct {
	broker.Broker
	createErr    error
	subscribeErr error
	deleted      []string
}

func (b *failingBroker) Connect(_ context.Context) error    { return nil }
func (b *failingBroker) Disconnect(_ context.Context) error { return nil }
func (b *failingBroker) IsConnected() bool                  { return true }

func (b *failingBroker) CreateQueue(name string, _ broker.QueueOptions) (broker.Queue, error) {
	if b.createErr != nil {
		return nil, b.createErr
	}
	return &broker.SimpleQueue{QName: name}, nil
}

func (b *failingBroker) Subscribe(_ broker.Queue, _ string) error {
	return b.subscribeErr
}

func (b *failingBroker) DeleteQueue(name string) error {
	b.deleted = append(b.deleted, name)
	return nil
}

func newExecutionServiceForFailureTest(br broker.Broker) *ExecutionService {
	exec := NewExecutionService(ExecutionServiceParams{
		Namespace: "test-ns",
		Broker:    br,
		BrokerCli: NewBrokerClient(br, "test-ns", nil, nil),
	})
	return exec
}

func TestExecutionService_Start_CreateQueueError(t *testing.T) {
	br := &failingBroker{createErr: errors.New("broker rejected create")}
	exec := newExecutionServiceForFailureTest(br)
	err := exec.Start(context.Background())
	if err == nil {
		t.Fatal("Start: want error from CreateQueue failure")
	}
	if !strings.Contains(err.Error(), "create trigger queue") {
		t.Errorf("error message %q missing %q context", err, "create trigger queue")
	}
}

func TestExecutionService_Start_SubscribeError(t *testing.T) {
	br := &failingBroker{subscribeErr: errors.New("broker rejected subscribe")}
	exec := newExecutionServiceForFailureTest(br)
	err := exec.Start(context.Background())
	if err == nil {
		t.Fatal("Start: want error from Subscribe failure")
	}
	if !strings.Contains(err.Error(), "subscribe trigger topic") {
		t.Errorf("error message %q missing %q context", err, "subscribe trigger topic")
	}
	if len(br.deleted) != 1 || br.deleted[0] != "test-ns/q/eval/trigger" {
		t.Errorf("expected DeleteQueue(\"test-ns/q/eval/trigger\") after subscribe failure; got %v",
			br.deleted)
	}
}
