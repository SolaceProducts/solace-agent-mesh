// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/discovery"
	"github.com/SolaceProducts/solace-agent-mesh/internal/health"
)

type countingBroker struct {
	stubBroker
	mu                  sync.Mutex
	createdQueues       []string
	directSubCount      int
	directSubSeq        uint64
	deletedQueues       []string
	deprovisionedQueues []string
}

func (b *countingBroker) CreateQueue(name string, _ broker.QueueOptions) (broker.Queue, error) {
	b.mu.Lock()
	b.createdQueues = append(b.createdQueues, name)
	b.mu.Unlock()
	return &broker.SimpleQueue{QName: name}, nil
}

func (b *countingBroker) CreateDirectSubscription() (broker.Queue, error) {
	b.mu.Lock()
	b.directSubSeq++
	b.directSubCount++
	name := "direct-mock-" + strconv.FormatUint(b.directSubSeq, 10)
	b.mu.Unlock()
	return &broker.SimpleQueue{QName: name, DirectSubscription: true}, nil
}

func (b *countingBroker) DeleteQueue(name string) error {
	b.mu.Lock()
	b.deletedQueues = append(b.deletedQueues, name)
	b.mu.Unlock()
	return nil
}

func (b *countingBroker) DeprovisionQueue(name string) error {
	b.mu.Lock()
	b.deprovisionedQueues = append(b.deprovisionedQueues, name)
	b.mu.Unlock()
	return nil
}

func TestWorkflow_OnlyOneDurableQueue(t *testing.T) {
	cfg := &WorkflowConfig{
		Name:      "one-q-workflow",
		Namespace: "test-ns",
		Nodes:     []*NodeConfig{{ID: "step1", Type: "agent", AgentName: "agentA"}},
	}

	brk := &countingBroker{}
	w := NewWorkflowInstance(cfg)
	if err := w.Init(context.Background(), nil, &stubServices{brk: brk}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := w.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = w.Stop(context.Background()) })

	brk.mu.Lock()
	defer brk.mu.Unlock()

	var durable []string
	for _, name := range brk.createdQueues {
		if !strings.HasPrefix(name, "direct-mock-") {
			durable = append(durable, name)
		}
	}
	if len(durable) != 1 {
		t.Fatalf("Workflow must create exactly 1 durable queue (request). Got %d: %v",
			len(durable), durable)
	}
	if durable[0] != "test-ns/q/a2a/one-q-workflow" {
		t.Errorf("durable queue name = %q, want %q", durable[0], "test-ns/q/a2a/one-q-workflow")
	}
	if brk.directSubCount < 1 {
		t.Errorf("Workflow must create >=1 direct subscription (response/status/discovery). Got %d",
			brk.directSubCount)
	}
}

