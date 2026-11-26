// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package spend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

const testNamespace = "test-ns"

var discardLogger = slog.New(slog.NewTextHandler(io.Discard, nil))

type scriptedClient struct {
	resp   *llm.ChatResponse
	chunks []llm.StreamChunk
	err    error
}

func (c *scriptedClient) ChatCompletion(context.Context, *llm.ChatRequest) (*llm.ChatResponse, error) {
	return c.resp, c.err
}

func (c *scriptedClient) ChatCompletionStream(context.Context, *llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	if c.err != nil {
		return nil, c.err
	}
	ch := make(chan llm.StreamChunk, len(c.chunks))
	for _, chunk := range c.chunks {
		ch <- chunk
	}
	close(ch)
	return ch, nil
}

type spendBus struct {
	broker *broker.DevBroker
	queue  broker.Queue
}

func newSpendBus(t *testing.T) *spendBus {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	q, err := brk.CreateQueue("spend-test", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create queue: %v", err)
	}
	if err := brk.Subscribe(q, a2a.SpendEventTopic(testNamespace)); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	return &spendBus{broker: brk, queue: q}
}

func (b *spendBus) publisher(t *testing.T) *Publisher {
	t.Helper()
	p := NewPublisher(b.broker, testNamespace, discardLogger)
	t.Cleanup(func() { _ = p.Close(context.Background()) })
	return p
}

func (b *spendBus) published(t *testing.T, p *Publisher) []Event {
	t.Helper()
	if err := p.Close(t.Context()); err != nil {
		t.Fatalf("close publisher: %v", err)
	}
	var events []Event
	for {
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
		msg, err := b.broker.Receive(ctx, b.queue)
		cancel()
		if err != nil {
			return events
		}
		var e Event
		if err := json.Unmarshal(msg.Payload, &e); err != nil {
			t.Fatalf("decode spend event: %v", err)
		}
		events = append(events, e)
	}
}

const (
	testUserID       = "0192f1a0-0000-7000-8000-000000000001"
	testAgentID      = "0192f1a0-0000-7000-8000-000000000002"
	testWorkflowID   = "0192f1a0-0000-7000-8000-000000000003"
	testRootID       = "0192f1a0-0000-7000-8000-000000000004"
	testEntrypointID = "0192f1a0-0000-7000-8000-000000000005"
)

func attribution() *llm.SpendAttribution {
	return &llm.SpendAttribution{
		UserID: testUserID, AgentID: testAgentID, WorkflowID: testWorkflowID, WorkflowNodeID: "node-1",
		RootKind: "workflow", RootID: testRootID, RootNodeID: "root-node", EntrypointID: testEntrypointID,
		TriggerKind: "schedule", TriggerID: "trigger-1", Surface: "tool", ToolName: "search",
		ModelKey: "alias-1", TaskID: "task-1", ParentTaskID: "parent-1", TraceID: "trace-1", SessionID: "session-1",
		Channel: "ui",
	}
}

func namedAttribution() *llm.SpendAttribution {
	a := attribution()
	a.UserID, a.UserSub = "", "alice@example.com"
	a.AgentID, a.AgentName = "", "agent-1"
	a.WorkflowID, a.WorkflowName = "", "wf-1"
	a.RootID, a.RootName = "", "wf-root"
	a.EntrypointID, a.EntrypointName = "", "entry-1"
	return a
}

var cachedUsage = &llm.Usage{
	PromptTokens: 1000, CachedTokens: 600, CacheWriteTokens: 200, CompletionTokens: 50, TotalTokens: 1050,
}

