// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package artifact

import (
	"strings"
	"testing"
)

func TestSanitizeFilename(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"plain ASCII", "report.txt", "report.txt"},
		{"with spaces", "my report.txt", "my report.txt"},
		{"path traversal", "../../../etc/passwd", "passwd"},
		{"absolute path", "/etc/passwd", "passwd"},
		{"unicode accents", "café_résumé.pdf", "caf__r_sum_.pdf"},
		{"CJK characters", "报告.txt", "__.txt"},
		{"emoji", "📊data.csv", "_data.csv"},
		{"mixed ASCII and non-ASCII", "hello_wörld.go", "hello_w_rld.go"},
		{"all non-ASCII", "日本語", "___"},
		{"single dot reduced to underscore", ".", "_"},
		{"double dot reduced to underscore", "..", "_"},
		{"triple dot is a real filename", "...", "..."},
		{"empty input reduced to underscore", "", "_"},
		{"trailing slash leaves basename", "foo/", "foo"},
		{"parent traversal reduced to underscore", "../..", "_"},
		{"dotdot in path reduced to basename underscore", "/etc/..", "_"},
		{"just filename no ext", "readme", "readme"},
		{"special ASCII chars preserved", "file-name_v2 (1).tar.gz", "file-name_v2 (1).tar.gz"},
		{"tab and control chars", "file\tname.txt", "file_name.txt"},
		{"null byte", "file\x00name.txt", "file_name.txt"},
		{"DEL char", "file\x7Fname.txt", "file_name.txt"},
		{"leading whitespace preserved", "  report.txt", "  report.txt"},
		{"trailing whitespace preserved", "report.txt  ", "report.txt  "},
		{"cyrillic", "документ.docx", "________.docx"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SanitizeFilename(tt.input)
			if got != tt.want {
				t.Errorf("SanitizeFilename(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestBuildArtifactPrefix(t *testing.T) {
	tests := []struct {
		name      string
		loc       Locator
		segments  int
		hasUser   bool
		wantStart string
	}{
		{
			name:      "session-scoped, slash injection in user and session",
			loc:       Locator{AppName: "app", UserID: "alice/../bob", SessionID: "sess/extra", Filename: "data.csv"},
			segments:  5,
			hasUser:   false,
			wantStart: "app/",
		},
		{
			name:      "session-scoped, dotdot session",
			loc:       Locator{AppName: "app", UserID: "u", SessionID: "..", Filename: "data.csv"},
			segments:  5,
			hasUser:   false,
			wantStart: "app/u/_/data.csv/",
		},
		{
			name:      "user-scoped, slash injection",
			loc:       Locator{AppName: "app", UserID: "alice/../bob", Filename: "user:profile.json"},
			segments:  5,
			hasUser:   true,
			wantStart: "app/",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildArtifactPrefix(tt.loc)
			parts := strings.Split(got, "/")
			if len(parts) != tt.segments {
				t.Errorf("BuildArtifactPrefix(%+v) = %q (%d segments), want %d", tt.loc, got, len(parts), tt.segments)
			}
			if strings.Contains(parts[1], "/") || strings.Contains(parts[2], "/") {
				t.Errorf("BuildArtifactPrefix(%+v) = %q: unsanitized segment", tt.loc, got)
			}
			if !strings.HasSuffix(got, "/") {
				t.Errorf("BuildArtifactPrefix(%+v) = %q: must end with /", tt.loc, got)
			}
			if tt.hasUser && parts[2] != "user" {
				t.Errorf("BuildArtifactPrefix(%+v) = %q: user-scoped path must place 'user' at segment 3", tt.loc, got)
			}
		})
	}
}

func TestBuildScopePrefix(t *testing.T) {
	got := BuildScopePrefix(KeyScope{AppName: "app", UserID: "alice/../bob", SessionID: "sess/extra"})
	parts := strings.Split(got, "/")
	if len(parts) != 3 {
		t.Errorf("BuildScopePrefix slash-injection: got %q (%d segments), want 3", got, len(parts))
	}
	if strings.Contains(parts[1], "/") || strings.Contains(parts[2], "/") {
		t.Errorf("BuildScopePrefix: unsanitized segment in %q", got)
	}
	if strings.HasSuffix(got, "/") {
		t.Errorf("BuildScopePrefix must NOT end with /, got %q", got)
	}
}

func TestBuildUserScopePrefix(t *testing.T) {
	got := BuildUserScopePrefix(KeyScope{AppName: "app", UserID: "alice/../bob"})
	if got != "app/bob/user/" {
		t.Errorf("BuildUserScopePrefix slash-injection: got %q, want %q", got, "app/bob/user/")
	}
	got = BuildUserScopePrefix(KeyScope{AppName: "app", UserID: ".."})
	if got != "app/_/user/" {
		t.Errorf("BuildUserScopePrefix sentinel: got %q, want %q", got, "app/_/user/")
	}
}
