// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build product_analytics

package entrypoint

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/modelfamily"
	"github.com/SolaceProducts/solace-agent-mesh/internal/productanalytics"
	"github.com/SolaceProducts/solace-agent-mesh/internal/usagefacts"
)

func (h *runAnalyticsHarness) observeStatusUpdates() <-chan string {
	seen := make(chan string, 16)
	h.core.OnStatusUpdate(func(taskID string, _ *a2a.TaskStatusUpdateEvent) {
		select {
		case seen <- taskID:
		default:
		}
	})
	return seen
}

func (h *runAnalyticsHarness) announceModel(t *testing.T, seen <-chan string, taskID, family, provider string) {
	t.Helper()
	metadata := map[string]any{"agent_name": "acme-payroll-assistant"}
	if family != "" {
		metadata[a2a.MetadataModelFamily] = family
	}
	if provider != "" {
		metadata[a2a.MetadataModelProvider] = provider
	}
	h.publishStatus(t, seen, taskID, metadata)
}

func (h *runAnalyticsHarness) publishStatus(t *testing.T, seen <-chan string, taskID string, metadata map[string]any) {
	t.Helper()
	event := a2a.TaskStatusUpdateEvent{
		TaskID: taskID,
		Kind:   "status-update",
		Status: a2a.TaskStatus{
			State: a2a.TaskStateWorking,
			Message: &a2a.Message{
				Kind: "message", Role: a2a.RoleAgent,
				Parts: []a2a.Part{a2a.NewTextPart("working")},
			},
		},
		Metadata: metadata,
	}
	payload, err := json.Marshal(a2a.NewResponse(taskID, event))
	if err != nil {
		t.Fatalf("marshal status event: %v", err)
	}
	topic := a2a.GatewayStatusTopic(h.core.Namespace(), h.core.cfg.GatewayID, taskID)
	if err := h.core.svc.Broker().PublishGuaranteed(context.Background(), topic, &broker.Message{Payload: payload}); err != nil {
		t.Fatalf("publish status event: %v", err)
	}
	select {
	case <-seen:
	case <-time.After(5 * time.Second):
		t.Fatal("the status event was never delivered to the gateway")
	}
}

func timedOut() *a2a.Error {
	return &a2a.Error{
		Code:    TaskTimeoutErrorCode,
		Message: "The request timed out. Please try again.",
		Data:    map[string]any{"taskStatus": "timeout", "timeout_seconds": 3600},
	}
}

func TestATimedOutRunReportsTheModelItWasOn(t *testing.T) {
	err := timedOut()

	info := &TaskInfo{
		TaskOrigin: TaskOrigin{AgentType: productanalytics.AgentTypeUserCreated},
		Model:      usagefacts.Model{Family: modelfamily.ClaudeSonnet, Provider: "bedrock"},
	}
	props := runReportProperties(info, nil, err, readUsageFacts(nil, err).orAnnounced(info))
	if got := props[productanalytics.PropOutcome]; got != productanalytics.OutcomeTimeout {
		t.Fatalf("outcome = %v, want %q — this is not the timeout path", got, productanalytics.OutcomeTimeout)
	}
	if got := props[productanalytics.PropModel]; got != modelfamily.ClaudeSonnet {
		t.Errorf("model = %v, want %q", got, modelfamily.ClaudeSonnet)
	}
	if got := props[productanalytics.PropProvider]; got != "bedrock" {
		t.Errorf("provider = %v, want %q", got, "bedrock")
	}

	h := newRunAnalyticsHarness(t)
	seen := h.observeStatusUpdates()
	h.core.agents.AddOrUpdate(testAgentCard("my-agent"))
	taskID := h.submit(t, "my-agent", "task-timeout-model", nil)
	h.announceModel(t, seen, taskID, modelfamily.ClaudeSonnet, "bedrock")
	h.core.fireTaskCompleteHooks(taskID, nil, err)

	ev := onlyEvent(t, h.drain(t))
	if got := ev.Properties[productanalytics.PropOutcome]; got != productanalytics.OutcomeTimeout {
		t.Fatalf("outcome = %v, want %q", got, productanalytics.OutcomeTimeout)
	}
	if got := ev.Properties[productanalytics.PropModel]; got != modelfamily.ClaudeSonnet {
		t.Errorf("delivered model = %v, want %q", got, modelfamily.ClaudeSonnet)
	}
	if got := ev.Properties[productanalytics.PropProvider]; got != "bedrock" {
		t.Errorf("delivered provider = %v, want %q", got, "bedrock")
	}
}

func TestTheAnnouncedModelDoesNotDisplaceWhatTheRunReported(t *testing.T) {
	h := newRunAnalyticsHarness(t)
	seen := h.observeStatusUpdates()
	h.core.agents.AddOrUpdate(testAgentCard("my-agent"))
	taskID := h.submit(t, "my-agent", "task-model-both", nil)

	h.announceModel(t, seen, taskID, modelfamily.GPT4o, "openai")
	h.publishAgentTask(t, taskID, modelMetadata(modelfamily.ClaudeSonnet, "bedrock"))

	ev := onlyEvent(t, h.drain(t))
	if got := ev.Properties[productanalytics.PropModel]; got != modelfamily.ClaudeSonnet {
		t.Errorf("model = %v, want the terminal message's %q", got, modelfamily.ClaudeSonnet)
	}
	if got := ev.Properties[productanalytics.PropProvider]; got != "bedrock" {
		t.Errorf("provider = %v, want the terminal message's %q", got, "bedrock")
	}
}

