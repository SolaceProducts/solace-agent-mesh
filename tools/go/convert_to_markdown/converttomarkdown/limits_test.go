// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package converttomarkdown

import (
	"archive/zip"
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func buildZipBomb(t *testing.T, entries []zipBombEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		w, err := zw.Create(e.name)
		if err != nil {
			t.Fatalf("zip create %s: %v", e.name, err)
		}
		filler := bytes.Repeat([]byte{'a'}, 64*1024)
		remaining := e.size
		for remaining > 0 {
			n := len(filler)
			if int64(n) > remaining {
				n = int(remaining)
			}
			if _, err := w.Write(filler[:n]); err != nil {
				t.Fatalf("zip write %s: %v", e.name, err)
			}
			remaining -= int64(n)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

type zipBombEntry struct {
	name string
	size int64
}

func withLoweredCaps(t *testing.T, perEntry, total int64) {
	t.Helper()
	origEntry := maxUncompressedBytesPerEntry
	origTotal := maxUncompressedBytesTotal
	maxUncompressedBytesPerEntry = perEntry
	maxUncompressedBytesTotal = total
	t.Cleanup(func() {
		maxUncompressedBytesPerEntry = origEntry
		maxUncompressedBytesTotal = origTotal
	})
}

func TestDOCXZipBomb_PerEntryCapExceeded(t *testing.T) {
	withLoweredCaps(t, 1024*1024, 10*1024*1024)

	bomb := buildZipBomb(t, []zipBombEntry{
		{name: "word/document.xml", size: 2 * 1024 * 1024},
	})

	_, err := convertDOCXToMarkdown(bomb)
	if err == nil {
		t.Fatal("expected error for oversized docx entry, got nil")
	}
	if !strings.Contains(err.Error(), "exceeds limit") {
		t.Fatalf("expected 'exceeds limit' in error, got %q", err.Error())
	}
}

func TestPPTXZipBomb_TotalBudgetExceeded(t *testing.T) {
	withLoweredCaps(t, 5*1024*1024, 6*1024*1024)

	var entries []zipBombEntry
	for i := range 3 {
		entries = append(entries, zipBombEntry{
			name: fmt.Sprintf("ppt/slides/slide%d.xml", i+1),
			size: 3 * 1024 * 1024,
		})
	}
	bomb := buildZipBomb(t, entries)

	_, err := convertPPTXToMarkdown(bomb)
	if err == nil {
		t.Fatal("expected error for pptx total budget breach, got nil")
	}
	if !strings.Contains(err.Error(), "exceeds limit") {
		t.Fatalf("expected 'exceeds limit' in error, got %q", err.Error())
	}
}

func TestEPUBZipBomb_PerEntryCapExceeded(t *testing.T) {
	withLoweredCaps(t, 1024*1024, 10*1024*1024)

	bomb := buildZipBomb(t, []zipBombEntry{
		{name: "META-INF/container.xml", size: 2 * 1024 * 1024},
	})

	_, err := convertEPUBToMarkdown(bomb)
	if err == nil {
		t.Fatal("expected error for oversized epub entry, got nil")
	}
	if !strings.Contains(err.Error(), "exceeds limit") {
		t.Fatalf("expected 'exceeds limit' in error, got %q", err.Error())
	}
}
