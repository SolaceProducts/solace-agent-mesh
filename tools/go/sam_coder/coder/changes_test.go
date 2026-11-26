// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package coder

import (
	"fmt"
	"strings"
	"testing"
)

func TestChangesFoldsABulkDirectoryAndKeepsTheRealEdit(t *testing.T) {
	var files []string
	var diff strings.Builder
	for i := range 300 {
		path := fmt.Sprintf(".gocache/build/%02x/entry-%d", i%256, i)
		files = append(files, "A "+path)
		fmt.Fprintf(&diff, "diff --git a/%s b/%s\n+cache line %d\n", path, path, i)
	}
	files = append(files, "M mesh/tool/main.go")
	diff.WriteString("diff --git a/mesh/tool/main.go b/mesh/tool/main.go\n-old\n+new")

	list, folded := fileList(files)
	if !strings.Contains(list, "M mesh/tool/main.go") {
		t.Fatalf("real edit missing from list:\n%s", list)
	}
	if !strings.Contains(list, ".gocache/: 300 changed files not listed") || strings.Contains(list, "entry-") {
		t.Fatalf("cache not folded:\n%s", list)
	}
	got := withoutDirs(diff.String(), folded)
	if got != "diff --git a/mesh/tool/main.go b/mesh/tool/main.go\n-old\n+new" {
		t.Fatalf("diff not filtered to the real edit:\n%s", got)
	}
}

func TestChangesListsEveryFileWhenFew(t *testing.T) {
	list, folded := fileList([]string{"M a.go", "A b/c.go"})
	if list != "M a.go\nA b/c.go" || folded != nil {
		t.Fatalf("got %q %v", list, folded)
	}
}
