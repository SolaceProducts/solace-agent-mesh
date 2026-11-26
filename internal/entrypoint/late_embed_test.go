// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/embed"
)

func TestHandleStatusEvent_EmbedGate(t *testing.T) {
	statusTopic := "test-ns/a2a/v1/gateway/status/test-gw/"

	dispatch := func(t *testing.T, core *GatewayCore, taskID, text, contextID string) *a2a.TaskStatusUpdateEvent {
		t.Helper()
		event := &a2a.TaskStatusUpdateEvent{
			TaskID: taskID, ContextID: contextID, Kind: "status-update",
			Status: a2a.TaskStatus{
				State:   a2a.TaskStateWorking,
				Message: &a2a.Message{Kind: "message", Role: a2a.RoleAgent, Parts: []a2a.Part{a2a.NewTextPart(text)}},
			},
			Metadata: map[string]any{"agent_name": "TestAgent"},
		}
		payload, err := json.Marshal(a2a.Response{JSONRPC: "2.0", ID: taskID, Result: event})
		if err != nil {
			t.Fatal(err)
		}
		core.handleStatusEvent(context.Background(), &broker.Message{Topic: statusTopic + taskID, Payload: payload})
		rec := core.adapter.(*recordingAdapter)
		if len(rec.statusUpdates) != 1 {
			t.Fatalf("expected 1 dispatched status update, got %d", len(rec.statusUpdates))
		}
		return rec.statusUpdates[0]
	}

	t.Run("embed resolved end-to-end", func(t *testing.T) {
		core, store := setupTemplateBlockGateway(t)
		saveTestArtifact(t, store, "sess-e2e", "doc.md", []byte("# Title\n\nResolved body content"), "text/markdown")
		got := dispatch(t, core, "task-embed", "see «artifact_content:doc.md»", "sess-e2e")
		text := got.Status.Message.Parts[0].Text
		if !strings.Contains(text, "Resolved body content") {
			t.Errorf("embed not resolved through handleStatusEvent: %q", text)
		}
		if strings.Contains(text, "«artifact_content:") {
			t.Errorf("raw marker should be gone: %q", text)
		}

		rec := core.adapter.(*recordingAdapter)
		var emitted a2a.Response
		if err := json.Unmarshal(rec.statusRawPayloads[0], &emitted); err != nil {
			t.Fatalf("re-emitted payload not valid JSON-RPC: %v", err)
		}
		if emitted.JSONRPC != "2.0" || emitted.ID != "task-embed" {
			t.Errorf("re-emitted envelope lost fields: jsonrpc=%q id=%q", emitted.JSONRPC, emitted.ID)
		}
		if !strings.Contains(string(rec.statusRawPayloads[0]), "Resolved body content") {
			t.Errorf("re-emitted payload missing resolved content: %s", rec.statusRawPayloads[0])
		}
	})

	t.Run("plain text passes through untouched", func(t *testing.T) {
		core, _ := setupTemplateBlockGateway(t)
		const plain = "just a normal streaming delta, no markers"
		got := dispatch(t, core, "task-plain", plain, "sess-plain")
		if got.Status.Message.Parts[0].Text != plain {
			t.Errorf("plain text was modified: %q", got.Status.Message.Parts[0].Text)
		}
	})
}

func TestStatusMayHaveEmbeds(t *testing.T) {
	dataPart := func(typ string) a2a.Part {
		return a2a.Part{Kind: "data", Data: map[string]any{"type": typ}}
	}
	msg := func(parts ...a2a.Part) *a2a.TaskStatusUpdateEvent {
		return &a2a.TaskStatusUpdateEvent{Status: a2a.TaskStatus{Message: &a2a.Message{Parts: parts}}}
	}

	cases := []struct {
		name  string
		event *a2a.TaskStatusUpdateEvent
		want  bool
	}{
		{"plain text", msg(a2a.NewTextPart("just a normal streaming delta")), false},
		{"late embed marker", msg(a2a.NewTextPart("«artifact_content:x.md»")), true},
		{"triple template marker", msg(a2a.NewTextPart("«««template_liquid: t\nbody»»»")), true},
		{"marker in later part", msg(a2a.NewTextPart("intro"), a2a.NewTextPart("see «artifact_return:c.png»")), true},
		{"template_block data part", msg(dataPart("template_block")), true},
		{"other data part", msg(dataPart("chart")), false},
		{"nil message", &a2a.TaskStatusUpdateEvent{Status: a2a.TaskStatus{Message: nil}}, false},
		{"no parts", msg(), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := statusMayHaveEmbeds(tc.event); got != tc.want {
				t.Errorf("statusMayHaveEmbeds = %v, want %v", got, tc.want)
			}
		})
	}

	weird := &a2a.TaskStatusUpdateEvent{Status: a2a.TaskStatus{Message: &a2a.Message{
		Parts: []a2a.Part{{Kind: "data", Data: "not-a-map"}},
	}}}
	if statusMayHaveEmbeds(weird) {
		t.Error("non-map data part should not match")
	}
}