func TestRecordingClient_ANonStreamingCallRecordsOneEventWithItsAttributionAndDisjointTokens(t *testing.T) {
	cases := map[string]struct {
		attribution *llm.SpendAttribution
		identities  Event
	}{
		"identities by row UUID": {attribution(), Event{
			UserID: testUserID, AgentID: testAgentID, WorkflowID: testWorkflowID, RootID: testRootID, EntrypointID: testEntrypointID,
		}},
		"identities by name": {namedAttribution(), Event{
			UserSub: "alice@example.com", AgentName: "agent-1", WorkflowName: "wf-1", RootName: "wf-root", EntrypointName: "entry-1",
		}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			bus := newSpendBus(t)
			pub := bus.publisher(t)
			inner := &scriptedClient{resp: &llm.ChatResponse{Content: "hi", Usage: cachedUsage}}
			client := NewRecordingClient(inner, "bound-model", pub)

			req := &llm.ChatRequest{Model: "req-model", ModelOverride: &llm.BifrostConfig{Model: "override-model"}, Spend: c.attribution}
			resp, err := client.ChatCompletion(t.Context(), req)

			if err != nil || resp != inner.resp {
				t.Fatalf("call altered: resp=%v err=%v", resp, err)
			}
			events := bus.published(t, pub)
			if len(events) != 1 {
				t.Fatalf("got %d events, want 1", len(events))
			}
			e := events[0]
			want := c.identities
			want.SchemaVersion, want.ID, want.CompletedTime = EventSchemaVersion, e.ID, e.CompletedTime
			want.WorkflowNodeID, want.RootKind, want.RootNodeID = "node-1", RootWorkflow, "root-node"
			want.TriggerKind, want.TriggerID, want.Surface, want.ToolName = TriggerSchedule, "trigger-1", SurfaceTool, "search"
			want.ModelKey, want.ModelName, want.TaskID, want.ParentTaskID = "alias-1", "openai/override-model", "task-1", "parent-1"
			want.TraceID, want.SessionID = "trace-1", "session-1"
			want.Channel = "ui"
			want.Tokens = Tokens{UncachedInput: 400, Output: 50, CacheRead: 400, CacheWrite: 200}
			want.CacheWritesReported = new(cachedUsage.CacheWritesReported)
			if !reflect.DeepEqual(e, want) {
				t.Errorf("event:\n got %+v\nwant %+v", e, want)
			}
			if e.CompletedTime <= 0 {
				t.Errorf("completed time not stamped: %d", e.CompletedTime)
			}
		})
	}
}

func TestRecordingClient_TheModelFallsBackToTheBoundModel(t *testing.T) {
	bus := newSpendBus(t)
	pub := bus.publisher(t)
	client := NewRecordingClient(&scriptedClient{resp: &llm.ChatResponse{Usage: cachedUsage}}, "bound-model", pub)

	if _, err := client.ChatCompletion(t.Context(), &llm.ChatRequest{Spend: attribution()}); err != nil {
		t.Fatalf("call: %v", err)
	}

	events := bus.published(t, pub)
	if len(events) != 1 || events[0].ModelName != "openai/bound-model" {
		t.Errorf("events = %+v, want one on openai/bound-model", events)
	}
}

func TestRecordingClient_TheModelTheClientSentTheCallToOutranksTheRequestedOne(t *testing.T) {
	cases := map[string]struct {
		inner llm.Client
		call  func(llm.Client, *llm.ChatRequest)
		want  string
	}{
		"unary": {&scriptedClient{resp: &llm.ChatResponse{Usage: cachedUsage, Model: "openai/sent"}}, unaryCall, "openai/sent"},
		"stream": {&scriptedClient{chunks: []llm.StreamChunk{
			{ContentDelta: "hi", Model: "openai/sent"}, {FinishReason: "stop", Usage: cachedUsage, Model: "openai/sent"},
		}}, drainCall, "openai/sent"},
		"a failure the client reports no model for": {&scriptedClient{err: billedErr}, unaryCall, "openai/requested"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			bus := newSpendBus(t)
			pub := bus.publisher(t)

			c.call(NewRecordingClient(c.inner, "bound", pub), &llm.ChatRequest{Model: "requested", Spend: attribution()})

			events := bus.published(t, pub)
			if len(events) != 1 || events[0].ModelName != c.want {
				t.Errorf("events = %+v, want one on %s", events, c.want)
			}
		})
	}
}

func TestRecordingClient_AStreamingCallRecordsOnceWhenTheStreamEndsAndPassesChunksThrough(t *testing.T) {
	bus := newSpendBus(t)
	pub := bus.publisher(t)
	chunks := []llm.StreamChunk{
		{ContentDelta: "he"},
		{ContentDelta: "llo", Usage: &llm.Usage{PromptTokens: 10, CompletionTokens: 1, TotalTokens: 11}},
		{FinishReason: "stop", Usage: cachedUsage},
	}
	client := NewRecordingClient(&scriptedClient{chunks: chunks}, "bound-model", pub)

	stream, err := client.ChatCompletionStream(t.Context(), &llm.ChatRequest{Model: "req-model", Spend: attribution()})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	var got []llm.StreamChunk
	for c := range stream {
		got = append(got, c)
	}

	if len(got) != len(chunks) {
		t.Fatalf("stream altered: got %d chunks, want %d", len(got), len(chunks))
	}
	for i := range chunks {
		if got[i].ContentDelta != chunks[i].ContentDelta || got[i].FinishReason != chunks[i].FinishReason || got[i].Usage != chunks[i].Usage {
			t.Errorf("chunk %d altered: got %+v, want %+v", i, got[i], chunks[i])
		}
	}
	events := bus.published(t, pub)
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	if want := (Tokens{UncachedInput: 400, Output: 50, CacheRead: 400, CacheWrite: 200}); events[0].Tokens != want || events[0].ModelName != "openai/req-model" {
		t.Errorf("event = %+v, want tokens %+v on openai/req-model", events[0], want)
	}
}

