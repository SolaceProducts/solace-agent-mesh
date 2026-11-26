// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestExtractWorkflowInput_SchedulerDataPartShape(t *testing.T) {
	w := &WorkflowInstance{}
	msg := a2a.Message{
		Kind: "message",
		Role: a2a.RoleUser,
		Parts: []a2a.Part{
			{Kind: "text", Text: "annotate four images"},
			a2a.NewDataPart(map[string]any{
				"image_1": "https://a",
				"image_2": "https://b",
			}),
		},
	}

	input, isStructured := w.extractWorkflowInput(
		context.Background(), msg, a2a.UserProperties{}, discardLogger())

	if isStructured {
		t.Error("isStructured = true, want false — a plain data part is not a structured invocation")
	}
	if got, _ := input["image_1"].(string); got != "https://a" {
		t.Errorf("input[image_1] = %q, want %q", got, "https://a")
	}
	if got, _ := input["image_2"].(string); got != "https://b" {
		t.Errorf("input[image_2] = %q, want %q", got, "https://b")
	}
	if _, present := input["text"]; present {
		t.Error("input carries \"text\": the data part should have won Priority 2 outright")
	}
}

func TestExtractWorkflowInput_SchemaAdvertisingPartIsNotInput(t *testing.T) {
	w := &WorkflowInstance{}
	msg := a2a.Message{
		Kind: "message",
		Role: a2a.RoleUser,
		Parts: []a2a.Part{
			{Kind: "text", Text: "fallback text"},
			a2a.NewDataPart(map[string]any{
				"type":         "structured_invocation_request",
				"input_schema": map[string]any{"type": "object"},
			}),
		},
	}

	input, isStructured := w.extractWorkflowInput(
		context.Background(), msg, a2a.UserProperties{}, discardLogger())

	if isStructured {
		t.Error("isStructured = true, want false")
	}
	if _, present := input["input_schema"]; present {
		t.Fatal("schema-advertising part was consumed as input; it must be skipped")
	}
	if got, _ := input["text"].(string); got != "fallback text" {
		t.Errorf("input[text] = %q, want %q (Priority 3 fallback)", got, "fallback text")
	}
}

func TestExtractWorkflowInput_TextOnly(t *testing.T) {
	w := &WorkflowInstance{}
	msg := a2a.Message{
		Kind:  "message",
		Role:  a2a.RoleUser,
		Parts: []a2a.Part{{Kind: "text", Text: "https://cameras.example.com/camera?id=133"}},
	}

	input, isStructured := w.extractWorkflowInput(
		context.Background(), msg, a2a.UserProperties{}, discardLogger())

	if isStructured {
		t.Error("isStructured = true, want false")
	}
	if got, _ := input["text"].(string); got != "https://cameras.example.com/camera?id=133" {
		t.Errorf("input[text] = %q, want the URL", got)
	}
}

func TestExtractWorkflowInput_NonMapDataFallsThrough(t *testing.T) {
	w := &WorkflowInstance{}
	msg := a2a.Message{
		Kind: "message",
		Role: a2a.RoleUser,
		Parts: []a2a.Part{
			{Kind: "text", Text: "fallback"},
			a2a.NewDataPart([]any{"a", "b"}),
		},
	}

	input, _ := w.extractWorkflowInput(
		context.Background(), msg, a2a.UserProperties{}, discardLogger())

	if got, _ := input["text"].(string); got != "fallback" {
		t.Errorf("input[text] = %q, want \"fallback\"", got)
	}
}

func newArtifactInputWorkflow(scope string) (*WorkflowInstance, *artifact.ScopedService) {
	art := &artifact.ScopedService{Inner: memory.New(), Scope: scope, Namespace: "ns"}
	w := newTestWorkflowInstance(&WorkflowConfig{Name: "OrderWorkflow"}, &capturingServices{art: art})
	return w, art
}

func TestExtractWorkflowInput_GatewayLocatorURI(t *testing.T) {
	for _, scope := range []string{artifact.ScopeNamespace, "app"} {
		t.Run(scope, func(t *testing.T) {
			w, art := newArtifactInputWorkflow(scope)
			gatewayLoc := art.Resolve("", "user-1", "gateway-session-1", "order.json")
			if _, err := art.Inner.Save(t.Context(), gatewayLoc, []byte(`{"order_id":"ORD-1"}`), "application/json"); err != nil {
				t.Fatalf("save: %v", err)
			}
			uri, err := artifact.FormatURI(gatewayLoc, 0)
			if err != nil {
				t.Fatalf("FormatURI: %v", err)
			}
			msg := a2a.Message{
				Kind: "message",
				Role: a2a.RoleUser,
				Parts: []a2a.Part{
					{Kind: "text", Text: "process this order"},
					a2a.NewFilePart(strings.TrimSuffix(uri, "?version=0"), "order.json", "application/json"),
				},
			}

			input, isStructured := w.extractWorkflowInput(
				t.Context(), msg, a2a.UserProperties{UserID: "user-1"}, discardLogger())

			if !isStructured {
				t.Errorf("isStructured = false, want true; input = %v", input)
			}
			if got, _ := input["order_id"].(string); got != "ORD-1" {
				t.Errorf("input[order_id] = %q, want ORD-1; input = %v", got, input)
			}
		})
	}
}