func TestResolveLateEmbedsInStatus_ArtifactContent(t *testing.T) {
	core, store := setupTemplateBlockGateway(t)
	ctx := context.Background()

	saveTestArtifact(t, store, "sess-ac", "org_chart.md",
		[]byte("# Org Chart\n\n- CEO: Alice\n- CTO: Bob"),
		"text/markdown")

	event := &a2a.TaskStatusUpdateEvent{
		TaskID:    "task-ac",
		ContextID: "sess-ac",
		Kind:      "status-update",
		Status: a2a.TaskStatus{
			State: a2a.TaskStateWorking,
			Message: &a2a.Message{
				Kind:  "message",
				Role:  a2a.RoleAgent,
				Parts: []a2a.Part{a2a.NewTextPart("«artifact_content:org_chart.md»")},
			},
		},
		Metadata: map[string]any{"agent_name": "TestAgent"},
	}

	changed, _ := core.resolveLateEmbedsInStatus(ctx, "task-ac", event)
	if !changed {
		t.Fatal("expected changed=true")
	}
	if got := event.Status.Message.Parts[0].Text; !strings.Contains(got, "CEO: Alice") {
		t.Errorf("resolved text missing artifact body: %q", got)
	}
	if got := event.Status.Message.Parts[0].Text; strings.Contains(got, "«artifact_content:") {
		t.Errorf("raw marker should be gone, got: %q", got)
	}
}

func TestResolveLateEmbedsInStatus_ArtifactReturn(t *testing.T) {
	core, store := setupTemplateBlockGateway(t)
	ctx := context.Background()

	saveTestArtifact(t, store, "sess-ar", "report.md",
		[]byte("# Test Report"),
		"text/markdown")

	event := &a2a.TaskStatusUpdateEvent{
		TaskID:    "task-ar",
		ContextID: "sess-ar",
		Kind:      "status-update",
		Status: a2a.TaskStatus{
			State: a2a.TaskStateWorking,
			Message: &a2a.Message{
				Kind:  "message",
				Role:  a2a.RoleAgent,
				Parts: []a2a.Part{a2a.NewTextPart("Here is the report. «artifact_return:report.md» thanks")},
			},
		},
		Metadata: map[string]any{"agent_name": "TestAgent"},
	}

	changed, _ := core.resolveLateEmbedsInStatus(ctx, "task-ar", event)
	if !changed {
		t.Fatal("expected changed=true")
	}

	parts := event.Status.Message.Parts
	var sawFile bool
	for _, p := range parts {
		if p.Kind == "file" && p.File != nil && p.File.Name == "report.md" {
			sawFile = true
		}
		if p.Kind == "text" && strings.Contains(p.Text, "«artifact_return:") {
			t.Errorf("raw marker should be gone, got: %q", p.Text)
		}
	}
	if !sawFile {
		t.Errorf("expected interleaved file part for report.md, got parts=%+v", parts)
	}
}

func resolveArtifactReturnURI(t *testing.T, core *GatewayCore, sessionID, embedText, filename string) string {
	t.Helper()
	if part := resolveArtifactReturnPart(t, core, sessionID, embedText, filename); part != nil {
		return part.File.URI
	}
	return ""
}

