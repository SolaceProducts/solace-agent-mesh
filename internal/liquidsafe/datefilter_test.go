// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package liquidsafe

import (
	"strings"
	"testing"
	"time"
)

func TestDateFilter_JiraMillisecondsNoColonOffsetRenders(t *testing.T) {
	engine := NewEngine()
	out, err := engine.ParseAndRenderString(
		`{{ created | date: "%Y-%m-%d" }}`,
		map[string]any{"created": "2026-05-22T06:15:31.963-0400"},
	)
	if err != nil {
		t.Fatalf("render failed: %v", err)
	}
	if out != "2026-05-22" {
		t.Fatalf("unexpected output: %q", out)
	}
}

func TestDateFilter_JiraFormat_InForLoop(t *testing.T) {
	engine := NewEngine()
	tmpl := `{% for item in items %}{{ item.key }}:{{ item.created | date: "%Y-%m-%d" }} {% endfor %}`
	out, err := engine.ParseAndRenderString(tmpl, map[string]any{
		"items": []map[string]any{
			{"key": "JIRA-1", "created": "2026-05-22T06:15:31.963-0400"},
			{"key": "JIRA-2", "created": "2026-05-23T10:30:00.123-0400"},
		},
	})
	if err != nil {
		t.Fatalf("render failed: %v", err)
	}
	want := "JIRA-1:2026-05-22 JIRA-2:2026-05-23 "
	if out != want {
		t.Fatalf("unexpected output:\n got:  %q\n want: %q", out, want)
	}
}

func TestDateFilter_StandardFormats(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"RFC3339_colon_zone", "2026-05-22T06:15:31-04:00", "2026-05-22"},
		{"RFC3339_UTC", "2026-05-22T06:15:31Z", "2026-05-22"},
		{"RFC1123Z", "Fri, 22 May 2026 06:15:31 -0400", "2026-05-22"},
		{"ISO_date_only", "2026-05-22", "2026-05-22"},
		{"Jekyll_long", "22 May 2026", "2026-05-22"},
		{"space_separated", "2026-05-22 06:15:31", "2026-05-22"},
	}

	engine := NewEngine()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := engine.ParseAndRenderString(
				`{{ d | date: "%Y-%m-%d" }}`,
				map[string]any{"d": tc.in},
			)
			if err != nil {
				t.Fatalf("render failed for %q: %v", tc.in, err)
			}
			if out != tc.want {
				t.Fatalf("input %q: got %q, want %q", tc.in, out, tc.want)
			}
		})
	}
}

func TestDateFilter_RFC3339Nano(t *testing.T) {
	engine := NewEngine()
	out, err := engine.ParseAndRenderString(
		`{{ d | date: "%H:%M:%S" }}`,
		map[string]any{"d": "2026-05-22T06:15:31.123456789-04:00"},
	)
	if err != nil {
		t.Fatalf("render failed: %v", err)
	}
	if out != "06:15:31" {
		t.Fatalf("unexpected output: %q", out)
	}
}

func TestDateFilter_NowKeyword(t *testing.T) {
	engine := NewEngine()
	out, err := engine.ParseAndRenderString(
		`{{ "now" | date: "%Y" }}`,
		nil,
	)
	if err != nil {
		t.Fatalf("render failed: %v", err)
	}
	wantYear := time.Now().Format("2006")
	if out != wantYear {
		t.Fatalf("unexpected output: got %q, want %q", out, wantYear)
	}
}

func TestDateFilter_DefaultFormat(t *testing.T) {
	engine := NewEngine()
	out, err := engine.ParseAndRenderString(
		`{{ d | date }}`,
		map[string]any{"d": "2026-05-22T06:15:31Z"},
	)
	if err != nil {
		t.Fatalf("render failed: %v", err)
	}
	if !strings.Contains(out, "May 22") {
		t.Fatalf("default-format output %q does not look like the stock default", out)
	}
}

func TestDateFilter_UnparseableInput(t *testing.T) {
	engine := NewEngine()
	out, err := engine.ParseAndRenderString(
		`prefix-{{ d | date: "%Y-%m-%d" }}-suffix`,
		map[string]any{"d": "definitely not a date"},
	)
	if err != nil {
		t.Fatalf("render must not fail on unparseable input, got: %v", err)
	}
	want := "prefix-definitely not a date-suffix"
	if out != want {
		t.Fatalf("got %q, want %q", out, want)
	}
}

func TestDateFilter_EmptyInput(t *testing.T) {
	engine := NewEngine()
	out, err := engine.ParseAndRenderString(
		`[{{ missing | date: "%Y-%m-%d" }}]`,
		nil,
	)
	if err != nil {
		t.Fatalf("render failed: %v", err)
	}
	if out != "[]" {
		t.Fatalf("got %q, want %q", out, "[]")
	}
}

func TestParsePermissiveDate_TimezoneOffsetPreserved(t *testing.T) {
	got, err := parsePermissiveDate("2026-05-22T06:15:31.963-0400")
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	_, offset := got.Zone()
	wantOffset := -4 * 60 * 60
	if offset != wantOffset {
		t.Fatalf("offset = %d seconds, want %d (-04:00)", offset, wantOffset)
	}
}
