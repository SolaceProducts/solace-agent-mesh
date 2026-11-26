// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	artmem "github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func toolResultProcessorFixture(t *testing.T) (*Agent, *tool.Context) {
	t.Helper()
	agent, _, _ := newTestAgent()
	memStore := artmem.New()
	agent.svc.(*mockServices).arts = &artifact.ScopedService{Inner: memStore}
	toolCtx := &tool.Context{
		ArtifactService: &artifact.ScopedService{Inner: memStore},
		AgentName:       "test-agent",
		UserID:          "user-1",
		SessionID:       "sess-1",
	}
	return agent, toolCtx
}

func TestInlineOutputsArrayWhenMultipleInlineDataObjects(t *testing.T) {
	agent, toolCtx := toolResultProcessorFixture(t)

	r := &tool.Result{
		Status:  "success",
		Message: "two inline objects",
		DataObjects: []tool.DataObject{
			{Name: "first", Content: []byte("alpha"), MIMEType: "text/plain", Disposition: tool.DispositionInline},
			{Name: "second", Content: []byte("beta"), MIMEType: "text/plain", Disposition: tool.DispositionInline},
		},
	}

	out, _ := agent.formatToolResult(context.Background(), r, toolCtx, nil)
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v: %s", err, out)
	}

	if _, stillHasFlattened := parsed["inline_content"]; stillHasFlattened {
		t.Errorf("legacy flattened inline_content field must not be emitted when Python expects inline_outputs: %s", out)
	}

	list, ok := parsed["inline_outputs"].([]any)
	if !ok {
		t.Fatalf("inline_outputs not an array: %T %s", parsed["inline_outputs"], out)
	}
	if len(list) != 2 {
		t.Fatalf("inline_outputs length = %d, want 2", len(list))
	}
	for i, want := range []struct{ name, content string }{{"first", "alpha"}, {"second", "beta"}} {
		m, ok := list[i].(map[string]any)
		if !ok {
			t.Fatalf("entry %d not a map: %T", i, list[i])
		}
		if m["name"] != want.name {
			t.Errorf("entry %d name = %v, want %q", i, m["name"], want.name)
		}
		if m["content"] != want.content {
			t.Errorf("entry %d content = %v, want %q", i, m["content"], want.content)
		}
		if _, hasTruncated := m["truncated"]; hasTruncated {
			t.Errorf("entry %d should not carry a truncated flag (truncation removed): %s", i, out)
		}
	}
}

func TestSingleInlineDataObjectFlattensToContent(t *testing.T) {
	agent, toolCtx := toolResultProcessorFixture(t)

	r := &tool.Result{
		Status: "success",
		DataObjects: []tool.DataObject{
			{Name: "only", Content: []byte("hello"), MIMEType: "text/plain", Disposition: tool.DispositionInline},
		},
	}
	out, _ := agent.formatToolResult(context.Background(), r, toolCtx, nil)
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if parsed["content"] != "hello" {
		t.Errorf("content = %v, want 'hello'", parsed["content"])
	}
	if _, ok := parsed["inline_outputs"]; ok {
		t.Errorf("single inline should not use inline_outputs array")
	}
	if _, ok := parsed["artifacts_created"]; ok {
		t.Errorf("explicit inline must not also save an artifact: %s", out)
	}
}

func TestAutoSmallInlinedAndSaved(t *testing.T) {
	agent, toolCtx := toolResultProcessorFixture(t)

	content := "small auto result"
	r := &tool.Result{
		Status: "success",
		DataObjects: []tool.DataObject{
			{Name: "auto.txt", Content: []byte(content), MIMEType: "text/plain", Disposition: tool.DispositionAuto},
		},
	}
	out, _ := agent.formatToolResult(context.Background(), r, toolCtx, nil)
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v: %s", err, out)
	}
	if parsed["content"] != content {
		t.Errorf("content = %v, want full inline %q: %s", parsed["content"], content, out)
	}
	created, ok := parsed["artifacts_created"].([]any)
	if !ok || len(created) != 1 {
		t.Fatalf("artifacts_created = %v, want one saved artifact: %s", parsed["artifacts_created"], out)
	}
	if entry, _ := created[0].(map[string]any); entry["filename"] != "auto.txt" {
		t.Errorf("artifacts_created[0].filename = %v, want auto.txt", created[0])
	}
}