func TestResolveLateEmbedsInStatus_ArtifactReturnCarriesWorkingTag(t *testing.T) {
	core, store := setupTemplateBlockGateway(t)
	svc := &artifact.ScopedService{Inner: store, Scope: "namespace", Namespace: "test-ns"}
	if _, err := artifact.SaveWithMetadata(t.Context(), svc, "TestAgent", defaultTestUserID, "sess-working", "deck-spec.yaml",
		[]byte("- layout: title\n"), "application/yaml", artifact.MetadataOpts{Tags: []string{a2a.ArtifactTagWorking}}, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatalf("save spec: %v", err)
	}
	saveTestArtifact(t, store, "sess-working", "deck.pptx", []byte("pptx-bytes"), "application/vnd.openxmlformats-officedocument.presentationml.presentation")

	embeds := "Done. «artifact_return:deck.pptx» «artifact_return:deck-spec.yaml»"
	spec := resolveArtifactReturnPart(t, core, "sess-working", embeds, "deck-spec.yaml")
	deck := resolveArtifactReturnPart(t, core, "sess-working", embeds, "deck.pptx")
	if spec == nil || deck == nil {
		t.Fatalf("expected both file parts, got spec=%v deck=%v", spec, deck)
	}

	if !slices.Contains(partTags(*spec), a2a.ArtifactTagWorking) {
		t.Errorf("in-memory spec part lacks the __working tag: %v", spec.Metadata)
	}

	raw, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	var onTheWire a2a.Part
	if err := json.Unmarshal(raw, &onTheWire); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(partTags(onTheWire), a2a.ArtifactTagWorking) {
		t.Errorf("returned spec lost its __working tag on the wire: %s", raw)
	}
	if deck.Metadata != nil {
		t.Errorf("untagged deliverable must carry no tags, got metadata %v", deck.Metadata)
	}
}

func partTags(p a2a.Part) []string {
	return tagStrings(p.Metadata[filePartTagsKey])
}

func TestResolveLateEmbedsInStatus_ArtifactReturnTagsFollowTheReturnedVersion(t *testing.T) {
	core, store := setupTemplateBlockGateway(t)
	svc := &artifact.ScopedService{Inner: store, Scope: "namespace", Namespace: "test-ns"}
	saveWorking := func(t *testing.T, sessionID, filename string) {
		t.Helper()
		if _, err := artifact.SaveWithMetadata(t.Context(), svc, "TestAgent", defaultTestUserID, sessionID, filename,
			[]byte("working: true\n"), "application/yaml", artifact.MetadataOpts{Tags: []string{a2a.ArtifactTagWorking}}, slog.New(slog.DiscardHandler)); err != nil {
			t.Fatalf("save working version: %v", err)
		}
	}
	returnedPart := func(t *testing.T, sessionID, embed, filename string) *a2a.Part {
		t.Helper()
		part := resolveArtifactReturnPart(t, core, sessionID, "Here. "+embed, filename)
		if part == nil {
			t.Fatalf("expected a file part for %s", embed)
		}
		return part
	}

	t.Run("bare return of a later untagged version", func(t *testing.T) {
		saveTestArtifact(t, store, "sess-tagver", "g.yaml", []byte("v0\n"), "application/yaml")
		saveWorking(t, "sess-tagver", "g.yaml")
		saveTestArtifact(t, store, "sess-tagver", "g.yaml", []byte("v2\n"), "application/yaml")

		part := returnedPart(t, "sess-tagver", "«artifact_return:g.yaml»", "g.yaml")
		if !strings.HasSuffix(part.File.URI, "?version=2") {
			t.Fatalf("expected latest version in URI (?version=2), got %q", part.File.URI)
		}
		if slices.Contains(partTags(*part), a2a.ArtifactTagWorking) {
			t.Errorf("v2 deliverable carries v1's __working tag: %v", part.Metadata)
		}
	})

	t.Run("explicit versions", func(t *testing.T) {
		saveWorking(t, "sess-tagver-explicit", "h.yaml")
		saveTestArtifact(t, store, "sess-tagver-explicit", "h.yaml", []byte("v1\n"), "application/yaml")

		tests := []struct {
			embed       string
			wantWorking bool
		}{
			{"«artifact_return:h.yaml:0»", true},
			{"«artifact_return:h.yaml:1»", false},
			{"«artifact_return:h.yaml»", false},
		}
		for _, tt := range tests {
			part := returnedPart(t, "sess-tagver-explicit", tt.embed, "h.yaml")
			if got := slices.Contains(partTags(*part), a2a.ArtifactTagWorking); got != tt.wantWorking {
				t.Errorf("%s: __working = %v, want %v (metadata %v)", tt.embed, got, tt.wantWorking, part.Metadata)
			}
		}
	})
}

