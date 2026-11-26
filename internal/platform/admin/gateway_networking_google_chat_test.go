// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func TestRequiresIngress_GoogleChatOnlyForHTTPSDelivery(t *testing.T) {
	for name, tc := range map[string]struct {
		values *string
		want   bool
	}{
		"pubsub":    {values: new(`{"delivery":"pubsub"}`), want: false},
		"https":     {values: new(`{"delivery":"https"}`), want: true},
		"no values": {values: nil, want: true},
		"default":   {values: new(`{"endpoint_url":"https://sam.example.com/gw/gc/events"}`), want: true},
		"corrupt":   {values: new(`{not json`), want: false},
	} {
		t.Run(name, func(t *testing.T) {
			var logs bytes.Buffer
			if got := requiresIngress(&store.Gateway{Type: "google_chat", Values: tc.values}, slog.New(slog.NewTextHandler(&logs, nil))); got != tc.want {
				t.Errorf("requiresIngress = %v, want %v", got, tc.want)
			}
			if warned := strings.Contains(logs.String(), "level=WARN"); warned != (name == "corrupt") {
				t.Errorf("warned = %v (%s); only corrupt values are worth a warning", warned, logs.String())
			}
		})
	}
	if !requiresIngress(&store.Gateway{Type: "teams"}, slog.New(slog.DiscardHandler)) {
		t.Error("types in the static set must still require ingress")
	}
	if paths := getAPIPaths("google_chat", ""); len(paths) != 1 || paths[0].Path != "/events" {
		t.Errorf("google_chat API paths = %+v, want /events", paths)
	}
}

func TestGoogleChatEndpointURL(t *testing.T) {
	t.Setenv("WEBUI_FRONTEND_SERVER_URL", "https://sam.example.com")
	for name, tc := range map[string]struct {
		values string
		want   string
	}{
		"derived from the external base": {values: `{}`, want: "https://sam.example.com/gw/hr-chat/events"},
		"configured endpoint wins":       {values: `{"endpoint_url":"https://chat.example.com/events"}`, want: "https://chat.example.com/events"},
		"pubsub has none":                {values: `{"delivery":"pubsub"}`, want: ""},
	} {
		t.Run(name, func(t *testing.T) {
			if got := googleChatEndpointURL(&store.Gateway{ID: "gw-1", Slug: "hr-chat", Type: "google_chat", Values: new(tc.values)}); got != tc.want {
				t.Errorf("googleChatEndpointURL = %q, want %q", got, tc.want)
			}
		})
	}
}