func TestRecordingClient_NothingIsRecordedWithoutAttributionOrUsage(t *testing.T) {
	withUsage := &scriptedClient{
		resp:   &llm.ChatResponse{Usage: cachedUsage},
		chunks: []llm.StreamChunk{{FinishReason: "stop", Usage: cachedUsage}},
	}
	withoutUsage := &scriptedClient{
		resp:   &llm.ChatResponse{},
		chunks: []llm.StreamChunk{{ContentDelta: "x"}, {FinishReason: "stop", Usage: &llm.Usage{}}},
	}
	zeroUsage := &scriptedClient{
		resp:   &llm.ChatResponse{Usage: &llm.Usage{}},
		chunks: []llm.StreamChunk{{FinishReason: "stop", Usage: &llm.Usage{}}},
	}
	cases := map[string]struct {
		inner *scriptedClient
		req   *llm.ChatRequest
	}{
		"no attribution": {withUsage, &llm.ChatRequest{Model: "m"}},
		"no usage":       {withoutUsage, &llm.ChatRequest{Model: "m", Spend: attribution()}},
		"all-zero usage": {zeroUsage, &llm.ChatRequest{Model: "m", Spend: attribution()}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			bus := newSpendBus(t)
			pub := bus.publisher(t)
			client := NewRecordingClient(c.inner, "bound", pub)

			if _, err := client.ChatCompletion(t.Context(), c.req); err != nil {
				t.Fatalf("call: %v", err)
			}
			stream, err := client.ChatCompletionStream(t.Context(), c.req)
			if err != nil {
				t.Fatalf("stream: %v", err)
			}
			for range stream {
			}

			if events := bus.published(t, pub); len(events) != 0 {
				t.Errorf("recorded %+v, want nothing", events)
			}
		})
	}
}

func TestRecordingClient_AStreamReportingTokensWithoutATotalIsStillRecorded(t *testing.T) {
	bus := newSpendBus(t)
	pub := bus.publisher(t)
	usage := &llm.Usage{PromptTokens: 30, CompletionTokens: 7}
	client := NewRecordingClient(&scriptedClient{chunks: []llm.StreamChunk{{FinishReason: "stop", Usage: usage}}}, "bound", pub)

	stream, err := client.ChatCompletionStream(t.Context(), &llm.ChatRequest{Model: "m", Spend: attribution()})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	for range stream {
	}

	events := bus.published(t, pub)
	if want := (Tokens{UncachedInput: 30, Output: 7}); len(events) != 1 || events[0].Tokens != want {
		t.Errorf("events = %+v, want one with tokens %+v", events, want)
	}
}

type heldStreamClient struct {
	scriptedClient
	stream chan llm.StreamChunk
}

func (c *heldStreamClient) ChatCompletionStream(ctx context.Context, _ *llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	out := make(chan llm.StreamChunk)
	go func() {
		defer close(out)
		for chunk := range c.stream {
			select {
			case out <- chunk:
			case <-ctx.Done():
				for {
					select {
					case chunk, ok := <-c.stream:
						if !ok {
							return
						}
						out <- chunk
					default:
						return
					}
				}
			}
		}
	}()
	return out, nil
}