func TestWorkflow_RemoveDeprovisionsRequestQueue(t *testing.T) {
	cfg := &WorkflowConfig{
		Name:      "remove-workflow",
		Namespace: "test-ns",
		Nodes:     []*NodeConfig{{ID: "step1", Type: "agent", AgentName: "agentA"}},
	}

	brk := &countingBroker{}
	w := NewWorkflowInstance(cfg)
	if err := w.Init(context.Background(), nil, &stubServices{brk: brk}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := w.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if err := w.Remove(context.Background()); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	brk.mu.Lock()
	defer brk.mu.Unlock()
	if len(brk.deprovisionedQueues) != 1 {
		t.Fatalf("Workflow.Remove deprovisioned %d queues, want exactly 1 (request). Got: %v",
			len(brk.deprovisionedQueues), brk.deprovisionedQueues)
	}
	if brk.deprovisionedQueues[0] != "test-ns/q/a2a/remove-workflow" {
		t.Errorf("Workflow.Remove deprovisioned %q, want %q",
			brk.deprovisionedQueues[0], "test-ns/q/a2a/remove-workflow")
	}
}

type publishingBroker struct {
	countingBroker
	pubMu          sync.Mutex
	published      []*broker.Message
	publishErr     error
	deprovisionErr error
}

func (b *publishingBroker) Publish(_ context.Context, _ string, msg *broker.Message) error {
	b.pubMu.Lock()
	b.published = append(b.published, msg)
	b.pubMu.Unlock()
	return b.publishErr
}

func (b *publishingBroker) DeprovisionQueue(name string) error {
	_ = b.countingBroker.DeprovisionQueue(name)
	return b.deprovisionErr
}

func (b *publishingBroker) removalCards() int {
	b.pubMu.Lock()
	defer b.pubMu.Unlock()
	n := 0
	for _, m := range b.published {
		if m.UserProperties["removed"] == "true" {
			n++
		}
	}
	return n
}

func TestWorkflow_RemovePublishesRemovalCard(t *testing.T) {
	cfg := &WorkflowConfig{
		Name:      "removal-card-workflow",
		Namespace: "test-ns",
		Nodes:     []*NodeConfig{{ID: "step1", Type: "agent", AgentName: "agentA"}},
	}
	brk := &publishingBroker{}
	w := NewWorkflowInstance(cfg)
	if err := w.Init(context.Background(), nil, &stubServices{brk: brk}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := w.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := w.Remove(context.Background()); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if got := brk.removalCards(); got != 1 {
		t.Fatalf("Remove published %d removal cards, want 1", got)
	}
	brk.pubMu.Lock()
	last := brk.published[len(brk.published)-1]
	brk.pubMu.Unlock()
	if last.UserProperties["removed"] != "true" {
		t.Error("a card was published after the removal card")
	}
	instanceID := last.UserProperties["instance_id"]
	if instanceID == "" {
		t.Fatal("removal card carries no instance_id")
	}
	brk.pubMu.Lock()
	defer brk.pubMu.Unlock()
	for _, m := range brk.published {
		if got := m.UserProperties["instance_id"]; got != instanceID {
			t.Errorf("card instance_id = %q, removal card's = %q; want the same", got, instanceID)
		}
	}
}

func TestWorkflow_StopPublishesInstanceRemoval(t *testing.T) {
	cfg := &WorkflowConfig{
		Name:      "stop-workflow",
		Namespace: "test-ns",
		Nodes:     []*NodeConfig{{ID: "step1", Type: "agent", AgentName: "agentA"}},
	}
	brk := &publishingBroker{}
	w := NewWorkflowInstance(cfg)
	if err := w.Init(context.Background(), nil, &stubServices{brk: brk}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := w.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := w.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if got := brk.removalCards(); got != 1 {
		t.Fatalf("Stop published %d removal cards, want 1", got)
	}
	brk.pubMu.Lock()
	defer brk.pubMu.Unlock()
	last := brk.published[len(brk.published)-1]
	if last.UserProperties["removed"] != "true" {
		t.Fatal("a card was published after the removal card")
	}
	if last.UserProperties["instance_id"] != w.instanceID || w.instanceID == "" {
		t.Errorf("removal instance_id = %q, want this instance's %q", last.UserProperties["instance_id"], w.instanceID)
	}
}

type unreachableBroker struct{ countingBroker }

func (*unreachableBroker) CreateDirectSubscription() (broker.Queue, error) {
	return nil, errors.New("broker unreachable")
}

func TestWorkflow_CardReportsBrokenDiscovery(t *testing.T) {
	cfg := &WorkflowConfig{
		Name:      "discovery-health-workflow",
		Namespace: "test-ns",
		Nodes:     []*NodeConfig{{ID: "step1", Type: "agent", AgentName: "agentA"}},
	}
	w := NewWorkflowInstance(cfg)
	if err := w.Init(context.Background(), nil, &stubServices{brk: &publishingBroker{}}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	dir := discovery.NewDirectory(&unreachableBroker{}, cfg.Namespace, slog.New(slog.DiscardHandler))
	t.Cleanup(func() { _ = dir.Close() })
	w.SetPeerDirectory(dir)
	if err := w.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = w.Stop(context.Background()) })

	problems := a2a.HealthProblemsFromCard(w.BuildAgentCard())
	if len(problems) == 0 {
		t.Fatal("the workflow card reports no problem although its discovery subscription failed")
	}
	if want := dir.HealthSnapshot(); len(want) == 0 || !slices.ContainsFunc(problems, func(p health.Problem) bool { return p.ID == want[0].ID }) {
		t.Errorf("card problems = %+v, want the directory's %+v", problems, want)
	}
}

func TestWorkflow_RemoveStillDeprovisionsWhenRemovalCardPublishFails(t *testing.T) {
	cfg := &WorkflowConfig{
		Name:      "removal-publish-fails-workflow",
		Namespace: "test-ns",
		Nodes:     []*NodeConfig{{ID: "step1", Type: "agent", AgentName: "agentA"}},
	}
	brk := &publishingBroker{publishErr: errors.New("publish rejected")}
	w := NewWorkflowInstance(cfg)
	if err := w.Init(context.Background(), nil, &stubServices{brk: brk}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := w.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := w.Remove(context.Background()); err != nil {
		t.Fatalf("Remove returned %v, want nil: a failed removal card must not fail the undeploy", err)
	}
	if got := brk.removalCards(); got != 1 {
		t.Errorf("Remove attempted %d removal cards, want 1", got)
	}
	brk.mu.Lock()
	defer brk.mu.Unlock()
	if len(brk.deprovisionedQueues) != 1 {
		t.Errorf("Remove deprovisioned %d queues after the removal card failed, want 1: %v",
			len(brk.deprovisionedQueues), brk.deprovisionedQueues)
	}
}

func TestWorkflow_RemoveSucceedsWhenDeprovisionFails(t *testing.T) {
	cfg := &WorkflowConfig{
		Name:      "deprovision-fails-workflow",
		Namespace: "test-ns",
		Nodes:     []*NodeConfig{{ID: "step1", Type: "agent", AgentName: "agentA"}},
	}
	brk := &publishingBroker{deprovisionErr: errors.New("deprovision rejected")}
	w := NewWorkflowInstance(cfg)
	if err := w.Init(context.Background(), nil, &stubServices{brk: brk}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := w.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := w.Remove(context.Background()); err != nil {
		t.Fatalf("Remove returned %v, want nil: a deprovision failure must not fail the undeploy", err)
	}
	if got := brk.removalCards(); got != 1 {
		t.Errorf("Remove published %d removal cards, want 1", got)
	}
	brk.mu.Lock()
	defer brk.mu.Unlock()
	if len(brk.deprovisionedQueues) != 1 {
		t.Errorf("Remove attempted %d deprovisions, want 1", len(brk.deprovisionedQueues))
	}
}
