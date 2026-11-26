// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	artmem "github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const testInlineCap = 1024

func newAgentWithoutArtifactTools(t *testing.T, extraTools ...string) (*Agent, *tool.Context) {
	t.Helper()
	off := false
	ag := New(AgentConfig{
		Name:                         "test-agent",
		Model:                        "test-model",
		MaxTokens:                    1000,
		MaxLLMCalls:                  5,
		Namespace:                    "test-ns",
		AutoInjectArtifactTools:      &off,
		AutoInjectDataAnalysisTools:  &off,
		ToolOutputSaveThresholdBytes: testInlineCap,
	})
	store := artmem.New()
	svc := newMockServices(newMockLLM(), newMemSessionStore())
	svc.arts = &artifact.ScopedService{Inner: store}
	ag.svc = svc
	if err := ag.Init(context.Background(), nil, svc); err != nil {
		t.Fatalf("Init: %v", err)
	}
	for _, name := range append([]string{"query_ledger"}, extraTools...) {
		if err := ag.tools.Register(&testBuiltinTool{name: name, params: &tool.JSONSchema{Type: "object"}}); err != nil {
			t.Fatalf("Register %s: %v", name, err)
		}
	}
	if ag.tools.GetTool(loadArtifactToolName) != nil {
		t.Fatal("load_artifact is registered; the test needs an agent without it")
	}
	return ag, &tool.Context{
		ArtifactService: &artifact.ScopedService{Inner: store},
		AgentName:       "test-agent",
		UserID:          "user-1",
		SessionID:       "sess-1",
	}
}

func processedResult(t *testing.T, ag *Agent, toolCtx *tool.Context, objects ...tool.DataObject) map[string]any {
	t.Helper()
	text, _ := ag.processDataObjects(context.Background(), &tool.Result{Status: "success", Message: "done", DataObjects: objects}, toolCtx, nil)
	var parsed map[string]any
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		t.Fatalf("result is not JSON: %v (%q)", err, text)
	}
	return parsed
}

func oversizedRows() []byte {
	return []byte("[" + strings.Repeat(`{"id":1,"total":12.5},`, testInlineCap/10) + `{"id":2}]`)
}

func TestOutOfReachNoteOnOversizedAutoResult(t *testing.T) {
	ag, toolCtx := newAgentWithoutArtifactTools(t)

	got := processedResult(t, ag, toolCtx, tool.DataObject{
		Name: "rows.json", Content: oversizedRows(), MIMEType: mimeJSON, Disposition: tool.DispositionAuto,
	})

	if got["note"] != outOfReachNote {
		t.Errorf("note = %v, want the narrow-the-request note for a result saved only because it was too large", got["note"])
	}
}

func TestOutOfReachNoteSkipsFilesSavedOnPurpose(t *testing.T) {
	ag, toolCtx := newAgentWithoutArtifactTools(t)

	for _, disposition := range []tool.DataDisposition{tool.DispositionArtifact, tool.DispositionArtifactWithPreview} {
		got := processedResult(t, ag, toolCtx, tool.DataObject{
			Name: "report.json", Content: oversizedRows(), MIMEType: mimeJSON, Disposition: disposition,
		})
		if _, has := got["note"]; has {
			t.Errorf("disposition %s: note = %v, want none for a file the tool saved on purpose", disposition, got["note"])
		}
	}
}

func TestOutOfReachNoteSkipsAgentThatCanQueryTheFile(t *testing.T) {
	ag, toolCtx := newAgentWithoutArtifactTools(t, jmespathToolName)

	got := processedResult(t, ag, toolCtx, tool.DataObject{
		Name: "rows.json", Content: oversizedRows(), MIMEType: mimeJSON, Disposition: tool.DispositionAuto,
	})

	if _, has := got["note"]; has {
		t.Errorf("note = %v, want none when %s can read the saved JSON", got["note"], jmespathToolName)
	}
}

func TestOutOfReachNoteJudgesAToolReferenceByItsSize(t *testing.T) {
	for _, c := range []struct {
		name        string
		size        int64
		disposition tool.DataDisposition
		wantNote    bool
	}{
		{"reference spilled for size", testInlineCap * 4, tool.DispositionAuto, true},
		{"small reference the tool SDK saved", 5, tool.DispositionAuto, false},
		{"reference the tool asked for", testInlineCap * 4, tool.DispositionArtifact, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			ag, toolCtx := newAgentWithoutArtifactTools(t)
			got := processedResult(t, ag, toolCtx, tool.DataObject{
				Name: "rows.txt", MIMEType: "text/plain", Disposition: c.disposition,
				ArtifactRef: &tool.ArtifactRef{Filename: "rows.txt", Version: 1, SizeBytes: c.size},
			})
			if _, has := got["note"]; has != c.wantNote {
				t.Errorf("note present = %v, want %v (%v)", has, c.wantNote, got["note"])
			}
		})
	}
}
