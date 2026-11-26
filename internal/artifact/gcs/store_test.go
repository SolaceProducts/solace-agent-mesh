// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package gcs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"cloud.google.com/go/storage"
	"google.golang.org/api/iterator"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
)

type mockBucket struct {
	mu      sync.Mutex
	objects map[string]*mockObj

	uploadStartSize int
}

type mockObj struct {
	data        []byte
	contentType string
	metadata    map[string]string
}

func newMockBucket() *mockBucket {
	return &mockBucket{objects: make(map[string]*mockObj)}
}

func (m *mockBucket) Object(name string) ObjectAPI {
	return &mockObjectHandle{bucket: m, name: name}
}

func (m *mockBucket) Objects(_ context.Context, query *storage.Query) ObjectIteratorAPI {
	m.mu.Lock()
	defer m.mu.Unlock()

	prefix := ""
	delimiter := ""
	if query != nil {
		prefix = query.Prefix
		delimiter = query.Delimiter
	}

	allKeys := make([]string, 0, len(m.objects))
	for k := range m.objects {
		allKeys = append(allKeys, k)
	}
	sort.Strings(allKeys)

	var items []*storage.ObjectAttrs
	commonPrefixes := make(map[string]bool)

	for _, k := range allKeys {
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		if delimiter != "" {
			rest := k[len(prefix):]
			idx := strings.Index(rest, delimiter)
			if idx >= 0 {
				cp := prefix + rest[:idx+len(delimiter)]
				if !commonPrefixes[cp] {
					commonPrefixes[cp] = true
					items = append(items, &storage.ObjectAttrs{Prefix: cp})
				}
				continue
			}
		}
		items = append(items, &storage.ObjectAttrs{Name: k})
	}

	return &mockIterator{items: items}
}

type mockObjectHandle struct {
	bucket *mockBucket
	name   string
}

func (o *mockObjectHandle) NewWriter(_ context.Context) WriterAPI {
	return &mockWriter{bucket: o.bucket, name: o.name}
}

func (o *mockObjectHandle) NewWriterIfNotExists(_ context.Context) WriterAPI {
	return &mockWriter{bucket: o.bucket, name: o.name, doesNotExist: true}
}

func (o *mockObjectHandle) NewReader(_ context.Context) (ReaderAPI, error) {
	o.bucket.mu.Lock()
	defer o.bucket.mu.Unlock()

	obj, ok := o.bucket.objects[o.name]
	if !ok {
		return nil, storage.ErrObjectNotExist
	}
	return &mockReader{
		Reader:      bytes.NewReader(obj.data),
		contentType: obj.contentType,
	}, nil
}

func (o *mockObjectHandle) Attrs(_ context.Context) (*storage.ObjectAttrs, error) {
	o.bucket.mu.Lock()
	defer o.bucket.mu.Unlock()

	obj, ok := o.bucket.objects[o.name]
	if !ok {
		return nil, storage.ErrObjectNotExist
	}
	return &storage.ObjectAttrs{
		Name:        o.name,
		ContentType: obj.contentType,
		Size:        int64(len(obj.data)),
		Metadata:    obj.metadata,
	}, nil
}

func (o *mockObjectHandle) Delete(_ context.Context) error {
	o.bucket.mu.Lock()
	defer o.bucket.mu.Unlock()

	if _, ok := o.bucket.objects[o.name]; !ok {
		return storage.ErrObjectNotExist
	}
	delete(o.bucket.objects, o.name)
	return nil
}

func (o *mockObjectHandle) CopierTo(dst ObjectAPI) CopierAPI {
	dstObj := dst.(*mockObjectHandle)
	return &mockCopier{
		src:    o,
		dst:    dstObj,
		bucket: o.bucket,
	}
}

type mockWriter struct {
	bucket       *mockBucket
	name         string
	buf          bytes.Buffer
	contentType  string
	metadata     map[string]string
	doesNotExist bool
}

func (w *mockWriter) Write(p []byte) (int, error) {
	if w.bucket.uploadStartSize > 0 && w.buf.Len()+len(p) >= w.bucket.uploadStartSize {
		w.bucket.mu.Lock()
		err := w.preconditionFailureLocked()
		w.bucket.mu.Unlock()
		if err != nil {
			return 0, err
		}
	}
	return w.buf.Write(p)
}

func (w *mockWriter) preconditionFailureLocked() error {
	if _, exists := w.bucket.objects[w.name]; w.doesNotExist && exists {
		return fmt.Errorf("googleapi: Error 412: Precondition Failed, conditionNotMet")
	}
	return nil
}

