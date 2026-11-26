// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package strutil

import "testing"

func TestTruncate(t *testing.T) {
	tests := []struct {
		name   string
		s      string
		max    int
		suffix string
		want   string
	}{
		{"no truncation needed", "hello", 10, "...", "hello"},
		{"exact length", "hello", 5, "...", "hello"},
		{"simple ASCII truncation", "hello world", 5, "...", "hello..."},
		{"empty suffix", "hello world", 5, "", "hello"},
		{"multi-byte: don't split 2-byte rune", "café", 4, "...", "caf..."},
		{"multi-byte: don't split 3-byte rune", "日本語", 4, "...", "日..."},
		{"multi-byte: exact rune boundary", "日本語", 6, "...", "日本..."},
		{"multi-byte: full 3-byte runes", "日本語", 9, "...", "日本語"},
		{"emoji: don't split 4-byte rune", "hi🎉bye", 4, "...", "hi..."},
		{"zero max", "hello", 0, "...", "..."},
		{"empty string", "", 5, "...", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Truncate(tt.s, tt.max, tt.suffix)
			if got != tt.want {
				t.Errorf("Truncate(%q, %d, %q) = %q, want %q", tt.s, tt.max, tt.suffix, got, tt.want)
			}
		})
	}
}
