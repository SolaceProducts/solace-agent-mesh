// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package adrscan

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestV1TimestampFieldRE(t *testing.T) {
	dir := t.TempDir()
	src := "package x\n" +
		"type a struct {\n" +
		"\tCreatedAt   int64 `json:\"createdAt\"`\n" +
		"\tUpdatedAt   int64 `json:\"updatedAt,omitempty\"`\n" +
		"\tCreatedTime int64 `json:\"createdTime\"`\n" +
		"}\n"
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}

	got := scan(t, dir, v1TimestampFieldRE, nil)
	want := []string{`a.go:3 — json:"createdAt"`, `a.go:4 — json:"updatedAt,omitempty"`}
	if !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}
