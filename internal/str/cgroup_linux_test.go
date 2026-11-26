// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build linux

package str

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadCgroupMemoryValue(t *testing.T) {
	cases := []struct {
		name     string
		contents string
		want     int64
		wantOK   bool
	}{
		{"plain bytes", "2147483648\n", 2147483648, true},
		{"no trailing newline", "1073741824", 1073741824, true},
		{"cgroup v2 unlimited", "max\n", 0, false},
		{"empty", "", 0, false},
		{"non-numeric", "garbage", 0, false},
		{"zero", "0", 0, false},
		{"negative", "-1", 0, false},
		{"v1 unlimited sentinel", "9223372036854771712", 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "memlimit")
			if err := os.WriteFile(path, []byte(tc.contents), 0o600); err != nil {
				t.Fatalf("write: %v", err)
			}
			got, ok := readCgroupMemoryValue(path)
			if got != tc.want || ok != tc.wantOK {
				t.Errorf("readCgroupMemoryValue(%q) = (%d, %v), want (%d, %v)", tc.contents, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

func TestReadCgroupMemoryValue_MissingFile(t *testing.T) {
	got, ok := readCgroupMemoryValue(filepath.Join(t.TempDir(), "does-not-exist"))
	if got != 0 || ok {
		t.Errorf("missing file = (%d, %v), want (0, false)", got, ok)
	}
}
