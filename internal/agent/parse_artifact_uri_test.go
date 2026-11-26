// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
)

func TestParseArtifactFilePartURI(t *testing.T) {
	tests := []struct {
		name        string
		uri         string
		wantOK      bool
		wantApp     string
		wantUser    string
		wantSession string
		wantFile    string
		wantVersion *int
	}{
		{
			name:        "canonical URI with version",
			uri:         "artifact://my-app/user-1/sess-1/photo.png?version=3",
			wantOK:      true,
			wantApp:     "my-app",
			wantUser:    "user-1",
			wantSession: "sess-1",
			wantFile:    "photo.png",
			wantVersion: intPtr(3),
		},
		{
			name:        "URI without version resolves to latest",
			uri:         "artifact://my-app/user-1/sess-1/report.txt",
			wantOK:      true,
			wantApp:     "my-app",
			wantUser:    "user-1",
			wantSession: "sess-1",
			wantFile:    "report.txt",
			wantVersion: nil,
		},
		{
			name:        "URL-encoded filename with spaces",
			uri:         "artifact://my-app/user-1/sess-1/Financial%20Sample.xlsx",
			wantOK:      true,
			wantApp:     "my-app",
			wantUser:    "user-1",
			wantSession: "sess-1",
			wantFile:    "Financial Sample.xlsx",
			wantVersion: nil,
		},
		{
			name:        "URL-encoded user_id with @",
			uri:         "artifact://my-app/user%40example.com/sess-1/file.txt",
			wantOK:      true,
			wantApp:     "my-app",
			wantUser:    "user@example.com",
			wantSession: "sess-1",
			wantFile:    "file.txt",
			wantVersion: nil,
		},
		{
			name:   "non-artifact scheme is silently skipped",
			uri:    "https://example.com/file.txt",
			wantOK: false,
		},
		{
			name:   "too-short path produces error note",
			uri:    "artifact://app/short",
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ref, _, ok := parseArtifactFilePartURI(tt.uri)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if ref.appName != tt.wantApp {
				t.Errorf("appName = %q, want %q", ref.appName, tt.wantApp)
			}
			if ref.userID != tt.wantUser {
				t.Errorf("userID = %q, want %q", ref.userID, tt.wantUser)
			}
			if ref.sessionID != tt.wantSession {
				t.Errorf("sessionID = %q, want %q", ref.sessionID, tt.wantSession)
			}
			if ref.filename != tt.wantFile {
				t.Errorf("filename = %q, want %q", ref.filename, tt.wantFile)
			}
			if (ref.version == nil) != (tt.wantVersion == nil) {
				t.Errorf("version nil-ness mismatch: got %v, want %v", ref.version, tt.wantVersion)
			} else if ref.version != nil && *ref.version != *tt.wantVersion {
				t.Errorf("version = %d, want %d", *ref.version, *tt.wantVersion)
			}
		})
	}
}

func intPtr(v int) *int {
	return &v
}

func TestFormatURIRoundTripsThroughParse(t *testing.T) {
	tests := []struct {
		name string
		loc  artifact.Locator
	}{
		{"plain ASCII", artifact.Locator{AppName: "my-app", UserID: "user-1", SessionID: "sess-1", Filename: "photo.png"}},
		{"space in filename", artifact.Locator{AppName: "my-app", UserID: "user-1", SessionID: "sess-1", Filename: "Financial Sample.xlsx"}},
		{"space in user and session", artifact.Locator{AppName: "my-app", UserID: "user one", SessionID: "sess one", Filename: "my report.txt"}},
		{"reserved characters", artifact.Locator{AppName: "my-app", UserID: "user-1", SessionID: "sess-1", Filename: "100% of a?b#c+d.csv"}},
		{"unicode", artifact.Locator{AppName: "my-app", UserID: "user-1", SessionID: "sess-1", Filename: "报告.txt"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			uri, err := artifact.FormatURI(tt.loc, 3)
			if err != nil {
				t.Fatalf("FormatURI: %v", err)
			}
			ref, note, ok := parseArtifactFilePartURI(uri)
			if !ok {
				t.Fatalf("parse %q failed: note=%q", uri, note)
			}
			if ref.appName != tt.loc.AppName {
				t.Errorf("appName = %q, want %q", ref.appName, tt.loc.AppName)
			}
			if ref.userID != tt.loc.UserID {
				t.Errorf("userID = %q, want %q", ref.userID, tt.loc.UserID)
			}
			if ref.sessionID != tt.loc.SessionID {
				t.Errorf("sessionID = %q, want %q", ref.sessionID, tt.loc.SessionID)
			}
			if ref.filename != tt.loc.Filename {
				t.Errorf("filename = %q, want %q", ref.filename, tt.loc.Filename)
			}
			if ref.version == nil || *ref.version != 3 {
				t.Errorf("version = %v, want 3", ref.version)
			}
		})
	}
}

func TestFormatURISegmentSeparatorsRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		loc  artifact.Locator
	}{
		{"slash in user", artifact.Locator{AppName: "my-app", UserID: "a/b", SessionID: "sess-1", Filename: "f.txt"}},
		{"slash in session", artifact.Locator{AppName: "my-app", UserID: "user-1", SessionID: "s/x", Filename: "f.txt"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			uri, err := artifact.FormatURI(tt.loc, 3)
			if err != nil {
				t.Fatalf("FormatURI: %v", err)
			}
			ref, note, ok := parseArtifactFilePartURI(uri)
			if !ok {
				t.Fatalf("parse failed: note=%q", note)
			}
			if ref.appName != tt.loc.AppName || ref.userID != tt.loc.UserID || ref.sessionID != tt.loc.SessionID || ref.filename != tt.loc.Filename {
				t.Errorf("parsed (app, user, session, file) = (%q, %q, %q, %q), want %+v",
					ref.appName, ref.userID, ref.sessionID, ref.filename, tt.loc)
			}
		})
	}
}
