// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package storage

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestMemoryRoundtrip(t *testing.T) {
	t.Parallel()
	m := NewMemory()
	if err := m.PutObject("a/b.json", []byte(`{"k":1}`)); err != nil {
		t.Fatalf("put: %v", err)
	}
	body, err := m.GetObject("a/b.json")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if string(body) != `{"k":1}` {
		t.Fatalf("got body %q", body)
	}
	if _, err := m.GetObject("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestMemoryDeletePrefix(t *testing.T) {
	t.Parallel()
	m := NewMemory()
	for _, k := range []string{"runs/A/x.json", "runs/A/y.json", "runs/B/z.json"} {
		if err := m.PutObject(k, []byte("body")); err != nil {
			t.Fatalf("put %s: %v", k, err)
		}
	}
	n, err := m.DeletePrefix(t.Context(), "runs/A/")
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if n != 2 {
		t.Fatalf("delete count = %d, want 2", n)
	}
	if _, err := m.GetObject("runs/A/x.json"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected runs/A/x.json gone, got %v", err)
	}
	if _, err := m.GetObject("runs/B/z.json"); err != nil {
		t.Fatalf("runs/B/z.json must survive prefix delete: %v", err)
	}
}

func TestFilesystemRoundtrip(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	fs, err := NewFilesystem(root)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if perr := fs.PutObject("ns/eval/runs/A/x.json", []byte("hello")); perr != nil {
		t.Fatalf("put: %v", perr)
	}
	body, err := fs.GetObject("ns/eval/runs/A/x.json")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if string(body) != "hello" {
		t.Fatalf("body mismatch: %q", body)
	}
	if _, err := fs.GetObject("missing/x.json"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestFilesystemDeletePrefix(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	fs, err := NewFilesystem(root)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	for _, k := range []string{"runs/A/x.json", "runs/A/y.json", "runs/B/z.json"} {
		if perr := fs.PutObject(k, []byte("body")); perr != nil {
			t.Fatalf("put %s: %v", k, perr)
		}
	}
	n, err := fs.DeletePrefix(t.Context(), "runs/A/")
	if err != nil {
		t.Fatalf("delete prefix: %v", err)
	}
	if n != 2 {
		t.Fatalf("count = %d, want 2", n)
	}
	if _, err := fs.GetObject("runs/B/z.json"); err != nil {
		t.Fatalf("B/z.json must survive: %v", err)
	}
}

func TestFilesystemRejectsTraversal(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	fs, err := NewFilesystem(root)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if err := fs.PutObject("../escape.json", []byte("body")); err == nil {
		t.Fatalf("expected error for path traversal key")
	}
	if _, err := fs.GetObject(filepath.Join("..", "escape.json")); err == nil {
		t.Fatalf("expected error reading via traversal")
	}
}
