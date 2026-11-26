// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package artifact

import (
	"context"
	"testing"
)

func TestCopyLatest_Basic(t *testing.T) {
	ctx := context.Background()
	store := newMockStore()

	from := Locator{AppName: "app", UserID: "u1", SessionID: "src", Filename: "hello.txt"}
	to := Locator{AppName: "app", UserID: "u1", SessionID: "dst", Filename: "hello.txt"}

	if _, err := store.Save(ctx, from, []byte("hello world"), "text/plain"); err != nil {
		t.Fatal(err)
	}

	if err := CopyLatest(ctx, store, from, to); err != nil {
		t.Fatalf("CopyLatest failed: %v", err)
	}

	content, mime, err := store.Load(ctx, to, nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "hello world" {
		t.Errorf("expected 'hello world', got %q", string(content))
	}
	if mime != "text/plain" {
		t.Errorf("expected text/plain, got %s", mime)
	}
}

func TestCopyLatest_CopiesLatestVersion(t *testing.T) {
	ctx := context.Background()
	store := newMockStore()

	from := Locator{AppName: "app", UserID: "u1", SessionID: "src", Filename: "data.txt"}
	to := Locator{AppName: "app", UserID: "u1", SessionID: "dst", Filename: "data.txt"}

	_, _ = store.Save(ctx, from, []byte("v0"), "text/plain")
	_, _ = store.Save(ctx, from, []byte("v1"), "text/plain")
	_, _ = store.Save(ctx, from, []byte("v2-latest"), "text/csv")

	if err := CopyLatest(ctx, store, from, to); err != nil {
		t.Fatalf("CopyLatest failed: %v", err)
	}

	content, mime, _ := store.Load(ctx, to, nil)
	if string(content) != "v2-latest" {
		t.Errorf("expected 'v2-latest', got %q", string(content))
	}
	if mime != "text/csv" {
		t.Errorf("expected text/csv, got %s", mime)
	}
}

func TestCopyLatest_SourceNotFound(t *testing.T) {
	ctx := context.Background()
	store := newMockStore()

	from := Locator{AppName: "app", UserID: "u1", SessionID: "src", Filename: "missing.txt"}
	to := Locator{AppName: "app", UserID: "u1", SessionID: "dst", Filename: "missing.txt"}

	err := CopyLatest(ctx, store, from, to)
	if err == nil {
		t.Fatal("expected error for missing source artifact")
	}
	if got := err.Error(); got != `source artifact "missing.txt" not found` {
		t.Errorf("unexpected error: %s", got)
	}
}

func TestCopyLatest_WithMetadata(t *testing.T) {
	ctx := context.Background()
	store := newMetadataMockStore()

	from := Locator{AppName: "app", UserID: "u1", SessionID: "src", Filename: "report.csv"}
	to := Locator{AppName: "app", UserID: "u1", SessionID: "dst", Filename: "report.csv"}

	ver, _ := store.Save(ctx, from, []byte("a,b\n1,2"), "text/csv")
	meta := &VersionMetadata{
		MIMEType:    "text/csv",
		SizeBytes:   7,
		Description: "Test CSV",
	}
	_ = store.SaveMetadata(ctx, from, ver, meta)

	if err := CopyLatest(ctx, store, from, to); err != nil {
		t.Fatalf("CopyLatest failed: %v", err)
	}

	content, _, _ := store.Load(ctx, to, nil)
	if string(content) != "a,b\n1,2" {
		t.Errorf("expected content 'a,b\\n1,2', got %q", string(content))
	}

	destMeta, err := store.GetVersionMetadata(ctx, to, nil)
	if err != nil {
		t.Fatal(err)
	}
	if destMeta == nil {
		t.Fatal("expected metadata to be copied")
	}
	if destMeta.Description != "Test CSV" {
		t.Errorf("expected description 'Test CSV', got %q", destMeta.Description)
	}
	if destMeta.MIMEType != "text/csv" {
		t.Errorf("expected mime text/csv, got %s", destMeta.MIMEType)
	}
}

func TestCopyLatest_NoMetadata(t *testing.T) {
	ctx := context.Background()
	store := newMetadataMockStore()

	from := Locator{AppName: "app", UserID: "u1", SessionID: "src", Filename: "plain.txt"}
	to := Locator{AppName: "app", UserID: "u1", SessionID: "dst", Filename: "plain.txt"}

	_, _ = store.Save(ctx, from, []byte("no meta"), "text/plain")

	if err := CopyLatest(ctx, store, from, to); err != nil {
		t.Fatalf("CopyLatest failed: %v", err)
	}

	content, _, _ := store.Load(ctx, to, nil)
	if string(content) != "no meta" {
		t.Errorf("expected 'no meta', got %q", string(content))
	}
}

type metadataMockStore struct {
	mockStore
	meta map[string]*VersionMetadata
}

func newMetadataMockStore() *metadataMockStore {
	return &metadataMockStore{
		mockStore: mockStore{
			data: make(map[string][][]byte),
			mime: make(map[string][]string),
		},
		meta: make(map[string]*VersionMetadata),
	}
}

func (m *metadataMockStore) metaKey(loc Locator, version int) string {
	return m.key(loc) + "/" + string(rune(version+'0'))
}

func (m *metadataMockStore) SaveMetadata(_ context.Context, loc Locator, version int, meta *VersionMetadata) error {
	m.meta[m.metaKey(loc, version)] = meta
	return nil
}

func (m *metadataMockStore) GetVersionMetadata(_ context.Context, loc Locator, version *int) (*VersionMetadata, error) {
	k := m.key(loc)
	v := len(m.data[k]) - 1
	if version != nil {
		v = *version
	}
	if v < 0 {
		return nil, nil
	}
	return m.meta[m.metaKey(loc, v)], nil
}
