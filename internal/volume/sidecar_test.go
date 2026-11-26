// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package volume

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteLinkedSidecar_WritesRestrictedJSON(t *testing.T) {
	dir := t.TempDir()
	if err := writeLinkedSidecar(dir, "vol-abc", "/host/work", "/host/home/work"); err != nil {
		t.Fatalf("writeLinkedSidecar: %v", err)
	}

	path := filepath.Join(dir, sidecarFilename)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat sidecar: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("sidecar mode = %v, want 0o600 (host path is sensitive)", got)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var sc sidecar
	if err := json.Unmarshal(data, &sc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if sc.ID != "vol-abc" || !sc.Linked ||
		sc.ExternalPath != "/host/work" || sc.ExternalPathContainer != "/host/home/work" {
		t.Errorf("sidecar payload mismatch: %+v", sc)
	}
}

func TestWriteLinkedSidecar_MissingDirReturnsError(t *testing.T) {
	tmp := t.TempDir()
	missing := filepath.Join(tmp, "does", "not", "exist")
	err := writeLinkedSidecar(missing, "vol-x", "/host", "/host/home")
	if err == nil {
		t.Fatal("expected error when volume dir is missing")
	}
}

func TestReadSidecar_AbsentReturnsNilNil(t *testing.T) {
	dir := t.TempDir()
	sc, err := readSidecar(dir)
	if err != nil {
		t.Fatalf("readSidecar absent: %v", err)
	}
	if sc != nil {
		t.Errorf("expected nil sidecar, got %+v", sc)
	}
}

func TestReadSidecar_MalformedJSON(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, sidecarFilename), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	sc, err := readSidecar(dir)
	if err == nil {
		t.Fatalf("expected parse error, got sidecar=%+v", sc)
	}
}

func TestSidecar_WriteReadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	const (
		id        = "vol-roundtrip"
		host      = "/Users/me/work"
		container = "/host/home/me/work"
	)
	if err := writeLinkedSidecar(dir, id, host, container); err != nil {
		t.Fatalf("write: %v", err)
	}
	sc, err := readSidecar(dir)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if sc == nil {
		t.Fatal("expected sidecar, got nil")
	}
	if sc.ID != id || !sc.Linked || sc.ExternalPath != host || sc.ExternalPathContainer != container {
		t.Errorf("round-trip mismatch: %+v", sc)
	}
}
