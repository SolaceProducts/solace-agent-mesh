// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package broker

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateSubscription(t *testing.T) {
	tests := []struct {
		sub  string
		want error
	}{
		{"acme/shipments/delayed", nil},
		{"acme/shipments/*/delayed", nil},
		{"acme/shipments/>", nil},
		{">", nil},
		{"*", nil},
		{"acme/ship*", nil},
		{"acme/shipments/v1/{region}", nil},
		{"bestellungen/geändert/>", nil},
		{strings.Repeat("a", MaxSubscriptionBytes), nil},
		{strings.Repeat("a/", MaxSubscriptionBytes/2-1) + "a", nil},

		{"", ErrSubscriptionEmpty},
		{strings.Repeat("a", MaxSubscriptionBytes+1), ErrSubscriptionTooLong},
		{"#share/group/acme/>", ErrSubscriptionReserved},
		{"#P2P/v:host/acme", ErrSubscriptionReserved},
		{"acme/ship ments", ErrSubscriptionCharacters},
		{"acme/ship\tments", ErrSubscriptionCharacters},
		{"acme/ship\x00ments", ErrSubscriptionCharacters},
		{"acme/\xff", ErrSubscriptionCharacters},
		{"/acme/shipments", ErrSubscriptionEmptyLevel},
		{"acme/shipments/", ErrSubscriptionEmptyLevel},
		{"acme//shipments", ErrSubscriptionEmptyLevel},
		{"acme/>/delayed", ErrSubscriptionMultiLevel},
		{"acme/shipments>", ErrSubscriptionMultiLevel},
		{"acme/>>", ErrSubscriptionMultiLevel},
		{"acme/*ments", ErrSubscriptionSingleLevelUse},
		{"acme/ship*ments", ErrSubscriptionSingleLevelUse},
		{"acme/**", ErrSubscriptionSingleLevelUse},
	}
	for _, tt := range tests {
		t.Run(tt.sub, func(t *testing.T) {
			if got := ValidateSubscription(tt.sub); !errors.Is(got, tt.want) {
				t.Errorf("ValidateSubscription(%q) = %v, want %v", tt.sub, got, tt.want)
			}
		})
	}
}

func TestTopicMatch(t *testing.T) {
	tests := []struct {
		name         string
		subscription string
		topic        string
		want         bool
	}{
		{"exact match", "a/b/c", "a/b/c", true},
		{"exact no match", "a/b/c", "a/b/d", false},
		{"single level", "foo", "foo", true},
		{"single level no match", "foo", "bar", false},

		{"star matches one level", "a/*/c", "a/b/c", true},
		{"star different middle", "a/*/c", "a/xyz/c", true},
		{"star does not match multi-level", "a/*/c", "a/b/d/c", false},
		{"star does not match empty", "a/*/c", "a//c", false},
		{"star at start", "*/b/c", "a/b/c", true},
		{"star at end", "a/b/*", "a/b/c", true},
		{"multiple stars", "*/*/c", "a/b/c", true},
		{"star does not match no level", "a/*", "a", false},

		{"> matches one level", "a/>", "a/b", true},
		{"> matches many levels", "a/>", "a/b/c/d/e", true},
		{"> does not match zero levels", "a/>", "a", false},
		{"> at root", ">", "a/b/c", true},
		{"> at root single", ">", "a", true},

		{"sam request topic", "solace-agent-mesh/v1/agents/*/request",
			"solace-agent-mesh/v1/agents/web-agent/request", true},
		{"sam wildcard sub", "solace-agent-mesh/v1/>",
			"solace-agent-mesh/v1/agents/web-agent/request", true},
		{"sam discovery", "solace-agent-mesh/v1/discovery/>",
			"solace-agent-mesh/v1/discovery/agent-cards", true},

		{"empty subscription vs empty topic", "", "", true},
		{"trailing slash", "a/b/", "a/b/", true},
		{"different lengths", "a/b", "a/b/c", false},
		{"longer sub", "a/b/c/d", "a/b/c", false},

		{"dots in topic", "a.b/c.d", "a.b/c.d", true},
		{"dots no match", "a.b/c.d", "axb/cxd", false},
		{"plus in topic", "a+b/c", "a+b/c", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TopicMatch(tt.subscription, tt.topic)
			if got != tt.want {
				t.Errorf("TopicMatch(%q, %q) = %v, want %v",
					tt.subscription, tt.topic, got, tt.want)
			}
		})
	}
}