func TestAutoTagsSurviveSaveAndSignal(t *testing.T) {
	agent, toolCtx := toolResultProcessorFixture(t)

	mb := broker.NewDevBroker()
	if err := mb.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mb.Disconnect(t.Context()) }()
	statusTopic := "test-ns/a2a/v1/gateway/status/test-gw/task-tags"
	sp := NewStatusPublisher(mb, StatusPublisherConfig{
		StatusTopic: statusTopic, TaskID: "task-tags", ContextID: "ctx-tags",
		AgentName: toolCtx.AgentName, ClientID: "client-1", UserID: toolCtx.UserID,
	}, slog.Default())
	q, err := mb.CreateQueue("tags-signals", broker.QueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if subErr := mb.Subscribe(q, statusTopic); subErr != nil {
		t.Fatal(subErr)
	}

	content := `{"a":1}`
	r := &tool.Result{
		Status: "success",
		DataObjects: []tool.DataObject{
			{
				Name:        "working.json",
				Content:     []byte(content),
				MIMEType:    "application/json",
				Disposition: tool.DispositionAuto,
				Tags:        []string{a2a.ArtifactTagWorking},
			},
		},
	}
	out, _ := agent.formatToolResult(context.Background(), r, toolCtx, sp)
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v: %s", err, out)
	}
	if parsed["content"] != content {
		t.Errorf("content = %v, want full inline %q: %s", parsed["content"], content, out)
	}

	progressData := receiveSignalDataPart(t, mb, q)
	var progress a2a.ArtifactCreationProgressData
	if err := json.Unmarshal(progressData, &progress); err != nil {
		t.Fatalf("unmarshal ArtifactCreationProgressData: %v", err)
	}
	if progress.Type != string(a2a.SignalArtifactCreationProgress) {
		t.Fatalf("first signal Type = %q, want %q — wrong signal, Tags check below would be meaningless",
			progress.Type, a2a.SignalArtifactCreationProgress)
	}
	if len(progress.Tags) != 1 || progress.Tags[0] != a2a.ArtifactTagWorking {
		t.Errorf("artifact_creation_progress signal Tags = %v, want [%q]", progress.Tags, a2a.ArtifactTagWorking)
	}

	savedData := receiveSignalDataPart(t, mb, q)
	var saved a2a.ArtifactSavedData
	if err := json.Unmarshal(savedData, &saved); err != nil {
		t.Fatalf("unmarshal ArtifactSavedData: %v", err)
	}
	if saved.Type != string(a2a.SignalArtifactSaved) {
		t.Fatalf("second signal Type = %q, want %q — wrong signal, Tags check below would be meaningless",
			saved.Type, a2a.SignalArtifactSaved)
	}
	if len(saved.Tags) != 1 || saved.Tags[0] != a2a.ArtifactTagWorking {
		t.Errorf("artifact_saved signal Tags = %v, want [%q]", saved.Tags, a2a.ArtifactTagWorking)
	}

	meta := artifact.LoadCompanionMetadata(context.Background(), toolCtx.ArtifactService,
		toolCtx.AgentName, toolCtx.UserID, toolCtx.SessionID, "working.json", nil)
	if meta == nil {
		t.Fatal("no companion metadata found for working.json")
	}
	tags, _ := meta["tags"].([]any)
	if len(tags) != 1 || tags[0] != a2a.ArtifactTagWorking {
		t.Errorf("persisted metadata tags = %v, want [%q]", meta["tags"], a2a.ArtifactTagWorking)
	}
}

func TestAutoLargeSavedNotInlined(t *testing.T) {
	agent, toolCtx := toolResultProcessorFixture(t)
	agent.cfg.ToolOutputSaveThresholdBytes = 16

	big := strings.Repeat("y", 500)
	r := &tool.Result{
		Status: "success",
		DataObjects: []tool.DataObject{
			{Name: "big.txt", Content: []byte(big), MIMEType: "text/plain", Disposition: tool.DispositionAuto},
		},
	}
	out, _ := agent.formatToolResult(context.Background(), r, toolCtx, nil)
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v: %s", err, out)
	}
	if _, ok := parsed["content"]; ok {
		t.Errorf("oversized auto must not be inlined: %s", out)
	}
	if _, ok := parsed["content_truncated"]; ok {
		t.Errorf("content_truncated must not be emitted: %s", out)
	}
	created, ok := parsed["artifacts_created"].([]any)
	if !ok || len(created) != 1 {
		t.Fatalf("artifacts_created = %v, want one saved artifact: %s", parsed["artifacts_created"], out)
	}
}

