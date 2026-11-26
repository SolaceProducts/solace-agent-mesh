// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func TestPersistToolDataObjects(t *testing.T) {
	art := &artifact.ScopedService{Inner: memory.New(), Scope: "namespace", Namespace: "ns"}
	e := &DAGExecutor{logger: slog.Default()}
	toolCtx := &tool.Context{ArtifactService: art, AgentName: "wf", UserID: "u1", SessionID: "s1"}

	result := &tool.Result{
		Status: tool.StatusSuccess,
		DataObjects: []tool.DataObject{
			{Name: "query_task_health.json", Content: []byte(`{"totalTasks":42}`), MIMEType: "application/json"},
			{Name: "already_saved.json", ArtifactRef: &tool.ArtifactRef{Filename: "already_saved.json", Version: 0}},
			{Name: "empty.json"},
		},
	}
	saved, err := e.persistToolDataObjects(context.Background(), toolCtx, result)
	if err != nil {
		t.Fatalf("persistToolDataObjects: %v", err)
	}
	if len(saved) != 1 || saved[0] != (savedArtifact{name: "query_task_health.json", version: 0}) {
		t.Fatalf("saved = %v, want [query_task_health.json v0]", saved)
	}

	loc := art.Resolve("wf", "u1", "s1", "query_task_health.json")
	content, _, err := art.Inner.Load(context.Background(), loc, nil)
	if err != nil {
		t.Fatalf("load persisted artifact: %v", err)
	}
	if string(content) != `{"totalTasks":42}` {
		t.Fatalf("unexpected persisted content: %s", content)
	}

	refLoc := art.Resolve("wf", "u1", "s1", "already_saved.json")
	if refContent, _, err := art.Inner.Load(context.Background(), refLoc, nil); err == nil && len(refContent) > 0 {
		t.Fatalf("ArtifactRef DataObject should not be re-saved; found %d bytes", len(refContent))
	}
}

func TestPersistToolDataObjects_NoStoreOrEmpty(t *testing.T) {
	e := &DAGExecutor{logger: slog.Default()}
	if _, err := e.persistToolDataObjects(context.Background(),
		&tool.Context{}, &tool.Result{Status: tool.StatusSuccess,
			DataObjects: []tool.DataObject{{Name: "x.json", Content: []byte("{}")}}}); err != nil {
		t.Fatalf("expected no error with nil artifact service, got %v", err)
	}
	if _, err := e.persistToolDataObjects(context.Background(), &tool.Context{}, nil); err != nil {
		t.Fatalf("nil result should be a no-op, got %v", err)
	}
}

func TestPersistToolDataObjects_NonSuccessStatuses(t *testing.T) {
	for _, status := range []string{tool.StatusError, tool.StatusFailed, tool.StatusPending} {
		t.Run(status, func(t *testing.T) {
			art := &artifact.ScopedService{Inner: memory.New(), Scope: "namespace", Namespace: "ns"}
			e := &DAGExecutor{logger: slog.Default()}
			toolCtx := &tool.Context{
				ArtifactService: art, AgentName: "wf", UserID: "u1", SessionID: "s1",
			}
			saved, err := e.persistToolDataObjects(context.Background(), toolCtx, &tool.Result{
				Status: status,
				DataObjects: []tool.DataObject{
					{Name: "partial.json", Content: []byte(`{"half":"written"}`), MIMEType: "application/json"},
				},
			})
			if err != nil {
				t.Fatalf("persistToolDataObjects: %v", err)
			}
			if len(saved) != 0 {
				t.Fatalf("status %q reported saved names %v; a non-success result has no durable output",
					status, saved)
			}
			loc := art.Resolve("wf", "u1", "s1", "partial.json")
			if content, _, loadErr := art.Inner.Load(context.Background(), loc, nil); loadErr == nil && len(content) > 0 {
				t.Fatalf("status %q persisted %d bytes; nothing should have been written",
					status, len(content))
			}
		})
	}
}

