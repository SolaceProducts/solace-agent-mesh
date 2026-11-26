// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/googlechat"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

const googleChatTestKey = `{"type":"service_account","client_email":"sam@p.iam.gserviceaccount.com","private_key":"stored-private-key"}`

type capturedGoogleChatProbe struct {
	called int
	req    googlechat.ProbeRequest
	opts   googlechat.ProbeOptions
}

func captureGoogleChatProbe(t *testing.T, res googlechat.ProbeResult) *capturedGoogleChatProbe {
	t.Helper()
	prev := probeGoogleChat
	t.Cleanup(func() { probeGoogleChat = prev })
	captured := &capturedGoogleChatProbe{}
	probeGoogleChat = func(_ context.Context, req googlechat.ProbeRequest, opts googlechat.ProbeOptions) googlechat.ProbeResult {
		captured.called++
		captured.req = req
		captured.opts = opts
		return res
	}
	return captured
}

func googleChatValues() map[string]any {
	return map[string]any{
		"pubsub_subscription": "projects/p/subscriptions/chat-events",
		"service_account_key": googleChatTestKey,
		"default_agent_name":  "Orchestrator",
	}
}

func createGoogleChatGatewayForTest(t *testing.T, svc *GatewayService) string {
	t.Helper()
	b, err := json.Marshal(googleChatValues())
	if err != nil {
		t.Fatalf("marshal values: %v", err)
	}
	values := string(b)
	gw := &store.Gateway{Name: "test-google-chat", Type: "google_chat", Values: &values, CreatedBy: "test-user", UpdatedBy: "test-user"}
	if err := svc.gatewayRepo.Create(t.Context(), gw); err != nil {
		t.Fatalf("create gateway: %v", err)
	}
	return gw.ID
}

func TestTestConnection_DispatchesToGoogleChat(t *testing.T) {
	svc := newGatewayServiceWithGoogleChatFlag(t, true)
	probe := captureGoogleChatProbe(t, googlechat.ProbeResult{
		ErrorClass:   googlechat.ProbeClassSubscriptionDenied,
		Hint:         "Grant Pub/Sub Subscriber.",
		Stage:        googlechat.ProbeStageInbound,
		GoogleStatus: 403,
	})

	values := googleChatValues()
	values["pubsub_base_url"] = "http://attacker.example/collect"
	resp, err := svc.TestConnection(t.Context(), TestConnectionRequest{GatewayType: "google_chat", Values: values})
	if err != nil {
		t.Fatalf("expected nil error (the response carries the failure); got %v", err)
	}
	if probe.called != 1 {
		t.Fatalf("probe called %d times, want 1: the request fell through to the always-success default", probe.called)
	}
	want := TestConnectionResponse{Message: "Grant Pub/Sub Subscriber.", ErrorClass: string(googlechat.ProbeClassSubscriptionDenied), Stage: googlechat.ProbeStageInbound}
	if *resp != want {
		t.Errorf("response = %+v, want %+v", *resp, want)
	}
	if probe.req.ServiceAccountKey != googleChatTestKey || probe.req.PubSubSubscription != "projects/p/subscriptions/chat-events" {
		t.Errorf("probe request = %+v, want the submitted key and subscription", probe.req)
	}
	if probe.opts != (googlechat.ProbeOptions{}) {
		t.Errorf("probe options = %+v, want the zero value: a value-bag URL must never receive the service account's token", probe.opts)
	}
}

func TestTestGoogleChatConnection_SuccessIsBare(t *testing.T) {
	svc := newGatewayServiceWithGoogleChatFlag(t, true)
	captureGoogleChatProbe(t, googlechat.ProbeResult{Success: true})

	resp := svc.testGoogleChatConnection(t.Context(), googleChatValues())
	if *resp != (TestConnectionResponse{Success: true}) {
		t.Errorf("response = %+v, want bare success", *resp)
	}
}

func TestTestGoogleChatConnection_MergesRedactedKeyFromStored(t *testing.T) {
	svc := newGatewayServiceWithGoogleChatFlag(t, true)
	id := createGoogleChatGatewayForTest(t, svc)
	probe := captureGoogleChatProbe(t, googlechat.ProbeResult{Success: true})

	if _, err := svc.TestConnection(t.Context(), TestConnectionRequest{
		GatewayID: id,
		Values:    map[string]any{"service_account_key": redactedSentinel, "pubsub_subscription": "projects/p/subscriptions/edited"},
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if probe.req.ServiceAccountKey != googleChatTestKey {
		t.Errorf("probe key = %q, want the stored key (the merge must resolve <REDACTED>)", probe.req.ServiceAccountKey)
	}
	if probe.req.PubSubSubscription != "projects/p/subscriptions/edited" {
		t.Errorf("probe subscription = %q, want the edited value", probe.req.PubSubSubscription)
	}
}

func TestTestGoogleChatConnection_PlaceholderValuesAreNotProbed(t *testing.T) {
	svc := newGatewayServiceWithGoogleChatFlag(t, true)
	probe := captureGoogleChatProbe(t, googlechat.ProbeResult{Success: true})

	for _, key := range []string{"service_account_key", "pubsub_subscription"} {
		t.Run(key, func(t *testing.T) {
			values := googleChatValues()
			values[key] = "<<__SAM_SECRET_GOOGLE_CHAT__>>"
			resp := svc.testGoogleChatConnection(t.Context(), values)
			if resp.Success || resp.ErrorClass != string(googlechat.ProbeClassIncomplete) || !strings.Contains(resp.Message, "substituted") {
				t.Errorf("response = %+v, want a config_incomplete refusal naming substitution", *resp)
			}
		})
	}
	if probe.called != 0 {
		t.Errorf("probe called %d times for placeholder values, want 0", probe.called)
	}
}

func TestTestGoogleChatConnection_RefusedWhenFeatureDisabled(t *testing.T) {
	svc := newGatewayServiceWithGoogleChatFlag(t, false)
	probe := captureGoogleChatProbe(t, googlechat.ProbeResult{Success: true})

	if _, err := svc.TestConnection(t.Context(), TestConnectionRequest{GatewayType: "google_chat", Values: googleChatValues()}); err == nil {
		t.Fatal("expected a rejection when the google_chat feature is disabled")
	}
	if probe.called != 0 {
		t.Errorf("probe called %d times with the feature disabled, want 0", probe.called)
	}
}

func TestTestGoogleChatConnection_BreadcrumbCarriesNoSecrets(t *testing.T) {
	const googleText = "Resource projects/p/subscriptions/chat-events denied for sam@p.iam.gserviceaccount.com"
	var buf bytes.Buffer
	svc := newGatewayServiceWithGoogleChatFlag(t, true)
	svc.logger = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	captureGoogleChatProbe(t, googlechat.ProbeResult{
		ErrorClass:   googlechat.ProbeClassSubscriptionDenied,
		Hint:         googleText,
		Stage:        googlechat.ProbeStageInbound,
		GoogleStatus: 403,
	})

	svc.testGoogleChatConnection(t.Context(), googleChatValues())

	out := buf.String()
	for _, want := range []string{"google_chat.test_connection_failed", "errorClass=subscription_permission_denied", "stage=inbound", "googleStatus=403"} {
		if !strings.Contains(out, want) {
			t.Errorf("breadcrumb missing %q: %s", want, out)
		}
	}
	for _, leak := range []string{"stored-private-key", "chat-events", "sam@p.iam", googleText} {
		if strings.Contains(out, leak) {
			t.Errorf("breadcrumb leaks %q: %s", leak, out)
		}
	}
}