func TestBinaryAutoSavedOnly(t *testing.T) {
	agent, toolCtx := toolResultProcessorFixture(t)

	r := &tool.Result{
		Status: "success",
		DataObjects: []tool.DataObject{
			{Name: "tiny.bin", Content: []byte{0xFF, 0x00, 0xFE}, MIMEType: "application/octet-stream", Disposition: tool.DispositionAuto},
		},
	}
	out, _ := agent.formatToolResult(context.Background(), r, toolCtx, nil)
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v: %s", err, out)
	}
	if _, ok := parsed["content"]; ok {
		t.Errorf("binary content must never be inlined: %s", out)
	}
	created, ok := parsed["artifacts_created"].([]any)
	if !ok || len(created) != 1 {
		t.Fatalf("artifacts_created = %v, want one saved artifact: %s", parsed["artifacts_created"], out)
	}
}

func TestInlineMaxBytesOverrideForcesInline(t *testing.T) {
	agent, toolCtx := toolResultProcessorFixture(t)
	agent.cfg.ToolOutputSaveThresholdBytes = 16

	content := strings.Repeat("z", 500)
	r := &tool.Result{
		Status: "success",
		DataObjects: []tool.DataObject{
			{Name: "forced.txt", Content: []byte(content), MIMEType: "text/plain", Disposition: tool.DispositionAuto, InlineMaxBytes: 1000},
		},
	}
	out, _ := agent.formatToolResult(context.Background(), r, toolCtx, nil)
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v: %s", err, out)
	}
	if parsed["content"] != content {
		t.Errorf("override should force full inline; content len=%d want %d: %s", len(parsedString(parsed["content"])), len(content), out)
	}
	if _, ok := parsed["artifacts_created"]; !ok {
		t.Errorf("auto object should still be saved even when force-inlined: %s", out)
	}
}

func TestInlineMaxBytesOverrideClampedToCeiling(t *testing.T) {
	agent, toolCtx := toolResultProcessorFixture(t)
	agent.cfg.ToolOutputSaveThresholdBytes = 16
	agent.cfg.ToolOutputLLMReturnMaxBytes = 256

	content := strings.Repeat("q", 500)
	r := &tool.Result{
		Status: "success",
		DataObjects: []tool.DataObject{
			{Name: "toobig.txt", Content: []byte(content), MIMEType: "text/plain", Disposition: tool.DispositionAuto, InlineMaxBytes: 100000},
		},
	}
	out, _ := agent.formatToolResult(context.Background(), r, toolCtx, nil)
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v: %s", err, out)
	}
	if _, ok := parsed["content"]; ok {
		t.Errorf("content above the ceiling must not be inlined even with override: %s", out)
	}
	if _, ok := parsed["artifacts_created"]; !ok {
		t.Errorf("over-ceiling object should be saved: %s", out)
	}
}

func TestAutoEmptyContentNotFlaggedAsPhantom(t *testing.T) {
	agent, toolCtx := toolResultProcessorFixture(t)

	r := &tool.Result{
		Status: "success",
		DataObjects: []tool.DataObject{
			{Name: "empty.txt", Content: nil, MIMEType: "text/plain", Disposition: tool.DispositionAuto},
		},
	}
	out, _ := agent.formatToolResult(context.Background(), r, toolCtx, nil)
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v: %s", err, out)
	}
	if parsed["status"] != "success" {
		t.Errorf("status = %v, want success (empty auto must not be a phantom failure): %s", parsed["status"], out)
	}
	if _, ok := parsed["failed_artifacts"]; ok {
		t.Errorf("empty auto object must not produce failed_artifacts: %s", out)
	}
	if _, ok := parsed["artifacts_created"]; ok {
		t.Errorf("empty auto object has nothing to save: %s", out)
	}
}

func parsedString(v any) string {
	s, _ := v.(string)
	return s
}

func TestExplicitInlineOversizedFallsBackToSave(t *testing.T) {
	agent, toolCtx := toolResultProcessorFixture(t)
	agent.cfg.ToolOutputSaveThresholdBytes = 16

	big := strings.Repeat("x", 500)
	r := &tool.Result{
		Status: "success",
		DataObjects: []tool.DataObject{
			{Name: "big.txt", Content: []byte(big), MIMEType: "text/plain", Disposition: tool.DispositionInline},
		},
	}
	out, _ := agent.formatToolResult(context.Background(), r, toolCtx, nil)
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if _, ok := parsed["content_truncated"]; ok {
		t.Errorf("content_truncated must not be emitted (truncation removed): %s", out)
	}
	if _, ok := parsed["content"]; ok {
		t.Errorf("oversized inline must be saved, not shown inline: %s", out)
	}
	created, ok := parsed["artifacts_created"].([]any)
	if !ok || len(created) != 1 {
		t.Fatalf("artifacts_created = %v, want one saved artifact: %s", parsed["artifacts_created"], out)
	}
}

