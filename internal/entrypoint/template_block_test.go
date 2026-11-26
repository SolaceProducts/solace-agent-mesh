// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/embed/directive"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samcli/render"
)

const defaultTestUserID = "sam_dev_user"

func setupTemplateBlockGateway(t *testing.T) (*GatewayCore, *memory.Store) {
	t.Helper()
	store := memory.New()
	return setupTemplateBlockGatewayWithStore(t, store), store
}

func setupTemplateBlockGatewayWithStore(t *testing.T, inner artifact.Service) *GatewayCore {
	t.Helper()
	return setupTemplateBlockGatewayInNamespace(t, inner, "test-ns")
}

func setupTemplateBlockGatewayInNamespace(t *testing.T, inner artifact.Service, artifactNamespace string) *GatewayCore {
	t.Helper()

	artSvc := &artifact.ScopedService{
		Inner:     inner,
		Scope:     "namespace",
		Namespace: artifactNamespace,
	}

	brk := broker.NewDevBroker()
	rt := runtime.NewRuntime(brk, nil, runtime.WithArtifacts(artSvc))

	adapter := &recordingAdapter{}
	appConfig := map[string]any{
		"namespace":             "test-ns",
		"gateway_id":            "test-gw",
		"default_user_identity": defaultTestUserID,
	}
	core := NewGatewayCore(adapter, appConfig)
	core.SetServices(rt.Services())

	if err := core.Init(context.Background(), nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	core.cfg.EnableEmbedResolution = true

	return core
}

func saveTestArtifact(t *testing.T, store *memory.Store, sessionID, filename string, content []byte, mimeType string) {
	t.Helper()
	loc := artifact.Locator{AppName: "test-ns", UserID: defaultTestUserID, SessionID: sessionID, Filename: filename}
	if _, err := store.Save(context.Background(), loc, content, mimeType); err != nil {
		t.Fatal(err)
	}
}

func TestResolveTemplateBlocksInStatus_RendersTemplate(t *testing.T) {
	core, store := setupTemplateBlockGateway(t)
	ctx := context.Background()

	saveTestArtifact(t, store, "sess-1", "items.json",
		[]byte(`[{"name": "Widget", "price": 9.99}, {"name": "Gadget", "price": 24.50}]`),
		"application/json")

	event := &a2a.TaskStatusUpdateEvent{
		TaskID:    "task-1",
		ContextID: "sess-1",
		Kind:      "status-update",
		Status: a2a.TaskStatus{
			State: a2a.TaskStateWorking,
			Message: &a2a.Message{
				Kind: "message",
				Role: a2a.RoleAgent,
				Parts: []a2a.Part{
					a2a.NewDataPart(map[string]any{
						"type":             "template_block",
						"template_id":      "tmpl-1",
						"data_artifact":    "items.json",
						"template_content": "Items:{% for item in items %}\n- {{ item.name }}: ${{ item.price }}{% endfor %}",
					}),
				},
			},
		},
		Metadata: map[string]any{"agent_name": "TestAgent"},
	}

	modified := core.resolveTemplateBlocksInStatus(ctx, "test-task", event)
	if !modified {
		t.Fatal("expected modified=true")
	}

	if len(event.Status.Message.Parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(event.Status.Message.Parts))
	}
	part := event.Status.Message.Parts[0]
	if part.Kind != "text" {
		t.Fatalf("expected text part, got %q", part.Kind)
	}
	if !strings.Contains(part.Text, "Widget") {
		t.Errorf("expected 'Widget' in output, got: %s", part.Text)
	}
	if !strings.Contains(part.Text, "Gadget") {
		t.Errorf("expected 'Gadget' in output, got: %s", part.Text)
	}
	if !strings.Contains(part.Text, "$9.99") {
		t.Errorf("expected '$9.99' in output, got: %s", part.Text)
	}
}

