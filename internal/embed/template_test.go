// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package embed

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

func newTestArtifactSvc(store *memArtifactStore) *artifact.ScopedService {
	return &artifact.ScopedService{
		Inner:     store,
		Scope:     "namespace",
		Namespace: "test",
	}
}

func TestParseTemplateBlockParams_Basic(t *testing.T) {
	params := parseTemplateBlockParams(`data="sales.csv" jsonpath="$.items" limit="25"`)
	if params["data"] != "sales.csv" {
		t.Errorf("data = %q, want %q", params["data"], "sales.csv")
	}
	if params["jsonpath"] != "$.items" {
		t.Errorf("jsonpath = %q, want %q", params["jsonpath"], "$.items")
	}
	if params["limit"] != "25" {
		t.Errorf("limit = %q, want %q", params["limit"], "25")
	}
}

func TestParseTemplateBlockParams_DataOnly(t *testing.T) {
	params := parseTemplateBlockParams(`data="report.json"`)
	if params["data"] != "report.json" {
		t.Errorf("data = %q, want %q", params["data"], "report.json")
	}
	if len(params) != 1 {
		t.Errorf("expected 1 param, got %d", len(params))
	}
}

func TestParseTemplateBlockParams_Empty(t *testing.T) {
	params := parseTemplateBlockParams("")
	if len(params) != 0 {
		t.Errorf("expected 0 params, got %d", len(params))
	}
}

func TestTemplateBlockRegex_BasicMatch(t *testing.T) {
	text := `«««template_liquid: data="file.csv"
| Name |
{% for row in data_rows %}| {{ row[0] }} |
{% endfor %}»»»`

	matches := templateBlockRegex.FindAllStringSubmatch(text, -1)
	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(matches))
	}
	if !strings.Contains(matches[0][1], `data="file.csv"`) {
		t.Errorf("params = %q", matches[0][1])
	}
	if !strings.Contains(matches[0][2], "for row in data_rows") {
		t.Errorf("content = %q", matches[0][2])
	}
}

func TestTemplateBlockRegex_WithoutLiquidSuffix(t *testing.T) {
	text := `«««template: data="file.json"
{{ name }}»»»`

	matches := templateBlockRegex.FindAllStringSubmatch(text, -1)
	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(matches))
	}
}

func TestTemplateBlockRegex_MultipleBlocks(t *testing.T) {
	text := `Before «««template_liquid: data="a.json"
{{ x }}»»» Middle «««template_liquid: data="b.json"
{{ y }}»»» After`

	matches := templateBlockRegex.FindAllStringSubmatch(text, -1)
	if len(matches) != 2 {
		t.Fatalf("expected 2 matches, got %d", len(matches))
	}
}

func TestTemplateBlockRegex_NoMatch(t *testing.T) {
	text := "No template blocks here."
	matches := templateBlockRegex.FindAllStringSubmatch(text, -1)
	if len(matches) != 0 {
		t.Fatalf("expected 0 matches, got %d", len(matches))
	}
}