func TestOversizedInlineWithoutArtifactServiceIsNotDropped(t *testing.T) {
	agent, _, _ := newTestAgent()
	agent.cfg.ToolOutputSaveThresholdBytes = 16
	toolCtx := &tool.Context{ArtifactService: nil, AgentName: "a", UserID: "u", SessionID: "s"}

	big := strings.Repeat("x", 2000)
	r := &tool.Result{
		Status: "success",
		DataObjects: []tool.DataObject{
			{Name: "big.txt", Content: []byte(big), MIMEType: "text/plain", Disposition: tool.DispositionInline},
		},
	}
	out, _ := agent.formatToolResult(context.Background(), r, toolCtx, nil)
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	content, ok := parsed["content"].(string)
	if !ok {
		t.Fatalf("expected content inline when no artifact service; got: %s", out)
	}
	if !strings.HasPrefix(content, "xxxx") {
		t.Errorf("real content bytes must survive the truncation; got %q", content)
	}
	if !strings.Contains(content, "no artifact service available") {
		t.Errorf("content should carry the degradation marker; got %q", content)
	}
	if len(content) >= len(big) {
		t.Errorf("content should be truncated, got %d of %d bytes", len(content), len(big))
	}
	if parsed["status"] != tool.StatusPartial {
		t.Errorf("lossy truncation must downgrade to partial, got %v", parsed["status"])
	}
	if _, ok := parsed["failed_artifacts"].([]any); !ok {
		t.Errorf("lossy truncation must record failed_artifacts: %s", out)
	}
	if _, ok := parsed["artifacts_created"]; ok {
		t.Errorf("no artifact can be created without a service: %s", out)
	}
}

func TestSmallAutoWithoutArtifactServiceInlinedOnce(t *testing.T) {
	agent, _, _ := newTestAgent()
	toolCtx := &tool.Context{ArtifactService: nil, AgentName: "a", UserID: "u", SessionID: "s"}

	content := strings.Repeat("z", 1000)
	r := &tool.Result{
		Status: "success",
		DataObjects: []tool.DataObject{
			{Name: "auto.txt", Content: []byte(content), MIMEType: "text/plain", Disposition: tool.DispositionAuto},
		},
	}
	out, _ := agent.formatToolResult(context.Background(), r, toolCtx, nil)
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if _, dup := parsed["inline_outputs"]; dup {
		t.Fatalf("single object must not produce an inline_outputs array: %s", out)
	}
	got, ok := parsed["content"].(string)
	if !ok || got != content {
		t.Fatalf("expected the full content inline exactly once; got: %s", out)
	}
	if strings.Contains(got, "no artifact service available") {
		t.Errorf("small content fits inline and must not carry a truncation marker: %q", got)
	}
}

func TestMultipleInlineOutputsWithDegradedEntry(t *testing.T) {
	agent, _, _ := newTestAgent()
	agent.cfg.ToolOutputSaveThresholdBytes = 16
	toolCtx := &tool.Context{ArtifactService: nil, AgentName: "a", UserID: "u", SessionID: "s"}

	r := &tool.Result{
		Status: "success",
		DataObjects: []tool.DataObject{
			{Name: "small.txt", Content: []byte("tiny"), MIMEType: "text/plain", Disposition: tool.DispositionInline},
			{Name: "big.txt", Content: []byte(strings.Repeat("x", 2000)), MIMEType: "text/plain", Disposition: tool.DispositionInline},
		},
	}
	out, _ := agent.formatToolResult(context.Background(), r, toolCtx, nil)
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	list, ok := parsed["inline_outputs"].([]any)
	if !ok || len(list) != 2 {
		t.Fatalf("expected inline_outputs of 2 entries; got: %s", out)
	}
	byName := map[string]string{}
	for _, e := range list {
		entry := e.(map[string]any)
		byName[entry["name"].(string)], _ = entry["content"].(string)
	}
	if byName["small.txt"] != "tiny" {
		t.Errorf("the normal neighbor's content must stay intact; got %q", byName["small.txt"])
	}
	if !strings.Contains(byName["big.txt"], "no artifact service available") {
		t.Errorf("degraded entry should carry the marker; got %q", byName["big.txt"])
	}
	if parsed["status"] != tool.StatusPartial {
		t.Errorf("a lossy entry must downgrade the whole envelope to partial, got %v", parsed["status"])
	}
}