func TestRecordingClient_ACancelledConsumerEndsTheStreamAndKeepsTheUsageSeenSoFar(t *testing.T) {
	const outputBuffer = 16
	cases := map[string]struct {
		firstChunk llm.StreamChunk
		want       int
	}{
		"usage seen before the cancel":         {llm.StreamChunk{ContentDelta: "x", Usage: cachedUsage}, 1},
		"no usage seen":                        {llm.StreamChunk{ContentDelta: "x"}, 0},
		"usage arrived but not read by cancel": {llm.StreamChunk{ContentDelta: "x"}, 1},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			bus := newSpendBus(t)
			pub := bus.publisher(t)
			inner := &heldStreamClient{stream: make(chan llm.StreamChunk, 2*outputBuffer)}
			inner.stream <- c.firstChunk
			for range 2*outputBuffer - 2 {
				inner.stream <- llm.StreamChunk{ContentDelta: "x"}
			}
			if name == "usage arrived but not read by cancel" {
				inner.stream <- llm.StreamChunk{FinishReason: "stop", Usage: cachedUsage}
			} else {
				inner.stream <- llm.StreamChunk{ContentDelta: "x"}
			}
			close(inner.stream)
			client := NewRecordingClient(inner, "bound", pub)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			out, err := client.ChatCompletionStream(ctx, &llm.ChatRequest{Model: "m", Spend: attribution()})
			if err != nil {
				t.Fatalf("stream: %v", err)
			}
			for len(out) < outputBuffer {
				time.Sleep(time.Millisecond)
			}
			cancel()

			closed := make(chan struct{})
			go func() {
				defer close(closed)
				for range out {
				}
			}()
			select {
			case <-closed:
			case <-time.After(5 * time.Second):
				t.Fatal("output channel stayed open after the consumer cancelled")
			}
			if events := bus.published(t, pub); len(events) != c.want {
				t.Errorf("got %d events, want %d", len(events), c.want)
			}
		})
	}
}

func TestRecordingClient_ANilPublisherLeavesTheClientUntouched(t *testing.T) {
	inner := &scriptedClient{resp: &llm.ChatResponse{Usage: cachedUsage}}

	client := NewRecordingClient(inner, "bound", nil)

	if client != llm.Client(inner) {
		t.Errorf("client = %T, want the inner client", client)
	}
}

func TestRecordingClient_ErrorsPassThroughWithoutRecording(t *testing.T) {
	bus := newSpendBus(t)
	pub := bus.publisher(t)
	boom := errors.New("boom")
	client := NewRecordingClient(&scriptedClient{err: boom}, "bound", pub)
	req := &llm.ChatRequest{Model: "m", Spend: attribution()}

	if _, err := client.ChatCompletion(t.Context(), req); !errors.Is(err, boom) {
		t.Errorf("ChatCompletion err = %v, want boom", err)
	}
	if _, err := client.ChatCompletionStream(t.Context(), req); !errors.Is(err, boom) {
		t.Errorf("ChatCompletionStream err = %v, want boom", err)
	}
	if events := bus.published(t, pub); len(events) != 0 {
		t.Errorf("recorded %+v, want nothing", events)
	}
}

var fastPublisherTiming = publisherTiming{
	summaryInterval: time.Hour,
	retryBackoff:    time.Millisecond,
	maxRetryBackoff: 5 * time.Millisecond,
	giveUpAfter:     50 * time.Millisecond,
	provisionRetry:  time.Hour,
}

func TestRecordingClient_ABrokerThatRefusesPublishesNeverFailsTheCallAndTheLossIsReported(t *testing.T) {
	disconnected := broker.NewDevBroker()
	logs := &syncBuffer{}
	pub := newPublisher(disconnected, testNamespace, slog.New(slog.NewTextHandler(logs, nil)), fastPublisherTiming)
	client := NewRecordingClient(&scriptedClient{resp: &llm.ChatResponse{Content: "ok", Usage: cachedUsage}}, "bound", pub)

	resp, err := client.ChatCompletion(t.Context(), &llm.ChatRequest{Model: "m", Spend: attribution()})

	if err != nil || resp.Content != "ok" {
		t.Errorf("call failed: resp=%+v err=%v", resp, err)
	}
	if err := pub.Close(t.Context()); err != nil {
		t.Errorf("close: %v", err)
	}
	logged := logs.String()
	for _, want := range []string{"spend event not published; retrying", "level=ERROR", "spend event lost: the broker kept refusing it", dropReasonRefused} {
		if !strings.Contains(logged, want) {
			t.Errorf("log is missing %q:\n%s", want, logged)
		}
	}
	if got := pub.drops.count(); got != 1 {
		t.Errorf("drops = %d, want the refused event counted", got)
	}
}

type flakyBroker struct {
	*broker.DevBroker
	mu       sync.Mutex
	refusals int
	ttls     []time.Duration
}

func (b *flakyBroker) PublishGuaranteed(ctx context.Context, topic string, msg *broker.Message) error {
	b.mu.Lock()
	b.ttls = append(b.ttls, msg.TTL)
	if b.refusals > 0 {
		b.refusals--
		b.mu.Unlock()
		return errors.New("not connected")
	}
	b.mu.Unlock()
	return b.DevBroker.PublishGuaranteed(ctx, topic, msg)
}

