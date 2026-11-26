// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/modelfamily"
	"github.com/SolaceProducts/solace-agent-mesh/internal/usagefacts"
)

func announcedModel(t *testing.T, payload []byte) (family, provider string) {
	t.Helper()
	var envelope struct {
		Result struct {
			Metadata map[string]any `json:"metadata"`
		} `json:"result"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		t.Fatalf("unmarshal status event: %v", err)
	}
	family, _ = envelope.Result.Metadata[a2a.MetadataModelFamily].(string)
	provider, _ = envelope.Result.Metadata[a2a.MetadataModelProvider].(string)
	return family, provider
}

func announcementsIn(t *testing.T, msgs []*broker.Message) (count int, family, provider string) {
	t.Helper()
	for _, msg := range msgs {
		gotFamily, gotProvider := announcedModel(t, msg.Payload)
		if gotFamily == "" && gotProvider == "" {
			continue
		}
		count++
		family, provider = gotFamily, gotProvider
	}
	return count, family, provider
}

func runAnnouncingModel(t *testing.T, taskID, modelID string) []*broker.Message {
	t.Helper()
	agent, _, _ := newTestAgent(&llm.ChatResponse{Content: "done", FinishReason: "stop"})
	agent.cfg.Model = modelID
	agent.cfg.OriginalModel = modelID

	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })
	agent.svc.(*mockServices).brk = brk

	statusTopic := "test-ns/a2a/v1/gateway/status/test-gw/" + taskID
	q, err := brk.CreateQueue("announce-"+taskID, broker.QueueOptions{})
	if err != nil {
		t.Fatalf("create queue: %v", err)
	}
	if err := brk.Subscribe(q, statusTopic); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	props := a2a.UserProperties{ClientID: "c", UserID: "u", A2AStatusTopic: statusTopic}
	if _, err := agent.HandleTask(t.Context(), taskID, newUserMessage("go"), props); err != nil {
		t.Fatalf("HandleTask: %v", err)
	}
	return receiveAll(t, brk, q, 32, 2*time.Second)
}

func TestARunningTaskAnnouncesItsModelBeforeItCouldTimeOut(t *testing.T) {
	msgs := runAnnouncingModel(t, "task-announce-1", "anthropic/claude-sonnet-4-5")
	if len(msgs) == 0 {
		t.Fatal("the run published no status events at all")
	}

	count, family, provider := announcementsIn(t, msgs)
	if count == 0 {
		t.Fatalf("none of the %d status events carried the model classification", len(msgs))
	}
	if family != modelfamily.ClaudeSonnet {
		t.Errorf("announced family = %q, want %q", family, modelfamily.ClaudeSonnet)
	}
	if provider != "anthropic" {
		t.Errorf("announced provider = %q, want %q", provider, "anthropic")
	}
}

func TestTheAnnouncementIsMadeOnceAndAddsNoPublishOfItsOwn(t *testing.T) {
	msgs := runAnnouncingModel(t, "task-announce-once", "anthropic/claude-sonnet-4-5")
	count, _, _ := announcementsIn(t, msgs)
	if count != 1 {
		t.Errorf("%d of %d status events carried the classification, want exactly 1", count, len(msgs))
	}
}

func TestTheAnnouncementCarriesOnlyClosedEnums(t *testing.T) {
	for i, modelID := range []string{
		"ft:gpt-4o:acme-corp:legal-triage:AbCd1234",
		"arn:aws:bedrock:us-east-1:123456789012:inference-profile/anthropic.claude-sonnet-4-5",
		"acme-prod-westeurope-deployment",
	} {
		t.Run(modelID, func(t *testing.T) {
			msgs := runAnnouncingModel(t, "task-announce-raw-"+string(rune('a'+i)), modelID)
			count, family, provider := announcementsIn(t, msgs)
			if count == 0 {
				t.Fatal("no status event carried a classification, so this proves nothing about what it contains")
			}
			for _, secret := range []string{
				modelID, "acme-corp", "legal-triage", "AbCd1234",
				"123456789012", "westeurope",
			} {
				if strings.Contains(family, secret) || strings.Contains(provider, secret) {
					t.Fatalf("%q reached the announced pair (%q, %q)", secret, family, provider)
				}
			}
			if !slices.Contains(modelfamily.Families, family) {
				t.Errorf("announced family = %q, which is not one of the closed set", family)
			}
			if !slices.Contains(llm.ProviderLabels(), provider) {
				t.Errorf("announced provider = %q, which is not one of the closed set", provider)
			}
		})
	}
}

func TestTheAnnouncementFollowsTheModelOverride(t *testing.T) {
	agent, _, _ := newTestAgent()
	sp := newCollectingStatusPublisher(t, "task-announce-override")
	state := newTestTaskState(agent)
	state.ModelOverride = &llm.BifrostConfig{Model: "gemini-2.5-flash", Provider: "vertex"}

	sp.publisher.ClassifyModel(state.usageFacts.recordModel(callModel(state, "anthropic/claude-sonnet-4-5", "")))
	sp.publisher.PublishText(t.Context(), "hello")

	msgs := receiveAll(t, sp.broker, sp.queue, 1, 2*time.Second)
	if len(msgs) != 1 {
		t.Fatalf("published %d status events, want 1", len(msgs))
	}
	family, provider := announcedModel(t, msgs[0].Payload)
	if family != modelfamily.GeminiFlash {
		t.Errorf("announced family = %q, want the override's %q", family, modelfamily.GeminiFlash)
	}
	if provider != "vertex" {
		t.Errorf("announced provider = %q, want the override's %q", provider, "vertex")
	}
}

func TestOnlyANewClassificationIsAnnouncedAgain(t *testing.T) {
	sp := newCollectingStatusPublisher(t, "task-announce-change")

	sp.publisher.ClassifyModel(usagefacts.Model{Family: modelfamily.ClaudeSonnet, Provider: "anthropic"})
	sp.publisher.PublishText(t.Context(), "one")
	sp.publisher.ClassifyModel(usagefacts.Model{Family: modelfamily.ClaudeSonnet, Provider: "anthropic"})
	sp.publisher.PublishText(t.Context(), "two")
	sp.publisher.ClassifyModel(usagefacts.Model{Family: modelfamily.GPT4o, Provider: "openai"})
	sp.publisher.PublishText(t.Context(), "three")

	msgs := receiveAll(t, sp.broker, sp.queue, 3, 2*time.Second)
	if len(msgs) != 3 {
		t.Fatalf("published %d status events, want 3", len(msgs))
	}
	wantFamilies := []string{modelfamily.ClaudeSonnet, "", modelfamily.GPT4o}
	wantProviders := []string{"anthropic", "", "openai"}
	for i, msg := range msgs {
		family, provider := announcedModel(t, msg.Payload)
		if family != wantFamilies[i] || provider != wantProviders[i] {
			t.Errorf("event %d announced (%q, %q), want (%q, %q)",
				i, family, provider, wantFamilies[i], wantProviders[i])
		}
	}
}

func TestAFailedPublishReArmsTheAnnouncement(t *testing.T) {
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })
	flaky := &refusingBroker{Broker: brk}

	statusTopic := "test-ns/a2a/v1/gateway/status/test-gw/task-announce-retry"
	q, err := brk.CreateQueue("announce-retry", broker.QueueOptions{})
	if err != nil {
		t.Fatalf("create queue: %v", err)
	}
	if err := brk.Subscribe(q, statusTopic); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	publisher := NewStatusPublisher(flaky, StatusPublisherConfig{
		StatusTopic: statusTopic,
		TaskID:      "task-announce-retry",
		AgentName:   "test-agent",
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	publisher.ClassifyModel(usagefacts.Model{Family: modelfamily.ClaudeSonnet, Provider: "anthropic"})
	flaky.refuse.Store(true)
	publisher.PublishText(t.Context(), "lost")
	flaky.refuse.Store(false)
	publisher.PublishText(t.Context(), "delivered")

	msgs := receiveAll(t, brk, q, 1, 2*time.Second)
	if len(msgs) != 1 {
		t.Fatalf("the broker accepted %d status events, want 1", len(msgs))
	}
	family, provider := announcedModel(t, msgs[0].Payload)
	if family != modelfamily.ClaudeSonnet || provider != "anthropic" {
		t.Errorf("the delivered event announced (%q, %q), want the classification the refused one carried",
			family, provider)
	}
}

func TestAForwardedPeerEventCarriesNoModelAnnouncement(t *testing.T) {
	sp := newCollectingStatusPublisher(t, "peer-subtask-1")
	sp.publisher.ClassifyModel(usagefacts.Model{Family: modelfamily.GPT4o, Provider: "openai"})
	sp.publisher.PublishLLMInvocation(t.Context(), &llm.ChatRequest{Model: "gpt-4o"}, "gpt-4o")

	msgs := receiveAll(t, sp.broker, sp.queue, 1, 2*time.Second)
	if len(msgs) != 1 {
		t.Fatalf("the peer published %d status events, want 1", len(msgs))
	}
	if family, _ := announcedModel(t, msgs[0].Payload); family != modelfamily.GPT4o {
		t.Fatalf("the peer's own event announced %q, want %q — nothing is being forwarded here",
			family, modelfamily.GPT4o)
	}

	forwarded, sigType := rewriteStatusEvent(msgs[0].Payload, "parent-task-1", "orchestrator", "peer-agent", "call-1", false)
	if forwarded == nil {
		t.Fatalf("the event was filtered rather than forwarded (signal %q)", sigType)
	}
	family, provider := announcedModel(t, forwarded)
	if family != "" || provider != "" {
		t.Errorf("the forwarded event announced (%q, %q), want the peer's model left with the peer's run",
			family, provider)
	}
}

type collectingStatusPublisher struct {
	publisher *StatusPublisher
	broker    broker.Broker
	queue     broker.Queue
}

func newCollectingStatusPublisher(t *testing.T, taskID string) *collectingStatusPublisher {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	statusTopic := "test-ns/a2a/v1/gateway/status/test-gw/" + taskID
	q, err := brk.CreateQueue("collect-"+taskID, broker.QueueOptions{})
	if err != nil {
		t.Fatalf("create queue: %v", err)
	}
	if err := brk.Subscribe(q, statusTopic); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	return &collectingStatusPublisher{
		publisher: NewStatusPublisher(brk, StatusPublisherConfig{
			StatusTopic: statusTopic,
			TaskID:      taskID,
			ContextID:   "ctx-" + taskID,
			AgentName:   "test-agent",
		}, slog.New(slog.NewTextHandler(io.Discard, nil))),
		broker: brk,
		queue:  q,
	}
}

type refusingBroker struct {
	broker.Broker
	refuse atomic.Bool
}

func (b *refusingBroker) PublishGuaranteed(ctx context.Context, topic string, msg *broker.Message) error {
	if b.refuse.Load() {
		return errors.New("broker refused the publish")
	}
	return b.Broker.PublishGuaranteed(ctx, topic, msg)
}