func TestEmptyContentWithoutArtifactServiceIsNotLossy(t *testing.T) {
	agent, _, _ := newTestAgent()
	toolCtx := &tool.Context{ArtifactService: nil, AgentName: "a", UserID: "u", SessionID: "s"}

	r := &tool.Result{
		Status: "success",
		DataObjects: []tool.DataObject{
			{Name: "empty.txt", MIMEType: "text/plain", Disposition: tool.DispositionArtifact},
		},
	}
	out, _ := agent.formatToolResult(context.Background(), r, toolCtx, nil)
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if c, _ := parsed["content"].(string); !strings.Contains(c, "no artifact service available") {
		t.Errorf("empty object should surface a marker; got %q", c)
	}
	if parsed["status"] != tool.StatusSuccess {
		t.Errorf("empty (no data to lose) must stay success, got %v", parsed["status"])
	}
	if _, ok := parsed["failed_artifacts"]; ok {
		t.Errorf("empty object is not a data loss: %s", out)
	}
}

func TestNonTextMIMEValidUTF8WithoutArtifactServiceSummarized(t *testing.T) {
	agent, _, _ := newTestAgent()
	toolCtx := &tool.Context{ArtifactService: nil, AgentName: "a", UserID: "u", SessionID: "s"}

	svg := `<svg xmlns="http://www.w3.org/2000/svg"><rect/></svg>`
	r := &tool.Result{
		Status: "success",
		DataObjects: []tool.DataObject{
			{Name: "icon.svg", Content: []byte(svg), MIMEType: "image/svg+xml", Disposition: tool.DispositionInline},
		},
	}
	out, _ := agent.formatToolResult(context.Background(), r, toolCtx, nil)
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	content, ok := parsed["content"].(string)
	if !ok {
		t.Fatalf("expected a content marker inline; got: %s", out)
	}
	if !strings.Contains(content, "binary content") || strings.Contains(content, "<svg") {
		t.Errorf("non-text MIME must be summarized, not inlined as text; got %q", content)
	}
	if parsed["status"] != tool.StatusPartial {
		t.Errorf("summarized (undelivered) content must downgrade to partial, got %v", parsed["status"])
	}
}

func TestArtifactRefWithoutArtifactServiceReportedAsSaved(t *testing.T) {
	agent, _, _ := newTestAgent()
	toolCtx := &tool.Context{ArtifactService: nil, AgentName: "a", UserID: "u", SessionID: "s"}

	r := &tool.Result{
		Status: "success",
		DataObjects: []tool.DataObject{
			{Name: "spilled.json", MIMEType: "application/json", ArtifactRef: &tool.ArtifactRef{Filename: "spilled.json", Version: 3, SizeBytes: 12345}},
		},
	}
	out, _ := agent.formatToolResult(context.Background(), r, toolCtx, nil)
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	created, ok := parsed["artifacts_created"].([]any)
	if !ok || len(created) != 1 {
		t.Fatalf("pre-saved artifact should be reported in artifacts_created; got: %s", out)
	}
	if entry := created[0].(map[string]any); entry["filename"] != "spilled.json" {
		t.Errorf("artifacts_created filename = %v, want spilled.json", entry["filename"])
	}
}

func TestOversizedAutoWithoutArtifactServiceIsNotDropped(t *testing.T) {
	agent, _, _ := newTestAgent()
	agent.cfg.ToolOutputSaveThresholdBytes = 16
	toolCtx := &tool.Context{ArtifactService: nil, AgentName: "a", UserID: "u", SessionID: "s"}

	r := &tool.Result{
		Status: "success",
		DataObjects: []tool.DataObject{
			{Name: "auto.json", Content: []byte(strings.Repeat("y", 2000)), MIMEType: "application/json", Disposition: tool.DispositionAuto},
		},
	}
	out, _ := agent.formatToolResult(context.Background(), r, toolCtx, nil)
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	content, ok := parsed["content"].(string)
	if !ok || !strings.HasPrefix(content, "yyyy") || !strings.Contains(content, "no artifact service available") {
		t.Fatalf("auto content should surface real data + marker inline; got: %s", out)
	}
	if parsed["status"] != tool.StatusPartial {
		t.Errorf("lossy truncation must downgrade to partial, got %v", parsed["status"])
	}
	if _, ok := parsed["failed_artifacts"].([]any); !ok {
		t.Errorf("lossy truncation must record failed_artifacts: %s", out)
	}
	if _, ok := parsed["artifacts_created"]; ok {
		t.Errorf("no artifact can be created without a service: %s", out)
	}
}