func TestPublisher_RetriesARefusedPublishUntilTheBrokerTakesIt(t *testing.T) {
	bus := newSpendBus(t)
	brk := &flakyBroker{DevBroker: bus.broker, refusals: 3}
	pub := newPublisher(brk, testNamespace, discardLogger, fastPublisherTiming)
	pub.giveUpAfterForTest(time.Minute)
	client := NewRecordingClient(&scriptedClient{resp: &llm.ChatResponse{Usage: cachedUsage}}, "bound", pub)

	if _, err := client.ChatCompletion(t.Context(), &llm.ChatRequest{Model: "m", Spend: attribution()}); err != nil {
		t.Fatalf("call: %v", err)
	}

	if events := bus.published(t, pub); len(events) != 1 {
		t.Errorf("published %d events, want the refused event delivered once on retry", len(events))
	}
	brk.mu.Lock()
	defer brk.mu.Unlock()
	for _, ttl := range brk.ttls {
		if ttl != RetentionWindow {
			t.Errorf("published with TTL %v, want the retention window %v", ttl, RetentionWindow)
		}
	}
}

func TestPublisher_EventsAbandonedAtShutdownAreCountedLoggedAndReturned(t *testing.T) {
	brk := newGatedBroker(t)
	logs := &syncBuffer{}
	pub := NewPublisher(brk, testNamespace, slog.New(slog.NewTextHandler(logs, nil)))
	client := NewRecordingClient(&scriptedClient{resp: &llm.ChatResponse{Usage: cachedUsage}}, "bound", pub)
	for range 3 {
		if _, err := client.ChatCompletion(t.Context(), &llm.ChatRequest{Model: "m", Spend: attribution()}); err != nil {
			t.Fatalf("call: %v", err)
		}
	}

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	err := pub.Close(ctx)

	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "3 events unpublished") {
		t.Errorf("close err = %v, want the 3 unpublished events reported with the deadline", err)
	}
	if line := logs.String(); !strings.Contains(line, "level=ERROR") || !strings.Contains(line, "spend events lost at shutdown") ||
		!strings.Contains(line, "events=3") {
		t.Errorf("want an ERROR counting the lost events:\n%s", line)
	}
}

func TestPublisher_AFullBufferIsCountedAsDrops(t *testing.T) {
	brk := newGatedBroker(t)
	pub := NewPublisher(brk, testNamespace, discardLogger)
	e := Event{
		ID: idgen.New(), CompletedTime: time.Now().UnixMilli(), UserSub: "u", Surface: SurfaceAgent, ModelName: "m",
	}

	for range publisherBufferSize + 50 {
		e.ID = idgen.New()
		pub.Record(e)
	}

	if got := pub.drops.count(); got < 49 || got > 50 {
		t.Errorf("drops = %d, want the 50 events past the buffer counted", got)
	}
	close(brk.gate)
	if err := pub.Close(t.Context()); err != nil {
		t.Fatalf("close: %v", err)
	}
}

func TestPublisher_RecordingWhileClosingNeverPanics(t *testing.T) {
	bus := newSpendBus(t)
	pub := NewPublisher(bus.broker, testNamespace, discardLogger)
	e := Event{ID: idgen.New(), CompletedTime: time.Now().UnixMilli(), UserSub: "u", Surface: SurfaceAgent, ModelName: "m"}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 500 {
				pub.Record(e)
			}
		})
	}
	wg.Go(func() { _ = pub.Close(t.Context()) })
	wg.Wait()
}

