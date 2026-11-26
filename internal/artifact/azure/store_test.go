// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package azure

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/bloberror"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
)

type mockContainer struct {
	mu    sync.Mutex
	blobs map[string]*mockBlob
}

type mockBlob struct {
	data        []byte
	contentType string
	metadata    map[string]string
}

func newMockContainer() *mockContainer {
	return &mockContainer{blobs: make(map[string]*mockBlob)}
}

func (m *mockContainer) UploadBlob(_ context.Context, name string, content []byte, contentType string, metadata map[string]string, ifNoneMatch bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if ifNoneMatch {
		if _, exists := m.blobs[name]; exists {
			return fmt.Errorf("ConditionNotMet: blob already exists (412)")
		}
	}

	data := make([]byte, len(content))
	copy(data, content)
	meta := make(map[string]string, len(metadata))
	for k, v := range metadata {
		meta[k] = v
	}
	m.blobs[name] = &mockBlob{
		data:        data,
		contentType: contentType,
		metadata:    meta,
	}
	return nil
}

func (m *mockContainer) DownloadBlob(_ context.Context, name string) ([]byte, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	b, ok := m.blobs[name]
	if !ok {
		return nil, "", fmt.Errorf("BlobNotFound: %s", name)
	}
	data := make([]byte, len(b.data))
	copy(data, b.data)
	return data, b.contentType, nil
}

func (m *mockContainer) DownloadBlobStream(_ context.Context, name string) (io.ReadCloser, string, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	b, ok := m.blobs[name]
	if !ok {
		return nil, "", 0, fmt.Errorf("BlobNotFound: %s", name)
	}
	data := make([]byte, len(b.data))
	copy(data, b.data)
	return io.NopCloser(bytes.NewReader(data)), b.contentType, int64(len(data)), nil
}

func (m *mockContainer) DeleteBlob(_ context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.blobs[name]; !ok {
		return fmt.Errorf("BlobNotFound: %s", name)
	}
	delete(m.blobs, name)
	return nil
}

func (m *mockContainer) GetBlobProperties(_ context.Context, name string) (*BlobProperties, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	b, ok := m.blobs[name]
	if !ok {
		return nil, fmt.Errorf("BlobNotFound: %s", name)
	}
	return &BlobProperties{
		ContentType: b.contentType,
		SizeBytes:   int64(len(b.data)),
		Metadata:    b.metadata,
	}, nil
}

func (m *mockContainer) SetBlobMetadata(_ context.Context, name string, metadata map[string]string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	b, ok := m.blobs[name]
	if !ok {
		return fmt.Errorf("BlobNotFound: %s", name)
	}
	b.metadata = make(map[string]string, len(metadata))
	for k, v := range metadata {
		b.metadata[k] = v
	}
	return nil
}

func (m *mockContainer) SetBlobHTTPHeaders(_ context.Context, name string, contentType string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	b, ok := m.blobs[name]
	if !ok {
		return fmt.Errorf("BlobNotFound: %s", name)
	}
	b.contentType = contentType
	return nil
}

func (m *mockContainer) ListBlobs(_ context.Context, prefix string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var names []string
	for k := range m.blobs {
		if strings.HasPrefix(k, prefix) {
			names = append(names, k)
		}
	}
	sort.Strings(names)
	return names, nil
}

func testLoc(filename string) artifact.Locator {
	return artifact.Locator{
		AppName:   "app",
		UserID:    "user-1",
		SessionID: "sess-1",
		Filename:  filename,
	}
}

func testScope() artifact.KeyScope {
	return artifact.KeyScope{
		AppName:   "app",
		UserID:    "user-1",
		SessionID: "sess-1",
	}
}

func newTestStore() (*Store, *mockContainer) {
	mock := newMockContainer()
	return New(mock, "test-container"), mock
}

