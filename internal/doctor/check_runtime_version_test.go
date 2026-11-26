// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package doctor

import (
	"context"
	"strings"
	"testing"
)

func TestRuntimeVersionCheck(t *testing.T) {
	tests := []struct {
		name       string
		got        string
		min        string
		wantStatus Status
		wantReason string
	}{
		{"pass on equal", "1.26", "1.26", StatusPass, ""},
		{"pass on greater", "1.27.3", "1.26", StatusPass, ""},
		{"pass on patch greater", "1.26.5", "1.26", StatusPass, ""},
		{"fail on minor older", "1.25", "1.26", StatusFail, "1.26 or later"},
		{"fail on major older", "0.99", "1.26", StatusFail, "1.26 or later"},
		{"pass with rc suffix", "1.26rc1", "1.26", StatusPass, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &RuntimeVersionCheck{Version: tt.got, MinVersion: tt.min}
			got := c.Run(context.Background(), Env{}, ContextLocal)
			if got.Status != tt.wantStatus {
				t.Errorf("status: got %s, want %s; reason=%q", got.Status, tt.wantStatus, got.Reason)
			}
			if tt.wantReason != "" && !strings.Contains(got.Reason, tt.wantReason) {
				t.Errorf("reason: got %q, want contains %q", got.Reason, tt.wantReason)
			}
			if got.Detail != tt.got {
				t.Errorf("detail: got %q, want %q", got.Detail, tt.got)
			}
		})
	}
}

func TestCompareGoVersions(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"1.26", "1.26", 0},
		{"1.26.1", "1.26.0", 1},
		{"1.25.9", "1.26", -1},
		{"1.26", "1.26.0", 0},
		{"go1.26", "1.26", 0},
	}
	for _, tt := range tests {
		t.Run(tt.a+"_vs_"+tt.b, func(t *testing.T) {
			got, err := compareGoVersions(tt.a, tt.b)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("got %d, want %d", got, tt.want)
			}
		})
	}
}
