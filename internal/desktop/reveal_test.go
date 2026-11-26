// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package desktop

import (
	"runtime"
	"slices"
	"testing"
)

func TestOpenFolderCommandPerPlatform(t *testing.T) {
	tests := []struct {
		goos     string
		wantName string
		wantArgs []string
	}{
		{"darwin", "open", []string{"/d/diagnostics"}},
		{"windows", "explorer", []string{"/d/diagnostics"}},
		{"linux", "xdg-open", []string{"/d/diagnostics"}},
	}
	for _, tt := range tests {
		name, args := openFolderCommand(tt.goos, "/d/diagnostics")
		if name != tt.wantName || !slices.Equal(args, tt.wantArgs) {
			t.Errorf("openFolderCommand(%s) = %s %v, want %s %v", tt.goos, name, args, tt.wantName, tt.wantArgs)
		}
	}
}

func TestOpenFolderRunsResolvedCommand(t *testing.T) {
	var gotName string
	var gotArgs []string
	orig := startFileManager
	startFileManager = func(name string, args ...string) error {
		gotName, gotArgs = name, args
		return nil
	}
	t.Cleanup(func() { startFileManager = orig })

	if err := openFolder("/d/diagnostics"); err != nil {
		t.Fatal(err)
	}
	wantName, wantArgs := openFolderCommand(runtime.GOOS, "/d/diagnostics")
	if gotName != wantName || !slices.Equal(gotArgs, wantArgs) {
		t.Errorf("openFolder ran %s %v, want %s %v", gotName, gotArgs, wantName, wantArgs)
	}
}
