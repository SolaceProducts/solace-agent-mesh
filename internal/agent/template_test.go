// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
)

type memArtifactStore struct {
	data map[string]struct {
		content  []byte
		mimeType string
	}
}

func newMemArtifactStore() *memArtifactStore {
	return &memArtifactStore{
		data: make(map[string]struct {
			content  []byte
			mimeType string
		}),
	}
}

func (m *memArtifactStore) Save(_ context.Context, loc artifact.Locator, content []byte, mimeType string) (int, error) {
	m.data[loc.Filename] = struct {
		content  []byte
		mimeType string
	}{content: content, mimeType: mimeType}
	return 0, nil
}

func (m *memArtifactStore) Load(_ context.Context, loc artifact.Locator, _ *int) ([]byte, string, error) {
	d, ok := m.data[loc.Filename]
	if !ok {
		return nil, "", nil
	}
	return d.content, d.mimeType, nil
}

func (m *memArtifactStore) OpenReader(ctx context.Context, loc artifact.Locator, version *int) (io.ReadCloser, string, int64, error) {
	data, mime, err := m.Load(ctx, loc, version)
	if err != nil {
		return nil, "", 0, err
	}
	if data == nil {
		return nil, "", 0, nil
	}
	return io.NopCloser(bytes.NewReader(data)), mime, int64(len(data)), nil
}

func (m *memArtifactStore) Delete(_ context.Context, _ artifact.Locator) error { return nil }
func (m *memArtifactStore) ListKeys(_ context.Context, _ artifact.KeyScope) ([]string, error) {
	return nil, nil
}
func (m *memArtifactStore) ListVersions(_ context.Context, _ artifact.Locator) ([]int, error) {
	return nil, nil
}
func (m *memArtifactStore) GetVersionMetadata(_ context.Context, _ artifact.Locator, _ *int) (*artifact.VersionMetadata, error) {
	return nil, nil
}
func (m *memArtifactStore) SaveMetadata(_ context.Context, _ artifact.Locator, _ int, _ *artifact.VersionMetadata) error {
	return nil
}
func (m *memArtifactStore) SaveVersion(_ context.Context, _ artifact.Locator, _ int, _ []byte, _ string) error {
	return nil
}

func newTestTemplateRenderer(store *memArtifactStore) *TemplateRenderer {
	arts := &artifact.ScopedService{
		Inner:     store,
		Scope:     "namespace",
		Namespace: "test",
	}
	return NewTemplateRenderer(arts, "test-agent", "user1", "sess1", slog.Default())
}

func TestRenderBlockCSV(t *testing.T) {
	store := newMemArtifactStore()
	ctx := context.Background()

	loc := artifact.Locator{AppName: "test", UserID: "user1", SessionID: "sess1", Filename: "data.csv"}
	if _, err := store.Save(ctx, loc, []byte("Name,Age\nAlice,30\nBob,25\nCharlie,35"), "text/csv"); err != nil {
		t.Fatal(err)
	}

	tr := newTestTemplateRenderer(store)

	block := &FencedBlockEvent{
		Type:       FencedBlockTemplate,
		DataSource: "data.csv",
		Content:    "| Name | Age |\n| --- | --- |\n{% for row in data_rows %}| {{ row[0] }} | {{ row[1] }} |\n{% endfor %}",
		Limit:      -1,
	}

	got, err := tr.RenderBlock(ctx, block)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !containsAll(got, "Alice", "Bob", "Charlie", "30", "25", "35") {
		t.Errorf("rendered output missing expected data:\n%s", got)
	}
}

func TestRenderBlockJSON(t *testing.T) {
	store := newMemArtifactStore()
	ctx := context.Background()

	loc := artifact.Locator{AppName: "test", UserID: "user1", SessionID: "sess1", Filename: "data.json"}
	if _, err := store.Save(ctx, loc, []byte(`[{"name":"Alice","score":95},{"name":"Bob","score":87}]`), "application/json"); err != nil {
		t.Fatal(err)
	}

	tr := newTestTemplateRenderer(store)

	block := &FencedBlockEvent{
		Type:       FencedBlockTemplate,
		DataSource: "data.json",
		Content:    "{% for item in items %}{{ item.name }}: {{ item.score }}\n{% endfor %}",
		Limit:      -1,
	}

	got, err := tr.RenderBlock(ctx, block)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !containsAll(got, "Alice: 95", "Bob: 87") {
		t.Errorf("rendered output missing expected data:\n%s", got)
	}
}