func TestPublisher_ProvisionsTheSpendQueueSoEventsWaitForAGovernorThatStartsLater(t *testing.T) {
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	pub := NewPublisher(brk, testNamespace, discardLogger)
	client := NewRecordingClient(&scriptedClient{resp: &llm.ChatResponse{Usage: cachedUsage}}, "bound", pub)
	if _, err := client.ChatCompletion(t.Context(), &llm.ChatRequest{Model: "m", Spend: attribution()}); err != nil {
		t.Fatalf("call: %v", err)
	}
	if err := pub.Close(t.Context()); err != nil {
		t.Fatalf("close: %v", err)
	}

	name, opts := EventQueue(testNamespace)
	q, err := brk.CreateQueue(name, opts)
	if err != nil {
		t.Fatalf("governor creates its queue: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if _, err := brk.Receive(ctx, q); err != nil {
		t.Errorf("the governor's queue holds no event published before it started: %v", err)
	}
}

func TestPublisher_RepairsAreSummarisedInTheLog(t *testing.T) {
	logs := &syncBuffer{}
	bus := newSpendBus(t)
	pub := NewPublisher(bus.broker, testNamespace, slog.New(slog.NewTextHandler(logs, nil)))
	e := Event{
		ID: idgen.New(), CompletedTime: time.Now().UnixMilli(), UserSub: "u", Surface: SurfaceAgent, ModelName: "m",
		RootKind: RootAgent,
	}

	pub.Record(e)
	bus.published(t, pub)

	if line := logs.String(); !strings.Contains(line, "spend events repaired") || !strings.Contains(line, "root cleared") {
		t.Errorf("want the repair summarised:\n%s", line)
	}
}

type gatedBroker struct {
	*broker.DevBroker
	gate      chan struct{}
	mu        sync.Mutex
	published int
}

func (b *gatedBroker) PublishGuaranteed(ctx context.Context, topic string, msg *broker.Message) error {
	select {
	case <-b.gate:
	case <-ctx.Done():
		return ctx.Err()
	}
	b.mu.Lock()
	b.published++
	b.mu.Unlock()
	return b.DevBroker.PublishGuaranteed(ctx, topic, msg)
}

func (b *gatedBroker) publishedCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.published
}

func newGatedBroker(t *testing.T) *gatedBroker {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	return &gatedBroker{DevBroker: brk, gate: make(chan struct{})}
}

func TestPublisher_AFullBufferDropsEventsWithoutBlockingTheCaller(t *testing.T) {
	brk := newGatedBroker(t)
	pub := NewPublisher(brk, testNamespace, discardLogger)
	client := NewRecordingClient(&scriptedClient{resp: &llm.ChatResponse{Usage: cachedUsage}}, "bound", pub)
	req := &llm.ChatRequest{Model: "m", Spend: attribution()}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for range publisherBufferSize + 50 {
			if _, err := client.ChatCompletion(t.Context(), req); err != nil {
				t.Errorf("call failed: %v", err)
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		close(brk.gate)
		t.Fatal("recording blocked the caller while the broker was stalled")
	}

	close(brk.gate)
	if err := pub.Close(t.Context()); err != nil {
		t.Fatalf("close: %v", err)
	}
	if got := brk.publishedCount(); got < publisherBufferSize || got > publisherBufferSize+1 {
		t.Errorf("published %d events, want the buffer's %d plus at most the one in flight", got, publisherBufferSize)
	}
}

func TestPublisher_CloseDrainsWhatIsBuffered(t *testing.T) {
	brk := newGatedBroker(t)
	pub := NewPublisher(brk, testNamespace, discardLogger)
	client := NewRecordingClient(&scriptedClient{resp: &llm.ChatResponse{Usage: cachedUsage}}, "bound", pub)
	req := &llm.ChatRequest{Model: "m", Spend: attribution()}
	const calls = 25
	for range calls {
		if _, err := client.ChatCompletion(t.Context(), req); err != nil {
			t.Fatalf("call: %v", err)
		}
	}

	go close(brk.gate)
	if err := pub.Close(t.Context()); err != nil {
		t.Fatalf("close: %v", err)
	}

	if got := brk.publishedCount(); got != calls {
		t.Errorf("published %d events by the time Close returned, want %d", got, calls)
	}
}

func TestPublisher_CloseGivesUpWhenItsContextEnds(t *testing.T) {
	brk := newGatedBroker(t)
	pub := NewPublisher(brk, testNamespace, discardLogger)
	client := NewRecordingClient(&scriptedClient{resp: &llm.ChatResponse{Usage: cachedUsage}}, "bound", pub)
	if _, err := client.ChatCompletion(t.Context(), &llm.ChatRequest{Model: "m", Spend: attribution()}); err != nil {
		t.Fatalf("call: %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	err := pub.Close(ctx)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("close err = %v, want deadline exceeded", err)
	}
}

func TestPublisher_AClosedPublisherDiscardsEvents(t *testing.T) {
	bus := newSpendBus(t)
	pub := bus.publisher(t)
	client := NewRecordingClient(&scriptedClient{resp: &llm.ChatResponse{Usage: cachedUsage}}, "bound", pub)
	req := &llm.ChatRequest{Model: "m", Spend: attribution()}
	if err := pub.Close(t.Context()); err != nil {
		t.Fatalf("close: %v", err)
	}

	if _, err := client.ChatCompletion(t.Context(), req); err != nil {
		t.Fatalf("call after close: %v", err)
	}

	if events := bus.published(t, pub); len(events) != 0 {
		t.Errorf("recorded %+v after close, want nothing", events)
	}
}

func TestPublisher_AnInvalidEventIsNotPublished(t *testing.T) {
	bus := newSpendBus(t)
	pub := bus.publisher(t)
	incomplete := attribution()
	incomplete.UserID = ""
	client := NewRecordingClient(&scriptedClient{resp: &llm.ChatResponse{Usage: cachedUsage}}, "bound", pub)

	if _, err := client.ChatCompletion(t.Context(), &llm.ChatRequest{Model: "m", Spend: incomplete}); err != nil {
		t.Fatalf("call: %v", err)
	}

	if events := bus.published(t, pub); len(events) != 0 {
		t.Errorf("published %+v, want the invalid event dropped", events)
	}
}

type syncBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestPublisher_DropsOfEveryKindAreLoggedOncePerInterval(t *testing.T) {
	valid := Event{
		ID: idgen.New(), CompletedTime: time.Now().UnixMilli(), UserSub: "u", TriggerKind: TriggerChat, Surface: SurfaceAgent,
		ModelName: "m", TaskID: "t", TraceID: "tr",
	}
	invalid := valid
	invalid.UserSub = ""
	cases := map[string]struct {
		close  bool
		event  Event
		reason string
	}{
		"invalid events":                    {false, invalid, "neither user_id nor user_sub is set"},
		"events after the publisher closes": {true, valid, dropReasonClosed},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			logs := &syncBuffer{}
			bus := newSpendBus(t)
			pub := NewPublisher(bus.broker, testNamespace, slog.New(slog.NewTextHandler(logs, nil)))
			t.Cleanup(func() { _ = pub.Close(context.Background()) })
			if c.close {
				if err := pub.Close(t.Context()); err != nil {
					t.Fatalf("close: %v", err)
				}
			}

			for range 200 {
				pub.Record(c.event)
			}

			lines := strings.Split(strings.TrimSpace(logs.String()), "\n")
			if len(lines) != 1 || !strings.Contains(lines[0], "level=ERROR") || !strings.Contains(lines[0], c.reason) ||
				!strings.Contains(lines[0], "totalSinceStart=1") || !strings.Contains(lines[0], c.reason+":t]") ||
				!strings.Contains(lines[0], c.reason+":tr]") {
				t.Errorf("log lines = %q, want one ERROR mentioning %q with its sampled task and trace", lines, c.reason)
			}
			if got := pub.drops.count(); got != 200 {
				t.Errorf("dropped = %d, want 200", got)
			}
		})
	}
}

