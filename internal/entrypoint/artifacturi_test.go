// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import "testing"

func TestParseArtifactURI(t *testing.T) {
	cases := []struct {
		name     string
		uri      string
		wantName string
		wantVer  *int
		wantOK   bool
	}{
		{"valid no version", "artifact://app/user/sess/report.csv", "report.csv", nil, true},
		{"valid with version", "artifact://app/user/sess/data.json?version=4", "data.json", intptr(4), true},
		{"percent-encoded filename", "artifact://app/u/s/Financial%20Sample.xlsx", "Financial Sample.xlsx", nil, true},
		{"percent-encoded special chars", "artifact://app/user/sess/file%26name%3D1.txt", "file&name=1.txt", nil, true},
		{"non-artifact scheme", "https://example.com/x.png", "", nil, false},
		{"empty URI", "", "", nil, false},
		{"missing host", "artifact:///u/s/f.txt", "", nil, false},
		{"too few path segments", "artifact://app/onlyone", "", nil, false},
		{"trailing slash empty leaf", "artifact://app/user/sess/", "", nil, false},
		{"bad percent escape", "artifact://app/u/s/bad%ZZname", "", nil, false},
		{"unparseable", "://::::", "", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotName, gotVer, gotOK := ParseArtifactURI(tc.uri)
			if gotOK != tc.wantOK {
				t.Fatalf("ok = %v, want %v", gotOK, tc.wantOK)
			}
			if gotName != tc.wantName {
				t.Errorf("name = %q, want %q", gotName, tc.wantName)
			}
			if (gotVer == nil) != (tc.wantVer == nil) || (gotVer != nil && *gotVer != *tc.wantVer) {
				t.Errorf("version = %v, want %v", gotVer, tc.wantVer)
			}
		})
	}
}

func TestDataIntPtr(t *testing.T) {
	if got := DataIntPtr(float64(7)); got == nil || *got != 7 {
		t.Errorf("float64 → %v, want 7", got)
	}
	if got := DataIntPtr(int(3)); got == nil || *got != 3 {
		t.Errorf("int → %v, want 3", got)
	}
	if got := DataIntPtr(int64(9)); got == nil || *got != 9 {
		t.Errorf("int64 → %v, want 9", got)
	}
	if got := DataIntPtr("42"); got == nil || *got != 42 {
		t.Errorf("string numeric → %v, want 42", got)
	}
	if got := DataIntPtr("not a number"); got != nil {
		t.Errorf("string non-numeric → %v, want nil", got)
	}
	if got := DataIntPtr(nil); got != nil {
		t.Errorf("nil → %v, want nil", got)
	}
	if got := DataIntPtr(true); got != nil {
		t.Errorf("bool → %v, want nil", got)
	}
}

func intptr(n int) *int { return &n }