func TestExtractWorkflowInput_PeerShorthandURI(t *testing.T) {
	w, art := newArtifactInputWorkflow("app")
	loc := art.Resolve("OrderWorkflow", "user-1", "ctx-1", "wi_order.json")
	version, err := art.Inner.Save(t.Context(), loc, []byte(`{"order_id":"ORD-2"}`), "application/json")
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	msg := a2a.Message{
		Kind:      "message",
		Role:      a2a.RoleUser,
		ContextID: "ctx-1",
		Parts: []a2a.Part{
			a2a.NewFilePart(fmt.Sprintf("artifact://wi_order.json:%d", version), "wi_order.json", "application/json"),
		},
	}

	input, isStructured := w.extractWorkflowInput(
		t.Context(), msg, a2a.UserProperties{UserID: "user-1"}, discardLogger())

	if !isStructured {
		t.Errorf("isStructured = false, want true; input = %v", input)
	}
	if got, _ := input["order_id"].(string); got != "ORD-2" {
		t.Errorf("input[order_id] = %q, want ORD-2; input = %v", got, input)
	}
}

func saveJSONArtifact(t *testing.T, art *artifact.ScopedService, loc artifact.Locator, body string) {
	t.Helper()
	if _, err := art.Inner.Save(t.Context(), loc, []byte(body), "application/json"); err != nil {
		t.Fatalf("save %s: %v", loc.Filename, err)
	}
}

func TestLoadArtifactFromURI_RefusesAnotherUsersArtifact(t *testing.T) {
	w, art := newArtifactInputWorkflow(artifact.ScopeNamespace)
	victimLoc := art.Resolve("", "user-2", "victim-session", "profile.json")
	saveJSONArtifact(t, art, victimLoc, `{"secret":"x"}`)
	uri, err := artifact.FormatURI(victimLoc, 0)
	if err != nil {
		t.Fatalf("FormatURI: %v", err)
	}

	data, err := w.loadArtifactFromURI(t.Context(), uri, a2a.UserProperties{UserID: "user-1"}, "")

	if !errors.Is(err, errForeignUserArtifact) {
		t.Errorf("err = %v, want errForeignUserArtifact", err)
	}
	if data != nil {
		t.Errorf("data = %v, want nil", data)
	}
	if level := artifactLoadFailureLevel(err); level != slog.LevelWarn {
		t.Errorf("failure level = %v, want WARN for a security rejection", level)
	}
}

func TestArtifactLoadFailureLevel(t *testing.T) {
	refused := fmt.Errorf("%w: %q", errForeignUserArtifact, "profile.json")
	if got := artifactLoadFailureLevel(refused); got != slog.LevelWarn {
		t.Errorf("level for a refused cross-user reference = %v, want WARN", got)
	}
	if got := artifactLoadFailureLevel(errors.New(`artifact "order.json" not found`)); got != slog.LevelError {
		t.Errorf("level for an ordinary load failure = %v, want ERROR", got)
	}
}

func TestLoadArtifactFromURI_OwnerMatchIgnoresCase(t *testing.T) {
	w, art := newArtifactInputWorkflow(artifact.ScopeNamespace)
	loc := art.Resolve("", "Alice@Example.com", "s1", "order.json")
	saveJSONArtifact(t, art, loc, `{"order_id":"ORD-3"}`)
	uri, err := artifact.FormatURI(loc, 0)
	if err != nil {
		t.Fatalf("FormatURI: %v", err)
	}

	data, err := w.loadArtifactFromURI(t.Context(), uri, a2a.UserProperties{UserID: "alice@example.com"}, "")

	if err != nil || data["order_id"] != "ORD-3" {
		t.Errorf("data, err = %v, %v; want order ORD-3", data, err)
	}
}

func TestLoadArtifactFromURI_HonoursLocatorVersion(t *testing.T) {
	w, art := newArtifactInputWorkflow(artifact.ScopeNamespace)
	loc := art.Resolve("", "user-1", "s1", "order.json")
	saveJSONArtifact(t, art, loc, `{"order_id":"v0"}`)
	saveJSONArtifact(t, art, loc, `{"order_id":"v1"}`)
	uri, err := artifact.FormatURI(loc, 0)
	if err != nil {
		t.Fatalf("FormatURI: %v", err)
	}

	data, err := w.loadArtifactFromURI(t.Context(), uri, a2a.UserProperties{UserID: "user-1"}, "")

	if err != nil || data["order_id"] != "v0" {
		t.Errorf("data, err = %v, %v; want version 0 content", data, err)
	}
}

func TestExtractNodeOutput_AgentReplyLocatorURI(t *testing.T) {
	w, art := newArtifactInputWorkflow("app")
	agentLoc := art.Resolve("NodeAgent", "user-1", "agent-session", "answer.json")
	saveJSONArtifact(t, art, agentLoc, `{"answer":42}`)
	uri, err := artifact.FormatURI(agentLoc, 0)
	if err != nil {
		t.Fatalf("FormatURI: %v", err)
	}
	task := a2a.Task{Status: a2a.TaskStatus{
		State: a2a.TaskStateCompleted,
		Message: &a2a.Message{
			Kind: "message",
			Role: a2a.RoleAgent,
			Parts: []a2a.Part{
				{Kind: "text", Text: "here is the answer"},
				a2a.NewFilePart(uri, "answer.json", "application/json"),
			},
		},
	}}

	output := w.extractNodeOutput(t.Context(), task, a2a.UserProperties{UserID: "user-1"}, "workflow-session", discardLogger())

	if got, _ := output["answer"].(float64); got != 42 {
		t.Errorf("output = %v, want the artifact's {answer: 42}", output)
	}
}

func TestLoadArtifactFromURI_MissingArtifactNamesTheFile(t *testing.T) {
	w, _ := newArtifactInputWorkflow(artifact.ScopeNamespace)

	_, err := w.loadArtifactFromURI(t.Context(), "artifact://ns/user-1/absent-session/order.json",
		a2a.UserProperties{UserID: "user-1"}, "")

	if err == nil || !strings.Contains(err.Error(), `"order.json" not found`) {
		t.Errorf("err = %v, want a not-found error naming order.json", err)
	}
}