func resolveArtifactReturnPart(t *testing.T, core *GatewayCore, sessionID, embedText, filename string) *a2a.Part {
	t.Helper()
	event := &a2a.TaskStatusUpdateEvent{
		TaskID:    "task-" + sessionID,
		ContextID: sessionID,
		Kind:      "status-update",
		Status: a2a.TaskStatus{
			State: a2a.TaskStateWorking,
			Message: &a2a.Message{
				Kind:  "message",
				Role:  a2a.RoleAgent,
				Parts: []a2a.Part{a2a.NewTextPart(embedText)},
			},
		},
		Metadata: map[string]any{"agent_name": "TestAgent"},
	}
	core.resolveLateEmbedsInStatus(t.Context(), event.TaskID, event)
	for _, p := range event.Status.Message.Parts {
		if p.Kind == "file" && p.File != nil && p.File.Name == filename {
			return &p
		}
	}
	return nil
}

func TestResolveLateEmbedsInStatus_ArtifactReturnLatestVersion(t *testing.T) {
	core, store := setupTemplateBlockGateway(t)

	saveTestArtifact(t, store, "sess-latest", "report.md", []byte("# v0"), "text/markdown")
	saveTestArtifact(t, store, "sess-latest", "report.md", []byte("# v1"), "text/markdown")
	saveTestArtifact(t, store, "sess-latest", "report.md", []byte("# v2"), "text/markdown")

	uri := resolveArtifactReturnURI(t, core, "sess-latest", "Updated report. «artifact_return:report.md»", "report.md")
	if uri == "" {
		t.Fatal("expected interleaved file part for report.md")
	}
	if !strings.HasSuffix(uri, "?version=2") {
		t.Errorf("expected latest version in URI (?version=2), got: %q", uri)
	}
}

func TestResolveLateEmbedsInStatus_ArtifactReturnExplicitVersion(t *testing.T) {
	core, store := setupTemplateBlockGateway(t)

	saveTestArtifact(t, store, "sess-explicit", "report.md", []byte("# v0"), "text/markdown")
	saveTestArtifact(t, store, "sess-explicit", "report.md", []byte("# v1"), "text/markdown")

	uri := resolveArtifactReturnURI(t, core, "sess-explicit", "Original report. «artifact_return:report.md:0»", "report.md")
	if !strings.HasSuffix(uri, "?version=0") {
		t.Errorf("expected explicit version in URI (?version=0), got: %q", uri)
	}
}

func TestResolveLateEmbedsInStatus_ArtifactReturnNoVersions(t *testing.T) {
	core, _ := setupTemplateBlockGateway(t)

	uri := resolveArtifactReturnURI(t, core, "sess-missing", "Here. «artifact_return:never-saved.md»", "never-saved.md")
	if !strings.HasSuffix(uri, "?version=0") {
		t.Errorf("expected fallback version in URI (?version=0), got: %q", uri)
	}
}

func TestResolveLateEmbedsInStatus_ArtifactReturnURIEscapesTheFilename(t *testing.T) {
	for _, filename := range []string{"Q3 report (final).pdf", "plan #2.md", "what?.txt", "a%2Fb.txt"} {
		t.Run(filename, func(t *testing.T) {
			core, store := setupTemplateBlockGateway(t)
			saveTestArtifact(t, store, "sess-escape", filename, []byte("body"), "text/plain")

			uri := resolveArtifactReturnURI(t, core, "sess-escape", "Here. «artifact_return:"+filename+"»", filename)

			loc, version, ok := artifact.ParseURI(uri)
			if !ok || version == nil || *version != 0 {
				t.Fatalf("ParseURI(%q) = %+v, version %v, ok %v", uri, loc, version, ok)
			}
			want := artifact.Locator{AppName: "test-ns", UserID: defaultTestUserID, SessionID: "sess-escape", Filename: filename}
			if loc != want {
				t.Fatalf("URI %q reads back as %+v, want %+v", uri, loc, want)
			}
		})
	}
}

