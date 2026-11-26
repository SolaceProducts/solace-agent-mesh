// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package slacktool

import (
	"fmt"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/pkg/slackapi"
	goslack "github.com/slack-go/slack"
)

func TestClassify(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  inputKind
	}{
		{"email basic", "user@company.com", kindEmail},
		{"email subdomain", "user@mail.co.uk", kindEmail},
		{"email minimal", "a@b.c", kindEmail},
		{"channel name", "#general", kindChannelName},
		{"channel name hyphen", "#ops-alerts", kindChannelName},
		{"channel ID", "C01234ABCDE", kindPassthrough},
		{"group ID", "G01234ABCDE", kindPassthrough},
		{"DM ID", "D01234ABCDE", kindPassthrough},
		{"user ID", "U01234ABCDE", kindPassthrough},
		{"bare name", "general", kindPassthrough},
		{"hash before email", "#user@domain.com", kindChannelName},
		{"at sign no dot", "user@nodot", kindPassthrough},
		{"empty string", "", kindPassthrough},
		{"hyphenated name", "some-channel", kindPassthrough},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classify(SlackChannel(tt.input))
			if got != tt.want {
				t.Errorf("classify(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestMapSlackChannelToChannelID_Email(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()
	api.SeedUserByEmail("user@co.com", &goslack.User{ID: "U123"})

	got, err := mapSlackChannelToChannelID(t.Context(), SlackChannel("user@co.com"), api)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "U123" {
		t.Errorf("got %q, want U123", got)
	}

	calls := callsByMethod(api.Calls(), "GetUserByEmail")
	if len(calls) != 1 {
		t.Fatalf("expected 1 GetUserByEmail call, got %d", len(calls))
	}
	if calls[0].Params["email"] != "user@co.com" {
		t.Errorf("email param = %q, want user@co.com", calls[0].Params["email"])
	}
}

func TestMapSlackChannelToChannelID_EmailNotFound(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()

	_, err := mapSlackChannelToChannelID(t.Context(), SlackChannel("missing@co.com"), api)
	if err == nil {
		t.Fatal("expected error for unknown email")
	}
	if !strings.Contains(err.Error(), "resolve email") {
		t.Errorf("error should mention resolve email, got: %v", err)
	}
}

func TestMapSlackChannelToChannelID_EmailAPIError(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()
	api.SetNextError("GetUserByEmail", fmt.Errorf("rate_limited"))

	_, err := mapSlackChannelToChannelID(t.Context(), SlackChannel("user@co.com"), api)
	if err == nil {
		t.Fatal("expected error for API failure")
	}
	if !strings.Contains(err.Error(), "rate_limited") {
		t.Errorf("error should contain rate_limited, got: %v", err)
	}
}

func TestMapSlackChannelToChannelID_ChannelName(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()

	got, err := mapSlackChannelToChannelID(t.Context(), SlackChannel("#general"), api)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "general" {
		t.Errorf("got %q, want general", got)
	}
	if len(api.Calls()) != 0 {
		t.Errorf("expected no API calls for channel name, got %d", len(api.Calls()))
	}
}

func TestMapSlackChannelToChannelID_Passthrough(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"channel ID", "C01234ABCDE"},
		{"group ID", "G01234ABCDE"},
		{"DM ID", "D01234ABCDE"},
		{"bare name", "general"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := slackapi.NewInMemorySlackAPI()

			got, err := mapSlackChannelToChannelID(t.Context(), SlackChannel(tt.input), api)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if string(got) != tt.input {
				t.Errorf("got %q, want %q", got, tt.input)
			}
			if len(api.Calls()) != 0 {
				t.Errorf("expected no API calls for passthrough, got %d", len(api.Calls()))
			}
		})
	}
}
