// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
)

const (
	testAgentName = "agent-x"
	testUserID    = "user-x"
	testSessionID = "sess-x"
	testToolName  = "my_tool"
)

func newTestScopedSvc() (*artifact.ScopedService, *memory.Store) {
	store := memory.New()
	return &artifact.ScopedService{Inner: store, Scope: "namespace", Namespace: "ns"}, store
}

func newDiscardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func loadSpilledTags(t *testing.T, store *memory.Store, filename string) []string {
	t.Helper()
	metaBytes, _, err := store.Load(t.Context(), artifact.Locator{
		AppName: "ns", UserID: testUserID, SessionID: testSessionID,
		Filename: filename + artifact.MetadataSuffix,
	}, nil)
	if err != nil {
		t.Fatalf("load companion metadata for %q: %v", filename, err)
	}
	var meta map[string]any
	if err := json.Unmarshal(metaBytes, &meta); err != nil {
		t.Fatalf("companion metadata for %q not valid JSON: %v", filename, err)
	}
	rawTags, _ := meta["tags"].([]any)
	tags := make([]string, 0, len(rawTags))
	for _, tg := range rawTags {
		if s, ok := tg.(string); ok {
			tags = append(tags, s)
		}
	}
	return tags
}

func TestSpillOversizedResult_TagsArtifactWorking(t *testing.T) {
	t.Run("data map spill", func(t *testing.T) {
		svc, store := newTestScopedSvc()
		envelope := map[string]any{
			"data": map[string]any{"summary": strings.Repeat("Z", 5000)},
		}
		spillOversizedResult(t.Context(), envelope, svc, testAgentName, testUserID, testSessionID, testToolName, 1024, newDiscardLogger())

		objs := envelope["data_objects"].([]any)
		filename := objs[0].(map[string]any)["artifact_ref"].(map[string]any)["filename"].(string)
		tags := loadSpilledTags(t, store, filename)
		if !slices.Contains(tags, a2a.ArtifactTagWorking) {
			t.Errorf("spilled data-map artifact %q tags = %v, want to contain %q", filename, tags, a2a.ArtifactTagWorking)
		}
	})

	t.Run("data_objects spill", func(t *testing.T) {
		svc, store := newTestScopedSvc()
		envelope := map[string]any{
			"data_objects": []any{
				map[string]any{
					"name":      "big.txt",
					"mime_type": "text/plain",
					"content":   strings.Repeat("L", 4096),
					"is_binary": false,
				},
			},
		}
		spillOversizedResult(t.Context(), envelope, svc, testAgentName, testUserID, testSessionID, testToolName, 512, newDiscardLogger())

		entry := envelope["data_objects"].([]any)[0].(map[string]any)
		filename := entry["artifact_ref"].(map[string]any)["filename"].(string)
		tags := loadSpilledTags(t, store, filename)
		if !slices.Contains(tags, a2a.ArtifactTagWorking) {
			t.Errorf("spilled data_object artifact %q tags = %v, want to contain %q", filename, tags, a2a.ArtifactTagWorking)
		}
	})
}

func TestSpillOversizedResult_NoOpWhenDisabled(t *testing.T) {
	svc, _ := newTestScopedSvc()
	envelope := map[string]any{
		"data": map[string]any{"x": strings.Repeat("a", 10000)},
	}
	spillOversizedResult(context.Background(), envelope, svc, testAgentName, testUserID, testSessionID, testToolName, 0, newDiscardLogger())
	if _, ok := envelope["data"]; !ok {
		t.Fatalf("data key removed when spill disabled")
	}
}

func TestSpillOversizedResult_DataUnderCapUnchanged(t *testing.T) {
	svc, _ := newTestScopedSvc()
	envelope := map[string]any{
		"data": map[string]any{"summary": "all good"},
	}
	spillOversizedResult(context.Background(), envelope, svc, testAgentName, testUserID, testSessionID, testToolName, 8192, newDiscardLogger())
	if _, ok := envelope["data"]; !ok {
		t.Fatalf("small data was spilled")
	}
	if _, ok := envelope["data_objects"]; ok {
		t.Fatalf("data_objects synthesized for small data")
	}
}