func TestResolveTemplateBlocksInStatus_NonTemplateDataPartPassedThrough(t *testing.T) {
	core, _ := setupTemplateBlockGateway(t)
	ctx := context.Background()

	event := &a2a.TaskStatusUpdateEvent{
		TaskID:    "task-2",
		ContextID: "sess-2",
		Kind:      "status-update",
		Status: a2a.TaskStatus{
			State: a2a.TaskStateWorking,
			Message: &a2a.Message{
				Kind: "message",
				Role: a2a.RoleAgent,
				Parts: []a2a.Part{
					a2a.NewDataPart(map[string]any{
						"type":     "artifact_saved",
						"filename": "report.txt",
						"version":  1,
					}),
				},
			},
		},
	}

	modified := core.resolveTemplateBlocksInStatus(ctx, "test-task", event)
	if modified {
		t.Error("expected modified=false for non-template data part")
	}
	if event.Status.Message.Parts[0].Kind != "data" {
		t.Errorf("expected data part unchanged, got kind=%q", event.Status.Message.Parts[0].Kind)
	}
}

func TestResolveTemplateBlocksInStatus_NilMessage(t *testing.T) {
	core, _ := setupTemplateBlockGateway(t)
	ctx := context.Background()

	event := &a2a.TaskStatusUpdateEvent{
		TaskID: "task-3",
		Kind:   "status-update",
		Status: a2a.TaskStatus{State: a2a.TaskStateWorking},
	}

	modified := core.resolveTemplateBlocksInStatus(ctx, "test-task", event)
	if modified {
		t.Error("expected modified=false for nil message")
	}
}

func TestResolveTemplateBlocksInStatus_MixedParts(t *testing.T) {
	core, store := setupTemplateBlockGateway(t)
	ctx := context.Background()

	saveTestArtifact(t, store, "sess-4", "names.json",
		[]byte(`["Alice", "Bob"]`), "application/json")

	event := &a2a.TaskStatusUpdateEvent{
		TaskID:    "task-4",
		ContextID: "sess-4",
		Kind:      "status-update",
		Status: a2a.TaskStatus{
			State: a2a.TaskStateWorking,
			Message: &a2a.Message{
				Kind: "message",
				Role: a2a.RoleAgent,
				Parts: []a2a.Part{
					a2a.NewTextPart("Before template."),
					a2a.NewDataPart(map[string]any{
						"type":             "template_block",
						"template_id":      "tmpl-2",
						"data_artifact":    "names.json",
						"template_content": "Names:{% for name in items %} {{ name }}{% endfor %}",
					}),
					a2a.NewTextPart("After template."),
				},
			},
		},
		Metadata: map[string]any{"agent_name": "TestAgent"},
	}

	modified := core.resolveTemplateBlocksInStatus(ctx, "test-task", event)
	if !modified {
		t.Fatal("expected modified=true")
	}

	if len(event.Status.Message.Parts) != 3 {
		t.Fatalf("expected 3 parts, got %d", len(event.Status.Message.Parts))
	}

	if event.Status.Message.Parts[0].Text != "Before template." {
		t.Errorf("first part text = %q", event.Status.Message.Parts[0].Text)
	}
	if event.Status.Message.Parts[1].Kind != "text" {
		t.Errorf("second part kind = %q, want text", event.Status.Message.Parts[1].Kind)
	}
	if !strings.Contains(event.Status.Message.Parts[1].Text, "Alice") {
		t.Errorf("expected 'Alice' in second part, got: %s", event.Status.Message.Parts[1].Text)
	}
	if event.Status.Message.Parts[2].Text != "After template." {
		t.Errorf("third part text = %q", event.Status.Message.Parts[2].Text)
	}
}

func TestReconstructTemplateBlock(t *testing.T) {
	jp := "$.items"
	limit := 10
	tb := &templateBlockData{
		DataArtifact:    "data.csv",
		JSONPath:        &jp,
		Limit:           &limit,
		TemplateContent: "{% for item in items %}{{ item.name }}{% endfor %}",
	}

	result := reconstructTemplateBlock(tb)

	if !strings.HasPrefix(result, "\u00AB\u00AB\u00ABtemplate_liquid:") {
		t.Errorf("missing opening delimiter, got: %s", result)
	}
	if !strings.HasSuffix(result, "\n\u00BB\u00BB\u00BB") {
		t.Errorf("missing closing delimiter, got: %s", result)
	}
	if !strings.Contains(result, `data="data.csv"`) {
		t.Errorf("missing data param")
	}
	if !strings.Contains(result, `jsonpath="$.items"`) {
		t.Errorf("missing jsonpath param")
	}
	if !strings.Contains(result, `limit="10"`) {
		t.Errorf("missing limit param")
	}
}