func (w *mockWriter) SetContentType(ct string) { w.contentType = ct }
func (w *mockWriter) SetMetadata(meta map[string]string) {
	w.metadata = make(map[string]string)
	for k, v := range meta {
		w.metadata[k] = v
	}
}
func (w *mockWriter) Close() error {
	w.bucket.mu.Lock()
	defer w.bucket.mu.Unlock()

	if err := w.preconditionFailureLocked(); err != nil {
		return err
	}

	data := make([]byte, w.buf.Len())
	copy(data, w.buf.Bytes())
	w.bucket.objects[w.name] = &mockObj{
		data:        data,
		contentType: w.contentType,
		metadata:    w.metadata,
	}
	return nil
}

type mockReader struct {
	*bytes.Reader
	contentType string
}

func (r *mockReader) Close() error        { return nil }
func (r *mockReader) ContentType() string { return r.contentType }

type mockIterator struct {
	items []*storage.ObjectAttrs
	idx   int
}

func (it *mockIterator) Next() (*storage.ObjectAttrs, error) {
	if it.idx >= len(it.items) {
		return nil, iterator.Done
	}
	item := it.items[it.idx]
	it.idx++
	return item, nil
}

type mockCopier struct {
	src         *mockObjectHandle
	dst         *mockObjectHandle
	bucket      *mockBucket
	contentType string
	metadata    map[string]string
}