func TestSpillOversizedResult_DataMapOverCapSpillsAsJSON(t *testing.T) {
	svc, store := newTestScopedSvc()
	blob := strings.Repeat("Z", 5000)
	envelope := map[string]any{
		"data": map[string]any{"summary": blob},
	}
	spillOversizedResult(context.Background(), envelope, svc, testAgentName, testUserID, testSessionID, testToolName, 1024, newDiscardLogger())

	if _, ok := envelope["data"]; ok {
		t.Fatalf("data not removed after spill")
	}
	objs, ok := envelope["data_objects"].([]any)
	if !ok || len(objs) != 1 {
		t.Fatalf("expected 1 data_objects entry, got %v", envelope["data_objects"])
	}
	entry := objs[0].(map[string]any)
	if entry["disposition"] != "artifact" {
		t.Errorf("disposition = %v, want artifact", entry["disposition"])
	}
	if entry["content"] != nil {
		t.Errorf("content not cleared")
	}
	ref, ok := entry["artifact_ref"].(map[string]any)
	if !ok {
		t.Fatalf("artifact_ref missing")
	}
	filename, _ := ref["filename"].(string)
	if !strings.HasPrefix(filename, "my_tool_result_") || !strings.HasSuffix(filename, ".json") {
		t.Errorf("unexpected filename: %q", filename)
	}
	if got, _ := entry["mime_type"].(string); got != "application/json" {
		t.Errorf("spilled data-map mime_type = %q, want application/json", got)
	}
	meta, _ := entry["metadata"].(map[string]any)
	if got, _ := meta["spilled_by_str"].(bool); !got {
		t.Errorf("spilled_by_str not set in metadata")
	}
	if got, _ := meta["reason"].(string); got != spillReason {
		t.Errorf("reason = %q, want %q", got, spillReason)
	}
	msg, _ := envelope["message"].(string)
	if !strings.Contains(msg, "Max result size exceeded") || !strings.Contains(msg, filename) {
		t.Errorf("message missing spill notice / filename: %q", msg)
	}
	for _, want := range []string{"query_data_with_sql", "transform_data_with_jmespath"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message no longer names %s; the model loses its route to the data: %q", want, msg)
		}
	}
	for _, banned := range []string{"artifact_grep", "search_artifacts"} {
		if strings.Contains(msg, banned) {
			t.Errorf("message names %s, which cannot safely read single-line JSON: %q", banned, msg)
		}
	}

	loaded, _, err := store.Load(context.Background(), artifact.Locator{
		AppName: "ns", UserID: testUserID, SessionID: testSessionID, Filename: filename,
	}, nil)
	if err != nil || len(loaded) == 0 {
		t.Fatalf("artifact not stored: %v / %d bytes", err, len(loaded))
	}
	var roundTrip map[string]any
	if err := json.Unmarshal(loaded, &roundTrip); err != nil {
		t.Fatalf("stored payload not valid JSON: %v", err)
	}
	if roundTrip["summary"] != blob {
		t.Errorf("stored payload did not round-trip")
	}
}

func TestSpillOversizedResult_SingleOversizedDataObject(t *testing.T) {
	svc, _ := newTestScopedSvc()
	bigText := strings.Repeat("L", 4096)
	envelope := map[string]any{
		"data_objects": []any{
			map[string]any{
				"name":        "small.txt",
				"mime_type":   "text/plain",
				"disposition": "auto",
				"content":     "tiny",
				"is_binary":   false,
			},
			map[string]any{
				"name":        "big.txt",
				"mime_type":   "text/plain",
				"disposition": "auto",
				"content":     bigText,
				"is_binary":   false,
			},
		},
	}
	spillOversizedResult(context.Background(), envelope, svc, testAgentName, testUserID, testSessionID, testToolName, 512, newDiscardLogger())

	objs := envelope["data_objects"].([]any)
	if len(objs) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(objs))
	}
	small := objs[0].(map[string]any)
	if small["content"] != "tiny" {
		t.Errorf("small entry mutated: %v", small["content"])
	}
	if _, has := small["artifact_ref"]; has {
		t.Errorf("small entry got artifact_ref")
	}
	big := objs[1].(map[string]any)
	if big["content"] != nil {
		t.Errorf("big entry content not cleared")
	}
	if big["disposition"] != "artifact" {
		t.Errorf("big entry disposition = %v", big["disposition"])
	}
	ref, ok := big["artifact_ref"].(map[string]any)
	if !ok || ref["filename"] != "big.txt" {
		t.Errorf("expected artifact_ref with filename big.txt, got %v", ref)
	}
	if sz, _ := ref["size_bytes"].(int); sz != len(bigText) {
		t.Errorf("size_bytes = %v, want %d", ref["size_bytes"], len(bigText))
	}
}

