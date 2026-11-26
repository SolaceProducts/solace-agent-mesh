// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package sources

import (
	"strings"
	"testing"
)

func TestParseGitHTTPS(t *testing.T) {
	src, err := Parse("git+https://github.com/org/repo@v1.4.2")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if src.Scheme != SchemeGitHTTPS {
		t.Errorf("Scheme = %q; want %q", src.Scheme, SchemeGitHTTPS)
	}
	if src.URL != "https://github.com/org/repo" {
		t.Errorf("URL = %q", src.URL)
	}
	if src.Ref != "v1.4.2" {
		t.Errorf("Ref = %q", src.Ref)
	}
	if src.Subdirectory != "" {
		t.Errorf("Subdirectory = %q; want empty", src.Subdirectory)
	}
	if src.Raw != "git+https://github.com/org/repo@v1.4.2" {
		t.Errorf("Raw = %q", src.Raw)
	}
}

func TestParseGitSSH(t *testing.T) {
	src, err := Parse("git+ssh://git@github.com/org/repo.git@v1.4.2")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if src.Scheme != SchemeGitSSH {
		t.Errorf("Scheme = %q", src.Scheme)
	}
	if src.URL != "ssh://git@github.com/org/repo.git" {
		t.Errorf("URL = %q", src.URL)
	}
	if src.Ref != "v1.4.2" {
		t.Errorf("Ref = %q", src.Ref)
	}
}

func TestParseGitFile(t *testing.T) {
	src, err := Parse("git+file:///tmp/foo@abc123")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if src.Scheme != SchemeGitFile {
		t.Errorf("Scheme = %q", src.Scheme)
	}
	if src.URL != "file:///tmp/foo" {
		t.Errorf("URL = %q", src.URL)
	}
	if src.Ref != "abc123" {
		t.Errorf("Ref = %q", src.Ref)
	}
}

func TestParseFile(t *testing.T) {
	cases := []struct {
		raw  string
		want string
	}{
		{"file:///abs/path", "/abs/path"},
		{"file://../relative", "../relative"},
		{"file://relative", "relative"},
	}
	for _, c := range cases {
		src, err := Parse(c.raw)
		if err != nil {
			t.Fatalf("Parse(%q): %v", c.raw, err)
		}
		if src.Scheme != SchemeFile {
			t.Errorf("Parse(%q).Scheme = %q", c.raw, src.Scheme)
		}
		if src.URL != c.want {
			t.Errorf("Parse(%q).URL = %q; want %q", c.raw, src.URL, c.want)
		}
		if src.Ref != "" {
			t.Errorf("Parse(%q).Ref = %q; want empty (file:// has no ref)", c.raw, src.Ref)
		}
	}
}

func TestParseSubdirectory(t *testing.T) {
	src, err := Parse("git+https://github.com/org/repo@v1.4.2#subdirectory=tools/")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if src.Subdirectory != "tools/" {
		t.Errorf("Subdirectory = %q; want %q", src.Subdirectory, "tools/")
	}
	if src.URL != "https://github.com/org/repo" {
		t.Errorf("URL = %q (subdirectory should not bleed into URL)", src.URL)
	}
	if src.Ref != "v1.4.2" {
		t.Errorf("Ref = %q", src.Ref)
	}

	src, err = Parse("file://./local#subdirectory=agents")
	if err != nil {
		t.Fatalf("file:// + subdirectory: %v", err)
	}
	if src.URL != "./local" {
		t.Errorf("URL = %q", src.URL)
	}
	if src.Subdirectory != "agents" {
		t.Errorf("Subdirectory = %q", src.Subdirectory)
	}
}

func TestParseEmptySubdirectory(t *testing.T) {
	_, err := Parse("git+https://github.com/org/repo@v1.4.2#subdirectory=")
	if err == nil {
		t.Fatal("expected error for empty subdirectory= value")
	}
	if !strings.Contains(err.Error(), "subdirectory") {
		t.Errorf("error = %v; want it to mention subdirectory", err)
	}
}

func TestParseSubdirectoryRejectsEscape(t *testing.T) {
	cases := []string{
		"git+https://h/p@v1#subdirectory=../etc",
		"git+https://h/p@v1#subdirectory=foo/../../etc",
		"git+https://h/p@v1#subdirectory=..",
	}
	for _, raw := range cases {
		if _, err := Parse(raw); err == nil {
			t.Errorf("Parse(%q): expected error for escaping subdirectory", raw)
		}
	}
}

func TestParseRejectsOptionLikeRef(t *testing.T) {
	cases := []string{
		"git+https://h/p@-rf",
		"git+https://h/p@--upload-pack=evil",
	}
	for _, raw := range cases {
		if _, err := Parse(raw); err == nil {
			t.Errorf("Parse(%q): expected error for option-like ref", raw)
		}
	}
}

func TestParseSubdirectoryRejectsAbsolute(t *testing.T) {
	cases := []string{
		"git+https://h/p@v1#subdirectory=/etc",
		"git+https://h/p@v1#subdirectory=/etc/passwd",
	}
	for _, raw := range cases {
		if _, err := Parse(raw); err == nil {
			t.Errorf("Parse(%q): expected error for absolute subdirectory", raw)
		}
	}
}

func TestParseMissingRef(t *testing.T) {
	src, err := Parse("git+https://github.com/org/repo")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if src.Ref != "" {
		t.Errorf("Ref = %q; want empty", src.Ref)
	}
	if src.URL != "https://github.com/org/repo" {
		t.Errorf("URL = %q", src.URL)
	}
}

func TestParseMalformed(t *testing.T) {
	cases := []string{
		"",
		"   ",
		"github.com/org/repo",
		"http://github.com/org/repo@v1",
		"file://",
		"git+https://host/p@v1#bad",
		"git+https://host/p@v1#subdirectory=",
		"git+https://host/p@v1#subdirectory",
	}
	for _, raw := range cases {
		if _, err := Parse(raw); err == nil {
			t.Errorf("Parse(%q): expected error, got nil", raw)
		}
	}
}

func TestParsePreservesRawForErrors(t *testing.T) {
	raw := "git+https://github.com/org/repo@v1.0.0#subdirectory=tools/"
	src, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if src.Raw != raw {
		t.Errorf("Raw = %q; want %q", src.Raw, raw)
	}
}
