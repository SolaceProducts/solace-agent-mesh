// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package s3

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
)

type mockS3 struct {
	mu      sync.Mutex
	objects map[string]*mockObject
}

type mockObject struct {
	data        []byte
	contentType string
	metadata    map[string]string
}

func newMockS3() *mockS3 {
	return &mockS3{objects: make(map[string]*mockObject)}
}

func (m *mockS3) PutObject(_ context.Context, params *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if params.IfNoneMatch != nil && *params.IfNoneMatch == "*" {
		if _, exists := m.objects[*params.Key]; exists {
			return nil, fmt.Errorf("PreconditionFailed: conditional request failed")
		}
	}

	data, err := io.ReadAll(params.Body)
	if err != nil {
		return nil, err
	}

	ct := ""
	if params.ContentType != nil {
		ct = *params.ContentType
	}

	meta := make(map[string]string)
	for k, v := range params.Metadata {
		meta[k] = v
	}

	m.objects[*params.Key] = &mockObject{
		data:        data,
		contentType: ct,
		metadata:    meta,
	}
	return &s3.PutObjectOutput{}, nil
}

func (m *mockS3) GetObject(_ context.Context, params *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	obj, ok := m.objects[*params.Key]
	if !ok {
		return nil, &types.NoSuchKey{Message: aws.String("not found")}
	}

	size := int64(len(obj.data))
	return &s3.GetObjectOutput{
		Body:          io.NopCloser(bytes.NewReader(obj.data)),
		ContentType:   aws.String(obj.contentType),
		ContentLength: &size,
	}, nil
}

func (m *mockS3) HeadObject(_ context.Context, params *s3.HeadObjectInput, _ ...func(*s3.Options)) (*s3.HeadObjectOutput, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	obj, ok := m.objects[*params.Key]
	if !ok {
		return nil, &types.NotFound{Message: aws.String("not found")}
	}

	size := int64(len(obj.data))
	return &s3.HeadObjectOutput{
		ContentType:   aws.String(obj.contentType),
		ContentLength: &size,
		Metadata:      obj.metadata,
	}, nil
}

func (m *mockS3) ListObjectsV2(_ context.Context, params *s3.ListObjectsV2Input, _ ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	prefix := ""
	if params.Prefix != nil {
		prefix = *params.Prefix
	}
	delimiter := ""
	if params.Delimiter != nil {
		delimiter = *params.Delimiter
	}

	var contents []types.Object
	commonPrefixes := make(map[string]bool)

	keys := make([]string, 0, len(m.objects))
	for k := range m.objects {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		if !strings.HasPrefix(k, prefix) {
			continue
		}

		if delimiter != "" {
			rest := k[len(prefix):]
			idx := strings.Index(rest, delimiter)
			if idx >= 0 {
				cp := prefix + rest[:idx+len(delimiter)]
				commonPrefixes[cp] = true
				continue
			}
		}

		contents = append(contents, types.Object{Key: aws.String(k)})
	}

	var cps []types.CommonPrefix
	cpKeys := make([]string, 0, len(commonPrefixes))
	for cp := range commonPrefixes {
		cpKeys = append(cpKeys, cp)
	}
	sort.Strings(cpKeys)
	for _, cp := range cpKeys {
		cps = append(cps, types.CommonPrefix{Prefix: aws.String(cp)})
	}

	return &s3.ListObjectsV2Output{
		Contents:       contents,
		CommonPrefixes: cps,
		IsTruncated:    aws.Bool(false),
	}, nil
}

func (m *mockS3) DeleteObjects(_ context.Context, params *s3.DeleteObjectsInput, _ ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, obj := range params.Delete.Objects {
		delete(m.objects, *obj.Key)
	}
	return &s3.DeleteObjectsOutput{}, nil
}

