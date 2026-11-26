// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"context"
	"errors"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

type publishLimitBroker struct {
	broker.Broker
	accept int
	seen   int
}

func (b *publishLimitBroker) Publish(_ context.Context, _ string, _ *broker.Message) error {
	b.seen++
	if b.seen > b.accept {
		return errors.New("broker dropped")
	}
	return nil
}

func (f *runServiceFixture) withBroker(br broker.Broker) {
	f.svc.trigger = NewTriggerPublisher(br, "test-ns")
}

func TestTriggerRunsIfIdlePartlySentIsAcceptedAndFailsTheRest(t *testing.T) {
	f := newRunFixture(t, 1)
	expID := f.twoModelExperiment(t, "partly-sent")
	f.withBroker(&publishLimitBroker{accept: 1})

	runs, err := f.svc.TriggerRunsIfIdle(f.ctx, expID, "alice", "", "manual")
	if err != nil {
		t.Fatalf("trigger = %v, want success once a run was sent", err)
	}
	if len(runs) != 2 {
		t.Fatalf("trigger = %d runs, want 2", len(runs))
	}
	statuses := map[string]int{}
	for _, run := range runs {
		statuses[run.Status]++
	}
	if statuses[StatusPending] != 1 || statuses[StatusFailed] != 1 {
		t.Errorf("statuses = %v, want one sent (pending) and one failed", statuses)
	}
}

func TestTriggerRunsIfIdleNothingSentFailsAllRuns(t *testing.T) {
	f := newRunFixture(t, 1)
	expID := f.twoModelExperiment(t, "none-sent")
	f.withBroker(&publishLimitBroker{accept: 0})

	if _, err := f.svc.TriggerRunsIfIdle(f.ctx, expID, "alice", "", "manual"); !errors.Is(err, ErrRunNotDispatched) {
		t.Fatalf("trigger = %v, want ErrRunNotDispatched", err)
	}
	rows, _, err := f.svc.ListRunsPaginated(f.ctx, expID, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, run := range rows {
		if run.Status != StatusFailed {
			t.Errorf("run %s = %s, want failed", run.ID, run.Status)
		}
	}
}

func TestTriggerRunsIfIdleFailureWritesSurviveACancelledContext(t *testing.T) {
	f := newRunFixture(t, 1)
	ctx, cancel := context.WithCancel(f.ctx)
	f.svc.trigger = NewTriggerPublisher(&cancellingBroker{cancel: cancel}, "test-ns")

	if _, err := f.svc.TriggerRunsIfIdle(ctx, f.experimentID, "alice", "", "manual"); !errors.Is(err, ErrRunNotDispatched) {
		t.Fatalf("trigger = %v, want ErrRunNotDispatched", err)
	}
	rows, _, err := f.svc.ListRunsPaginated(f.ctx, f.experimentID, 1, 10)
	if err != nil || len(rows) != 1 {
		t.Fatalf("runs = %d, %v; want one", len(rows), err)
	}
	if run := rows[0]; run.Status != StatusFailed {
		t.Errorf("run = %s, want failed so it cannot block later triggers", run.Status)
	}
}

type cancellingBroker struct {
	broker.Broker
	cancel context.CancelFunc
}

func (b *cancellingBroker) Publish(context.Context, string, *broker.Message) error {
	if b.cancel != nil {
		b.cancel()
	}
	return errors.New("context canceled")
}
