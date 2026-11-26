// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package runtime

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestDumpDiagnostics(t *testing.T) {
	pid := os.Getpid()
	patterns := []string{
		fmt.Sprintf("sam-awe-memstats-%d-*.json", pid),
		fmt.Sprintf("sam-awe-heap-%d-*.pprof", pid),
		fmt.Sprintf("sam-awe-goroutines-%d-*.txt", pid),
	}
	for _, p := range patterns {
		for _, f := range globTemp(t, p) {
			_ = os.Remove(f)
		}
	}

	DumpDiagnostics(discardLogger())

	for _, p := range patterns {
		matches := globTemp(t, p)
		if len(matches) == 0 {
			t.Errorf("no file matching %q after DumpDiagnostics", p)
		}
		for _, f := range matches {
			t.Cleanup(func() { _ = os.Remove(f) })
		}
	}
}

func globTemp(t *testing.T, pattern string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(os.TempDir(), pattern))
	if err != nil {
		t.Fatalf("glob %q: %v", pattern, err)
	}
	return matches
}