func TestReconstructTemplateBlockKeepsJSONPathBackslashes(t *testing.T) {
	jp := `$.items[?(@.sku =~ /^A\d+/)]`
	tb := &templateBlockData{
		DataArtifact:    "rows.json",
		JSONPath:        &jp,
		TemplateContent: "{{ items }}",
	}

	m := directive.TemplateBlockRegex.FindStringSubmatch(reconstructTemplateBlock(tb))
	if m == nil {
		t.Fatalf("reconstructed block does not parse: %q", reconstructTemplateBlock(tb))
	}
	params := map[string]string{}
	for _, p := range directive.TemplateParamRegex.FindAllStringSubmatch(m[1], -1) {
		params[p[1]] = p[2]
	}
	if params["jsonpath"] != jp {
		t.Errorf("parsed jsonpath = %q, want %q", params["jsonpath"], jp)
	}
	if params["data"] != tb.DataArtifact {
		t.Errorf("parsed data = %q, want %q", params["data"], tb.DataArtifact)
	}
}

func TestReconstructTemplateBlock_MinimalParams(t *testing.T) {
	tb := &templateBlockData{
		DataArtifact:    "data.json",
		TemplateContent: "Hello {{ name }}",
	}

	result := reconstructTemplateBlock(tb)
	if strings.Contains(result, "jsonpath") {
		t.Errorf("should not contain jsonpath when nil")
	}
	if strings.Contains(result, "limit") {
		t.Errorf("should not contain limit when nil")
	}
}

func TestResolveCtxFromStatusEvent(t *testing.T) {
	core, _ := setupTemplateBlockGateway(t)
	core.cfg.ForceUserIdentity = "forced-user"

	event := &a2a.TaskStatusUpdateEvent{
		TaskID:    "task-5",
		ContextID: "sess-5",
		Metadata:  map[string]any{"agent_name": "MyAgent"},
	}

	rctx := core.resolveCtxFromStatusEvent("test-task", event)
	if rctx.AgentName != "MyAgent" {
		t.Errorf("AgentName = %q, want %q", rctx.AgentName, "MyAgent")
	}
	if rctx.UserID != "forced-user" {
		t.Errorf("UserID = %q, want %q", rctx.UserID, "forced-user")
	}
	if rctx.SessionID != "sess-5" {
		t.Errorf("SessionID = %q, want %q", rctx.SessionID, "sess-5")
	}
}

func TestResolveTemplateBlocksInStatus_WithMathEmbed(t *testing.T) {
	core, store := setupTemplateBlockGateway(t)
	ctx := context.Background()

	saveTestArtifact(t, store, "sess-6", "prices.json",
		[]byte(`[{"name": "Widget", "price": 10, "qty": 3}]`),
		"application/json")

	event := &a2a.TaskStatusUpdateEvent{
		TaskID:    "task-6",
		ContextID: "sess-6",
		Kind:      "status-update",
		Status: a2a.TaskStatus{
			State: a2a.TaskStateWorking,
			Message: &a2a.Message{
				Kind: "message",
				Role: a2a.RoleAgent,
				Parts: []a2a.Part{
					a2a.NewDataPart(map[string]any{
						"type":             "template_block",
						"template_id":      "tmpl-math",
						"data_artifact":    "prices.json",
						"template_content": "{% for item in items %}{{ item.name }}: \u00ABmath:{{ item.price }} * {{ item.qty }}\u00BB{% endfor %}",
					}),
				},
			},
		},
		Metadata: map[string]any{"agent_name": "TestAgent"},
	}

	modified := core.resolveTemplateBlocksInStatus(ctx, "test-task", event)
	if !modified {
		t.Fatal("expected modified=true")
	}

	part := event.Status.Message.Parts[0]
	if part.Kind != "text" {
		t.Fatalf("expected text part, got %q", part.Kind)
	}

	if !strings.Contains(part.Text, "30") {
		t.Errorf("expected math result '30' in output, got: %s", part.Text)
	}
	if !strings.Contains(part.Text, "Widget") {
		t.Errorf("expected 'Widget' in output, got: %s", part.Text)
	}
	if strings.Contains(part.Text, "\u00ABmath:") {
		t.Errorf("raw math embed should be resolved, got: %s", part.Text)
	}
}

