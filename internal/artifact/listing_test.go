// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package artifact

import (
	"reflect"
	"testing"
)

func TestUniqueArtifactNames(t *testing.T) {
	prefix := "app/alice/sess/"
	keys := []string{
		"app/alice/sess/report.pdf/0",
		"app/alice/sess/report.pdf/1",
		"app/alice/sess/chart.png/0",
		"app/alice/sess/notes.txt/0",
		"app/alice/sess/notes.txt/1",
		"app/alice/other/foo.bin/0",
		"app/alice/sess/",
		"app/alice/sess/ghost.docx/",
		"app/alice/sess/stray",
	}

	got := UniqueArtifactNames(prefix, keys)
	want := []string{"chart.png", "notes.txt", "report.pdf"}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("UniqueArtifactNames mismatch:\n got: %v\nwant: %v", got, want)
	}
}

func TestUniqueArtifactNames_Empty(t *testing.T) {
	if got := UniqueArtifactNames("p/", nil); got != nil {
		t.Fatalf("expected nil for empty input, got %v", got)
	}
}

func TestArtifactVersionsByName(t *testing.T) {
	prefix := "app/alice/sess/"
	keys := []string{
		"app/alice/sess/report.pdf/0",
		"app/alice/sess/report.pdf/2",
		"app/alice/sess/report.pdf/1",
		"app/alice/sess/chart.png/0",
		"app/alice/other/foo.bin/0",
		"app/alice/sess/ghost.docx/",
		"app/alice/sess/weird.bin/latest",
		"app/alice/sess/stray",
	}

	got := ArtifactVersionsByName(prefix, keys)
	want := map[string][]int{
		"report.pdf": {0, 1, 2},
		"chart.png":  {0},
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ArtifactVersionsByName mismatch:\n got: %v\nwant: %v", got, want)
	}
}

func TestArtifactVersionsByName_Empty(t *testing.T) {
	got := ArtifactVersionsByName("p/", nil)
	if len(got) != 0 {
		t.Fatalf("expected empty map for empty input, got %v", got)
	}
}
