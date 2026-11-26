// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package artifact

import "testing"

func TestSplitVersionRef(t *testing.T) {
	tests := []struct {
		ref         string
		wantName    string
		wantVersion int
	}{
		{"data.csv", "data.csv", -1},
		{"data.csv:3", "data.csv", 3},
		{"data.csv:0", "data.csv", 0},
		{"data.csv:latest", "data.csv", -1},
		{"file:with:colons:5", "file:with:colons", 5},
		{"meeting 10:30.txt", "meeting 10:30.txt", -1},
		{"data.csv:3abc", "data.csv:3abc", -1},
		{"data.csv:-1", "data.csv:-1", -1},
		{"noversion:", "noversion:", -1},
		{"user:prefs.json", "user:prefs.json", -1},
	}
	for _, tt := range tests {
		name, version := SplitVersionRef(tt.ref)
		got := -1
		if version != nil {
			got = *version
		}
		if name != tt.wantName || got != tt.wantVersion {
			t.Errorf("SplitVersionRef(%q) = %q, %d; want %q, %d", tt.ref, name, got, tt.wantName, tt.wantVersion)
		}
	}
}

func TestFormatVersionRefRoundTrips(t *testing.T) {
	for _, name := range []string{"report.json", "file:with:colons", "meeting 10:30.txt"} {
		gotName, gotVersion := SplitVersionRef(FormatVersionRef(name, 7))
		if gotName != name || gotVersion == nil || *gotVersion != 7 {
			t.Errorf("round trip of %q gave %q, %v", name, gotName, gotVersion)
		}
	}
}
