// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package a2a

import (
	"strings"
	"testing"
)

func TestStateFromAuthURI(t *testing.T) {
	tests := []struct {
		name    string
		authURI string
		want    string
		wantErr bool
	}{
		{
			"valid",
			"https://auth.example.com/authorize?response_type=code&client_id=cid&state=abc123&code_challenge=xyz",
			"abc123",
			false,
		},
		{
			"url encoded state",
			"https://auth.example.com/authorize?state=a%2Bb%3Dc",
			"a+b=c",
			false,
		},
		{
			"no state",
			"https://auth.example.com/authorize?response_type=code&client_id=cid",
			"",
			true,
		},
		{
			"invalid URL",
			"://not-a-url",
			"",
			true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := StateFromAuthURI(tt.authURI)
			if (err != nil) != tt.wantErr {
				t.Errorf("error = %v, wantErr = %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("state = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestToolAuthGrantRef(t *testing.T) {
	tests := []struct {
		name  string
		state string
		want  string
	}{
		{"state", "CANARYstate0190f5f1", "7b981ede8114805c3ee5e473880c731f95c23e7e456c5dc636db3bd26a05522d"},
		{"empty state has no reference", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ToolAuthGrantRef(tt.state); got != tt.want {
				t.Errorf("ToolAuthGrantRef(%q) = %q, want %q", tt.state, got, tt.want)
			}
		})
	}
}

func TestStateFromAuthURI_ErrorOmitsState(t *testing.T) {
	const secret = "SUPERSECRETSTATE123"
	_, err := StateFromAuthURI("https://auth.example.com/authorize?state=" + secret + "\x7f")
	if err == nil {
		t.Fatal("expected a parse error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("error leaks the state token: %q", err.Error())
	}
	if strings.Contains(err.Error(), "auth.example.com") {
		t.Errorf("error leaks the auth URI: %q", err.Error())
	}
}
