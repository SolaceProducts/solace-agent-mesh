// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package desktop

import (
	"runtime"
	"strings"
	"testing"
)

func TestPlatformRefusal(t *testing.T) {
	cases := []struct {
		name         string
		facts        platformFacts
		wantContains string
	}{
		{
			name:  "darwin modern arm64 supported",
			facts: platformFacts{OS: "darwin", Arch: "arm64", OSVersion: "14.5"},
		},
		{
			name:  "darwin at the floor supported",
			facts: platformFacts{OS: "darwin", Arch: "amd64", OSVersion: "11.0"},
		},
		{
			name:  "darwin big sur legacy alias supported",
			facts: platformFacts{OS: "darwin", Arch: "amd64", OSVersion: "10.16"},
		},
		{
			name:         "darwin below floor refused naming minimum",
			facts:        platformFacts{OS: "darwin", Arch: "amd64", OSVersion: "10.15.7"},
			wantContains: "requires macOS 11",
		},
		{
			name:         "darwin rosetta refused even on new macOS",
			facts:        platformFacts{OS: "darwin", Arch: "amd64", OSVersion: "15.1", Translated: true},
			wantContains: "Apple silicon build",
		},
		{
			name:  "darwin unknown version passes",
			facts: platformFacts{OS: "darwin", Arch: "arm64"},
		},
		{
			name:  "darwin unparseable version passes",
			facts: platformFacts{OS: "darwin", Arch: "arm64", OSVersion: "beta"},
		},
		{
			name:  "windows 10 supported",
			facts: platformFacts{OS: "windows", Arch: "amd64", OSVersion: "10.0 (build 19045)"},
		},
		{
			name:         "windows 8.1 refused naming minimum",
			facts:        platformFacts{OS: "windows", Arch: "amd64", OSVersion: "6.3 (build 9600)"},
			wantContains: "requires Windows 10",
		},
		{
			name:         "windows arm emulation refused even on windows 11",
			facts:        platformFacts{OS: "windows", Arch: "amd64", OSVersion: "10.0 (build 22631)", Translated: true},
			wantContains: "Arm-based Windows PC",
		},
		{
			name:  "windows unknown version passes",
			facts: platformFacts{OS: "windows", Arch: "amd64"},
		},
		{
			name:  "linux always passes",
			facts: platformFacts{OS: "linux", Arch: "arm64", OSVersion: "6.8"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			refusal := platformRefusal(tc.facts)
			if tc.wantContains == "" {
				if refusal != "" {
					t.Fatalf("expected supported, got refusal %q", refusal)
				}
				return
			}
			if refusal == "" {
				t.Fatalf("expected refusal containing %q, got supported", tc.wantContains)
			}
			if !strings.Contains(refusal, tc.wantContains) {
				t.Fatalf("refusal %q does not contain %q", refusal, tc.wantContains)
			}
		})
	}
}

func TestParseMajorMinor(t *testing.T) {
	cases := []struct {
		version   string
		wantMajor int
		wantMinor int
	}{
		{"14.5", 14, 5},
		{"11", 11, 0},
		{"11.0", 11, 0},
		{"10.0.19045", 10, 0},
		{"10.0 (build 19045)", 10, 0},
		{"", 0, 0},
		{"beta", 0, 0},
		{"-1.2", 0, 0},
		{"10.x", 10, 0},
	}
	for _, tc := range cases {
		major, minor := parseMajorMinor(tc.version)
		if major != tc.wantMajor || minor != tc.wantMinor {
			t.Errorf("parseMajorMinor(%q) = (%d, %d), want (%d, %d)", tc.version, major, minor, tc.wantMajor, tc.wantMinor)
		}
	}
}

func TestDarwinMajor(t *testing.T) {
	cases := []struct {
		version string
		want    int
	}{
		{"10.16", 11},
		{"10.15", 10},
		{"12.3", 12},
		{"11.0", 11},
		{"", 0},
	}
	for _, tc := range cases {
		if got := darwinMajor(tc.version); got != tc.want {
			t.Errorf("darwinMajor(%q) = %d, want %d", tc.version, got, tc.want)
		}
	}
}

func TestCollectPlatformFactsReportsHost(t *testing.T) {
	f := collectPlatformFacts()
	t.Logf("collected facts: %+v", f)
	if f.OS != runtime.GOOS {
		t.Fatalf("OS = %q, want %q", f.OS, runtime.GOOS)
	}
	if f.Arch != runtime.GOARCH {
		t.Fatalf("Arch = %q, want %q", f.Arch, runtime.GOARCH)
	}
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		if major, _ := parseMajorMinor(f.OSVersion); major <= 0 {
			t.Fatalf("expected a parseable OS version on %s, got %q", runtime.GOOS, f.OSVersion)
		}
		refusal := platformRefusal(f)
		if f.Translated {
			if refusal == "" {
				t.Fatal("translated process should be refused")
			}
		} else if refusal != "" {
			t.Fatalf("native host running this test should be supported, got refusal %q", refusal)
		}
	}
}