func TestResolveTemplateBlocksInStatus_TableStaysATableAfterStreamedText(t *testing.T) {
	core, store := setupTemplateBlockGateway(t)
	ctx := context.Background()

	saveTestArtifact(t, store, "sess-5", "stock.json",
		[]byte(`[{"sku":"SKU-1","qty":4},{"sku":"SKU-2","qty":0},{"sku":"SKU-3","qty":9}]`), "application/json")

	event := &a2a.TaskStatusUpdateEvent{
		TaskID:    "task-5",
		ContextID: "sess-5",
		Kind:      "status-update",
		Status: a2a.TaskStatus{
			State: a2a.TaskStateWorking,
			Message: &a2a.Message{
				Kind: "message",
				Role: a2a.RoleAgent,
				Parts: []a2a.Part{
					a2a.NewDataPart(map[string]any{
						"type":             "template_block",
						"template_id":      "tmpl-5",
						"data_artifact":    "stock.json",
						"template_content": "| SKU | Qty |\n|---|---|\n{% for r in items %}| {{ r.sku }} | {{ r.qty }} |\n{% endfor %}",
					}),
				},
			},
		},
		Metadata: map[string]any{"agent_name": "NW Inventory"},
	}

	if !core.resolveTemplateBlocksInStatus(ctx, "test-task", event) {
		t.Fatal("expected modified=true")
	}

	chat := "Now I'm asking NW Inventory for the stock list." + event.Status.Message.Parts[0].Text + "That's everything."
	var tables []render.Block
	for _, b := range render.Blocks(chat) {
		if b.Kind == "table" {
			tables = append(tables, b)
		}
	}
	if len(tables) != 1 || tables[0].Rows != 3 {
		t.Errorf("want one 3-row table in the chat, got %+v from %q", tables, chat)
	}
}

func templateBlockEvent(sessionID, template string, metadata map[string]any) *a2a.TaskStatusUpdateEvent {
	return &a2a.TaskStatusUpdateEvent{
		TaskID:    "task-" + sessionID,
		ContextID: sessionID,
		Kind:      "status-update",
		Status: a2a.TaskStatus{
			State: a2a.TaskStateWorking,
			Message: &a2a.Message{
				Kind:     "message",
				Role:     a2a.RoleAgent,
				Metadata: metadata,
				Parts: []a2a.Part{
					a2a.NewDataPart(map[string]any{
						"type":             "template_block",
						"template_id":      "tmpl-" + sessionID,
						"data_artifact":    "stock.json",
						"template_content": template,
					}),
				},
			},
		},
		Metadata: map[string]any{"agent_name": "TestAgent"},
	}
}

func renderedTemplateText(t *testing.T, sessionID, template string, metadata map[string]any) string {
	t.Helper()
	core, store := setupTemplateBlockGateway(t)
	saveTestArtifact(t, store, sessionID, "stock.json",
		[]byte(`[{"sku":"SKU-1","qty":4},{"sku":"SKU-2","qty":0},{"sku":"SKU-3","qty":9}]`), "application/json")
	event := templateBlockEvent(sessionID, template, metadata)
	if !core.resolveTemplateBlocksInStatus(t.Context(), "test-task", event) {
		t.Fatal("expected modified=true")
	}
	return event.Status.Message.Parts[0].Text
}

func tablesIn(chat string) []render.Block {
	var tables []render.Block
	for _, b := range render.Blocks(chat) {
		if b.Kind == "table" {
			tables = append(tables, b)
		}
	}
	return tables
}