func TestRenderBlockWithLimit(t *testing.T) {
	store := newMemArtifactStore()
	ctx := context.Background()

	loc := artifact.Locator{AppName: "test", UserID: "user1", SessionID: "sess1", Filename: "data.json"}
	if _, err := store.Save(ctx, loc, []byte(`[{"n":"a"},{"n":"b"},{"n":"c"}]`), "application/json"); err != nil {
		t.Fatal(err)
	}

	tr := newTestTemplateRenderer(store)

	block := &FencedBlockEvent{
		Type:       FencedBlockTemplate,
		DataSource: "data.json",
		Content:    "{% for item in items %}{{ item.n }}\n{% endfor %}",
		Limit:      2,
	}

	got, err := tr.RenderBlock(ctx, block)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if containsAll(got, "c") {
		t.Errorf("limit=2 should exclude 'c', got:\n%s", got)
	}
	if !containsAll(got, "a", "b") {
		t.Errorf("limit=2 should include 'a' and 'b', got:\n%s", got)
	}
}

func TestRenderBlockMissingData(t *testing.T) {
	store := newMemArtifactStore()
	ctx := context.Background()
	tr := newTestTemplateRenderer(store)

	block := &FencedBlockEvent{
		Type:    FencedBlockTemplate,
		Content: "some template",
		Limit:   -1,
	}

	got, err := tr.RenderBlock(ctx, block)
	if err == nil {
		t.Fatal("expected error for missing data parameter")
	}
	if got != "[Template Error: Missing 'data' parameter]" {
		t.Errorf("unexpected error message: %s", got)
	}
}

func TestRenderBlockYAML(t *testing.T) {
	store := newMemArtifactStore()
	ctx := context.Background()

	loc := artifact.Locator{AppName: "test", UserID: "user1", SessionID: "sess1", Filename: "config.yaml"}
	if _, err := store.Save(ctx, loc, []byte("name: TestApp\nversion: \"1.0\"\nfeatures:\n  - auth\n  - logging"), "application/yaml"); err != nil {
		t.Fatal(err)
	}

	tr := newTestTemplateRenderer(store)

	block := &FencedBlockEvent{
		Type:       FencedBlockTemplate,
		DataSource: "config.yaml",
		Content:    "App: {{ name }} v{{ version }}",
		Limit:      -1,
	}

	got, err := tr.RenderBlock(ctx, block)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got != "App: TestApp v1.0" {
		t.Errorf("got %q, want %q", got, "App: TestApp v1.0")
	}
}