func TestBinaryWithoutArtifactServiceInlinesMarkerNotMojibake(t *testing.T) {
	agent, _, _ := newTestAgent()
	toolCtx := &tool.Context{ArtifactService: nil, AgentName: "a", UserID: "u", SessionID: "s"}

	bin := []byte{0xff, 0xfe, 0xfd, 0x00, 0x01}
	r := &tool.Result{
		Status: "success",
		DataObjects: []tool.DataObject{
			{Name: "blob.bin", Content: bin, MIMEType: "application/octet-stream", Disposition: tool.DispositionInline},
		},
	}
	out, _ := agent.formatToolResult(context.Background(), r, toolCtx, nil)
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	content, ok := parsed["content"].(string)
	if !ok {
		t.Fatalf("expected a content marker inline; got: %s", out)
	}
	if !strings.Contains(content, "binary content") || !strings.Contains(content, "omitted") {
		t.Errorf("binary must be summarized, not inlined raw; got %q", content)
	}
	if parsed["status"] != tool.StatusPartial {
		t.Errorf("fully-omitted binary must downgrade to partial, got %v", parsed["status"])
	}
	if _, ok := parsed["failed_artifacts"].([]any); !ok {
		t.Errorf("fully-omitted binary must record failed_artifacts: %s", out)
	}
}

func TestArtifactSaveFailureDowngradesToPartial(t *testing.T) {
	agent, toolCtx := toolResultProcessorFixture(t)

	r := &tool.Result{
		Status:  "success",
		Message: "query complete",
		DataObjects: []tool.DataObject{
			{Name: "../escape.csv", Content: []byte("a,b\n1,2"), MIMEType: "text/csv", Disposition: tool.DispositionArtifact},
		},
	}

	out, _ := agent.formatToolResult(context.Background(), r, toolCtx, nil)
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v: %s", err, out)
	}

	if parsed["status"] != tool.StatusPartial {
		t.Errorf("status = %v, want %q (a dropped artifact must not stay success): %s", parsed["status"], tool.StatusPartial, out)
	}
	if _, ok := parsed["artifacts_created"]; ok {
		t.Errorf("artifacts_created must be absent when the save failed: %s", out)
	}
	failed, ok := parsed["failed_artifacts"].([]any)
	if !ok || len(failed) != 1 {
		t.Fatalf("failed_artifacts = %v, want one entry: %s", parsed["failed_artifacts"], out)
	}
	entry, _ := failed[0].(map[string]any)
	if entry["filename"] != "../escape.csv" {
		t.Errorf("failed_artifacts[0].filename = %v, want %q", entry["filename"], "../escape.csv")
	}
	if entry["error"] == nil || entry["error"] == "" {
		t.Errorf("failed_artifacts[0].error must explain the failure: %s", out)
	}
	if msg, _ := parsed["message"].(string); !strings.Contains(msg, "escape.csv") {
		t.Errorf("message should mention the failed artifact, got %q", msg)
	}
}

func TestEmptyArtifactContentDowngradesToPartial(t *testing.T) {
	agent, toolCtx := toolResultProcessorFixture(t)

	r := &tool.Result{
		Status:  "success",
		Message: "127 Contact records saved to Contact_records.csv",
		DataObjects: []tool.DataObject{
			{Name: "Contact_records.csv", Content: nil, MIMEType: "text/csv", Disposition: tool.DispositionArtifact},
		},
	}

	out, _ := agent.formatToolResult(context.Background(), r, toolCtx, nil)
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v: %s", err, out)
	}

	if parsed["status"] != tool.StatusPartial {
		t.Errorf("status = %v, want %q (a 0-byte phantom must not stay success): %s", parsed["status"], tool.StatusPartial, out)
	}
	if _, ok := parsed["artifacts_created"]; ok {
		t.Errorf("artifacts_created must be absent for an empty/phantom artifact: %s", out)
	}
	failed, ok := parsed["failed_artifacts"].([]any)
	if !ok || len(failed) != 1 {
		t.Fatalf("failed_artifacts = %v, want one entry: %s", parsed["failed_artifacts"], out)
	}
	if entry, _ := failed[0].(map[string]any); entry["filename"] != "Contact_records.csv" {
		t.Errorf("failed_artifacts[0].filename = %v, want Contact_records.csv", failed[0])
	}
}