func TestSpillOversizedResult_BinaryMeasuredAfterDecode(t *testing.T) {
	svc, _ := newTestScopedSvc()
	raw := make([]byte, 800)
	for i := range raw {
		raw[i] = byte(i % 256)
	}
	envelope := map[string]any{
		"data_objects": []any{
			map[string]any{
				"name":      "blob.bin",
				"mime_type": "application/octet-stream",
				"content":   base64.StdEncoding.EncodeToString(raw),
				"is_binary": true,
			},
		},
	}
	spillOversizedResult(context.Background(), envelope, svc, testAgentName, testUserID, testSessionID, testToolName, 900, newDiscardLogger())
	entry := envelope["data_objects"].([]any)[0].(map[string]any)
	if _, has := entry["artifact_ref"]; has {
		t.Fatalf("entry was spilled despite decoded length being under cap")
	}

	raw2 := make([]byte, 2048)
	for i := range raw2 {
		raw2[i] = byte((i * 7) % 256)
	}
	svc2, store2 := newTestScopedSvc()
	envelope2 := map[string]any{
		"data_objects": []any{
			map[string]any{
				"name":      "big.bin",
				"mime_type": "application/octet-stream",
				"content":   base64.StdEncoding.EncodeToString(raw2),
				"is_binary": true,
			},
		},
	}
	spillOversizedResult(context.Background(), envelope2, svc2, testAgentName, testUserID, testSessionID, testToolName, 900, newDiscardLogger())
	entry2 := envelope2["data_objects"].([]any)[0].(map[string]any)
	ref, ok := entry2["artifact_ref"].(map[string]any)
	if !ok {
		t.Fatalf("big binary not spilled: %v", entry2)
	}
	if entry2["is_binary"] != false {
		t.Errorf("is_binary not reset on spill")
	}
	stored, _, err := store2.Load(context.Background(), artifact.Locator{
		AppName: "ns", UserID: testUserID, SessionID: testSessionID, Filename: ref["filename"].(string),
	}, nil)
	if err != nil {
		t.Fatalf("load stored: %v", err)
	}
	if string(stored) != string(raw2) {
		t.Errorf("stored bytes do not match decoded original (%d vs %d)", len(stored), len(raw2))
	}
}

func TestSpillOversizedResult_PreservesExistingArtifactRef(t *testing.T) {
	svc, _ := newTestScopedSvc()
	envelope := map[string]any{
		"data_objects": []any{
			map[string]any{
				"name":         "already.txt",
				"disposition":  "artifact",
				"content":      nil,
				"artifact_ref": map[string]any{"filename": "already.txt", "version": float64(3), "size_bytes": float64(99999)},
			},
		},
	}
	spillOversizedResult(context.Background(), envelope, svc, testAgentName, testUserID, testSessionID, testToolName, 100, newDiscardLogger())
	entry := envelope["data_objects"].([]any)[0].(map[string]any)
	ref := entry["artifact_ref"].(map[string]any)
	if ref["version"].(float64) != 3 {
		t.Errorf("existing artifact_ref was overwritten: %v", ref)
	}
}

func TestSpillOversizedResult_FilenameSanitization(t *testing.T) {
	svc, store := newTestScopedSvc()
	envelope := map[string]any{
		"data_objects": []any{
			map[string]any{
				"name":      "../../etc/passwd",
				"mime_type": "text/plain",
				"content":   strings.Repeat("x", 2048),
				"is_binary": false,
			},
		},
	}
	spillOversizedResult(context.Background(), envelope, svc, testAgentName, testUserID, testSessionID, testToolName, 512, newDiscardLogger())
	entry := envelope["data_objects"].([]any)[0].(map[string]any)
	ref := entry["artifact_ref"].(map[string]any)
	filename := ref["filename"].(string)
	if strings.ContainsAny(filename, "/\\:") || strings.HasPrefix(filename, ".") {
		t.Fatalf("unsafe filename was not sanitized: %q", filename)
	}
	stored, _, err := store.Load(context.Background(), artifact.Locator{
		AppName: "ns", UserID: testUserID, SessionID: testSessionID, Filename: filename,
	}, nil)
	if err != nil || len(stored) == 0 {
		t.Fatalf("expected artifact at sanitized name %q: %v", filename, err)
	}
}