func (c *mockCopier) SetContentType(ct string)           { c.contentType = ct }
func (c *mockCopier) SetMetadata(meta map[string]string) { c.metadata = meta }
func (c *mockCopier) Run(_ context.Context) (*storage.ObjectAttrs, error) {
	c.bucket.mu.Lock()
	defer c.bucket.mu.Unlock()

	srcObj, ok := c.bucket.objects[c.src.name]
	if !ok {
		return nil, storage.ErrObjectNotExist
	}

	newObj := &mockObj{
		data: srcObj.data,
	}
	if c.contentType != "" {
		newObj.contentType = c.contentType
	} else {
		newObj.contentType = srcObj.contentType
	}
	if c.metadata != nil {
		newObj.metadata = c.metadata
	} else {
		newObj.metadata = srcObj.metadata
	}

	c.bucket.objects[c.dst.name] = newObj
	return &storage.ObjectAttrs{Name: c.dst.name}, nil
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

func newTestStore() (*Store, *mockBucket) {
	mock := newMockBucket()
	return New(mock, "test-bucket"), mock
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

func TestOpenReader(t *testing.T) {
	store, _ := newTestStore()
	ctx := context.Background()
	loc := testLoc("data.txt")

	v0content := []byte("v0")
	v1content := []byte("v1-longer-content")
	_, _ = store.Save(ctx, loc, v0content, "text/plain")
	_, _ = store.Save(ctx, loc, v1content, "text/html")

	r, mime, size, err := store.OpenReader(ctx, loc, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r == nil {
		t.Fatal("expected non-nil reader")
	}
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(v1content) {
		t.Errorf("content = %q, want %q", got, v1content)
	}
	if mime != "text/html" {
		t.Errorf("mime = %q, want %q", mime, "text/html")
	}
	if size != int64(len(v1content)) {
		t.Errorf("size = %d, want %d", size, len(v1content))
	}
	if err = r.Close(); err != nil {
		t.Errorf("close: %v", err)
	}

	ver := 0
	r, mime, size, err = store.OpenReader(ctx, loc, &ver)
	if err != nil {
		t.Fatal(err)
	}
	got, err = io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(v0content) {
		t.Errorf("v0 content = %q, want %q", got, v0content)
	}
	if mime != "text/plain" {
		t.Errorf("v0 mime = %q, want %q", mime, "text/plain")
	}
	if size != int64(len(v0content)) {
		t.Errorf("v0 size = %d, want %d", size, len(v0content))
	}
	if err = r.Close(); err != nil {
		t.Errorf("close: %v", err)
	}
}

func TestOpenReaderNotFound(t *testing.T) {
	store, _ := newTestStore()
	r, mime, size, err := store.OpenReader(context.Background(), testLoc("nonexistent"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if r != nil || mime != "" || size != 0 {
		t.Errorf("expected (nil, \"\", 0), got (%v, %q, %d)", r, mime, size)
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

	schema := map[string]any{"type": "object"}
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
	if meta.Schema == nil || meta.Schema["type"] != "object" {
		t.Errorf("schema = %v, want map with type=object", meta.Schema)
	}
	if meta.Custom == nil || meta.Custom["source"] != "test" {
		t.Errorf("custom = %v, want map with source=test", meta.Custom)
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
	_, exists := mock.objects[expectedKey]
	mock.mu.Unlock()
	if !exists {
		mock.mu.Lock()
		keys := make([]string, 0, len(mock.objects))
		for k := range mock.objects {
			keys = append(keys, k)
		}
		mock.mu.Unlock()
		t.Errorf("expected key %q not found, existing keys: %v", expectedKey, keys)
	}

	userLoc := testLoc("user:profile.json")
	_, _ = store.Save(ctx, userLoc, []byte("profile"), "application/json")

	expectedUserKey := "app/user-1/user/profile.json/0"
	mock.mu.Lock()
	_, exists = mock.objects[expectedUserKey]
	mock.mu.Unlock()
	if !exists {
		mock.mu.Lock()
		keys := make([]string, 0, len(mock.objects))
		for k := range mock.objects {
			keys = append(keys, k)
		}
		mock.mu.Unlock()
		t.Errorf("expected key %q not found, existing keys: %v", expectedUserKey, keys)
	}
}

func TestGCSObjectMetadata(t *testing.T) {
	store, mock := newTestStore()
	ctx := context.Background()

	loc := testLoc("test.txt")
	_, _ = store.Save(ctx, loc, []byte("hello"), "text/plain")

	key := "app/user-1/sess-1/test.txt/0"
	mock.mu.Lock()
	obj := mock.objects[key]
	mock.mu.Unlock()

	if obj == nil {
		t.Fatal("object not found")
	}
	if obj.contentType != "text/plain" {
		t.Errorf("content type = %q", obj.contentType)
	}
	if obj.metadata["original_filename"] != "test.txt" {
		t.Errorf("original_filename = %q", obj.metadata["original_filename"])
	}
	if obj.metadata["user_id"] != "user-1" {
		t.Errorf("user_id = %q", obj.metadata["user_id"])
	}
	if obj.metadata["session_id"] != "sess-1" {
		t.Errorf("session_id = %q", obj.metadata["session_id"])
	}
	if obj.metadata["version"] != "0" {
		t.Errorf("version = %q", obj.metadata["version"])
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

func TestIsNotFound(t *testing.T) {
	if !isNotFound(storage.ErrObjectNotExist) {
		t.Error("expected ErrObjectNotExist to be not-found")
	}
	if isNotFound(nil) {
		t.Error("expected nil to not be not-found")
	}
	if isNotFound(io.EOF) {
		t.Error("expected EOF to not be not-found")
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
	tests := []struct {
		name            string
		uploadStartSize int
	}{
		{name: "upload checked when the writer closes"},
		{name: "upload checked when it starts inside a write", uploadStartSize: 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, bucket := newTestStore()
			bucket.uploadStartSize = tt.uploadStartSize
			loc := testLoc("test.txt")

			if err := store.SaveVersion(t.Context(), loc, 0, []byte("first"), "text/plain"); err != nil {
				t.Fatalf("first SaveVersion(0): %v", err)
			}

			err := store.SaveVersion(t.Context(), loc, 0, []byte("second"), "text/plain")
			if !errors.Is(err, artifact.ErrVersionConflict) {
				t.Fatalf("second SaveVersion(0) = %v, want ErrVersionConflict", err)
			}
		})
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

func TestGCSPreconditionFailed(t *testing.T) {
	if gcsPreconditionFailed(nil) {
		t.Error("nil should not be precondition failed")
	}
	if gcsPreconditionFailed(io.EOF) {
		t.Error("EOF should not be precondition failed")
	}
	if !gcsPreconditionFailed(fmt.Errorf("conditionNotMet")) {
		t.Error("conditionNotMet should be precondition failed")
	}
	if !gcsPreconditionFailed(fmt.Errorf("Precondition Failed")) {
		t.Error("Precondition Failed should be precondition failed")
	}
}

type racingBucket struct {
	*mockBucket
	beforeFirstWrite func()
}

func (r *racingBucket) Object(name string) ObjectAPI {
	return &racingObject{ObjectAPI: r.mockBucket.Object(name), bucket: r}
}

type racingObject struct {
	ObjectAPI
	bucket *racingBucket
}

func (o *racingObject) raceOnce() {
	if hook := o.bucket.beforeFirstWrite; hook != nil {
		o.bucket.beforeFirstWrite = nil
		hook()
	}
}

func (o *racingObject) NewWriter(ctx context.Context) WriterAPI {
	o.raceOnce()
	return o.ObjectAPI.NewWriter(ctx)
}

func (o *racingObject) NewWriterIfNotExists(ctx context.Context) WriterAPI {
	o.raceOnce()
	return o.ObjectAPI.NewWriterIfNotExists(ctx)
}

func TestSaveWhenAnotherStoreSavesTheSameVersionFirst(t *testing.T) {
	mock := newMockBucket()
	racing := &racingBucket{mockBucket: mock}
	first, second := New(racing, "test-bucket"), New(mock, "test-bucket")
	ctx := t.Context()
	loc := testLoc("shared.txt")
	racing.beforeFirstWrite = func() {
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