func TestArtifactSaveSuccessKeepsStatus(t *testing.T) {
	agent, toolCtx := toolResultProcessorFixture(t)

	r := &tool.Result{
		Status: "success",
		DataObjects: []tool.DataObject{
			{Name: "ok.csv", Content: []byte("a,b\n1,2"), MIMEType: "text/csv", Disposition: tool.DispositionArtifact},
		},
	}
	out, _ := agent.formatToolResult(context.Background(), r, toolCtx, nil)
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v: %s", err, out)
	}
	if parsed["status"] != "success" {
		t.Errorf("status = %v, want success: %s", parsed["status"], out)
	}
	if _, ok := parsed["failed_artifacts"]; ok {
		t.Errorf("failed_artifacts must be absent on a successful save: %s", out)
	}
	if _, ok := parsed["artifacts_created"]; !ok {
		t.Errorf("artifacts_created should be present on a successful save: %s", out)
	}
}

func TestProcessingDisabledPassesThrough(t *testing.T) {
	agent, toolCtx := toolResultProcessorFixture(t)
	disabled := false
	agent.cfg.ToolResultProcessingEnabled = &disabled

	r := &tool.Result{
		Status:  "success",
		Message: "raw passthrough",
		DataObjects: []tool.DataObject{
			{Name: "big.bin", Content: make([]byte, 10_000), MIMEType: "application/octet-stream", Disposition: tool.DispositionAuto},
		},
	}
	out, _ := agent.formatToolResult(context.Background(), r, toolCtx, nil)
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v: %s", err, out)
	}
	if _, hasArtifacts := parsed["artifacts_created"]; hasArtifacts {
		t.Error("artifacts_created should not be emitted when processor is disabled")
	}
	if _, hasInline := parsed["inline_outputs"]; hasInline {
		t.Error("inline_outputs should not be emitted when processor is disabled")
	}
	if parsed["status"] != "success" {
		t.Errorf("status = %v, want success", parsed["status"])
	}
	if parsed["message"] != "raw passthrough" {
		t.Errorf("message = %v", parsed["message"])
	}
}

func TestSaveDataObjectAutoGeneratesFilename(t *testing.T) {
	agent, toolCtx := toolResultProcessorFixture(t)

	do := tool.DataObject{
		Content:     []byte(`{"hello": "world"}`),
		MIMEType:    "application/json",
		Disposition: tool.DispositionArtifact,
	}
	_, info, _ := agent.saveDataObjectAsArtifact(context.Background(), do, toolCtx, nil)
	if info == nil {
		t.Fatal("expected savedArtifactInfo, got nil")
	}
	if !strings.HasPrefix(info.Filename, "output_") {
		t.Errorf("filename = %q, want output_<id>.json prefix", info.Filename)
	}
	if !strings.HasSuffix(info.Filename, ".json") {
		t.Errorf("filename = %q, want .json extension mapped from application/json mime", info.Filename)
	}
}

func TestSaveDataObjectAutoGeneratesFilenameBinaryExt(t *testing.T) {
	agent, toolCtx := toolResultProcessorFixture(t)

	do := tool.DataObject{
		Content:     []byte{0xFF, 0x00, 0xFE},
		MIMEType:    "application/x-something-weird",
		Disposition: tool.DispositionArtifact,
	}
	_, info, _ := agent.saveDataObjectAsArtifact(context.Background(), do, toolCtx, nil)
	if info == nil {
		t.Fatal("expected savedArtifactInfo")
	}
	if !strings.HasSuffix(info.Filename, ".bin") {
		t.Errorf("filename = %q, want .bin fallback extension", info.Filename)
	}
}