func TestSpillOversizedResult_FallbackFilenameExtMatchesMIME(t *testing.T) {
	svc, _ := newTestScopedSvc()
	envelope := map[string]any{
		"data_objects": []any{
			map[string]any{
				"name":      "",
				"mime_type": "application/json",
				"content":   `{"rows":[` + strings.Repeat(`{"a":1},`, 512) + `{"a":1}]}`,
				"is_binary": false,
			},
		},
	}
	spillOversizedResult(context.Background(), envelope, svc, testAgentName, testUserID, testSessionID, testToolName, 512, newDiscardLogger())

	entry := envelope["data_objects"].([]any)[0].(map[string]any)
	ref, ok := entry["artifact_ref"].(map[string]any)
	if !ok {
		t.Fatalf("expected data_object to spill, got %v", entry)
	}
	filename, _ := ref["filename"].(string)
	if !strings.HasSuffix(filename, ".json") {
		t.Errorf("fallback filename for application/json should end in .json, got %q", filename)
	}
	if got, _ := entry["mime_type"].(string); got != "application/json" {
		t.Errorf("spilled mime_type = %q, want application/json", got)
	}
}

func TestSpillOversizedResult_DefaultCapBoundary(t *testing.T) {
	const capBytes = 102400
	for _, size := range []int{8192, 32768, 65536, 102400} {
		svc, _ := newTestScopedSvc()
		envelope := map[string]any{
			"data_objects": []any{
				map[string]any{
					"name":        "result.txt",
					"mime_type":   "text/plain",
					"content":     strings.Repeat("a", size),
					"is_binary":   false,
					"disposition": "auto",
				},
			},
		}
		spillOversizedResult(context.Background(), envelope, svc, testAgentName, testUserID, testSessionID, testToolName, capBytes, newDiscardLogger())
		entry := envelope["data_objects"].([]any)[0].(map[string]any)
		if _, spilled := entry["artifact_ref"]; spilled {
			t.Errorf("size %d (<= %d) must not spill, but it did", size, capBytes)
		}
		if entry["content"] == nil {
			t.Errorf("size %d: content was cleared but should pass through", size)
		}
		if got, _ := entry["disposition"].(string); got != "auto" {
			t.Errorf("size %d: disposition mutated to %q, want auto (STR makes no inline decision)", size, got)
		}
	}

	svc, _ := newTestScopedSvc()
	envelope := map[string]any{
		"data_objects": []any{
			map[string]any{
				"name":        "huge.txt",
				"mime_type":   "text/plain",
				"content":     strings.Repeat("a", capBytes+1),
				"is_binary":   false,
				"disposition": "auto",
			},
		},
	}
	spillOversizedResult(context.Background(), envelope, svc, testAgentName, testUserID, testSessionID, testToolName, capBytes, newDiscardLogger())
	entry := envelope["data_objects"].([]any)[0].(map[string]any)
	if _, spilled := entry["artifact_ref"]; !spilled {
		t.Errorf("size %d (> %d) must spill to an artifact, but it did not", capBytes+1, capBytes)
	}
}

func TestExtForMIME(t *testing.T) {
	cases := map[string]string{
		"application/json":         ".json",
		"text/csv":                 ".csv",
		"application/x-yaml":       ".yaml",
		"application/yaml":         ".yaml",
		"text/yaml":                ".yaml",
		"application/xml":          ".xml",
		"text/xml":                 ".xml",
		"text/html":                ".html",
		"text/markdown":            ".md",
		"application/octet-stream": ".bin",
		"text/plain":               ".txt",
		"":                         ".txt",
		"application/x-unknown":    ".txt",
	}
	for mime, want := range cases {
		if got := extForMIME(mime); got != want {
			t.Errorf("extForMIME(%q) = %q, want %q", mime, got, want)
		}
	}
}