func TestResolveTemplateBlocks_NoBlocks(t *testing.T) {
	result, err := ResolveTemplateBlocks(
		context.Background(), "No blocks here", nil, "", "", "", nil, slog.Default(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if result != "No blocks here" {
		t.Errorf("got %q", result)
	}
}

func TestResolveTemplateBlocks_CSVTemplate(t *testing.T) {
	store := newMemArtifactStore()
	ctx := context.Background()
	loc := artifact.Locator{AppName: "test", UserID: "user1", SessionID: "sess1", Filename: "data.csv"}
	if _, err := store.Save(ctx, loc, []byte("Name,Age\nAlice,30\nBob,25"), "text/csv"); err != nil {
		t.Fatal(err)
	}

	svc := newTestArtifactSvc(store)

	text := `Before «««template_liquid: data="data.csv"
{% for row in data_rows %}{{ row[0] }}: {{ row[1] }}
{% endfor %}»»» After`

	result, err := ResolveTemplateBlocks(ctx, text, svc, "test", "user1", "sess1", nil, slog.Default())
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(result, "Before") || !strings.Contains(result, "After") {
		t.Errorf("surrounding text missing: %q", result)
	}
	if !strings.Contains(result, "Alice: 30") {
		t.Errorf("Alice data missing: %q", result)
	}
	if !strings.Contains(result, "Bob: 25") {
		t.Errorf("Bob data missing: %q", result)
	}
}

func TestResolveTemplateBlocks_JSONTemplate(t *testing.T) {
	store := newMemArtifactStore()
	ctx := context.Background()
	loc := artifact.Locator{AppName: "test", UserID: "user1", SessionID: "sess1", Filename: "data.json"}
	if _, err := store.Save(ctx, loc, []byte(`[{"name":"Alice","score":95},{"name":"Bob","score":87}]`), "application/json"); err != nil {
		t.Fatal(err)
	}

	svc := newTestArtifactSvc(store)

	text := `«««template_liquid: data="data.json"
{% for item in items %}{{ item.name }}: {{ item.score }}
{% endfor %}»»»`

	result, err := ResolveTemplateBlocks(ctx, text, svc, "test", "user1", "sess1", nil, slog.Default())
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(result, "Alice: 95") || !strings.Contains(result, "Bob: 87") {
		t.Errorf("expected data in output: %q", result)
	}
}

func TestResolveTemplateBlocks_YAMLTemplate(t *testing.T) {
	store := newMemArtifactStore()
	ctx := context.Background()
	loc := artifact.Locator{AppName: "test", UserID: "user1", SessionID: "sess1", Filename: "config.yaml"}
	if _, err := store.Save(ctx, loc, []byte("name: TestApp\nversion: \"1.0\""), "application/yaml"); err != nil {
		t.Fatal(err)
	}

	svc := newTestArtifactSvc(store)

	text := `«««template_liquid: data="config.yaml"
App: {{ name }} v{{ version }}»»»`

	result, err := ResolveTemplateBlocks(ctx, text, svc, "test", "user1", "sess1", nil, slog.Default())
	if err != nil {
		t.Fatal(err)
	}

	if result != "App: TestApp v1.0" {
		t.Errorf("got %q, want %q", result, "App: TestApp v1.0")
	}
}

func TestResolveTemplateBlocks_WithLimit(t *testing.T) {
	store := newMemArtifactStore()
	ctx := context.Background()
	loc := artifact.Locator{AppName: "test", UserID: "user1", SessionID: "sess1", Filename: "data.json"}
	if _, err := store.Save(ctx, loc, []byte(`[{"n":"a"},{"n":"b"},{"n":"c"}]`), "application/json"); err != nil {
		t.Fatal(err)
	}

	svc := newTestArtifactSvc(store)

	text := `«««template_liquid: data="data.json" limit="2"
{% for item in items %}{{ item.n }}
{% endfor %}»»»`

	result, err := ResolveTemplateBlocks(ctx, text, svc, "test", "user1", "sess1", nil, slog.Default())
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(result, "c") {
		t.Errorf("limit=2 should exclude 'c': %q", result)
	}
	if !strings.Contains(result, "a") || !strings.Contains(result, "b") {
		t.Errorf("limit=2 should include 'a' and 'b': %q", result)
	}
}

func TestResolveTemplateBlocks_MissingDataParam(t *testing.T) {
	store := newMemArtifactStore()
	svc := newTestArtifactSvc(store)

	text := `«««template_liquid: jsonpath="$.items"
some template»»»`

	result, err := ResolveTemplateBlocks(context.Background(), text, svc, "test", "user1", "sess1", nil, slog.Default())
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(result, "[Template Error:") {
		t.Errorf("expected template error, got %q", result)
	}
}

func TestResolveTemplateBlocks_ArtifactNotFound(t *testing.T) {
	store := newMemArtifactStore()
	svc := newTestArtifactSvc(store)

	text := `«««template_liquid: data="missing.csv"
some template»»»`

	result, err := ResolveTemplateBlocks(context.Background(), text, svc, "test", "user1", "sess1", nil, slog.Default())
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(result, "[Template Error:") || !strings.Contains(result, "not found") {
		t.Errorf("expected not found error, got %q", result)
	}
}

func TestResolveTemplateBlocks_NoArtifactService(t *testing.T) {
	text := `«««template_liquid: data="file.csv"
template»»»`

	result, err := ResolveTemplateBlocks(context.Background(), text, nil, "test", "user1", "sess1", nil, slog.Default())
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(result, "[Template Error: No artifact service available]") {
		t.Errorf("expected no artifact service error, got %q", result)
	}
}

func TestResolveTemplateBlocks_ErrorTextNamesOnlyTheArtifact(t *testing.T) {
	const secret = "123-45-6789"
	tests := []struct {
		name      string
		content   string
		mimeType  string
		params    string
		template  string
		wantText  string
		wantKind  string
		wantNoLog []string
	}{
		{
			name:      "unparseable data",
			content:   `{"ssn":"` + secret + `"`,
			mimeType:  "application/json",
			params:    `data="d.json"`,
			template:  "x",
			wantText:  "[Template Error: Data artifact 'd.json' could not be parsed]",
			wantKind:  "data_parse",
			wantNoLog: []string{"unexpected end"},
		},
		{
			name:     "jsonpath that matches nothing",
			content:  `{"ssn":"` + secret + `"}`,
			mimeType: "application/json",
			params:   `data="d.json" jsonpath="$.missing"`,
			template: "x",
			wantText: "[Template Error: The jsonpath filter on data artifact 'd.json' failed]",
			wantKind: "jsonpath",
		},
		{
			name:     "liquid conversion failure",
			content:  `[{"ssn":"` + secret + `"}]`,
			mimeType: "application/json",
			params:   `data="d.json"`,
			template: "{% for item in items %}{{ item.ssn | divided_by: 2 }}{% endfor %}",
			wantText: "[Template Error: Rendering against 'd.json' failed]",
			wantKind: "render",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newMemArtifactStore()
			saveArtifact(store, "d.json", []byte(tt.content), tt.mimeType)
			var logBuf bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&logBuf, nil))
			text := "«««template_liquid: " + tt.params + "\n" + tt.template + "»»»"

			result, err := ResolveTemplateBlocks(context.Background(), text, newTestArtifactSvc(store), "test", "user1", "sess1", nil, logger)
			if err != nil {
				t.Fatal(err)
			}
			if result != tt.wantText {
				t.Errorf("result = %q, want %q", result, tt.wantText)
			}
			logged := logBuf.String()
			if !strings.Contains(logged, "errorKind="+tt.wantKind) || !strings.Contains(logged, "dataArtifact=d.json") {
				t.Errorf("log = %q, want errorKind=%s and dataArtifact=d.json", logged, tt.wantKind)
			}
			for _, leaked := range append(tt.wantNoLog, secret) {
				if strings.Contains(logged, leaked) || strings.Contains(result, leaked) {
					t.Errorf("result %q or log %q leaks %q", result, logged, leaked)
				}
			}
		})
	}
}