func TestResolveLateEmbedsInStatus_ArtifactReturnStillAttachesUnderANamespaceNoURIHostCanHold(t *testing.T) {
	const unroutableNamespace = "acme/prod"
	for _, filename := range []string{"report.md", "plan #2.md"} {
		t.Run(filename, func(t *testing.T) {
			store := memory.New()
			core := setupTemplateBlockGatewayInNamespace(t, store, unroutableNamespace)
			loc := artifact.Locator{AppName: unroutableNamespace, UserID: defaultTestUserID, SessionID: "sess-slash", Filename: filename}
			if _, err := store.Save(t.Context(), loc, []byte("body"), "text/plain"); err != nil {
				t.Fatalf("save %q: %v", filename, err)
			}

			part := resolveArtifactReturnPart(t, core, "sess-slash", "Here. «artifact_return:"+filename+"»", filename)

			if part == nil {
				t.Fatalf("no file part for %q saved under namespace %q", filename, unroutableNamespace)
			}
			got, version, ok := artifact.ParseURI(part.File.URI)
			if !ok || version == nil || *version != 0 {
				t.Fatalf("ParseURI(%q) = %+v, version %v, ok %v", part.File.URI, got, version, ok)
			}
			if got.UserID != loc.UserID || got.SessionID != loc.SessionID || got.Filename != filename {
				t.Fatalf("URI %q reads back as %+v, want user %q session %q file %q", part.File.URI, got, loc.UserID, loc.SessionID, filename)
			}
		})
	}
}

type errListVersionsStore struct {
	*memory.Store
}

func (errListVersionsStore) ListVersions(context.Context, artifact.Locator) ([]int, error) {
	return nil, errors.New("simulated list-versions failure")
}

func TestResolveLateEmbedsInStatus_ArtifactReturnListVersionsError(t *testing.T) {
	core := setupTemplateBlockGatewayWithStore(t, errListVersionsStore{memory.New()})

	uri := resolveArtifactReturnURI(t, core, "sess-listerr", "Here. «artifact_return:report.md»", "report.md")
	if !strings.HasSuffix(uri, "?version=0") {
		t.Errorf("expected fallback version in URI (?version=0) on list error, got: %q", uri)
	}
}

func TestResolveLateEmbedsInStatus_NoEmbed(t *testing.T) {
	core, _ := setupTemplateBlockGateway(t)
	ctx := context.Background()

	event := &a2a.TaskStatusUpdateEvent{
		TaskID:    "task-plain",
		ContextID: "sess-plain",
		Kind:      "status-update",
		Status: a2a.TaskStatus{
			State: a2a.TaskStateWorking,
			Message: &a2a.Message{
				Kind:  "message",
				Role:  a2a.RoleAgent,
				Parts: []a2a.Part{a2a.NewTextPart("Here is a plain text update.")},
			},
		},
		Metadata: map[string]any{"agent_name": "TestAgent"},
	}

	changed, signals := core.resolveLateEmbedsInStatus(ctx, "task-plain", event)
	if changed {
		t.Error("expected changed=false for plain text")
	}
	if len(signals) != 0 {
		t.Errorf("expected no signals, got %d", len(signals))
	}
}

func TestResolveLateEmbedsInStatus_NilMessage(t *testing.T) {
	core, _ := setupTemplateBlockGateway(t)
	ctx := context.Background()

	event := &a2a.TaskStatusUpdateEvent{
		TaskID:    "task-nil",
		ContextID: "sess-nil",
		Kind:      "status-update",
		Status:    a2a.TaskStatus{State: a2a.TaskStateWorking},
	}

	changed, signals := core.resolveLateEmbedsInStatus(ctx, "task-nil", event)
	if changed {
		t.Error("expected changed=false for nil message")
	}
	if signals != nil {
		t.Errorf("expected nil signals, got %v", signals)
	}
}

