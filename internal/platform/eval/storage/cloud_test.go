// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package storage

import (
	"errors"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/objectstore"
)

func TestObjectStoreAdapter_RoundtripAndNotFound(t *testing.T) {
	t.Parallel()
	c, err := NewFromObjectStore(objectstore.NewMemoryClient())
	if err != nil {
		t.Fatalf("NewFromObjectStore: %v", err)
	}

	if putErr := c.PutObject("a/b.json", []byte(`{"k":1}`)); putErr != nil {
		t.Fatalf("PutObject: %v", putErr)
	}
	body, getErr := c.GetObject("a/b.json")
	if getErr != nil {
		t.Fatalf("GetObject: %v", getErr)
	}
	if string(body) != `{"k":1}` {
		t.Errorf("body = %q, want %q", body, `{"k":1}`)
	}

	if _, missingErr := c.GetObject("does/not/exist"); !errors.Is(missingErr, ErrNotFound) {
		t.Errorf("expected ErrNotFound for missing key, got %v", missingErr)
	}
}

func TestObjectStoreAdapter_RejectsEmptyKey(t *testing.T) {
	t.Parallel()
	c, err := NewFromObjectStore(objectstore.NewMemoryClient())
	if err != nil {
		t.Fatalf("NewFromObjectStore: %v", err)
	}
	if putErr := c.PutObject("", []byte("body")); putErr == nil {
		t.Error("expected error for empty key")
	}
}

func TestObjectStoreAdapter_DeletePrefix(t *testing.T) {
	t.Parallel()
	c, err := NewFromObjectStore(objectstore.NewMemoryClient())
	if err != nil {
		t.Fatalf("NewFromObjectStore: %v", err)
	}
	for _, k := range []string{"runs/A/x.json", "runs/A/y.json", "runs/B/z.json"} {
		if putErr := c.PutObject(k, []byte("body")); putErr != nil {
			t.Fatalf("PutObject %s: %v", k, putErr)
		}
	}
	n, delErr := c.DeletePrefix(t.Context(), "runs/A/")
	if delErr != nil {
		t.Fatalf("DeletePrefix: %v", delErr)
	}
	if n != 2 {
		t.Errorf("deleted = %d, want 2", n)
	}
	if _, getErr := c.GetObject("runs/A/x.json"); !errors.Is(getErr, ErrNotFound) {
		t.Errorf("runs/A/x.json should be gone, got %v", getErr)
	}
	if _, getErr := c.GetObject("runs/B/z.json"); getErr != nil {
		t.Errorf("runs/B/z.json should survive, got %v", getErr)
	}
}

func TestNewFromObjectStore_NilRejected(t *testing.T) {
	t.Parallel()
	if _, err := NewFromObjectStore(nil); err == nil {
		t.Error("expected error from nil client")
	}
}
