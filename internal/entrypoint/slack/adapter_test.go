// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package slack

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
)

func TestParseArtifactURI(t *testing.T) {
	t.Parallel()

	intp := func(n int) *int { return &n }

	cases := []struct {
		name        string
		uri         string
		wantOK      bool
		wantName    string
		wantVersion *int
	}{
		{
			name:        "plain filename with version",
			uri:         "artifact://app/userA/sess1/foo.png?version=3",
			wantOK:      true,
			wantName:    "foo.png",
			wantVersion: intp(3),
		},
		{
			name:        "version=0",
			uri:         "artifact://app/userA/sess1/foo.png?version=0",
			wantOK:      true,
			wantName:    "foo.png",
			wantVersion: intp(0),
		},
		{
			name:        "no version",
			uri:         "artifact://app/userA/sess1/foo.png",
			wantOK:      true,
			wantName:    "foo.png",
			wantVersion: nil,
		},
		{
			name:        "percent-encoded space",
			uri:         "artifact://app/userA/sess1/Financial%20Sample.xlsx?version=0",
			wantOK:      true,
			wantName:    "Financial Sample.xlsx",
			wantVersion: intp(0),
		},
		{
			name:        "percent-encoded unicode",
			uri:         "artifact://app/userA/sess1/r%C3%A9sum%C3%A9.pdf",
			wantOK:      true,
			wantName:    "résumé.pdf",
			wantVersion: nil,
		},
		{
			name:   "https URL rejected",
			uri:    "https://example.com/x",
			wantOK: false,
		},
		{
			name:   "missing host",
			uri:    "artifact:///",
			wantOK: false,
		},
		{
			name:   "not a URL",
			uri:    "not a url",
			wantOK: false,
		},
		{
			name:   "missing filename segment",
			uri:    "artifact://app/userA/sess1/",
			wantOK: false,
		},
		{
			name:   "too few path segments",
			uri:    "artifact://app/file",
			wantOK: false,
		},
		{
			name:        "non-numeric version ignored",
			uri:         "artifact://app/userA/sess1/foo.png?version=notanumber",
			wantOK:      true,
			wantName:    "foo.png",
			wantVersion: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotName, gotVersion, gotOK := entrypoint.ParseArtifactURI(tc.uri)
			if gotOK != tc.wantOK {
				t.Fatalf("ok = %v, want %v", gotOK, tc.wantOK)
			}
			if !tc.wantOK {
				return
			}
			if gotName != tc.wantName {
				t.Fatalf("name = %q, want %q", gotName, tc.wantName)
			}
			switch {
			case tc.wantVersion == nil && gotVersion != nil:
				t.Fatalf("version = %d, want nil", *gotVersion)
			case tc.wantVersion != nil && gotVersion == nil:
				t.Fatalf("version = nil, want %d", *tc.wantVersion)
			case tc.wantVersion != nil && *gotVersion != *tc.wantVersion:
				t.Fatalf("version = %d, want %d", *gotVersion, *tc.wantVersion)
			}
		})
	}
}

func TestValidateSlackFileURL_RejectsNonSlackHostsSoBotTokenNeverLeaves(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		rawURL  string
		wantErr bool
	}{
		{"valid files.slack.com", "https://files.slack.com/files-pri/T123-F123/example.png", false},
		{"valid bare slack.com", "https://slack.com/api/files.info", false},
		{"valid slack-files.com", "https://slack-files.com/T123-F123-abcdef", false},
		{"valid subdomain of slack.com", "https://files.example.slack.com/x.png", false},
		{"valid subdomain of slack-files.com", "https://e1.slack-files.com/foo", false},
		{"valid subdomain of files.slack.com", "https://cdn.files.slack.com/y.jpg", false},

		{"reject http scheme", "http://files.slack.com/x", true},
		{"reject ws scheme", "wss://files.slack.com/x", true},
		{"reject attacker host", "https://attacker.com/x", true},
		{"reject lookalike with slack.com substring", "https://slack.com.attacker.io/x", true},
		{"reject suffix without dot boundary", "https://evilslack.com/x", true},
		{"reject IPv4 literal", "https://169.254.169.254/", true},
		{"reject loopback", "https://127.0.0.1/x", true},
		{"reject empty URL", "", true},
		{"reject garbage URL", "://not a url", true},
		{"reject URL with no host", "https:///path", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateSlackFileURL(tc.rawURL)
			if tc.wantErr && err == nil {
				t.Fatalf("validateSlackFileURL(%q) = nil, want error", tc.rawURL)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("validateSlackFileURL(%q) = %v, want nil", tc.rawURL, err)
			}
		})
	}
}

func TestValidateSlackFileURL_ExtraOriginsMatchSchemeAndHostPortExactly(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		rawURL  string
		extras  []string
		wantErr bool
	}{
		{
			name:    "harness http origin matches exact extra",
			rawURL:  "http://127.0.0.1:55432/files/get/F-test-0",
			extras:  []string{"http://127.0.0.1:55432"},
			wantErr: false,
		},
		{
			name:    "https proxy origin matches exact extra",
			rawURL:  "https://corp-proxy.internal/file",
			extras:  []string{"https://corp-proxy.internal"},
			wantErr: false,
		},
		{
			name:    "http downgrade rejected when extra is https",
			rawURL:  "http://corp-proxy.internal/file",
			extras:  []string{"https://corp-proxy.internal"},
			wantErr: true,
		},
		{
			name:    "https request with http-only extra still requires slack-host match",
			rawURL:  "https://attacker.com/file",
			extras:  []string{"http://127.0.0.1:55432"},
			wantErr: true,
		},
		{
			name:    "port mismatch rejected",
			rawURL:  "http://127.0.0.1:9999/file",
			extras:  []string{"http://127.0.0.1:55432"},
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateSlackFileURL(tc.rawURL, tc.extras...)
			if tc.wantErr && err == nil {
				t.Fatalf("validateSlackFileURL(%q, %v) = nil, want error", tc.rawURL, tc.extras)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("validateSlackFileURL(%q, %v) = %v, want nil", tc.rawURL, tc.extras, err)
			}
		})
	}
}