func TestPublisher_ANilPublisherDiscardsEverything(t *testing.T) {
	var pub *Publisher

	pub.Record(Event{})
	if err := pub.Close(t.Context()); err != nil {
		t.Errorf("close: %v", err)
	}
}

func TestRecordingClient_EveryAttributionFieldReachesTheEventAndEveryCallerKnownEventFieldHasOne(t *testing.T) {
	recordedByTheClient := map[string]bool{"SchemaVersion": true, "ID": true, "CompletedTime": true, "ModelName": true, "Tokens": true, "AttemptCount": true, "CacheWritesReported": true}
	pairs := map[string]string{
		"UserID": "UserSub", "AgentID": "AgentName", "WorkflowID": "WorkflowName",
		"RootID": "RootName", "EntrypointID": "EntrypointName",
	}
	idSide, nameSide := map[string]bool{}, map[string]bool{}
	for id, name := range pairs {
		idSide[id], nameSide[name] = true, true
	}
	idSide["ModelConfigID"] = true
	attributionType := reflect.TypeFor[llm.SpendAttribution]()
	reached := map[string]bool{}
	for _, skip := range []map[string]bool{nameSide, idSide} {
		attribution := &llm.SpendAttribution{}
		av := reflect.ValueOf(attribution).Elem()
		want := map[string]string{}
		for i := range av.NumField() {
			name := attributionType.Field(i).Name
			if skip[name] {
				continue
			}
			if av.Field(i).Type() == reflect.TypeFor[*int64]() {
				want[name] = "4096"
				av.Field(i).Set(reflect.ValueOf(new(int64(4096))))
				continue
			}
			want[name] = "v-" + name
			if idSide[name] {
				want[name] = idgen.New()
			}
			av.Field(i).SetString(want[name])
		}
		bus := newSpendBus(t)
		pub := bus.publisher(t)
		client := NewRecordingClient(&scriptedClient{resp: &llm.ChatResponse{Usage: cachedUsage}}, "bound", pub)
		if _, err := client.ChatCompletion(t.Context(), &llm.ChatRequest{Model: "m", Spend: attribution}); err != nil {
			t.Fatalf("call: %v", err)
		}
		events := bus.published(t, pub)
		if len(events) != 1 {
			t.Fatalf("published %d events, want 1", len(events))
		}
		ev := reflect.ValueOf(events[0])
		for name, value := range want {
			field := ev.FieldByName(name)
			if !field.IsValid() {
				t.Errorf("SpendAttribution.%s has no Event field", name)
				continue
			}
			if got := fmt.Sprint(reflect.Indirect(field).Interface()); got != value {
				t.Errorf("Event.%s = %q, want the attribution's value %q", name, got, value)
			}
			reached[name] = true
		}
	}

	for i := range attributionType.NumField() {
		if name := attributionType.Field(i).Name; !reached[name] {
			t.Errorf("SpendAttribution.%s was never sent", name)
		}
	}
	eventType := reflect.TypeFor[Event]()
	for i := range eventType.NumField() {
		name := eventType.Field(i).Name
		if _, ok := attributionType.FieldByName(name); !ok && !recordedByTheClient[name] {
			t.Errorf("Event.%s has no SpendAttribution field, so no caller can set it", name)
		}
	}
}