func TestPartsEqual(t *testing.T) {
	mkFile := func(uri, name string) a2a.Part { return a2a.NewFilePart(uri, name, "text/plain") }

	cases := []struct {
		name string
		a, b []a2a.Part
		want bool
	}{
		{"identical text", []a2a.Part{a2a.NewTextPart("hi")}, []a2a.Part{a2a.NewTextPart("hi")}, true},
		{"len differs", []a2a.Part{a2a.NewTextPart("hi")}, []a2a.Part{a2a.NewTextPart("hi"), a2a.NewTextPart("bye")}, false},
		{"text differs", []a2a.Part{a2a.NewTextPart("hi")}, []a2a.Part{a2a.NewTextPart("bye")}, false},
		{"one file nil one not", []a2a.Part{a2a.NewTextPart("x")}, []a2a.Part{mkFile("u", "n")}, false},
		{"both file equal", []a2a.Part{mkFile("u", "n")}, []a2a.Part{mkFile("u", "n")}, true},
		{"file uri differs", []a2a.Part{mkFile("u1", "n")}, []a2a.Part{mkFile("u2", "n")}, false},
	}
	for _, tc := range cases {
		if got := partsEqual(tc.a, tc.b); got != tc.want {
			t.Errorf("%s: partsEqual = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestProcessEmbedSignals(t *testing.T) {
	core, _ := setupTemplateBlockGateway(t)
	ctx := context.Background()

	core.processEmbedSignals(ctx, nil, []embed.Signal{{Type: embed.SignalInlineBinaryContent}})

	msg := &a2a.Message{Kind: "message", Role: a2a.RoleAgent, Parts: []a2a.Part{a2a.NewTextPart("x")}}
	core.processEmbedSignals(ctx, msg, nil)
	if len(msg.Parts) != 1 {
		t.Errorf("empty signals must not change parts, got %d", len(msg.Parts))
	}

	core.processEmbedSignals(ctx, msg, []embed.Signal{{Type: embed.SignalArtifactReturn}})
	if len(msg.Parts) != 1 {
		t.Errorf("artifact_return signal must be skipped, got %d parts", len(msg.Parts))
	}

	core.processEmbedSignals(ctx, msg, []embed.Signal{{
		Type: embed.SignalInlineBinaryContent,
		Data: embed.InlineBinaryData{Bytes: nil, Name: "x.bin", MIMEType: "application/octet-stream"},
	}})
	if len(msg.Parts) != 1 {
		t.Errorf("empty inline_binary must be skipped, got %d parts", len(msg.Parts))
	}

	core.processEmbedSignals(ctx, msg, []embed.Signal{{
		Type: embed.SignalInlineBinaryContent,
		Data: embed.InlineBinaryData{Bytes: []byte("hello"), Name: "out.bin"},
	}})
	if len(msg.Parts) != 2 {
		t.Fatalf("expected file part appended, got %d parts", len(msg.Parts))
	}
	fp := msg.Parts[1]
	if fp.Kind != "file" || fp.File == nil || fp.File.Name != "out.bin" || fp.File.MIMEType != "application/octet-stream" {
		t.Errorf("unexpected inline binary file part: %+v", fp.File)
	}

	core.processEmbedSignals(ctx, msg, []embed.Signal{{
		Type: embed.SignalDeepResearchReport,
		Data: embed.DeepResearchReportData{Filename: "r.md", Content: "## report"},
	}})
	if len(msg.Parts) != 3 {
		t.Fatalf("expected deep_research_report data part appended, got %d parts", len(msg.Parts))
	}
	dp := msg.Parts[2]
	dataMap, ok := dp.Data.(map[string]any)
	if dp.Kind != "data" || !ok || dataMap["type"] != "deep_research_report" || dataMap["filename"] != "r.md" {
		t.Errorf("unexpected deep_research_report data part: %+v", dp.Data)
	}

	core.processEmbedSignals(ctx, msg, []embed.Signal{{Type: embed.SignalInlineBinaryContent, Data: "wrong"}})
	core.processEmbedSignals(ctx, msg, []embed.Signal{{Type: embed.SignalDeepResearchReport, Data: 42}})
	if len(msg.Parts) != 3 {
		t.Errorf("malformed signals must be ignored, got %d parts", len(msg.Parts))
	}
}
