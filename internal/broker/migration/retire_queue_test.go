// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package migration

import (
	"errors"
	"log/slog"
	"slices"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

var oldQueue = RetiredQueue{Queue: "ns/q/old", Topics: []string{"ns/a/>", "ns/b/>"}}

func TestRetireQueue(t *testing.T) {
	t.Parallel()
	full := []string{"bind ns/q/old", "unsubscribe ns/a/>", "unsubscribe ns/b/>", "close", "delete ns/q/old"}
	cases := []struct {
		name                          string
		bindErr, removeErr, deleteErr error
		wantCalls                     []string
		wantLevels                    []string
		wantErr                       bool
	}{
		{name: "retires the queue in order", wantCalls: full, wantLevels: []string{"INFO"}},
		{name: "queue already gone", bindErr: errMissing, wantCalls: []string{"bind ns/q/old"}},
		{name: "broker without the capability", bindErr: errors.ErrUnsupported, wantCalls: []string{"bind ns/q/old"}, wantLevels: []string{"WARN"}},
		{name: "bind refused", bindErr: errRefused, wantCalls: []string{"bind ns/q/old"}, wantLevels: []string{"ERROR"}},
		{name: "bind fails for now", bindErr: errTransient, wantCalls: []string{"bind ns/q/old"}, wantErr: true},
		{name: "unsubscribe refused, delete succeeds", removeErr: errRefused, wantCalls: full, wantLevels: []string{"INFO"}},
		{name: "delete refused after unsubscribe", deleteErr: errRefused, wantCalls: full, wantLevels: []string{"WARN"}},
		{name: "unsubscribe and delete refused", removeErr: errRefused, deleteErr: errRefused, wantCalls: full, wantLevels: []string{"ERROR"}},
		{name: "unsubscribe fails for now", removeErr: errTransient, wantCalls: []string{"bind ns/q/old", "unsubscribe ns/a/>", "close"}, wantErr: true},
		{name: "delete fails for now", deleteErr: errTransient, wantCalls: full, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			b := &fakeBroker{bindErr: tc.bindErr, removeErr: tc.removeErr, deleteErr: tc.deleteErr}
			logger, levels := captureLogs()

			err := RetireQueue(b, oldQueue, logger)

			if (err != nil) != tc.wantErr {
				t.Fatalf("RetireQueue err = %v, want error: %v", err, tc.wantErr)
			}
			if !slices.Equal(b.calls, tc.wantCalls) {
				t.Errorf("steps = %q\nwant    %q", b.calls, tc.wantCalls)
			}
			if got := levels(); !slices.Equal(got, tc.wantLevels) {
				t.Errorf("log levels = %v, want %v", got, tc.wantLevels)
			}
		})
	}
}

func TestRetire_BrokerWithoutCapability(t *testing.T) {
	t.Parallel()
	logger, levels := captureLogs()
	if err := RetireQueue(noBinderBroker{}, oldQueue, logger); err != nil {
		t.Fatalf("RetireQueue: %v", err)
	}
	if got := levels(); !slices.Equal(got, []string{"WARN"}) {
		t.Errorf("log levels = %v, want [WARN]; a skipped retire must never be silent", got)
	}
}

func TestRetireQueue_DevBroker(t *testing.T) {
	t.Parallel()
	b := broker.NewDevBroker()
	if err := b.Connect(t.Context()); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	r := ModelProviderQueue("ns", "general")
	q, err := b.CreateQueue(r.Queue, broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}
	for _, topic := range r.Topics {
		if err := b.Subscribe(q, topic); err != nil {
			t.Fatalf("Subscribe: %v", err)
		}
	}

	if err := RetireQueue(b, r, slog.Default()); err != nil {
		t.Fatalf("RetireQueue: %v", err)
	}
	if b.HasQueue(r.Queue) {
		t.Fatal("queue still exists after RetireQueue")
	}
	if got := b.MatchingQueues("ns/configuration/model/general"); len(got) != 0 {
		t.Errorf("model topic still reaches queues %v", got)
	}

	if err := RetireQueue(b, r, slog.Default()); err != nil {
		t.Fatalf("second RetireQueue: %v", err)
	}
	if b.HasQueue(r.Queue) {
		t.Fatal("second RetireQueue recreated the queue")
	}
}

func TestModelProviderQueue(t *testing.T) {
	t.Parallel()
	r := ModelProviderQueue("ns", "general")
	if r.Queue != "ns/q/platform/dynamic_llm_provider/general" {
		t.Errorf("Queue = %q", r.Queue)
	}
	if !slices.Equal(r.Topics, []string{"ns/configuration/model/>"}) {
		t.Errorf("Topics = %q", r.Topics)
	}
}
