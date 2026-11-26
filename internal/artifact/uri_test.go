// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package artifact

import (
	"errors"
	"testing"
)

func TestFormatURI(t *testing.T) {
	tests := []struct {
		name    string
		loc     Locator
		version int
		want    string
	}{
		{
			"plain ASCII",
			Locator{AppName: "app", UserID: "u1", SessionID: "s1", Filename: "report.txt"},
			1,
			"artifact://app/u1/s1/report.txt?version=1",
		},
		{
			"version zero is rendered, not omitted",
			Locator{AppName: "app", UserID: "u1", SessionID: "s1", Filename: "report.txt"},
			0,
			"artifact://app/u1/s1/report.txt?version=0",
		},
		{
			"space in filename",
			Locator{AppName: "app", UserID: "u1", SessionID: "s1", Filename: "Financial Sample.xlsx"},
			2,
			"artifact://app/u1/s1/Financial%20Sample.xlsx?version=2",
		},
		{
			"plus in filename stays literal",
			Locator{AppName: "app", UserID: "u1", SessionID: "s1", Filename: "a+b.txt"},
			0,
			"artifact://app/u1/s1/a+b.txt?version=0",
		},
		{
			"percent in filename",
			Locator{AppName: "app", UserID: "u1", SessionID: "s1", Filename: "100%.csv"},
			0,
			"artifact://app/u1/s1/100%25.csv?version=0",
		},
		{
			"hash in filename",
			Locator{AppName: "app", UserID: "u1", SessionID: "s1", Filename: "issue#3.md"},
			0,
			"artifact://app/u1/s1/issue%233.md?version=0",
		},
		{
			"question mark in filename",
			Locator{AppName: "app", UserID: "u1", SessionID: "s1", Filename: "why?.txt"},
			0,
			"artifact://app/u1/s1/why%3F.txt?version=0",
		},
		{
			"slash in filename",
			Locator{AppName: "app", UserID: "u1", SessionID: "s1", Filename: "a/b.txt"},
			0,
			"artifact://app/u1/s1/a%2Fb.txt?version=0",
		},
		{
			"unicode filename",
			Locator{AppName: "app", UserID: "u1", SessionID: "s1", Filename: "报告.txt"},
			0,
			"artifact://app/u1/s1/%E6%8A%A5%E5%91%8A.txt?version=0",
		},
		{
			"empty AppName yields an empty host segment",
			Locator{AppName: "", UserID: "u1", SessionID: "s1", Filename: "report.txt"},
			0,
			"artifact:///u1/s1/report.txt?version=0",
		},
		{
			"space in user and session",
			Locator{AppName: "my-app", UserID: "user one", SessionID: "sess one", Filename: "f.txt"},
			7,
			"artifact://my-app/user%20one/sess%20one/f.txt?version=7",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := FormatURI(tt.loc, tt.version)
			if err != nil {
				t.Fatalf("FormatURI() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("FormatURI() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFormatURIRejectsUnroutableAppName(t *testing.T) {
	tests := []struct {
		name    string
		appName string
	}{
		{"slash", "myorg/prod"},
		{"space", "my app"},
		{"at sign parses as userinfo", "ap@p"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loc := Locator{AppName: tt.appName, UserID: "u1", SessionID: "s1", Filename: "f.txt"}
			uri, err := FormatURI(loc, 0)
			if !errors.Is(err, ErrUnroutableAppName) {
				t.Fatalf("FormatURI(%q) = %q, %v; want ErrUnroutableAppName", tt.appName, uri, err)
			}
			if uri != "" {
				t.Errorf("FormatURI returned %q alongside its error; want the empty string", uri)
			}
		})
	}
}

func TestParseURIRoundTripsFormatURI(t *testing.T) {
	locs := []Locator{
		{AppName: "app", UserID: "u1", SessionID: "s1", Filename: "report.txt"},
		{AppName: "app", UserID: "u1", SessionID: "s1", Filename: "Financial Sample.xlsx"},
		{AppName: "app", UserID: "u1", SessionID: "s1", Filename: "a+b.txt"},
		{AppName: "app", UserID: "u1", SessionID: "s1", Filename: "100%.csv"},
		{AppName: "app", UserID: "u1", SessionID: "s1", Filename: "issue#3.md"},
		{AppName: "app", UserID: "u1", SessionID: "s1", Filename: "why?.txt"},
		{AppName: "app", UserID: "u1", SessionID: "s1", Filename: "a/b.txt"},
		{AppName: "app", UserID: "u1", SessionID: "s1", Filename: "报告.txt"},
		{AppName: "", UserID: "u1", SessionID: "s1", Filename: "report.txt"},
		{AppName: "my-app", UserID: "user one", SessionID: "sess one", Filename: "f.txt"},
	}
	for _, want := range locs {
		t.Run(want.Filename, func(t *testing.T) {
			uri, err := FormatURI(want, 4)
			if err != nil {
				t.Fatalf("FormatURI() error = %v", err)
			}
			got, version, ok := ParseURI(uri)
			if !ok {
				t.Fatalf("ParseURI(%q) ok = false", uri)
			}
			if got != want {
				t.Errorf("ParseURI(%q) = %+v, want %+v", uri, got, want)
			}
			if version == nil || *version != 4 {
				t.Errorf("ParseURI(%q) version = %v, want 4", uri, version)
			}
		})
	}
}

func TestParseURIWithoutVersionMeansLatest(t *testing.T) {
	got, version, ok := ParseURI("artifact://livetest/live-user/sess-1/order.json")
	if !ok {
		t.Fatal("ParseURI ok = false")
	}
	want := Locator{AppName: "livetest", UserID: "live-user", SessionID: "sess-1", Filename: "order.json"}
	if got != want {
		t.Errorf("ParseURI = %+v, want %+v", got, want)
	}
	if version != nil {
		t.Errorf("version = %d, want nil", *version)
	}
}

func TestParseURIRejectsNonPathForms(t *testing.T) {
	for _, uri := range []string{
		"artifact://input.json:3",
		"artifact://input.json",
		"artifact://report:latest",
		"artifact://app/u1/s1/",
		"artifact://app/s1/f.txt",
		"artifact://app/u1/s1/f.txt?version=abc",
		"artifact://app/u1/s1/f.txt?version=1.5",
		"artifact://app/u1/s1/f.txt?version=-1",
		"https://app/u1/s1/f.txt",
		"",
	} {
		if loc, _, ok := ParseURI(uri); ok {
			t.Errorf("ParseURI(%q) = %+v, ok; want not ok", uri, loc)
		}
	}
}