func TestSaveAndLoad(t *testing.T) {
	store, _ := newTestStore()
	ctx := context.Background()

	loc := testLoc("data.csv")
	v, err := store.Save(ctx, loc, []byte("a,b,c"), "text/csv")
	if err != nil {
		t.Fatal(err)
	}
	if v != 0 {
		t.Errorf("first version = %d, want 0", v)
	}

	content, mime, err := store.Load(ctx, loc, nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "a,b,c" {
		t.Errorf("content = %q", content)
	}
	if mime != "text/csv" {
		t.Errorf("mime = %q", mime)
	}
}

func TestSaveMultipleVersions(t *testing.T) {
	store, _ := newTestStore()
	ctx := context.Background()
	loc := testLoc("data.txt")

	v0, _ := store.Save(ctx, loc, []byte("v0"), "text/plain")
	v1, _ := store.Save(ctx, loc, []byte("v1"), "text/plain")
	v2, _ := store.Save(ctx, loc, []byte("v2"), "text/plain")

	if v0 != 0 || v1 != 1 || v2 != 2 {
		t.Errorf("versions = %d, %d, %d", v0, v1, v2)
	}

	content, _, _ := store.Load(ctx, loc, nil)
	if string(content) != "v2" {
		t.Errorf("latest content = %q", content)
	}

	ver := 0
	content, _, _ = store.Load(ctx, loc, &ver)
	if string(content) != "v0" {
		t.Errorf("v0 content = %q", content)
	}
}

func TestOpenReader(t *testing.T) {
	store, _ := newTestStore()
	ctx := context.Background()
	loc := testLoc("stream.bin")

	v0, err := store.Save(ctx, loc, []byte("v0-bytes"), "application/octet-stream")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Save(ctx, loc, []byte("v1-longer-bytes"), "text/plain"); err != nil {
		t.Fatal(err)
	}

	rc, mime, size, err := store.OpenReader(ctx, loc, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rc == nil {
		t.Fatal("expected non-nil reader for latest version")
	}
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	if err = rc.Close(); err != nil {
		t.Fatal(err)
	}
	if string(got) != "v1-longer-bytes" {
		t.Errorf("latest content = %q, want %q", got, "v1-longer-bytes")
	}
	if mime != "text/plain" {
		t.Errorf("latest mime = %q, want %q", mime, "text/plain")
	}
	if size != int64(len("v1-longer-bytes")) {
		t.Errorf("latest size = %d, want %d", size, len("v1-longer-bytes"))
	}

	rc, mime, size, err = store.OpenReader(ctx, loc, &v0)
	if err != nil {
		t.Fatal(err)
	}
	if rc == nil {
		t.Fatal("expected non-nil reader for v0")
	}
	got, err = io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	if err = rc.Close(); err != nil {
		t.Fatal(err)
	}
	if string(got) != "v0-bytes" {
		t.Errorf("v0 content = %q, want %q", got, "v0-bytes")
	}
	if mime != "application/octet-stream" {
		t.Errorf("v0 mime = %q, want %q", mime, "application/octet-stream")
	}
	if size != int64(len("v0-bytes")) {
		t.Errorf("v0 size = %d, want %d", size, len("v0-bytes"))
	}
}

func TestOpenReaderNotFound(t *testing.T) {
	store, _ := newTestStore()
	rc, mime, size, err := store.OpenReader(context.Background(), testLoc("nonexistent"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if rc != nil || mime != "" || size != 0 {
		t.Errorf("expected nil/empty/0, got %v / %q / %d", rc, mime, size)
	}
}

func TestLoadNotFound(t *testing.T) {
	store, _ := newTestStore()
	content, mime, err := store.Load(context.Background(), testLoc("nonexistent"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if content != nil || mime != "" {
		t.Errorf("expected nil/empty, got %q / %q", content, mime)
	}
}

func TestLoadSpecificVersionNotFound(t *testing.T) {
	store, _ := newTestStore()
	v := 42
	content, mime, err := store.Load(context.Background(), testLoc("nonexistent"), &v)
	if err != nil {
		t.Fatal(err)
	}
	if content != nil || mime != "" {
		t.Errorf("expected nil/empty, got %q / %q", content, mime)
	}
}

func TestDelete(t *testing.T) {
	store, _ := newTestStore()
	ctx := context.Background()
	loc := testLoc("temp.txt")

	_, _ = store.Save(ctx, loc, []byte("v0"), "text/plain")
	_, _ = store.Save(ctx, loc, []byte("v1"), "text/plain")

	if err := store.Delete(ctx, loc); err != nil {
		t.Fatal(err)
	}

	content, _, _ := store.Load(ctx, loc, nil)
	if content != nil {
		t.Error("artifact should be deleted")
	}

	versions, _ := store.ListVersions(ctx, loc)
	if len(versions) != 0 {
		t.Errorf("expected 0 versions after delete, got %d", len(versions))
	}
}

func TestDeleteNonexistent(t *testing.T) {
	store, _ := newTestStore()
	if err := store.Delete(context.Background(), testLoc("nonexistent")); err != nil {
		t.Fatal(err)
	}
}

func TestListKeys(t *testing.T) {
	store, _ := newTestStore()
	ctx := context.Background()

	_, _ = store.Save(ctx, testLoc("b.txt"), []byte("b"), "text/plain")
	_, _ = store.Save(ctx, testLoc("a.txt"), []byte("a"), "text/plain")

	_, _ = store.Save(ctx, artifact.Locator{
		AppName: "app", UserID: "user-2", SessionID: "sess-1", Filename: "c.txt",
	}, []byte("c"), "text/plain")

	keys, err := store.ListKeys(ctx, testScope())
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 {
		t.Fatalf("expected 2 keys, got %d: %v", len(keys), keys)
	}
	if keys[0] != "a.txt" || keys[1] != "b.txt" {
		t.Errorf("keys = %v", keys)
	}
}

func TestListKeysWithUserScoped(t *testing.T) {
	store, _ := newTestStore()
	ctx := context.Background()

	_, _ = store.Save(ctx, testLoc("session.txt"), []byte("s"), "text/plain")
	_, _ = store.Save(ctx, testLoc("user:profile.json"), []byte("p"), "text/plain")

	keys, err := store.ListKeys(ctx, testScope())
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 {
		t.Fatalf("expected 2 keys, got %d: %v", len(keys), keys)
	}
	if keys[0] != "session.txt" || keys[1] != "user:profile.json" {
		t.Errorf("keys = %v", keys)
	}
}

func TestListVersions(t *testing.T) {
	store, _ := newTestStore()
	ctx := context.Background()
	loc := testLoc("data.txt")

	_, _ = store.Save(ctx, loc, []byte("v0"), "text/plain")
	_, _ = store.Save(ctx, loc, []byte("v1"), "text/plain")
	_, _ = store.Save(ctx, loc, []byte("v2"), "text/plain")

	versions, err := store.ListVersions(ctx, loc)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 3 {
		t.Fatalf("expected 3 versions, got %d", len(versions))
	}
	for i, v := range versions {
		if v != i {
			t.Errorf("versions[%d] = %d", i, v)
		}
	}
}

func TestListVersionsNotFound(t *testing.T) {
	store, _ := newTestStore()
	versions, err := store.ListVersions(context.Background(), testLoc("nonexistent"))
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 0 {
		t.Errorf("expected empty, got %v", versions)
	}
}

func TestGetVersionMetadata(t *testing.T) {
	store, _ := newTestStore()
	ctx := context.Background()
	loc := testLoc("data.csv")

	_, _ = store.Save(ctx, loc, []byte("a,b,c"), "text/csv")

	meta, err := store.GetVersionMetadata(ctx, loc, nil)
	if err != nil {
		t.Fatal(err)
	}
	if meta == nil {
		t.Fatal("metadata not found")
	}
	if meta.MIMEType != "text/csv" {
		t.Errorf("mime = %q", meta.MIMEType)
	}
	if meta.SizeBytes != 5 {
		t.Errorf("size = %d, want 5", meta.SizeBytes)
	}
}

func TestGetVersionMetadataSpecificVersion(t *testing.T) {
	store, _ := newTestStore()
	ctx := context.Background()
	loc := testLoc("data.txt")

	_, _ = store.Save(ctx, loc, []byte("short"), "text/plain")
	_, _ = store.Save(ctx, loc, []byte("longer content"), "text/html")

	v := 0
	meta, err := store.GetVersionMetadata(ctx, loc, &v)
	if err != nil {
		t.Fatal(err)
	}
	if meta.MIMEType != "text/plain" {
		t.Errorf("v0 mime = %q", meta.MIMEType)
	}
	if meta.SizeBytes != 5 {
		t.Errorf("v0 size = %d", meta.SizeBytes)
	}
}

func TestGetVersionMetadataNotFound(t *testing.T) {
	store, _ := newTestStore()
	got, err := store.GetVersionMetadata(context.Background(), testLoc("nonexistent"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Error("expected nil metadata for nonexistent artifact")
	}
}

func TestSaveMetadata(t *testing.T) {
	store, _ := newTestStore()
	ctx := context.Background()
	loc := testLoc("data.csv")

	v, _ := store.Save(ctx, loc, []byte("a,b"), "text/csv")

	err := store.SaveMetadata(ctx, loc, v, &artifact.VersionMetadata{
		MIMEType: "text/tab-separated-values",
	})
	if err != nil {
		t.Fatal(err)
	}

	meta, err := store.GetVersionMetadata(ctx, loc, &v)
	if err != nil {
		t.Fatal(err)
	}
	if meta.MIMEType != "text/tab-separated-values" {
		t.Errorf("updated mime = %q", meta.MIMEType)
	}

	content, _, err := store.Load(ctx, loc, &v)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "a,b" {
		t.Errorf("content after metadata update = %q", content)
	}
}

func TestSaveMetadata_AllFields(t *testing.T) {
	store, _ := newTestStore()
	ctx := context.Background()
	loc := testLoc("full-meta.json")

	v, _ := store.Save(ctx, loc, []byte(`{}`), "application/json")

	schema := map[string]any{"type": "object", "properties": map[string]any{"name": map[string]any{"type": "string"}}}
	custom := map[string]any{"source": "test", "priority": float64(1)}

	err := store.SaveMetadata(ctx, loc, v, &artifact.VersionMetadata{
		MIMEType:    "application/json",
		Description: "Full metadata test",
		Schema:      schema,
		Custom:      custom,
	})
	if err != nil {
		t.Fatal(err)
	}

	meta, err := store.GetVersionMetadata(ctx, loc, &v)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Description != "Full metadata test" {
		t.Errorf("description = %q, want %q", meta.Description, "Full metadata test")
	}
	if meta.Schema == nil {
		t.Fatal("schema is nil")
	}
	if meta.Schema["type"] != "object" {
		t.Errorf("schema[type] = %v, want object", meta.Schema["type"])
	}
	if meta.Custom == nil {
		t.Fatal("custom is nil")
	}
	if meta.Custom["source"] != "test" {
		t.Errorf("custom[source] = %v, want test", meta.Custom["source"])
	}
}

func TestUserScopedArtifact(t *testing.T) {
	store, _ := newTestStore()
	ctx := context.Background()

	loc := testLoc("user:settings.json")
	v, err := store.Save(ctx, loc, []byte(`{"theme":"dark"}`), "application/json")
	if err != nil {
		t.Fatal(err)
	}
	if v != 0 {
		t.Errorf("version = %d", v)
	}

	content, mime, err := store.Load(ctx, loc, nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != `{"theme":"dark"}` {
		t.Errorf("content = %q", content)
	}
	if mime != "application/json" {
		t.Errorf("mime = %q", mime)
	}
}

func TestKeyStructureMatchesPython(t *testing.T) {
	store, mock := newTestStore()
	ctx := context.Background()

	loc := testLoc("report.pdf")
	_, _ = store.Save(ctx, loc, []byte("pdf-data"), "application/pdf")

	expectedKey := "app/user-1/sess-1/report.pdf/0"
	mock.mu.Lock()
	_, exists := mock.blobs[expectedKey]
	mock.mu.Unlock()
	if !exists {
		mock.mu.Lock()
		keys := make([]string, 0, len(mock.blobs))
		for k := range mock.blobs {
			keys = append(keys, k)
		}
		mock.mu.Unlock()
		t.Errorf("expected key %q not found, existing keys: %v", expectedKey, keys)
	}

	userLoc := testLoc("user:profile.json")
	_, _ = store.Save(ctx, userLoc, []byte("profile"), "application/json")

	expectedUserKey := "app/user-1/user/profile.json/0"
	mock.mu.Lock()
	_, exists = mock.blobs[expectedUserKey]
	mock.mu.Unlock()
	if !exists {
		mock.mu.Lock()
		keys := make([]string, 0, len(mock.blobs))
		for k := range mock.blobs {
			keys = append(keys, k)
		}
		mock.mu.Unlock()
		t.Errorf("expected key %q not found, existing keys: %v", expectedUserKey, keys)
	}
}

func TestMetadataNonASCIIRoundTrip(t *testing.T) {
	store, mock := newTestStore()
	ctx := context.Background()
	loc := testLoc("slides.pptx")

	v, _ := store.Save(ctx, loc, []byte("x"), "application/octet-stream")

	desc := "PowerPoint presentation: The State of AGI — Mid-2026"
	schema := map[string]any{"title": "Über résumé — déjà vu"}
	custom := map[string]any{"note": "naïve café ☕"}

	if err := store.SaveMetadata(ctx, loc, v, &artifact.VersionMetadata{
		MIMEType:    "application/octet-stream",
		Description: desc,
		Schema:      schema,
		Custom:      custom,
	}); err != nil {
		t.Fatal(err)
	}

	key := "app/user-1/sess-1/slides.pptx/" + strconv.Itoa(v)
	mock.mu.Lock()
	stored := mock.blobs[key].metadata
	mock.mu.Unlock()
	for k, val := range stored {
		for _, r := range val {
			if r >= utf8.RuneSelf {
				t.Errorf("metadata %q stored with non-ASCII rune %q in value %q", k, r, val)
			}
		}
	}

	meta, err := store.GetVersionMetadata(ctx, loc, &v)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Description != desc {
		t.Errorf("description = %q, want %q", meta.Description, desc)
	}
	if meta.Schema == nil || meta.Schema["title"] != schema["title"] {
		t.Errorf("schema title = %v, want %q", meta.Schema["title"], schema["title"])
	}
	if meta.Custom == nil || meta.Custom["note"] != custom["note"] {
		t.Errorf("custom note = %v, want %q", meta.Custom["note"], custom["note"])
	}
}

func TestBlobMetadata(t *testing.T) {
	store, mock := newTestStore()
	ctx := context.Background()

	loc := testLoc("test.txt")
	_, _ = store.Save(ctx, loc, []byte("hello"), "text/plain")

	key := "app/user-1/sess-1/test.txt/0"
	mock.mu.Lock()
	b := mock.blobs[key]
	mock.mu.Unlock()

	if b == nil {
		t.Fatal("blob not found")
	}
	if b.contentType != "text/plain" {
		t.Errorf("content type = %q", b.contentType)
	}
	if b.metadata["original_filename"] != "test.txt" {
		t.Errorf("original_filename = %q", b.metadata["original_filename"])
	}
	if b.metadata["user_id"] != "user-1" {
		t.Errorf("user_id = %q", b.metadata["user_id"])
	}
	if b.metadata["session_id"] != "sess-1" {
		t.Errorf("session_id = %q", b.metadata["session_id"])
	}
	if b.metadata["version"] != "0" {
		t.Errorf("version = %q", b.metadata["version"])
	}
}

func TestConcurrentSave(t *testing.T) {
	store, _ := newTestStore()
	ctx := context.Background()

	var wg sync.WaitGroup
	n := 10
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(idx int) {
			defer wg.Done()
			loc := testLoc(fmt.Sprintf("file-%d.txt", idx))
			v, err := store.Save(ctx, loc, []byte(strconv.Itoa(idx)), "text/plain")
			if err != nil {
				t.Error(err)
			}
			if v != 0 {
				t.Errorf("file-%d: version = %d, want 0", idx, v)
			}
		}(i)
	}
	wg.Wait()
}

func TestIsBlobNotFound(t *testing.T) {
	if !isBlobNotFound(fmt.Errorf("BlobNotFound: test")) {
		t.Error("expected BlobNotFound to match")
	}
	if !isBlobNotFound(fmt.Errorf("blob not found")) {
		t.Error("expected 'blob not found' to match")
	}
	if isBlobNotFound(nil) {
		t.Error("expected nil to not match")
	}
	if isBlobNotFound(fmt.Errorf("some other error")) {
		t.Error("expected other error to not match")
	}
}

func TestIsBlobConflict(t *testing.T) {
	if !isBlobConflict(fmt.Errorf("ConditionNotMet: blob exists (412)")) {
		t.Error("expected ConditionNotMet to match")
	}
	if !isBlobConflict(fmt.Errorf("BlobAlreadyExists")) {
		t.Error("expected BlobAlreadyExists to match")
	}
	if isBlobConflict(nil) {
		t.Error("expected nil to not match")
	}
	if isBlobConflict(fmt.Errorf("some other error")) {
		t.Error("expected other error to not match")
	}
}

func TestIsBlobConflictReadsTheResponseError(t *testing.T) {
	response := func(status int, code string) error {
		target, _ := url.Parse("https://acct.blob.core.windows.net/c/app/user-1/sess-1/invoice-4120.pdf/0")
		return fmt.Errorf("upload blob: %w", &azcore.ResponseError{
			StatusCode: status,
			ErrorCode:  code,
			RawResponse: &http.Response{
				StatusCode: status,
				Header:     http.Header{"X-Ms-Request-Id": []string{"4120-req"}},
				Request:    &http.Request{Method: http.MethodPut, URL: target},
			},
		})
	}
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "precondition failed", err: response(http.StatusPreconditionFailed, string(bloberror.ConditionNotMet)), want: true},
		{name: "blob already exists", err: response(http.StatusConflict, string(bloberror.BlobAlreadyExists)), want: true},
		{name: "forbidden for a blob whose name contains 412", err: response(http.StatusForbidden, string(bloberror.AuthorizationPermissionMismatch))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isBlobConflict(tt.err); got != tt.want {
				t.Errorf("isBlobConflict(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestInterfaceCompliance(t *testing.T) {
	store, _ := newTestStore()
	var _ artifact.Service = store
}

func TestSaveVersion_Basic(t *testing.T) {
	store, _ := newTestStore()
	ctx := context.Background()
	loc := testLoc("test.txt")

	if err := store.SaveVersion(ctx, loc, 0, []byte("hello"), "text/plain"); err != nil {
		t.Fatalf("SaveVersion(0): %v", err)
	}

	v := 0
	content, mime, err := store.Load(ctx, loc, &v)
	if err != nil {
		t.Fatalf("Load(0): %v", err)
	}
	if string(content) != "hello" {
		t.Errorf("content = %q, want %q", content, "hello")
	}
	if mime != "text/plain" {
		t.Errorf("mime = %q, want %q", mime, "text/plain")
	}
}

func TestSaveVersion_Conflict(t *testing.T) {
	store, _ := newTestStore()
	ctx := context.Background()
	loc := testLoc("test.txt")

	if err := store.SaveVersion(ctx, loc, 0, []byte("first"), "text/plain"); err != nil {
		t.Fatalf("first SaveVersion(0): %v", err)
	}

	err := store.SaveVersion(ctx, loc, 0, []byte("second"), "text/plain")
	if err != artifact.ErrVersionConflict {
		t.Fatalf("second SaveVersion(0) = %v, want ErrVersionConflict", err)
	}
}

func TestSaveVersion_ConflictPreservesOriginal(t *testing.T) {
	store, _ := newTestStore()
	ctx := context.Background()
	loc := testLoc("test.txt")

	if err := store.SaveVersion(ctx, loc, 0, []byte("original"), "text/plain"); err != nil {
		t.Fatalf("SaveVersion(0): %v", err)
	}

	_ = store.SaveVersion(ctx, loc, 0, []byte("intruder"), "text/plain")

	v := 0
	content, _, err := store.Load(ctx, loc, &v)
	if err != nil {
		t.Fatalf("Load(0): %v", err)
	}
	if string(content) != "original" {
		t.Errorf("content = %q, want %q", content, "original")
	}
}

func TestSaveVersion_MixedWithSave(t *testing.T) {
	store, _ := newTestStore()
	ctx := context.Background()
	loc := testLoc("test.txt")

	v0, err := store.Save(ctx, loc, []byte("auto0"), "text/plain")
	if err != nil || v0 != 0 {
		t.Fatalf("Save = (%d, %v), want (0, nil)", v0, err)
	}

	if svErr := store.SaveVersion(ctx, loc, 1, []byte("cond1"), "text/plain"); svErr != nil {
		t.Fatalf("SaveVersion(1): %v", svErr)
	}

	v2, err := store.Save(ctx, loc, []byte("auto2"), "text/plain")
	if err != nil || v2 != 2 {
		t.Fatalf("Save = (%d, %v), want (2, nil)", v2, err)
	}

	for _, tc := range []struct {
		ver  int
		want string
	}{{0, "auto0"}, {1, "cond1"}, {2, "auto2"}} {
		content, _, err := store.Load(ctx, loc, &tc.ver)
		if err != nil {
			t.Fatalf("Load(%d): %v", tc.ver, err)
		}
		if string(content) != tc.want {
			t.Errorf("Load(%d) = %q, want %q", tc.ver, content, tc.want)
		}
	}
}

type racingContainer struct {
	*mockContainer
	beforeFirstUpload func()
}

func (r *racingContainer) UploadBlob(ctx context.Context, name string, content []byte, contentType string, metadata map[string]string, ifNoneMatch bool) error {
	if hook := r.beforeFirstUpload; hook != nil {
		r.beforeFirstUpload = nil
		hook()
	}
	return r.mockContainer.UploadBlob(ctx, name, content, contentType, metadata, ifNoneMatch)
}

func TestSaveWhenAnotherStoreSavesTheSameVersionFirst(t *testing.T) {
	mock := newMockContainer()
	racing := &racingContainer{mockContainer: mock}
	first, second := New(racing, "test-container"), New(mock, "test-container")
	ctx := t.Context()
	loc := testLoc("shared.txt")
	racing.beforeFirstUpload = func() {
		if v, err := second.Save(ctx, loc, []byte("second"), "text/plain"); err != nil || v != 0 {
			t.Errorf("second Save() = (%d, %v), want version 0", v, err)
		}
	}

	v, err := first.Save(ctx, loc, []byte("first"), "text/plain")

	if err != nil || v != 1 {
		t.Fatalf("first Save() = (%d, %v), want version 1", v, err)
	}
	for version, want := range []string{"second", "first"} {
		got, _, err := first.Load(ctx, loc, &version)
		if err != nil || string(got) != want {
			t.Errorf("Load(version %d) = (%q, %v), want %q", version, got, err, want)
		}
	}
}