func TestSpillFilename_ExtensionHandling(t *testing.T) {
	content := []byte("payload")
	if got := spillFilename("my_tool", content, ".json"); !strings.HasSuffix(got, ".json") {
		t.Errorf("explicit ext not honored: %q", got)
	}
	if got := spillFilename("my_tool", content, ""); !strings.HasSuffix(got, ".txt") {
		t.Errorf("empty ext should default to .txt, got %q", got)
	}
}

func TestSpillOversizedResult_SaveFailureFallsBackToTruncation(t *testing.T) {
	svc := &artifact.ScopedService{Inner: failingSaveStore{}, Scope: "namespace", Namespace: "ns"}
	bigData := map[string]any{"summary": strings.Repeat("Q", 4096)}
	bigObjContent := strings.Repeat("R", 4096)
	envelope := map[string]any{
		"data": bigData,
		"data_objects": []any{
			map[string]any{
				"name":      "big.txt",
				"mime_type": "text/plain",
				"content":   bigObjContent,
				"is_binary": false,
			},
		},
	}
	spillOversizedResult(context.Background(), envelope, svc, testAgentName, testUserID, testSessionID, testToolName, 256, newDiscardLogger())

	dataMap, ok := envelope["data"].(map[string]any)
	if !ok {
		t.Fatalf("expected fallback data map, got %v", envelope["data"])
	}
	truncated, _ := dataMap["truncated_result"].(string)
	if !strings.Contains(truncated, truncationMarker) {
		t.Errorf("truncated_result missing marker: %q", truncated)
	}

	entry := envelope["data_objects"].([]any)[0].(map[string]any)
	if _, has := entry["artifact_ref"]; has {
		t.Errorf("artifact_ref set despite save failure")
	}
	if got, _ := entry["content"].(string); !strings.Contains(got, truncationMarker) {
		t.Errorf("content not truncated with marker: %q", got)
	}

	msg, _ := envelope["message"].(string)
	if !strings.Contains(msg, "artifact save failed") {
		t.Errorf("message missing save-failure notice: %q", msg)
	}
}

func TestSpillOversizedResult_NilEnvelopeAndEmptyData(t *testing.T) {
	svc, _ := newTestScopedSvc()
	logger := newDiscardLogger()
	spillOversizedResult(context.Background(), nil, svc, testAgentName, testUserID, testSessionID, testToolName, 100, logger)

	envelope := map[string]any{}
	spillOversizedResult(context.Background(), envelope, svc, testAgentName, testUserID, testSessionID, testToolName, 100, logger)
	if len(envelope) != 0 {
		t.Errorf("empty envelope mutated: %v", envelope)
	}
}

type failingSaveStore struct{}

func (failingSaveStore) Save(_ context.Context, _ artifact.Locator, _ []byte, _ string) (int, error) {
	return 0, errors.New("synthetic save failure")
}

func (failingSaveStore) SaveVersion(_ context.Context, _ artifact.Locator, _ int, _ []byte, _ string) error {
	return errors.New("synthetic save failure")
}

func (failingSaveStore) Load(_ context.Context, _ artifact.Locator, _ *int) ([]byte, string, error) {
	return nil, "", errors.New("not implemented")
}

func (failingSaveStore) OpenReader(_ context.Context, _ artifact.Locator, _ *int) (io.ReadCloser, string, int64, error) {
	return nil, "", 0, errors.New("not implemented")
}

func (failingSaveStore) Delete(_ context.Context, _ artifact.Locator) error {
	return errors.New("not implemented")
}

func (failingSaveStore) ListKeys(_ context.Context, _ artifact.KeyScope) ([]string, error) {
	return nil, errors.New("not implemented")
}

func (failingSaveStore) ListVersions(_ context.Context, _ artifact.Locator) ([]int, error) {
	return nil, errors.New("not implemented")
}

func (failingSaveStore) GetVersionMetadata(_ context.Context, _ artifact.Locator, _ *int) (*artifact.VersionMetadata, error) {
	return nil, errors.New("not implemented")
}

func (failingSaveStore) SaveMetadata(_ context.Context, _ artifact.Locator, _ int, _ *artifact.VersionMetadata) error {
	return errors.New("not implemented")
}
