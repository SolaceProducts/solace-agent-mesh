// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const probeTestSubscription = "projects/p/subscriptions/s"

type fakeGoogle struct {
	token        http.HandlerFunc
	chat         http.HandlerFunc
	pubsub       http.HandlerFunc
	pubsubCalls  atomic.Int32
	server       *httptest.Server
	tokenURL     string
	serviceKeyJS string
}

func newFakeGoogle(t *testing.T) *fakeGoogle {
	t.Helper()
	f := &fakeGoogle{}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		if f.token != nil {
			f.token(w, r)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"access_token": "probe-token", "token_type": "Bearer", "expires_in": 3600})
	})
	mux.HandleFunc("GET /chat/v1/spaces", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer probe-token" {
			writeJSON(w, http.StatusUnauthorized, googleErrorBody(http.StatusUnauthorized, ""))
			return
		}
		if f.chat != nil {
			f.chat(w, r)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"spaces": []any{}})
	})
	mux.HandleFunc("POST /pubsub/v1/{rest...}", func(w http.ResponseWriter, r *http.Request) {
		f.pubsubCalls.Add(1)
		if !strings.HasSuffix(r.URL.Path, "/"+probeTestSubscription+":testIamPermissions") {
			http.NotFound(w, r)
			return
		}
		if f.pubsub != nil {
			f.pubsub(w, r)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"permissions": []string{subscriptionConsumePermission}})
	})
	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)
	f.tokenURL = f.server.URL + "/token"
	f.serviceKeyJS = testServiceAccountKey(t, f.tokenURL)
	return f
}

func (f *fakeGoogle) options() ProbeOptions {
	return ProbeOptions{
		ChatBaseURL:   f.server.URL + "/chat/",
		PubSubBaseURL: f.server.URL + "/pubsub/",
		TokenURL:      f.tokenURL,
	}
}

func (f *fakeGoogle) probe(t *testing.T, req ProbeRequest) ProbeResult {
	t.Helper()
	if req.ServiceAccountKey == "" {
		req.ServiceAccountKey = f.serviceKeyJS
	}
	return Probe(t.Context(), req, f.options())
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func googleErrorBody(status int, reason string) map[string]any {
	errBody := map[string]any{"code": status, "message": "projects/p echoes back here"}
	if reason != "" {
		errBody["details"] = []any{map[string]any{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "reason": reason}}
	}
	return map[string]any{"error": errBody}
}

func respondWith(status int, reason string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, status, googleErrorBody(status, reason))
	}
}

func TestProbe_SucceedsForPubSubAndHTTPS(t *testing.T) {
	f := newFakeGoogle(t)
	if res := f.probe(t, ProbeRequest{Delivery: DeliveryPubSub, PubSubSubscription: probeTestSubscription}); !res.Success || res.Note != "" {
		t.Fatalf("pubsub probe = %+v, want an unqualified success", res)
	}
	if got := f.pubsubCalls.Load(); got != 1 {
		t.Fatalf("pubsub calls = %d, want 1", got)
	}
	res := f.probe(t, ProbeRequest{Delivery: DeliveryHTTPS})
	if !res.Success {
		t.Fatalf("https probe = %+v, want success", res)
	}
	if res.Note != ProbeNoteEndpointUntested {
		t.Errorf("https probe note = %q, want it to say the endpoint's reachability is untested", res.Note)
	}
	if got := f.pubsubCalls.Load(); got != 1 {
		t.Fatalf("https probe touched pubsub: calls = %d, want still 1", got)
	}
}

func TestProbe_UnsetDeliveryWithASubscriptionProbesPubSub(t *testing.T) {
	f := newFakeGoogle(t)
	if res := f.probe(t, ProbeRequest{PubSubSubscription: probeTestSubscription}); !res.Success {
		t.Fatalf("probe = %+v, want success", res)
	}
	if got := f.pubsubCalls.Load(); got != 1 {
		t.Errorf("pubsub calls = %d, want the subscription checked", got)
	}
}