const stockRowsTemplate = "{% for r in items %}| {{ r.sku }} | {{ r.qty }} |\n{% endfor %}"

func TestResolveTemplateBlocksInStatus_RowsContinueAHeaderStreamedBeforeTheBlock(t *testing.T) {
	chat := "Stock by SKU:\n\n| SKU | Qty |\n|---|---:|\n" + renderedTemplateText(t, "sess-6", stockRowsTemplate, nil) + "That's everything."
	if tables := tablesIn(chat); len(tables) != 1 || tables[0].Rows != 3 {
		t.Errorf("want one 3-row table in the chat, got %+v from %q", tables, chat)
	}
}

func TestResolveTemplateBlocksInStatus_ShortDelimiterTableStaysSeparate(t *testing.T) {
	template := "| SKU | Qty |\n|-|-:|\n" + stockRowsTemplate
	cases := []struct {
		name, before string
		wantTables   int
	}{
		{"after a streamed table", "| Region | Total |\n|---|---|\n| North | 13 |\n", 2},
		{"after a streamed list", "Checked:\n\n- north\n- south\n", 1},
		{"after prose with no trailing newline", "Here is the stock list.", 1},
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			chat := c.before + renderedTemplateText(t, fmt.Sprintf("sess-7%d", i), template, nil)
			tables := tablesIn(chat)
			if len(tables) != c.wantTables || tables[len(tables)-1].Rows != 3 {
				t.Errorf("want %d tables, the last with 3 rows, got %+v from %q", c.wantTables, tables, chat)
			}
		})
	}
}

func TestResolveTemplateBlocksInStatus_PeerRowsNeverJoinTheParentsText(t *testing.T) {
	metadata := map[string]any{forwardedFromPeerMetadataKey: "NW Inventory"}
	rendered := renderedTemplateText(t, "sess-8", stockRowsTemplate, metadata)
	if !strings.HasPrefix(rendered, templateBlockSeparator) {
		t.Errorf("a peer's rows must start their own block, got %q", rendered)
	}
}

func TestSeparatedTemplateOutput(t *testing.T) {
	cases := []struct {
		name, rendered          string
		streamedTableMayPrecede bool
		want                    string
	}{
		{"rows only continue the open table", "| a | 1 |\n| b | 2 |\n", true, "| a | 1 |\n| b | 2 |\n\n\n"},
		{"leading newlines before rows are dropped", "\n| a | 1 |\n", true, "| a | 1 |\n\n\n"},
		{"a delimiter and rows continue a header streamed alone", "|---|---|\n| a | 1 |\n", true, "|---|---|\n| a | 1 |\n\n\n"},
		{"a whole table starts its own block", "| h | n |\n|---|---|\n| a | 1 |\n", true, "\n\n| h | n |\n|---|---|\n| a | 1 |\n\n\n"},
		{"a one-hyphen delimiter is a whole table", "| h | n |\n|-|-|\n| a | 1 |\n", true, "\n\n| h | n |\n|-|-|\n| a | 1 |\n\n\n"},
		{"a two-hyphen delimiter is a whole table", "| h | n |\n|--|--|\n| a | 1 |\n", true, "\n\n| h | n |\n|--|--|\n| a | 1 |\n\n\n"},
		{"an aligned short delimiter is a whole table", "| h | n |\n|:-|-:|\n| a | 1 |\n", true, "\n\n| h | n |\n|:-|-:|\n| a | 1 |\n\n\n"},
		{"a peer's rows keep the separators", "| a | 1 |\n", false, "\n\n| a | 1 |\n\n\n"},
		{"prose starts its own block", "Items:\n- a\n", true, "\n\nItems:\n- a\n\n\n"},
		{"empty output keeps the separators", "", true, "\n\n\n\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := separatedTemplateOutput(c.rendered, c.streamedTableMayPrecede); got != c.want {
				t.Errorf("separatedTemplateOutput(%q, %v) = %q, want %q", c.rendered, c.streamedTableMayPrecede, got, c.want)
			}
		})
	}
}