func TestResolveTemplateBlocks_WithRecursiveResolve(t *testing.T) {
	store := newMemArtifactStore()
	ctx := context.Background()
	loc := artifact.Locator{AppName: "test", UserID: "user1", SessionID: "sess1", Filename: "data.json"}
	if _, err := store.Save(ctx, loc, []byte(`{"greeting":"hello"}`), "application/json"); err != nil {
		t.Fatal(err)
	}

	svc := newTestArtifactSvc(store)

	resolveEmbeds := func(_ context.Context, text string) (string, error) {
		return strings.ReplaceAll(text, "EMBED_MARKER", "RESOLVED"), nil
	}

	text := `«««template_liquid: data="data.json"
{{ greeting }} EMBED_MARKER»»»`

	result, err := ResolveTemplateBlocks(ctx, text, svc, "test", "user1", "sess1", resolveEmbeds, slog.Default())
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(result, "hello RESOLVED") {
		t.Errorf("recursive resolution not applied: %q", result)
	}
}

func TestResolveTemplateBlocks_MultipleBlocks(t *testing.T) {
	store := newMemArtifactStore()
	ctx := context.Background()
	loc1 := artifact.Locator{AppName: "test", UserID: "user1", SessionID: "sess1", Filename: "a.json"}
	if _, err := store.Save(ctx, loc1, []byte(`{"val":"AAA"}`), "application/json"); err != nil {
		t.Fatal(err)
	}
	loc2 := artifact.Locator{AppName: "test", UserID: "user1", SessionID: "sess1", Filename: "b.json"}
	if _, err := store.Save(ctx, loc2, []byte(`{"val":"BBB"}`), "application/json"); err != nil {
		t.Fatal(err)
	}

	svc := newTestArtifactSvc(store)

	text := `Start «««template_liquid: data="a.json"
{{ val }}»»» Middle «««template_liquid: data="b.json"
{{ val }}»»» End`

	result, err := ResolveTemplateBlocks(ctx, text, svc, "test", "user1", "sess1", nil, slog.Default())
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(result, "Start") || !strings.Contains(result, "Middle") || !strings.Contains(result, "End") {
		t.Errorf("surrounding text missing: %q", result)
	}
	if !strings.Contains(result, "AAA") || !strings.Contains(result, "BBB") {
		t.Errorf("template values missing: %q", result)
	}
}