func TestProbe_RefusesBeforeCallingGoogle(t *testing.T) {
	f := newFakeGoogle(t)
	f.token = func(w http.ResponseWriter, _ *http.Request) {
		t.Error("probe reached the token endpoint")
		w.WriteHeader(http.StatusInternalServerError)
	}
	foreignTokenURI := testServiceAccountKey(t, "https://attacker.example/token")
	var damaged map[string]string
	if err := json.Unmarshal([]byte(f.serviceKeyJS), &damaged); err != nil {
		t.Fatal(err)
	}
	damaged["private_key"] = damaged["private_key"][:200]
	damagedKey, err := json.Marshal(damaged)
	if err != nil {
		t.Fatal(err)
	}
	sub := ProbeRequest{Delivery: DeliveryPubSub, PubSubSubscription: probeTestSubscription}
	cases := []struct {
		name string
		key  string
		req  ProbeRequest
		opts ProbeOptions
		want ProbeErrorClass
	}{
		{name: "no key means ADC", key: " ", req: ProbeRequest{Delivery: DeliveryPubSub, PubSubSubscription: probeTestSubscription}, want: ProbeClassIncomplete},
		{name: "bare subscription id", key: f.serviceKeyJS, req: ProbeRequest{Delivery: DeliveryPubSub, PubSubSubscription: "chat-events"}, want: ProbeClassIncomplete},
		{name: "unsupported delivery", key: f.serviceKeyJS, req: ProbeRequest{Delivery: "push", PubSubSubscription: probeTestSubscription}, want: ProbeClassIncomplete},
		{name: "not json", key: "not-a-key", req: sub, want: ProbeClassKeyInvalid},
		{name: "not a service account", key: `{"type":"authorized_user"}`, req: sub, want: ProbeClassKeyInvalid},
		{name: "truncated private key", key: string(damagedKey), req: sub, opts: f.options(), want: ProbeClassKeyInvalid},
		{name: "foreign token_uri under production options", key: foreignTokenURI, req: sub, opts: ProbeOptions{}, want: ProbeClassKeyInvalid},
		{name: "foreign token_uri under test options", key: foreignTokenURI, req: sub, opts: f.options(), want: ProbeClassKeyInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.req.ServiceAccountKey = tc.key
			res := Probe(t.Context(), tc.req, tc.opts)
			if res.Success || res.ErrorClass != tc.want || res.Hint == "" {
				t.Fatalf("Probe = %+v, want class %q with a hint", res, tc.want)
			}
		})
	}
}