func TestAnUnapprovedAnnouncementNeverReachesTheRegistry(t *testing.T) {
	cases := map[string]struct{ family, provider string }{
		"a fine-tune id naming the customer":   {"ft:gpt-4o:acme-corp:custom:9xYz", "openai"},
		"a Bedrock inference-profile ARN":      {"arn:aws:bedrock:us-east-1:123456789012:inference-profile/acme-legal", "bedrock"},
		"an Azure deployment name":             {"acme-prod-gpt4o-westeurope", "azure"},
		"the identifier a family reduces from": {"claude-sonnet-4-5", "anthropic"},
		"a provider an operator named":         {modelfamily.Llama, "acme-internal-vllm"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			h := newRunAnalyticsHarness(t)
			seen := h.observeStatusUpdates()
			h.core.agents.AddOrUpdate(testAgentCard("my-agent"))
			taskID := h.submit(t, "my-agent", "task-refuse-"+strings.ReplaceAll(name, " ", "-"), nil)

			h.announceModel(t, seen, taskID, c.family, c.provider)

			info := h.core.tasks.Get(taskID)
			if info == nil {
				t.Fatal("the task is no longer tracked, so nothing here is being tested")
			}
			if !approvedFamily(c.family) && info.Model.Family != "" {
				t.Errorf("the registry kept family %q from the wire", info.Model.Family)
			}
			if !approvedProvider(c.provider) && info.Model.Provider != "" {
				t.Errorf("the registry kept provider %q from the wire", info.Model.Provider)
			}

			h.core.fireTaskCompleteHooks(taskID, nil, timedOut())
			ev := onlyEvent(t, h.drain(t))
			rendered, err := json.Marshal(ev.Properties)
			if err != nil {
				t.Fatalf("marshal properties: %v", err)
			}
			for _, unapproved := range []string{c.family, c.provider} {
				if approvedFamily(unapproved) || approvedProvider(unapproved) {
					continue
				}
				if strings.Contains(string(rendered), unapproved) {
					t.Errorf("%q reached the delivered event %s", unapproved, rendered)
				}
			}
		})
	}
}

func approvedFamily(v string) bool { return knownValue(v, modelfamily.Families) != "" }
func approvedProvider(v string) bool {
	return knownValue(v, productanalytics.ProviderLabels) != ""
}

func TestTheLatestAnnouncementIsTheOneReported(t *testing.T) {
	h := newRunAnalyticsHarness(t)
	seen := h.observeStatusUpdates()
	h.core.agents.AddOrUpdate(testAgentCard("my-agent"))
	taskID := h.submit(t, "my-agent", "task-model-latest", nil)

	h.announceModel(t, seen, taskID, modelfamily.GPT4o, "openai")
	h.announceModel(t, seen, taskID, modelfamily.ClaudeSonnet, "bedrock")
	h.publishStatus(t, seen, taskID, map[string]any{"agent_name": "acme-payroll-assistant"})

	h.core.fireTaskCompleteHooks(taskID, nil, timedOut())

	ev := onlyEvent(t, h.drain(t))
	if got := ev.Properties[productanalytics.PropModel]; got != modelfamily.ClaudeSonnet {
		t.Errorf("model = %v, want the latest announcement %q", got, modelfamily.ClaudeSonnet)
	}
	if got := ev.Properties[productanalytics.PropProvider]; got != "bedrock" {
		t.Errorf("provider = %v, want the latest announcement %q", got, "bedrock")
	}
}

func TestACancelledRunAlsoReportsTheModelItWasOn(t *testing.T) {
	h := newRunAnalyticsHarness(t)
	seen := h.observeStatusUpdates()
	h.core.agents.AddOrUpdate(testAgentCard("my-agent"))
	taskID := h.submit(t, "my-agent", "task-model-cancelled", nil)

	h.announceModel(t, seen, taskID, modelfamily.ClaudeSonnet, "bedrock")
	h.core.fireTaskCompleteHooks(taskID,
		&a2a.Task{ID: taskID, Status: a2a.TaskStatus{State: a2a.TaskStateCancelled}}, nil)

	ev := onlyEvent(t, h.drain(t))
	if got := ev.Properties[productanalytics.PropOutcome]; got != productanalytics.OutcomeCancelled {
		t.Fatalf("outcome = %v, want %q", got, productanalytics.OutcomeCancelled)
	}
	if got := ev.Properties[productanalytics.PropModel]; got != modelfamily.ClaudeSonnet {
		t.Errorf("model = %v, want %q", got, modelfamily.ClaudeSonnet)
	}
}

func TestAnAnnouncementForAnUntrackedTaskIsDropped(t *testing.T) {
	h := newRunAnalyticsHarness(t)
	seen := h.observeStatusUpdates()

	h.announceModel(t, seen, "task-never-registered", modelfamily.ClaudeSonnet, "bedrock")

	if info := h.core.tasks.Get("task-never-registered"); info != nil {
		t.Errorf("the registry now holds %#v for a task it never registered", info)
	}
}