func TestPrepareTemplateContextCSV(t *testing.T) {
	ctx, err := PrepareTemplateContext("a,b\n1,2\n3,4", "text/csv", "", -1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	headers, ok := ctx["headers"].([]any)
	if !ok || len(headers) != 2 {
		t.Fatalf("expected 2 headers, got: %v", ctx["headers"])
	}
	if headers[0] != "a" || headers[1] != "b" {
		t.Errorf("headers = %v, want [a, b]", headers)
	}

	rows, ok := ctx["data_rows"].([]any)
	if !ok || len(rows) != 2 {
		t.Fatalf("expected 2 data rows, got: %v", ctx["data_rows"])
	}
}

func TestPrepareTemplateContextJSONArray(t *testing.T) {
	ctx, err := PrepareTemplateContext(`[1,2,3]`, "application/json", "", -1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	items, ok := ctx["items"].([]any)
	if !ok || len(items) != 3 {
		t.Fatalf("expected 3 items, got: %v", ctx["items"])
	}
}

func TestPrepareTemplateContextJSONObject(t *testing.T) {
	ctx, err := PrepareTemplateContext(`{"name":"test","value":42}`, "application/json", "", -1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if ctx["name"] != "test" {
		t.Errorf("name = %v, want 'test'", ctx["name"])
	}
}

func TestPrepareTemplateContextPlainTextAndDotPath(t *testing.T) {
	ctx, err := PrepareTemplateContext("Hello world", "text/plain", "", -1)
	if err != nil || ctx["text"] != "Hello world" {
		t.Errorf("plain text ctx = %v, err = %v, want text=\"Hello world\"", ctx, err)
	}

	ctx, err = PrepareTemplateContext(`{"data":{"items":[1,2,3]}}`, "application/json", "$.data.items", 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if items, ok := ctx["items"].([]any); !ok || len(items) != 2 {
		t.Errorf("items = %v, want the first 2 of the jsonpath-selected array", ctx["items"])
	}
}

func TestPrepareTemplateContextJSONPathWildcard(t *testing.T) {
	data := `{"items":[{"name":"alice"},{"name":"bob"},{"name":"carol"}]}`
	ctx, err := PrepareTemplateContext(data, "application/json", "$.items[*].name", -1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	items, ok := ctx["items"].([]any)
	if !ok {
		t.Fatalf("expected items array, got: %T %v", ctx["items"], ctx["items"])
	}
	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %d: %v", len(items), items)
	}
	want := []string{"alice", "bob", "carol"}
	for i, w := range want {
		if items[i] != w {
			t.Errorf("items[%d] = %v, want %q", i, items[i], w)
		}
	}
}

func TestPrepareTemplateContextJSONPathFilter(t *testing.T) {
	data := `{"items":[{"name":"a","active":true},{"name":"b","active":false},{"name":"c","active":true}]}`
	ctx, err := PrepareTemplateContext(data, "application/json", "$.items[?(@.active==true)].name", -1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	items, ok := ctx["items"].([]any)
	if !ok {
		t.Fatalf("expected items array, got: %T %v", ctx["items"], ctx["items"])
	}
	if len(items) != 2 || items[0] != "a" || items[1] != "c" {
		t.Errorf("items = %v, want [a c]", items)
	}
}

func TestParseTemplateParams(t *testing.T) {
	block := &FencedBlockEvent{Type: FencedBlockTemplate, Limit: -1}
	parseTemplateParams(`data="sales.csv" jsonpath="$.items" limit="25"`, block)

	if block.DataSource != "sales.csv" {
		t.Errorf("DataSource = %q, want %q", block.DataSource, "sales.csv")
	}
	if block.JSONPath != "$.items" {
		t.Errorf("JSONPath = %q, want %q", block.JSONPath, "$.items")
	}
	if block.Limit != 25 {
		t.Errorf("Limit = %d, want 25", block.Limit)
	}
}

func TestValidateTemplateIndexBoundsCSV(t *testing.T) {
	store := newMemArtifactStore()
	ctx := context.Background()

	loc := artifact.Locator{AppName: "test", UserID: "user1", SessionID: "sess1", Filename: "people.csv"}
	if _, err := store.Save(ctx, loc, []byte("Name,Age,City\nAlice,30,NYC\nBob,25,LA"), "text/csv"); err != nil {
		t.Fatal(err)
	}

	arts := &artifact.ScopedService{Inner: store, Scope: "namespace", Namespace: "test"}

	tests := []struct {
		name    string
		content string
		wantErr bool
	}{
		{
			name:    "valid 0-indexed access",
			content: "{% for row in data_rows %}{{ row[0] }} {{ row[1] }} {{ row[2] }}{% endfor %}",
			wantErr: false,
		},
		{
			name:    "out of bounds row[3] with 3 columns",
			content: "{% for row in data_rows %}{{ row[0] }} {{ row[3] }}{% endfor %}",
			wantErr: true,
		},
		{
			name:    "1-based indexing row[1] through row[3] - last is out of bounds",
			content: "{% for row in data_rows %}{{ row[1] }} {{ row[2] }} {{ row[3] }}{% endfor %}",
			wantErr: true,
		},
		{
			name:    "no index access",
			content: "{% for row in data_rows %}{{ row }}{% endfor %}",
			wantErr: false,
		},
		{
			name:    "non-row variable index access - no bounds check",
			content: "{% for x in items %}{{ x[5] }}{% endfor %}",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			block := &FencedBlockEvent{
				Type:       FencedBlockTemplate,
				DataSource: "people.csv",
				Content:    tt.content,
				Limit:      -1,
			}
			err := validateTemplateIndexBounds(ctx, block, arts, "test-agent", "user1", "sess1")
			if tt.wantErr && err == nil {
				t.Error("expected error but got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestValidateTemplateIndexBoundsMissingArtifact(t *testing.T) {
	store := newMemArtifactStore()
	ctx := context.Background()
	arts := &artifact.ScopedService{Inner: store, Scope: "namespace", Namespace: "test"}

	block := &FencedBlockEvent{
		Type:       FencedBlockTemplate,
		DataSource: "missing.csv",
		Content:    "{{ row[99] }}",
		Limit:      -1,
	}
	err := validateTemplateIndexBounds(ctx, block, arts, "test-agent", "user1", "sess1")
	if err != nil {
		t.Errorf("expected nil for missing artifact, got: %v", err)
	}
}

func TestValidateTemplateIndexBoundsNoDataSource(t *testing.T) {
	store := newMemArtifactStore()
	ctx := context.Background()
	arts := &artifact.ScopedService{Inner: store, Scope: "namespace", Namespace: "test"}

	block := &FencedBlockEvent{
		Type:    FencedBlockTemplate,
		Content: "{{ row[5] }}",
		Limit:   -1,
	}
	err := validateTemplateIndexBounds(ctx, block, arts, "test-agent", "user1", "sess1")
	if err != nil {
		t.Errorf("expected nil for no data source, got: %v", err)
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}