func TestSaveDataObjectRejectsUnsafeFilename(t *testing.T) {
	agent, toolCtx := toolResultProcessorFixture(t)
	tests := []struct {
		name     string
		filename string
	}{
		{"path traversal", "../etc/passwd"},
		{"forward slash", "subdir/file.txt"},
		{"backslash", "subdir\\file.txt"},
		{"just dot", "."},
		{"whitespace only", "   "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			do := tool.DataObject{
				Name:        tt.filename,
				Content:     []byte("payload"),
				MIMEType:    "text/plain",
				Disposition: tool.DispositionArtifact,
			}
			ref, info, failed := agent.saveDataObjectAsArtifact(context.Background(), do, toolCtx, nil)
			if info != nil {
				t.Errorf("expected nil info for unsafe filename %q, got %+v", tt.filename, info)
			}
			if failed == nil {
				t.Errorf("expected failedArtifactInfo for unsafe filename %q, got nil", tt.filename)
			}
			if !strings.Contains(ref, "invalid filename") && !strings.Contains(ref, "failed to save") {
				t.Errorf("ref = %q, want failure message for unsafe filename %q", ref, tt.filename)
			}
		})
	}
}

func TestGeneratePreviewFromContentWhenAbsent(t *testing.T) {
	agent, _, _ := newTestAgent()
	agent.cfg.ToolResultPreviewLengthChars = 10

	longText := strings.Repeat("A", 50)
	do := tool.DataObject{
		Content:  []byte(longText),
		MIMEType: "text/plain",
	}
	preview := agent.generatePreview(do)
	if preview == "" {
		t.Fatal("preview should not be empty for non-empty text content")
	}
	if !strings.HasSuffix(preview, "...") {
		t.Errorf("preview = %q, want trailing '...' to indicate truncation", preview)
	}
	if len(preview) > 13 {
		t.Errorf("preview length = %d, want <= 13 (limit + suffix)", len(preview))
	}
}

func TestGeneratePreviewRespectsExplicitPreview(t *testing.T) {
	agent, _, _ := newTestAgent()
	do := tool.DataObject{
		Content:  []byte("raw content"),
		Preview:  "explicit preview",
		MIMEType: "text/plain",
	}
	if got := agent.generatePreview(do); got != "explicit preview" {
		t.Errorf("generatePreview = %q, want 'explicit preview'", got)
	}
}

func TestGeneratePreviewBinaryContent(t *testing.T) {
	agent, _, _ := newTestAgent()
	do := tool.DataObject{
		Content:  []byte{0xFF, 0xD8, 0xFF, 0xE0},
		MIMEType: "image/jpeg",
	}
	preview := agent.generatePreview(do)
	if !strings.Contains(preview, "Binary content") {
		t.Errorf("preview = %q, want 'Binary content' placeholder", preview)
	}
	if !strings.Contains(preview, "image/jpeg") {
		t.Errorf("preview = %q, want mime_type included", preview)
	}
}

func TestGeneratePreviewEmptyContent(t *testing.T) {
	agent, _, _ := newTestAgent()
	do := tool.DataObject{MIMEType: "text/plain"}
	if got := agent.generatePreview(do); got != "[Empty content]" {
		t.Errorf("generatePreview = %q, want '[Empty content]'", got)
	}
}

func TestGenerateArtifactFilenameMimeExtMap(t *testing.T) {
	tests := []struct {
		mime string
		ext  string
	}{
		{"text/plain", ".txt"},
		{"text/csv", ".csv"},
		{"application/json", ".json"},
		{"image/png", ".png"},
		{"image/jpeg", ".jpg"},
		{"application/pdf", ".pdf"},
		{"audio/mpeg", ".mp3"},
		{"some/unknown-type", ".bin"},
	}
	for _, tt := range tests {
		t.Run(tt.mime, func(t *testing.T) {
			name := generateArtifactFilename(tt.mime)
			if !strings.HasPrefix(name, "output_") {
				t.Errorf("filename %q missing output_ prefix", name)
			}
			if !strings.HasSuffix(name, tt.ext) {
				t.Errorf("filename %q missing extension %q", name, tt.ext)
			}
		})
	}
}

func TestGenerateArtifactFilename_EveryCallGetsAFreshName(t *testing.T) {
	t.Parallel()
	const callers, perCaller = 8, 64
	names := make(chan string, callers*perCaller)
	var wg sync.WaitGroup
	for range callers {
		wg.Go(func() {
			for range perCaller {
				names <- generateArtifactFilename("application/json")
			}
		})
	}
	wg.Wait()
	close(names)
	seen := map[string]bool{}
	for name := range names {
		if seen[name] {
			t.Fatalf("name %q was handed out twice among %d parallel results, so the second result would become a version of the first", name, callers*perCaller)
		}
		seen[name] = true
	}
}
