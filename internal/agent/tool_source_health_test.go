// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/health"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

func startAgentWithRegistrations(t *testing.T, name string, regs []ToolEntryRegistration) *Agent {
	t.Helper()
	agent := New(AgentConfig{Name: name, Namespace: "test-ns"})
	svc := newMockServices(newMockLLM(&llm.ChatResponse{Content: "ok"}), newMemSessionStore())
	svc.brk = newMockBroker()
	agent.svc = svc
	agent.Init(context.Background(), nil, svc) //nolint:errcheck // test helper
	agent.SetToolRegistrations(regs)
	if err := agent.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { agent.Stop(context.Background()) }) //nolint:errcheck // test helper
	return agent
}

func TestAgentStart_FailedConnectorToolSourceIsReportedOnCard(t *testing.T) {
	agent := startAgentWithRegistrations(t, "failed-source-agent", []ToolEntryRegistration{
		{ToolType: "mcp", ConnectorID: "conn-dead", Status: "failed", Error: "connection refused"},
		{ToolType: "mcp", ConnectorID: "conn-ok", Status: "ready", ToolCount: 2},
	})

	problems := a2a.HealthProblemsFromCard(agent.BuildAgentCard())
	if len(problems) != 1 {
		t.Fatalf("card health problems = %v, want exactly the failed connector", problems)
	}
	got := problems[0]
	if got.ID != "toolSource:conn-dead" {
		t.Errorf("problem id = %q, want it to name the failed connector", got.ID)
	}
	if got.Severity != health.Fatal {
		t.Errorf("severity = %v, want Fatal: the agent runs without the tool source", got.Severity)
	}
	if got.Detail != string(health.DetailToolSourceUnavailable) {
		t.Errorf("detail = %q, want the closed-vocabulary tool source detail", got.Detail)
	}
}

func TestAgentStart_HealthyToolSourcesReportNoProblem(t *testing.T) {
	agent := startAgentWithRegistrations(t, "healthy-source-agent", []ToolEntryRegistration{
		{ToolType: "mcp", ConnectorID: "conn-ok", Status: "ready", ToolCount: 2},
	})

	if problems := a2a.HealthProblemsFromCard(agent.BuildAgentCard()); problems != nil {
		t.Fatalf("card health problems = %v, want none", problems)
	}
}

func TestAgentStart_FailedHandAuthoredToolSourcesEachGetTheirOwnProblem(t *testing.T) {
	agent := startAgentWithRegistrations(t, "hand-authored-agent", []ToolEntryRegistration{
		{ToolType: "mcp", Status: "failed", Error: "connection refused"},
		{ToolType: "mcp", Status: "failed", Error: "connection refused"},
	})

	if problems := a2a.HealthProblemsFromCard(agent.BuildAgentCard()); len(problems) != 2 {
		t.Fatalf("card health problems = %v, want one per failed entry", problems)
	}
}

func toolRegistrationEntries(t *testing.T, card a2a.AgentCard) []map[string]any {
	t.Helper()
	for _, ext := range card.Capabilities.Extensions {
		if ext.URI != ExtensionURIToolRegistrations {
			continue
		}
		raw, _ := ext.Params["entries"].([]any)
		entries := make([]map[string]any, len(raw))
		for i, e := range raw {
			entries[i], _ = e.(map[string]any)
		}
		return entries
	}
	t.Fatal("card has no tool-registrations extension")
	return nil
}

func TestLateToolRegistration_ReplacesTheFailedEntryAndClearsItsProblem(t *testing.T) {
	agent := startAgentWithRegistrations(t, "late-source-agent", []ToolEntryRegistration{
		{ToolType: "mcp", ConnectorID: "conn-runner", Status: "failed", Error: "connection refused"},
		{ToolType: "mcp", ConnectorID: "conn-jira", Status: "ready", ToolCount: 1, ToolNames: []string{"search"}},
	})

	var wg sync.WaitGroup
	wg.Go(func() {
		agent.ReplaceToolRegistration(0, ToolEntryRegistration{ToolType: "mcp", ConnectorID: "conn-runner", Status: "ready", ToolCount: 1, ToolNames: []string{"job_status"}})
	})
	wg.Go(func() { _ = agent.BuildAgentCard() })
	wg.Wait()

	card := agent.BuildAgentCard()
	entries := toolRegistrationEntries(t, card)
	if len(entries) != 2 {
		t.Fatalf("entries = %v, want 2: a late registration replaces its entry", entries)
	}
	runner := entries[0]
	if runner["connector_id"] != "conn-runner" || runner["status"] != "ready" || runner["tool_count"] != 1 {
		t.Errorf("runner entry = %v, want conn-runner ready with one tool", runner)
	}
	if _, present := runner["error"]; present {
		t.Errorf("runner entry still carries the boot error: %v", runner)
	}
	if problems := a2a.HealthProblemsFromCard(card); problems != nil {
		t.Errorf("card health problems = %v, want none once the tool source registered", problems)
	}
}

func TestLateToolRegistration_KeepsAProblemAnotherFailedEntryStillReports(t *testing.T) {
	agent := startAgentWithRegistrations(t, "shared-connector-agent", []ToolEntryRegistration{
		{ToolType: "mcp", ConnectorID: "conn-shared", Status: "failed"},
		{ToolType: "mcp", ConnectorID: "conn-shared", Status: "failed"},
	})
	ready := ToolEntryRegistration{ToolType: "mcp", ConnectorID: "conn-shared", Status: "ready", ToolCount: 1}

	agent.ReplaceToolRegistration(0, ready)
	if problems := a2a.HealthProblemsFromCard(agent.BuildAgentCard()); len(problems) != 1 {
		t.Fatalf("card health problems = %v, want the connector's problem while its other entry still fails", problems)
	}
	agent.ReplaceToolRegistration(1, ready)
	if problems := a2a.HealthProblemsFromCard(agent.BuildAgentCard()); problems != nil {
		t.Fatalf("card health problems = %v, want none once both entries registered", problems)
	}
}

func TestAgentRestart_ReportsFailedToolSourceAgain(t *testing.T) {
	agent := startAgentWithRegistrations(t, "restarted-agent", []ToolEntryRegistration{
		{ToolType: "mcp", ConnectorID: "conn-dead", Status: "failed"},
	})
	if err := agent.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := agent.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if problems := a2a.HealthProblemsFromCard(agent.BuildAgentCard()); len(problems) != 1 {
		t.Fatalf("card health problems after restart = %v, want the failed connector", problems)
	}
}
