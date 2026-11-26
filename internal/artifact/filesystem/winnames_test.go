// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package filesystem

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
)

func TestWindowsNamesEscapeWhatWindowsRejects(t *testing.T) {
	cases := []struct{ name, want string }{
		{"system:default", "system%3Adefault"},
		{"slack:T01:U02", "slack%3AT01%3AU02"},
		{"auth0|abc", "auth0%7Cabc"},
		{`a<b>c"d?e*f\g`, "a%3Cb%3Ec%22d%3Fe%2Af%5Cg"},
		{"report.", "report%2E"},
		{"notes  ", "notes%20%20"},
		{"...", "%2E%2E%2E"},
		{"CON", "%43ON"},
		{"nul.txt", "%6Eul.txt"},
		{"Com1.tar.gz", "%43om1.tar.gz"},
		{"aux .log", "%61ux .log"},
		{"conin$", "%63onin$"},
		{"NUL.", "%4EUL%2E"},
	}
	for _, tc := range cases {
		if got := encodeWindowsName(tc.name); got != tc.want {
			t.Errorf("encodeWindowsName(%q) = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestWindowsNamesLeaveAcceptedNamesAloneForPreEscapingDirectories(t *testing.T) {
	for _, name := range []string{
		"report.csv", "user-1", "solace-agent-mesh", "0190f3c1-7a2b-7c3d-8e4f-123456789abc",
		"project-0190f3c1-7a2b-7c3d-8e4f-123456789abc", "alice@example.com",
		"50% margin.xlsx", "my%20file.pdf", "100%", "a%2Eb", "%41bc", "console.log", "CONSOLE",
		"file-name_v2 (1).tar.gz", "_",
	} {
		if got := encodeWindowsName(name); got != name {
			t.Errorf("encodeWindowsName(%q) = %q, want it unchanged", name, got)
		}
	}
}

func TestWindowsNamesEscapeLiteralsThatReadAsEscapes(t *testing.T) {
	cases := []struct{ name, want string }{
		{"a%3Ab", "a%253Ab"},
		{"100%25", "100%2525"},
		{"file%20", "file%2520"},
		{"%4EUL", "%254EUL"},
		{"a%3ab", "a%253ab"},
		{"report%2e", "report%252e"},
		{"%6eul", "%256eul"},
	}
	for _, tc := range cases {
		if got := encodeWindowsName(tc.name); got != tc.want {
			t.Errorf("encodeWindowsName(%q) = %q, want %q", tc.name, got, tc.want)
		}
		if got := decodeWindowsName(tc.want); got != tc.name {
			t.Errorf("decodeWindowsName(%q) = %q, want %q", tc.want, got, tc.name)
		}
	}
}

func TestWindowsNamesDecodeEscapeLikeDirectoriesWrittenBeforeEscaping(t *testing.T) {
	cases := []struct{ dir, want string }{
		{"a%3Ab", "a:b"},
		{"a%3ab", "a:b"},
		{"report%2e", "report."},
		{"%6eul", "nul"},
	}
	for _, tc := range cases {
		if got := decodeWindowsName(tc.dir); got != tc.want {
			t.Errorf("decodeWindowsName(%q) = %q, want %q", tc.dir, got, tc.want)
		}
		if got := encodeWindowsName(tc.want); !strings.EqualFold(got, tc.dir) {
			t.Errorf("encodeWindowsName(%q) = %q, which Windows does not resolve to %q", tc.want, got, tc.dir)
		}
	}
}

var windowsDeviceName = regexp.MustCompile(`(?i)^(CON|PRN|AUX|NUL|CONIN\$|CONOUT\$|COM[0-9]|LPT[0-9]) *(\.|$)`)

func windowsAccepts(name string) bool {
	return !strings.ContainsAny(name, `<>:"|?*\`) &&
		!strings.HasSuffix(name, ".") && !strings.HasSuffix(name, " ") &&
		!windowsDeviceName.MatchString(name)
}

func FuzzWindowsNamesRoundTrip(f *testing.F) {
	for _, seed := range []string{
		"system:default", "report.", "...", " ", "CON", "nul.txt", "NUL.", "aux .log",
		"a%3Ab", "file%20", "x%2E.", "%2E%2E", "%%3A", "%4EUL", "%4EUL.", "%254EUL", "a%",
		"%", "%2", "my%20file.pdf", "100%25", "c%3Aon", "%43ON", "lpt9 ", "a%2%20",
		"a%3ab", "report%2e", "%6eul", "slack%3aT1%3aU2", "50% margin.xlsx",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, name string) {
		name = artifact.SanitizeFilename(name)
		encoded := encodeWindowsName(name)
		if got := decodeWindowsName(encoded); got != name {
			t.Fatalf("decodeWindowsName(encodeWindowsName(%q)) = %q via %q", name, got, encoded)
		}
		if !windowsAccepts(encoded) {
			t.Fatalf("encodeWindowsName(%q) = %q, which Windows rejects", name, encoded)
		}
		for _, sameDir := range []string{strings.ToLower(encoded), strings.ToUpper(encoded)} {
			if got := decodeWindowsName(sameDir); !strings.EqualFold(got, name) {
				t.Fatalf("decodeWindowsName(%q) = %q, but Windows resolves it to the directory of %q", sameDir, got, name)
			}
		}
		if windowsAccepts(name) && decodeWindowsName(name) == name && encoded != name {
			t.Fatalf("encodeWindowsName(%q) = %q moves a directory Windows already accepts", name, encoded)
		}
	})
}

func TestWindowsNamesStoreRoundTripsNamesWindowsRejects(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("only a store on Windows escapes directory names")
	}
	dir := t.TempDir()
	s := New(dir)
	ctx := t.Context()
	scope := artifact.KeyScope{AppName: "solace-agent-mesh", UserID: "system:default", SessionID: "sess-1"}
	names := []string{"plan:v2?.md", "user:notes|1.txt", "report.", "nul.txt"}
	for _, name := range names {
		loc := artifact.Locator{AppName: scope.AppName, UserID: scope.UserID, SessionID: scope.SessionID, Filename: name}
		if _, err := s.Save(ctx, loc, []byte(name), "text/plain"); err != nil {
			t.Fatalf("Save(%q): %v", name, err)
		}
	}

	keys, err := s.ListKeys(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	want := slices.Clone(names)
	slices.Sort(want)
	if !slices.Equal(keys, want) {
		t.Errorf("ListKeys = %q, want %q", keys, want)
	}
	for _, name := range keys {
		loc := artifact.Locator{AppName: scope.AppName, UserID: scope.UserID, SessionID: scope.SessionID, Filename: name}
		content, _, err := s.Load(ctx, loc, nil)
		if err != nil || string(content) != name {
			t.Errorf("Load(%q) = %q, %v; want the saved content", name, content, err)
		}
	}

	userDir := filepath.Join(dir, "solace-agent-mesh", "system%3Adefault")
	if _, err := os.Stat(filepath.Join(userDir, "sess-1", "plan%3Av2%3F.md", "0")); err != nil {
		t.Errorf("artifact not at its escaped path: %v", err)
	}
	if _, err := os.Stat(filepath.Join(userDir, "user", "notes%7C1.txt", "0")); err != nil {
		t.Errorf("user-scoped artifact not at its escaped path: %v", err)
	}
}

func TestNonWindowsStoreKeepsItsLayout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a store on Windows escapes directory names")
	}
	dir := t.TempDir()
	s := New(dir)
	loc := artifact.Locator{AppName: "app", UserID: "system:default", SessionID: "sess-1", Filename: "a:b.txt"}
	if _, err := s.Save(t.Context(), loc, []byte("x"), "text/plain"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "app", "system:default", "sess-1", "a:b.txt", "0")); err != nil {
		t.Errorf("non-Windows store changed its layout: %v", err)
	}
}