func (m *mockS3) CopyObject(_ context.Context, params *s3.CopyObjectInput, _ ...func(*s3.Options)) (*s3.CopyObjectOutput, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	source := *params.CopySource
	parts := strings.SplitN(source, "/", 2)
	if len(parts) < 2 {
		return nil, fmt.Errorf("invalid copy source: %s", source)
	}
	sourceKey := parts[1]

	obj, ok := m.objects[sourceKey]
	if !ok {
		return nil, &types.NoSuchKey{Message: aws.String("source not found")}
	}

	newObj := &mockObject{
		data:     obj.data,
		metadata: make(map[string]string),
	}
	if params.ContentType != nil {
		newObj.contentType = *params.ContentType
	} else {
		newObj.contentType = obj.contentType
	}
	for k, v := range params.Metadata {
		newObj.metadata[k] = v
	}

	m.objects[*params.Key] = newObj
	return &s3.CopyObjectOutput{}, nil
}

func (m *mockS3) HeadBucket(_ context.Context, _ *s3.HeadBucketInput, _ ...func(*s3.Options)) (*s3.HeadBucketOutput, error) {
	return &s3.HeadBucketOutput{}, nil
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

func newTestStore() (*Store, *mockS3) {
	mock := newMockS3()
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

func TestOpenReader(t *testing.T) {
	store, _ := newTestStore()
	ctx := context.Background()
	loc := testLoc("data.csv")

	if _, err := store.Save(ctx, loc, []byte("a,b,c"), "text/csv"); err != nil {
		t.Fatal(err)
	}

	rc, mime, size, err := store.OpenReader(ctx, loc, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rc == nil {
		t.Fatal("expected non-nil reader for latest version")
	}
	defer func() { _ = rc.Close() }()

	content, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "a,b,c" {
		t.Errorf("content = %q, want %q", content, "a,b,c")
	}
	if mime != "text/csv" {
		t.Errorf("mime = %q, want %q", mime, "text/csv")
	}
	if size != int64(len("a,b,c")) {
		t.Errorf("size = %d, want %d", size, len("a,b,c"))
	}
}

func TestOpenReaderSpecificVersion(t *testing.T) {
	store, _ := newTestStore()
	ctx := context.Background()
	loc := testLoc("data.txt")

	if _, err := store.Save(ctx, loc, []byte("v0"), "text/plain"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Save(ctx, loc, []byte("longer v1"), "text/html"); err != nil {
		t.Fatal(err)
	}

	ver := 0
	rc, mime, size, err := store.OpenReader(ctx, loc, &ver)
	if err != nil {
		t.Fatal(err)
	}
	if rc == nil {
		t.Fatal("expected non-nil reader for v0")
	}
	defer func() { _ = rc.Close() }()

	content, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "v0" {
		t.Errorf("v0 content = %q, want %q", content, "v0")
	}
	if mime != "text/plain" {
		t.Errorf("v0 mime = %q, want %q", mime, "text/plain")
	}
	if size != int64(len("v0")) {
		t.Errorf("v0 size = %d, want %d", size, len("v0"))
	}
}

func TestOpenReaderNotFound(t *testing.T) {
	store, _ := newTestStore()
	rc, mime, size, err := store.OpenReader(context.Background(), testLoc("nonexistent"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if rc != nil || mime != "" || size != 0 {
		t.Errorf("expected (nil, \"\", 0, nil), got (%v, %q, %d)", rc, mime, size)
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

func TestListArtifactVersions(t *testing.T) {
	store, _ := newTestStore()
	ctx := context.Background()

	for range 3 {
		_, _ = store.Save(ctx, testLoc("data.txt"), []byte("x"), "text/plain")
	}
	_, _ = store.Save(ctx, testLoc("a.txt"), []byte("a"), "text/plain")
	_, _ = store.Save(ctx, testLoc("user:profile.json"), []byte("p0"), "application/json")
	_, _ = store.Save(ctx, testLoc("user:profile.json"), []byte("p1"), "application/json")
	_, _ = store.Save(ctx, artifact.Locator{
		AppName: "app", UserID: "user-2", SessionID: "sess-1", Filename: "other.txt",
	}, []byte("o"), "text/plain")

	got, err := store.ListArtifactVersions(ctx, testScope())
	if err != nil {
		t.Fatal(err)
	}

	want := map[string][]int{
		"data.txt":          {0, 1, 2},
		"a.txt":             {0},
		"user:profile.json": {0, 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ListArtifactVersions mismatch:\n got: %v\nwant: %v", got, want)
	}

	for name, versions := range got {
		loc := testLoc(name)
		direct, err := store.ListVersions(ctx, loc)
		if err != nil {
			t.Fatalf("ListVersions(%q): %v", name, err)
		}
		if !reflect.DeepEqual(direct, versions) {
			t.Errorf("version mismatch for %q: scan=%v perArtifact=%v", name, versions, direct)
		}
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

func TestMetadataNonASCIIRoundTrip(t *testing.T) {
	store, mock := newTestStore()
	ctx := context.Background()
	loc := testLoc("slides.pptx")
	mimeType := "application/vnd.openxmlformats-officedocument.presentationml.presentation"

	v, _ := store.Save(ctx, loc, []byte("x"), mimeType)

	desc := "PowerPoint presentation: The State of AGI — Mid-2026"
	schema := map[string]any{"title": "Über résumé — déjà vu"}
	custom := map[string]any{"note": "naïve café ☕"}

	if err := store.SaveMetadata(ctx, loc, v, &artifact.VersionMetadata{
		MIMEType:    mimeType,
		Description: desc,
		Schema:      schema,
		Custom:      custom,
	}); err != nil {
		t.Fatal(err)
	}

	key := "app/user-1/sess-1/slides.pptx/" + strconv.Itoa(v)
	mock.mu.Lock()
	stored := mock.objects[key].metadata
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

func TestGetVersionMetadataDecodesEncodedWord(t *testing.T) {
	store, mock := newTestStore()
	ctx := context.Background()
	loc := testLoc("slides.pptx")

	v, _ := store.Save(ctx, loc, []byte("x"), "application/octet-stream")

	key := "app/user-1/sess-1/slides.pptx/" + strconv.Itoa(v)
	mock.mu.Lock()
	mock.objects[key].metadata["description"] = "=?utf-8?q?The_State_of_AGI?="
	mock.mu.Unlock()

	meta, err := store.GetVersionMetadata(ctx, loc, &v)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Description != "The State of AGI" {
		t.Errorf("description = %q, want decoded %q", meta.Description, "The State of AGI")
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

func TestS3ObjectMetadata(t *testing.T) {
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

func TestValidateBucket(t *testing.T) {
	store, _ := newTestStore()
	if err := store.ValidateBucket(context.Background()); err != nil {
		t.Fatal(err)
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

func TestIsPreconditionFailed(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"unrelated", io.EOF, false},
		{"precondition string", fmt.Errorf("PreconditionFailed: conditional request failed"), true},
		{"412 status", fmt.Errorf("StatusCode: 412"), true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isPreconditionFailed(tc.err); got != tc.want {
				t.Errorf("isPreconditionFailed(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestListSessionsWithArtifactsForUser_ReturnsSessionIDs(t *testing.T) {
	store, _ := newTestStore()
	ctx := context.Background()

	_, _ = store.Save(ctx, artifact.Locator{AppName: "app", UserID: "u1", SessionID: "sess-a", Filename: "file1.txt"}, []byte("x"), "text/plain")
	_, _ = store.Save(ctx, artifact.Locator{AppName: "app", UserID: "u1", SessionID: "sess-a", Filename: "file1.txt"}, []byte("y"), "text/plain")
	_, _ = store.Save(ctx, artifact.Locator{AppName: "app", UserID: "u1", SessionID: "sess-b", Filename: "file2.txt"}, []byte("z"), "text/plain")
	_, _ = store.Save(ctx, artifact.Locator{AppName: "app", UserID: "u2", SessionID: "sess-c", Filename: "file3.txt"}, []byte("w"), "text/plain")

	got, hasFilter, err := store.ListSessionsWithArtifactsForUser(ctx, "app", "u1")
	if err != nil {
		t.Fatalf("ListSessionsWithArtifactsForUser: %v", err)
	}
	if !hasFilter {
		t.Fatal("hasFilter = false, want true")
	}
	want := map[string]struct{}{"sess-a": {}, "sess-b": {}}
	if len(got) != len(want) {
		t.Fatalf("sessions = %v, want %v", got, want)
	}
	for k := range want {
		if _, ok := got[k]; !ok {
			t.Errorf("missing session %q in %v", k, got)
		}
	}
}

func TestListSessionsWithArtifactsForUser_ExcludesUserScopedSegment(t *testing.T) {
	store, _ := newTestStore()
	ctx := context.Background()

	_, _ = store.Save(ctx, artifact.Locator{AppName: "app", UserID: "u1", SessionID: "sess-a", Filename: "file1.txt"}, []byte("x"), "text/plain")
	_, _ = store.Save(ctx, artifact.Locator{AppName: "app", UserID: "u1", SessionID: "ignored", Filename: "user:profile.txt"}, []byte("p"), "text/plain")

	got, hasFilter, err := store.ListSessionsWithArtifactsForUser(ctx, "app", "u1")
	if err != nil {
		t.Fatalf("ListSessionsWithArtifactsForUser: %v", err)
	}
	if !hasFilter {
		t.Fatal("hasFilter = false, want true (sess-a is present)")
	}
	if _, ok := got["user"]; ok {
		t.Error("the literal 'user' segment must not appear as a session id")
	}
	if _, ok := got["sess-a"]; !ok {
		t.Errorf("missing sess-a in %v", got)
	}
}

func TestListSessionsWithArtifactsForUser_UserScopedOnlyReturnsNoFilter(t *testing.T) {
	store, _ := newTestStore()
	ctx := context.Background()

	_, _ = store.Save(ctx, artifact.Locator{AppName: "app", UserID: "u1", SessionID: "x", Filename: "user:a.txt"}, []byte("a"), "text/plain")
	_, _ = store.Save(ctx, artifact.Locator{AppName: "app", UserID: "u1", SessionID: "x", Filename: "user:b.txt"}, []byte("b"), "text/plain")

	got, hasFilter, err := store.ListSessionsWithArtifactsForUser(ctx, "app", "u1")
	if err != nil {
		t.Fatalf("ListSessionsWithArtifactsForUser: %v", err)
	}
	if hasFilter {
		t.Errorf("hasFilter = true, want false; sessions = %v", got)
	}
}

func TestListSessionsWithArtifactsForUser_NoArtifacts(t *testing.T) {
	store, _ := newTestStore()
	ctx := context.Background()

	got, hasFilter, err := store.ListSessionsWithArtifactsForUser(ctx, "app", "u1")
	if err != nil {
		t.Fatalf("ListSessionsWithArtifactsForUser: %v", err)
	}
	if !hasFilter {
		t.Fatal("hasFilter = false, want true; an empty user should yield an empty filter set (caller skips ALL sessions)")
	}
	if len(got) != 0 {
		t.Errorf("sessions = %v, want empty", got)
	}
}

type fakeS3ListErr struct {
	*mockS3
	err error
}

func (f *fakeS3ListErr) ListObjectsV2(_ context.Context, _ *s3.ListObjectsV2Input, _ ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	return nil, f.err
}

func TestListSessionsWithArtifactsForUser_ListError(t *testing.T) {
	want := fmt.Errorf("simulated S3 outage")
	store := New(&fakeS3ListErr{mockS3: newMockS3(), err: want}, "test-bucket")

	got, hasFilter, err := store.ListSessionsWithArtifactsForUser(context.Background(), "app", "u1")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "simulated S3 outage") {
		t.Errorf("error not wrapped: %v", err)
	}
	if hasFilter {
		t.Error("hasFilter must be false on error")
	}
	if got != nil {
		t.Errorf("sessions = %v, want nil on error", got)
	}
}

type paginatingS3 struct {
	*mockS3
	pageOne []string
	pageTwo []string
	seenTok string
}

func (p *paginatingS3) ListObjectsV2(_ context.Context, in *s3.ListObjectsV2Input, _ ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	if in.ContinuationToken == nil {
		objs := make([]types.Object, len(p.pageOne))
		for i, k := range p.pageOne {
			objs[i] = types.Object{Key: aws.String(k)}
		}
		return &s3.ListObjectsV2Output{
			Contents:              objs,
			IsTruncated:           aws.Bool(true),
			NextContinuationToken: aws.String("page-2"),
		}, nil
	}
	p.seenTok = *in.ContinuationToken
	objs := make([]types.Object, len(p.pageTwo))
	for i, k := range p.pageTwo {
		objs[i] = types.Object{Key: aws.String(k)}
	}
	return &s3.ListObjectsV2Output{Contents: objs, IsTruncated: aws.Bool(false)}, nil
}

func TestListSessionsWithArtifactsForUser_Paginates(t *testing.T) {
	p := &paginatingS3{
		mockS3:  newMockS3(),
		pageOne: []string{"app/u1/sess-a/file/0", "app/u1/sess-b/file/0"},
		pageTwo: []string{"app/u1/sess-c/file/0"},
	}
	store := New(p, "test-bucket")

	got, hasFilter, err := store.ListSessionsWithArtifactsForUser(context.Background(), "app", "u1")
	if err != nil {
		t.Fatalf("ListSessionsWithArtifactsForUser: %v", err)
	}
	if !hasFilter || len(got) != 3 {
		t.Fatalf("expected 3 sessions across two pages; got hasFilter=%v sessions=%v", hasFilter, got)
	}
	if p.seenTok != "page-2" {
		t.Errorf("continuation token not propagated; seen=%q", p.seenTok)
	}
	for _, s := range []string{"sess-a", "sess-b", "sess-c"} {
		if _, ok := got[s]; !ok {
			t.Errorf("missing session %q", s)
		}
	}
}

type shortKeyS3 struct{ *mockS3 }

func (s *shortKeyS3) ListObjectsV2(_ context.Context, _ *s3.ListObjectsV2Input, _ ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	return &s3.ListObjectsV2Output{
		Contents: []types.Object{
			{Key: aws.String("app/u1/sess-a/file/0")},
			{Key: aws.String("app/u1/garbage")},
			{Key: nil},
		},
		IsTruncated: aws.Bool(false),
	}, nil
}

func TestListSessionsWithArtifactsForUser_SkipsMalformedKeys(t *testing.T) {
	store := New(&shortKeyS3{newMockS3()}, "test-bucket")

	got, hasFilter, err := store.ListSessionsWithArtifactsForUser(context.Background(), "app", "u1")
	if err != nil {
		t.Fatalf("ListSessionsWithArtifactsForUser: %v", err)
	}
	if !hasFilter || len(got) != 1 {
		t.Fatalf("expected 1 valid session; got hasFilter=%v sessions=%v", hasFilter, got)
	}
	if _, ok := got["sess-a"]; !ok {
		t.Errorf("missing sess-a; got %v", got)
	}
}

func TestListSessionsWithArtifactsForUser_SanitizesAppAndUser(t *testing.T) {
	store, mock := newTestStore()
	ctx := context.Background()

	_, _ = store.Save(ctx, artifact.Locator{AppName: "app", UserID: "u1", SessionID: "sess-a", Filename: "file.txt"}, []byte("x"), "text/plain")

	got, hasFilter, err := store.ListSessionsWithArtifactsForUser(ctx, "app/../evil", "u1")
	if err != nil {
		t.Fatalf("ListSessionsWithArtifactsForUser: %v", err)
	}
	if !hasFilter {
		t.Errorf("hasFilter = false, want true")
	}
	if len(got) != 0 {
		t.Errorf("sessions = %v, want empty (different sanitized prefix)", got)
	}

	got, hasFilter, err = store.ListSessionsWithArtifactsForUser(ctx, "app", "u1")
	if err != nil || !hasFilter || len(got) != 1 {
		t.Errorf("expected single session for valid prefix; got=%v hasFilter=%v err=%v", got, hasFilter, err)
	}
	_ = mock
}

type racingS3 struct {
	*mockS3
	beforeFirstPut func()
}

func (r *racingS3) PutObject(ctx context.Context, params *s3.PutObjectInput, opts ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	if hook := r.beforeFirstPut; hook != nil {
		r.beforeFirstPut = nil
		hook()
	}
	return r.mockS3.PutObject(ctx, params, opts...)
}

func TestSaveWhenAnotherStoreSavesTheSameVersionFirst(t *testing.T) {
	mock := newMockS3()
	racing := &racingS3{mockS3: mock}
	first, second := New(racing, "test-bucket"), New(mock, "test-bucket")
	ctx := t.Context()
	loc := testLoc("shared.txt")
	racing.beforeFirstPut = func() {
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