func TestPersistToolDataObjects_ANamelessObjectIsSavedUnderAGeneratedName(t *testing.T) {
	art := &artifact.ScopedService{Inner: memory.New(), Scope: "namespace", Namespace: "ns"}
	e := &DAGExecutor{logger: slog.Default()}
	toolCtx := &tool.Context{ArtifactService: art, AgentName: "wf", UserID: "u1", SessionID: "s1"}
	result := &tool.Result{
		Status:  tool.StatusSuccess,
		Message: "Retrieved query_task_health",
		DataObjects: []tool.DataObject{
			{Content: []byte(`{"totalTasks":42}`), MIMEType: "application/json", Disposition: tool.DispositionAuto},
			{Content: []byte(`{"totalTasks":7}`), MIMEType: "application/json", Disposition: tool.DispositionAuto},
		},
	}
	saved, err := e.persistToolDataObjects(context.Background(), toolCtx, result)
	if err != nil {
		t.Fatalf("persistToolDataObjects: %v", err)
	}
	if len(saved) != 2 {
		t.Fatalf("saved = %v, want both nameless objects saved", saved)
	}
	if saved[0].name == saved[1].name {
		t.Fatalf("both nameless objects were saved as %q, so the second replaced the first", saved[0].name)
	}
	for _, s := range saved {
		if !strings.HasPrefix(s.name, "output_") || !strings.HasSuffix(s.name, ".json") {
			t.Errorf("generated name %q, want output_<id>.json", s.name)
		}
		if _, _, loadErr := art.Inner.Load(context.Background(), art.Resolve("wf", "u1", "s1", s.name), nil); loadErr != nil {
			t.Errorf("load %s: %v", s.name, loadErr)
		}
	}

	_, output, classifyErr := classifyToolResult("query_task_health", &tool.Result{
		Status:      tool.StatusSuccess,
		Message:     "Retrieved query_task_health",
		DataObjects: result.DataObjects[:1],
	}, nil, saved[:1])
	if classifyErr != nil {
		t.Fatalf("classifyToolResult: %v", classifyErr)
	}
	if output["created_artifact_name"] != saved[0].name {
		t.Errorf("created_artifact_name = %v, want %s: the node must expose the saved artifact", output["created_artifact_name"], saved[0].name)
	}
}

func TestToolNodeOutput_ExposesCreatedArtifactNameOnlyWhenExactlyOneArtifactWasSaved(t *testing.T) {
	nameless := func(content string) tool.DataObject {
		return tool.DataObject{Content: []byte(content), MIMEType: "application/json", Disposition: tool.DispositionAuto}
	}
	for _, tc := range []struct {
		name        string
		objects     []tool.DataObject
		wantCreated bool
	}{
		{"one nameless object", []tool.DataObject{nameless(`{"n":1}`)}, true},
		{"two nameless objects", []tool.DataObject{nameless(`{"n":1}`), nameless(`{"n":2}`)}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			node := &NodeConfig{ID: "query_node", Type: "tool", ToolName: "query"}
			cfg, dag := singleToolNodeWorkflow(node)
			e, _ := newCapturingExecutor(t, cfg, dag, NewExecutionState("wf", "exec-1"))
			e.instance.svc.(*capturingServices).art = &artifact.ScopedService{Inner: memory.New(), Scope: "namespace", Namespace: "ns"}
			e.instance.tools = toolSetWith(t, scriptedTool{
				schemaTool: &schemaTool{name: "query"},
				result:     tool.OK("queried", tool.WithDataObjects(tc.objects...)),
			})

			runToCompletion(t, e)

			output, _ := e.state.NodeOutputs["query_node"]["output"].(map[string]any)
			if output == nil {
				t.Fatalf("query_node has no output; node outputs: %v", e.state.NodeOutputs)
			}
			for _, key := range []string{"created_artifact", "created_artifact_name", "created_artifact_version"} {
				if _, present := output[key]; present != tc.wantCreated {
					t.Errorf("%s present = %v, want %v; output = %v", key, present, tc.wantCreated, output)
				}
			}
		})
	}
}
