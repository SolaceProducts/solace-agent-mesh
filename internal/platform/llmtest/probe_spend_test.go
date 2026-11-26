// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package llmtest

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget/budgettest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
	"github.com/SolaceProducts/solace-agent-mesh/internal/testing/mockllm"
)

const spendNamespace = "probe-spend"

type spendTopic struct {
	brk       *broker.DevBroker
	queue     broker.Queue
	publisher *spend.Publisher
}

func newSpendTopic(t *testing.T) *spendTopic {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })
	name, opts := spend.EventQueue(spendNamespace)
	q, err := brk.CreateQueue(name, opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := brk.Subscribe(q, a2a.SpendEventTopic(spendNamespace)); err != nil {
		t.Fatal(err)
	}
	return &spendTopic{brk: brk, queue: q, publisher: spend.NewPublisher(brk, spendNamespace, slog.Default())}
}

func (s *spendTopic) published(t *testing.T) []spend.Event {
	t.Helper()
	if err := s.publisher.Close(t.Context()); err != nil {
		t.Fatalf("close publisher: %v", err)
	}
	var events []spend.Event
	for {
		ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
		msg, err := s.brk.Receive(ctx, s.queue)
		cancel()
		if errors.Is(err, context.DeadlineExceeded) {
			return events
		}
		if err != nil {
			t.Fatalf("receive spend: %v", err)
		}
		var e spend.Event
		if err := json.Unmarshal(msg.Payload, &e); err != nil {
			t.Fatalf("decode spend: %v", err)
		}
		events = append(events, e)
	}
}

func billedProvider(t *testing.T) *httptest.Server {
	t.Helper()
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mockllm.AnswerCompletion(w, raw, []byte(`{
			"id":"chatcmpl-test","object":"chat.completion","created":1,"model":"gpt-4o",
			"choices":[{"index":0,"message":{"role":"assistant","content":"It is 3pm."},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":10,"completion_tokens":4,"total_tokens":14}
		}`))
	}))
	t.Cleanup(provider.Close)
	return provider
}

func TestProbe_RecordsItsSpendOnlyWhenItHasAnAttribution(t *testing.T) {
	t.Parallel()
	attribution := &llm.SpendAttribution{UserSub: "admin-sub", Surface: string(spend.SurfaceModelTest), TriggerKind: string(spend.TriggerRequest)}
	cases := map[string]struct {
		recording   func(*spend.Publisher) *SpendRecording
		wantRecords int
	}{
		"no recording": {recording: func(*spend.Publisher) *SpendRecording { return nil }},
		"no attribution": {recording: func(p *spend.Publisher) *SpendRecording {
			return &SpendRecording{Publisher: p, Gate: budgettest.NewGate()}
		}},
		"with an attribution": {
			recording: func(p *spend.Publisher) *SpendRecording {
				return &SpendRecording{Publisher: p, Attribution: attribution, Gate: budgettest.NewGate()}
			},
			wantRecords: 1,
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			topic := newSpendTopic(t)
			provider := billedProvider(t)

			if ok, msg := Probe(t.Context(), newTestModelConfig(provider.URL, "sk-test"), slog.Default(), c.recording(topic.publisher)); !ok {
				t.Fatalf("probe failed: %s", msg)
			}

			events := topic.published(t)
			if len(events) != c.wantRecords {
				t.Fatalf("recorded %d events, want %d", len(events), c.wantRecords)
			}
			if c.wantRecords == 1 && (events[0].UserSub != "admin-sub" || events[0].Surface != spend.SurfaceModelTest || events[0].Tokens.Output != 4) {
				t.Errorf("recorded %+v, want the probe's 4 output tokens booked to admin-sub as a model test", events[0])
			}
		})
	}
}