func TestResolveTemplateBlocks_ComplexLiquid(t *testing.T) {
	store := newMemArtifactStore()
	ctx := context.Background()
	loc := artifact.Locator{AppName: "test", UserID: "user1", SessionID: "sess1", Filename: "data.json"}
	if _, err := store.Save(ctx, loc, []byte(`[{"name":"Alice","active":true},{"name":"Bob","active":false},{"name":"Charlie","active":true}]`), "application/json"); err != nil {
		t.Fatal(err)
	}

	svc := newTestArtifactSvc(store)

	text := `«««template_liquid: data="data.json"
{% for item in items %}{% if item.active %}Active: {{ item.name }}
{% endif %}{% endfor %}»»»`

	result, err := ResolveTemplateBlocks(ctx, text, svc, "test", "user1", "sess1", nil, slog.Default())
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(result, "Active: Alice") || !strings.Contains(result, "Active: Charlie") {
		t.Errorf("active users missing: %q", result)
	}
	if strings.Contains(result, "Active: Bob") {
		t.Errorf("Bob should not be active: %q", result)
	}
}

func TestResolveTemplateBlocks_LiquidInDataNotInterpreted(t *testing.T) {
	store := newMemArtifactStore()
	ctx := context.Background()
	loc := artifact.Locator{AppName: "test", UserID: "user1", SessionID: "sess1", Filename: "data.json"}
	data := `{"user_input": "{{ 'INJECTED' | upcase }}", "safe": "hello"}`
	if _, err := store.Save(ctx, loc, []byte(data), "application/json"); err != nil {
		t.Fatal(err)
	}

	svc := newTestArtifactSvc(store)

	text := `«««template_liquid: data="data.json"
User said: {{ user_input }}, safe={{ safe }}»»»`

	result, err := ResolveTemplateBlocks(ctx, text, svc, "test", "user1", "sess1", nil, slog.Default())
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(result, "{{ 'INJECTED' | upcase }}") {
		t.Errorf("Liquid syntax in data value should appear literally, got: %q", result)
	}
	if !strings.Contains(result, "safe=hello") {
		t.Errorf("safe value missing: %q", result)
	}
}