func TestProbe_ClassifiesGoogleRejections(t *testing.T) {
	cases := []struct {
		name      string
		configure func(*fakeGoogle)
		want      ProbeErrorClass
		stage     string
		status    int
	}{
		{name: "key revoked", configure: func(f *fakeGoogle) {
			f.token = func(w http.ResponseWriter, _ *http.Request) {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_grant"})
			}
		}, want: ProbeClassCredentialsRejected, stage: ProbeStageOutbound, status: http.StatusBadRequest},
		{name: "token endpoint throttled", configure: func(f *fakeGoogle) {
			f.token = func(w http.ResponseWriter, _ *http.Request) {
				writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "rate_limit_exceeded"})
			}
		}, want: ProbeClassRateLimited, stage: ProbeStageOutbound, status: http.StatusTooManyRequests},
		{name: "token endpoint down", configure: func(f *fakeGoogle) {
			f.token = func(w http.ResponseWriter, _ *http.Request) {
				writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "unavailable"})
			}
		}, want: ProbeClassUnknown, stage: ProbeStageOutbound, status: http.StatusServiceUnavailable},
		{name: "token endpoint redirects", configure: func(f *fakeGoogle) {
			f.token = func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, "https://attacker.example/token", http.StatusTemporaryRedirect)
			}
		}, want: ProbeClassUnknown, stage: ProbeStageOutbound, status: http.StatusTemporaryRedirect},
		{name: "chat api disabled", configure: func(f *fakeGoogle) { f.chat = respondWith(http.StatusForbidden, serviceDisabledReason) },
			want: ProbeClassChatAPIDisabled, stage: ProbeStageOutbound, status: http.StatusForbidden},
		{name: "chat app not configured", configure: func(f *fakeGoogle) { f.chat = respondWith(http.StatusNotFound, "") },
			want: ProbeClassChatAppNotConfigured, stage: ProbeStageOutbound, status: http.StatusNotFound},
		{name: "chat forbidden", configure: func(f *fakeGoogle) { f.chat = respondWith(http.StatusForbidden, "") },
			want: ProbeClassPermissionDenied, stage: ProbeStageOutbound, status: http.StatusForbidden},
		{name: "chat rate limited", configure: func(f *fakeGoogle) { f.chat = respondWith(http.StatusTooManyRequests, "") },
			want: ProbeClassRateLimited, stage: ProbeStageOutbound, status: http.StatusTooManyRequests},
		{name: "pubsub api disabled", configure: func(f *fakeGoogle) { f.pubsub = respondWith(http.StatusForbidden, serviceDisabledReason) },
			want: ProbeClassPubSubAPIDisabled, stage: ProbeStageInbound, status: http.StatusForbidden},
		{name: "subscription missing", configure: func(f *fakeGoogle) { f.pubsub = respondWith(http.StatusNotFound, "") },
			want: ProbeClassSubscriptionNotFound, stage: ProbeStageInbound, status: http.StatusNotFound},
		{name: "no subscriber role", configure: func(f *fakeGoogle) {
			f.pubsub = func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, http.StatusOK, map[string]any{}) }
		}, want: ProbeClassSubscriptionDenied, stage: ProbeStageInbound},
		{name: "subscription forbidden", configure: func(f *fakeGoogle) { f.pubsub = respondWith(http.StatusForbidden, "") },
			want: ProbeClassSubscriptionDenied, stage: ProbeStageInbound, status: http.StatusForbidden},
		{name: "pubsub server error", configure: func(f *fakeGoogle) { f.pubsub = respondWith(http.StatusInternalServerError, "") },
			want: ProbeClassUnknown, stage: ProbeStageInbound, status: http.StatusInternalServerError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeGoogle(t)
			tc.configure(f)
			res := f.probe(t, ProbeRequest{Delivery: DeliveryPubSub, PubSubSubscription: probeTestSubscription})
			if res.Success || res.ErrorClass != tc.want || res.Stage != tc.stage || res.GoogleStatus != tc.status {
				t.Fatalf("Probe = %+v, want class %q stage %q status %d", res, tc.want, tc.stage, tc.status)
			}
			if strings.Contains(res.Hint, "projects/p") {
				t.Fatalf("hint echoes Google's error text: %q", res.Hint)
			}
		})
	}
}

func TestProbe_TimesOutAsUnreachable(t *testing.T) {
	for _, stalled := range []string{"token", "chat", "pubsub"} {
		t.Run(stalled, func(t *testing.T) {
			f := newFakeGoogle(t)
			release := make(chan struct{})
			t.Cleanup(func() { close(release) })
			hang := func(http.ResponseWriter, *http.Request) { <-release }
			wantStage := ProbeStageOutbound
			switch stalled {
			case "token":
				f.token = hang
			case "chat":
				f.chat = hang
			case "pubsub":
				f.pubsub = hang
				wantStage = ProbeStageInbound
			}
			opts := f.options()
			opts.Timeout = 200 * time.Millisecond
			started := time.Now()
			res := Probe(t.Context(), ProbeRequest{ServiceAccountKey: f.serviceKeyJS, Delivery: DeliveryPubSub, PubSubSubscription: probeTestSubscription}, opts)
			if res.ErrorClass != ProbeClassNetworkUnreachable || res.Stage != wantStage {
				t.Fatalf("Probe = %+v, want network_unreachable on the %s stage", res, wantStage)
			}
			if elapsed := time.Since(started); elapsed > 2*time.Second {
				t.Fatalf("probe took %v with a 200ms timeout", elapsed)
			}
		})
	}
}
