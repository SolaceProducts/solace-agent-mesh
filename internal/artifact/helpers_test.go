// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package artifact

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestIsInternalArtifact(t *testing.T) {
	tests := []struct {
		filename string
		want     bool
	}{
		{"report.csv.metadata.json", true},
		{"data.json.metadata.json", true},
		{"file.converted.txt", true},
		{"project_bm25_index.zip", true},
		{"report.csv", false},
		{"data.json", false},
		{"image.png", false},
		{"metadata.json", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			got := IsInternalArtifact(tt.filename)
			if got != tt.want {
				t.Errorf("IsInternalArtifact(%q) = %v, want %v", tt.filename, got, tt.want)
			}
		})
	}
}

func TestSaveWithMetadata_Basic(t *testing.T) {
	ctx := context.Background()
	store := newMockStore()
	svc := &ScopedService{Inner: store, Scope: "agent"}

	content := []byte(`{"name": "Alice", "age": 30}`)
	ts := time.Date(2024, 2, 17, 12, 0, 0, 123000000, time.UTC)

	result, err := SaveWithMetadata(ctx, svc, "test-agent", "user1", "sess1",
		"data.json", content, "application/json",
		MetadataOpts{
			Description: "Test data",
			Timestamp:   ts,
		},
		slog.Default(),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.DataVersion != 0 {
		t.Errorf("expected data version 0, got %d", result.DataVersion)
	}

	if result.MetadataFilename != "data.json.metadata.json" {
		t.Errorf("unexpected metadata filename: %s", result.MetadataFilename)
	}
	if result.MetadataVersion != 0 {
		t.Errorf("expected metadata version 0, got %d", result.MetadataVersion)
	}

	metaLoc := svc.Resolve("test-agent", "user1", "sess1", "data.json.metadata.json")
	metaContent, metaMime, err := store.Load(ctx, metaLoc, nil)
	if err != nil {
		t.Fatalf("failed to load metadata: %v", err)
	}
	if metaMime != "application/json" {
		t.Errorf("expected metadata mime application/json, got %s", metaMime)
	}

	var meta map[string]any
	if err := json.Unmarshal(metaContent, &meta); err != nil {
		t.Fatalf("failed to parse metadata JSON: %v", err)
	}

	if meta["filename"] != "data.json" {
		t.Errorf("expected filename=data.json, got %v", meta["filename"])
	}
	if meta["mime_type"] != "application/json" {
		t.Errorf("expected mime_type=application/json, got %v", meta["mime_type"])
	}
	if meta["size_bytes"] != float64(len(content)) {
		t.Errorf("expected size_bytes=%d, got %v", len(content), meta["size_bytes"])
	}
	if meta["description"] != "Test data" {
		t.Errorf("expected description=Test data, got %v", meta["description"])
	}

	expectedTS := float64(ts.UnixMicro()) / 1e6
	if meta["timestamp_utc"] != expectedTS {
		t.Errorf("expected timestamp_utc=%v, got %v", expectedTS, meta["timestamp_utc"])
	}

	schema, ok := meta["schema"].(map[string]any)
	if !ok {
		t.Fatalf("expected schema to be map, got %T", meta["schema"])
	}
	if schema["type"] != "application/json" {
		t.Errorf("expected schema type=application/json, got %v", schema["type"])
	}
	if schema["inferred"] != true {
		t.Errorf("expected schema inferred=true, got %v", schema["inferred"])
	}
}

func TestSaveWithMetadata_SuppressSchema(t *testing.T) {
	ctx := context.Background()
	store := newMockStore()
	svc := &ScopedService{Inner: store, Scope: "agent"}

	result, err := SaveWithMetadata(ctx, svc, "agent", "u", "s",
		"file.bin", []byte{0xFF}, "application/octet-stream",
		MetadataOpts{SuppressSchema: true},
		slog.Default(),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.MetadataVersion < 0 {
		t.Fatal("metadata should have been saved")
	}

	metaLoc := svc.Resolve("agent", "u", "s", "file.bin.metadata.json")
	content, _, _ := store.Load(ctx, metaLoc, nil)
	var meta map[string]any
	if err := json.Unmarshal(content, &meta); err != nil {
		t.Fatalf("unmarshal metadata: %v", err)
	}

	if _, ok := meta["schema"]; ok {
		t.Error("expected no schema key when SuppressSchema=true")
	}
}

func TestSaveWithMetadata_CustomFields(t *testing.T) {
	ctx := context.Background()
	store := newMockStore()
	svc := &ScopedService{Inner: store, Scope: "agent"}

	result, err := SaveWithMetadata(ctx, svc, "agent", "u", "s",
		"report.csv", []byte("a,b\n1,2\n"), "text/csv",
		MetadataOpts{
			Custom: map[string]any{
				"row_count":   2,
				"data_source": "test",
			},
			Source: "unit_test",
		},
		slog.Default(),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	metaLoc := svc.Resolve("agent", "u", "s", "report.csv.metadata.json")
	content, _, _ := store.Load(ctx, metaLoc, nil)
	var meta map[string]any
	if err := json.Unmarshal(content, &meta); err != nil {
		t.Fatalf("unmarshal metadata: %v", err)
	}

	if meta["source"] != "unit_test" {
		t.Errorf("expected source=unit_test, got %v", meta["source"])
	}
	if meta["row_count"] != float64(2) {
		t.Errorf("expected row_count=2, got %v", meta["row_count"])
	}
	if meta["data_source"] != "test" {
		t.Errorf("expected data_source=test, got %v", meta["data_source"])
	}

	if result.DataVersion != 0 {
		t.Errorf("expected data version 0, got %d", result.DataVersion)
	}
}

func TestLoadCompanionMetadata(t *testing.T) {
	ctx := context.Background()
	store := newMockStore()
	svc := &ScopedService{Inner: store, Scope: "agent"}

	_, _ = SaveWithMetadata(ctx, svc, "agent", "u", "s",
		"data.json", []byte(`{"x":1}`), "application/json",
		MetadataOpts{Description: "test"},
		slog.Default(),
	)

	meta := LoadCompanionMetadata(ctx, svc, "agent", "u", "s", "data.json", nil)
	if meta == nil {
		t.Fatal("expected non-nil metadata")
	}
	if meta["filename"] != "data.json" {
		t.Errorf("expected filename=data.json, got %v", meta["filename"])
	}
	if meta["description"] != "test" {
		t.Errorf("expected description=test, got %v", meta["description"])
	}

	meta2 := LoadCompanionMetadata(ctx, svc, "agent", "u", "s", "missing.txt", nil)
	if meta2 != nil {
		t.Error("expected nil for non-existent companion")
	}
}

func TestFormatMetadataForLLM_Basic(t *testing.T) {
	meta := map[string]any{
		"filename":    "report.csv",
		"version":     3,
		"mime_type":   "text/csv",
		"size_bytes":  float64(4096),
		"description": "Sales report",
		"source":      "web_request",
		"schema": map[string]any{
			"type":     "text/csv",
			"inferred": true,
			"columns":  []any{"date", "region", "revenue"},
		},
	}

	result := FormatMetadataForLLM(meta)

	mustContain := []string{
		"--- Metadata for artifact 'report.csv' (v3) ---",
		"**Artifact Metadata:**",
		"*   **Description:** Sales report",
		"*   **Source:** web_request",
		"*   **Type:** text/csv",
		"*   **Size:** 4096 bytes",
		"*   **Schema:** text/csv (Inferred) Columns (0-indexed): [0] date, [1] region, [2] revenue",
		"--- End Metadata ---",
	}
	for _, s := range mustContain {
		if !strings.Contains(result, s) {
			t.Errorf("expected output to contain %q\ngot:\n%s", s, result)
		}
	}
}

func TestFormatMetadataForLLM_NoSchema(t *testing.T) {
	meta := map[string]any{
		"filename":   "image.png",
		"version":    0,
		"mime_type":  "image/png",
		"size_bytes": float64(1024),
	}

	result := FormatMetadataForLLM(meta)
	if !strings.Contains(result, "**Schema Type:** image/png") {
		t.Errorf("expected schema type fallback to mime_type\ngot:\n%s", result)
	}
}

func TestFormatMetadataForLLM_CustomFields(t *testing.T) {
	meta := map[string]any{
		"filename":  "data.json",
		"version":   1,
		"mime_type": "application/json",
		"url":       "https://example.com",
		"method":    "GET",
	}

	result := FormatMetadataForLLM(meta)
	if !strings.Contains(result, "**Other:**") {
		t.Errorf("expected Other section\ngot:\n%s", result)
	}
	if !strings.Contains(result, "method: GET") {
		t.Errorf("expected custom field 'method'\ngot:\n%s", result)
	}
	if !strings.Contains(result, "url: https://example.com") {
		t.Errorf("expected custom field 'url'\ngot:\n%s", result)
	}
}

type mockStore struct {
	data map[string][][]byte
	mime map[string][]string
}

func newMockStore() *mockStore {
	return &mockStore{
		data: make(map[string][][]byte),
		mime: make(map[string][]string),
	}
}

func (m *mockStore) key(loc Locator) string {
	return loc.AppName + "/" + loc.UserID + "/" + loc.SessionID + "/" + loc.Filename
}

func (m *mockStore) Save(_ context.Context, loc Locator, content []byte, mimeType string) (int, error) {
	k := m.key(loc)
	ver := len(m.data[k])
	m.data[k] = append(m.data[k], content)
	m.mime[k] = append(m.mime[k], mimeType)
	return ver, nil
}

func (m *mockStore) Load(_ context.Context, loc Locator, version *int) ([]byte, string, error) {
	k := m.key(loc)
	versions := m.data[k]
	if len(versions) == 0 {
		return nil, "", nil
	}
	v := len(versions) - 1
	if version != nil {
		v = *version
	}
	if v < 0 || v >= len(versions) {
		return nil, "", nil
	}
	return versions[v], m.mime[k][v], nil
}

func (m *mockStore) OpenReader(ctx context.Context, loc Locator, version *int) (io.ReadCloser, string, int64, error) {
	data, mime, err := m.Load(ctx, loc, version)
	if err != nil {
		return nil, "", 0, err
	}
	if data == nil {
		return nil, "", 0, nil
	}
	return io.NopCloser(bytes.NewReader(data)), mime, int64(len(data)), nil
}

func (m *mockStore) Delete(_ context.Context, _ Locator) error { return nil }

func (m *mockStore) ListKeys(_ context.Context, _ KeyScope) ([]string, error) {
	return nil, nil
}

func (m *mockStore) ListVersions(_ context.Context, _ Locator) ([]int, error) {
	return nil, nil
}

func (m *mockStore) GetVersionMetadata(_ context.Context, _ Locator, _ *int) (*VersionMetadata, error) {
	return nil, nil
}

func (m *mockStore) SaveMetadata(_ context.Context, _ Locator, _ int, _ *VersionMetadata) error {
	return nil
}
func (m *mockStore) SaveVersion(_ context.Context, _ Locator, _ int, _ []byte, _ string) error {
	return nil
}

func TestSaveWithMetadata_Tags(t *testing.T) {
	ctx := context.Background()
	store := newMockStore()
	svc := &ScopedService{Inner: store, Scope: "agent"}

	tags := []string{"__user_uploaded", "custom_tag"}
	result, err := SaveWithMetadata(ctx, svc, "agent", "u", "s",
		"photo.png", []byte{0x89, 0x50, 0x4E, 0x47}, "image/png",
		MetadataOpts{
			Description:    "User uploaded photo",
			SuppressSchema: true,
			Tags:           tags,
		},
		slog.Default(),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.DataVersion != 0 {
		t.Errorf("expected data version 0, got %d", result.DataVersion)
	}

	metaLoc := svc.Resolve("agent", "u", "s", "photo.png.metadata.json")
	metaContent, _, err := store.Load(ctx, metaLoc, nil)
	if err != nil {
		t.Fatalf("failed to load metadata: %v", err)
	}

	var meta map[string]any
	if err := json.Unmarshal(metaContent, &meta); err != nil {
		t.Fatalf("unmarshal metadata: %v", err)
	}

	rawTags, ok := meta["tags"].([]any)
	if !ok {
		t.Fatalf("expected tags to be []any, got %T (%v)", meta["tags"], meta["tags"])
	}
	if len(rawTags) != 2 {
		t.Fatalf("expected 2 tags, got %d", len(rawTags))
	}
	if rawTags[0] != "__user_uploaded" || rawTags[1] != "custom_tag" {
		t.Errorf("tags = %v, want [__user_uploaded, custom_tag]", rawTags)
	}
}

func TestSaveWithMetadata_NoTags(t *testing.T) {
	ctx := context.Background()
	store := newMockStore()
	svc := &ScopedService{Inner: store, Scope: "agent"}

	_, err := SaveWithMetadata(ctx, svc, "agent", "u", "s",
		"file.txt", []byte("hello"), "text/plain",
		MetadataOpts{SuppressSchema: true},
		slog.Default(),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	metaLoc := svc.Resolve("agent", "u", "s", "file.txt.metadata.json")
	metaContent, _, _ := store.Load(ctx, metaLoc, nil)
	var meta map[string]any
	if err := json.Unmarshal(metaContent, &meta); err != nil {
		t.Fatalf("unmarshal metadata: %v", err)
	}

	if _, ok := meta["tags"]; ok {
		t.Error("tags should be absent when not provided")
	}
}

func TestLoadCompanionMetadata_WithTags(t *testing.T) {
	ctx := context.Background()
	store := newMockStore()
	svc := &ScopedService{Inner: store, Scope: "agent"}

	_, _ = SaveWithMetadata(ctx, svc, "agent", "u", "s",
		"data.json", []byte(`{"x":1}`), "application/json",
		MetadataOpts{
			Tags:           []string{"__working"},
			SuppressSchema: true,
		},
		slog.Default(),
	)

	meta := LoadCompanionMetadata(ctx, svc, "agent", "u", "s", "data.json", nil)
	if meta == nil {
		t.Fatal("expected non-nil metadata")
	}
	rawTags, ok := meta["tags"].([]any)
	if !ok {
		t.Fatalf("expected tags to be []any, got %T", meta["tags"])
	}
	if len(rawTags) != 1 || rawTags[0] != "__working" {
		t.Errorf("tags = %v, want [__working]", rawTags)
	}
}

func TestFormatMetadataForLLM_WithTags(t *testing.T) {
	meta := map[string]any{
		"filename":  "data.json",
		"version":   1,
		"mime_type": "application/json",
		"tags":      []any{"__working", "custom"},
	}

	result := FormatMetadataForLLM(meta)
	if !strings.Contains(result, "tags:") {
		t.Errorf("expected tags in Other section\ngot:\n%s", result)
	}
}

func TestOrderedMap_MarshalJSON_PreservesOrder(t *testing.T) {
	m := newOrderedMap(4)
	m.set("z_last", "one")
	m.set("a_first", "two")
	m.set("m_middle", "three")

	got, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"z_last":"one","a_first":"two","m_middle":"three"}`
	if string(got) != want {
		t.Errorf("MarshalJSON =\n  %s\nwant\n  %s", got, want)
	}
}

func TestOrderedMap_MarshalJSON_OverwriteNoDuplicate(t *testing.T) {
	m := newOrderedMap(2)
	m.set("key", "original")
	m.set("other", "value")
	m.set("key", "updated")

	got, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"key":"updated","other":"value"}`
	if string(got) != want {
		t.Errorf("MarshalJSON after overwrite =\n  %s\nwant\n  %s", got, want)
	}
}

func TestOrderedMap_MarshalJSON_Empty(t *testing.T) {
	m := newOrderedMap(0)

	got, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "{}" {
		t.Errorf("empty orderedMap = %s, want {}", got)
	}
}

func TestOrderedMap_MarshalJSON_MixedTypes(t *testing.T) {
	m := newOrderedMap(3)
	m.set("name", "alice")
	m.set("age", 30)
	m.set("active", true)

	got, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"name":"alice","age":30,"active":true}`
	if string(got) != want {
		t.Errorf("MarshalJSON mixed types =\n  %s\nwant\n  %s", got, want)
	}
}

func TestSaveUpload(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	scope := UploadScope{AgentName: "agentA", UserID: "u1", SessionID: "s1"}

	t.Run("saves content and returns the canonical URI", func(t *testing.T) {
		store := newMockStore()
		svc := &ScopedService{Inner: store, Scope: "agent"}

		uri, version, err := SaveUpload(t.Context(), svc, scope,
			Upload{Filename: "my report.txt", MIMEType: "text/plain", Content: []byte("hello")},
			MetadataOpts{Source: "test"}, logger)
		if err != nil {
			t.Fatalf("SaveUpload: %v", err)
		}
		if version != 0 {
			t.Errorf("version = %d, want 0", version)
		}
		if want := "artifact://agentA/u1/s1/my%20report.txt?version=0"; uri != want {
			t.Errorf("uri = %q, want %q", uri, want)
		}

		loc := svc.Resolve(scope.AgentName, scope.UserID, scope.SessionID, "my report.txt")
		content, mime, err := store.Load(t.Context(), loc, &version)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if string(content) != "hello" {
			t.Errorf("content = %q, want %q", content, "hello")
		}
		if mime != "text/plain" {
			t.Errorf("mime = %q, want text/plain", mime)
		}
	})

	t.Run("second save bumps the version", func(t *testing.T) {
		store := newMockStore()
		svc := &ScopedService{Inner: store, Scope: "agent"}

		if _, _, err := SaveUpload(t.Context(), svc, scope,
			Upload{Filename: "f.txt", MIMEType: "text/plain", Content: []byte("v0")},
			MetadataOpts{}, logger); err != nil {
			t.Fatalf("first SaveUpload: %v", err)
		}
		uri, version, err := SaveUpload(t.Context(), svc, scope,
			Upload{Filename: "f.txt", MIMEType: "text/plain", Content: []byte("v1")},
			MetadataOpts{}, logger)
		if err != nil {
			t.Fatalf("second SaveUpload: %v", err)
		}
		if version != 1 {
			t.Errorf("version = %d, want 1", version)
		}
		if want := "artifact://agentA/u1/s1/f.txt?version=1"; uri != want {
			t.Errorf("uri = %q, want %q", uri, want)
		}
	})

	t.Run("namespace scope overrides the agent name", func(t *testing.T) {
		store := newMockStore()
		svc := &ScopedService{Inner: store, Scope: "namespace", Namespace: "ns1"}

		uri, _, err := SaveUpload(t.Context(), svc,
			UploadScope{AgentName: "", UserID: "u1", SessionID: "s1"},
			Upload{Filename: "f.txt", MIMEType: "text/plain", Content: []byte("x")},
			MetadataOpts{}, logger)
		if err != nil {
			t.Fatalf("SaveUpload: %v", err)
		}
		if want := "artifact://ns1/u1/s1/f.txt?version=0"; uri != want {
			t.Errorf("uri = %q, want %q", uri, want)
		}
	})

	t.Run("DeriveMIME replaces a generic type", func(t *testing.T) {
		store := newMockStore()
		svc := &ScopedService{Inner: store, Scope: "agent"}

		_, version, err := SaveUpload(t.Context(), svc, scope,
			Upload{
				Filename:   "data.csv",
				MIMEType:   "application/octet-stream",
				Content:    []byte("a,b\n1,2\n"),
				DeriveMIME: true,
			},
			MetadataOpts{}, logger)
		if err != nil {
			t.Fatalf("SaveUpload: %v", err)
		}
		loc := svc.Resolve(scope.AgentName, scope.UserID, scope.SessionID, "data.csv")
		_, mime, err := store.Load(t.Context(), loc, &version)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if mime == "application/octet-stream" {
			t.Error("DeriveMIME left the generic MIME type in place")
		}
	})

	t.Run("nil service returns ErrNoArtifactService", func(t *testing.T) {
		_, _, err := SaveUpload(t.Context(), nil, scope,
			Upload{Filename: "f.txt", Content: []byte("x")}, MetadataOpts{}, logger)
		if !errors.Is(err, ErrNoArtifactService) {
			t.Errorf("err = %v, want ErrNoArtifactService", err)
		}
	})

	t.Run("an unroutable namespace writes nothing", func(t *testing.T) {
		store := newMockStore()
		svc := &ScopedService{Inner: store, Scope: "namespace", Namespace: "myorg/prod"}

		_, _, err := SaveUpload(t.Context(), svc, scope,
			Upload{Filename: "f.txt", MIMEType: "text/plain", Content: []byte("hello")},
			MetadataOpts{}, logger)
		if !errors.Is(err, ErrUnroutableAppName) {
			t.Fatalf("err = %v, want ErrUnroutableAppName", err)
		}
		if len(store.data) != 0 {
			t.Errorf("store holds %d keys, want 0 — the rejected upload persisted bytes", len(store.data))
		}
	})

	t.Run("unsafe and reserved filenames return ErrUnsafeFilename", func(t *testing.T) {
		svc := &ScopedService{Inner: newMockStore(), Scope: "agent"}
		for _, filename := range []string{
			"../etc/passwd",
			"a/b.txt",
			"",
			"report.txt" + MetadataSuffix,
			"report.txt" + ConvertedTextSuffix,
			BM25IndexFilename,
		} {
			t.Run(filename, func(t *testing.T) {
				_, _, err := SaveUpload(t.Context(), svc, scope,
					Upload{Filename: filename, Content: []byte("x")}, MetadataOpts{}, logger)
				if !errors.Is(err, ErrUnsafeFilename) {
					t.Errorf("err = %v, want ErrUnsafeFilename", err)
				}
			})
		}
	})
}