func TestRecordingClient_TheAttemptCountOfACallReachesItsEvent(t *testing.T) {
	exhausted := &llm.Error{Message: "retries exhausted", BilledUsage: cachedUsage, Attempts: 3}
	cases := map[string]struct {
		inner llm.Client
		call  func(llm.Client, *llm.ChatRequest)
	}{
		"unary": {&scriptedClient{resp: &llm.ChatResponse{Usage: cachedUsage, Attempts: 3}}, unaryCall},
		"stream": {&scriptedClient{chunks: []llm.StreamChunk{
			{ContentDelta: "hi", Attempts: 3}, {FinishReason: "stop", Usage: cachedUsage, Attempts: 3},
		}}, drainCall},
		"unary failing with billed usage": {&scriptedClient{err: exhausted}, unaryCall},
		"stream failing after content": {&scriptedClient{chunks: []llm.StreamChunk{
			{ContentDelta: "hi", Attempts: 3}, {Err: billedErr},
		}}, drainCall},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			bus := newSpendBus(t)
			pub := bus.publisher(t)

			c.call(NewRecordingClient(c.inner, "bound", pub), &llm.ChatRequest{Model: "m", Spend: attribution()})

			events := bus.published(t, pub)
			if len(events) != 1 || events[0].AttemptCount != 3 {
				t.Fatalf("events = %+v, want one with AttemptCount 3", events)
			}
		})
	}
}

func TestRecordingClient_WhetherTheProviderReportedCacheWritesReachesTheEvent(t *testing.T) {
	for _, reported := range []bool{true, false} {
		usage := *cachedUsage
		usage.CacheWritesReported = reported
		cases := map[string]struct {
			inner llm.Client
			call  func(llm.Client, *llm.ChatRequest)
		}{
			"unary": {&scriptedClient{resp: &llm.ChatResponse{Usage: &usage}}, unaryCall},
			"stream": {&scriptedClient{chunks: []llm.StreamChunk{
				{ContentDelta: "hi"}, {FinishReason: "stop", Usage: &usage},
			}}, drainCall},
			"unary failing with billed usage": {&scriptedClient{err: &llm.Error{Message: "reset", BilledUsage: &usage}}, unaryCall},
		}
		for name, c := range cases {
			t.Run(fmt.Sprintf("%s reported=%t", name, reported), func(t *testing.T) {
				bus := newSpendBus(t)
				pub := bus.publisher(t)

				c.call(NewRecordingClient(c.inner, "bound", pub), &llm.ChatRequest{Model: "m", Spend: attribution()})

				events := bus.published(t, pub)
				if len(events) != 1 || events[0].CacheWritesReported == nil || *events[0].CacheWritesReported != reported {
					t.Fatalf("events = %+v, want one with CacheWritesReported %t", events, reported)
				}
			})
		}
	}
}
