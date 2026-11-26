// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/googlechat"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

const googleChatAddOnSigner = "service-123456789012@gcp-sa-gsuiteaddons.iam.gserviceaccount.com"

func googleChatDetailsOrNone(t *testing.T, values map[string]any) map[string][]string {
	t.Helper()
	if validateGatewayValues("google_chat", values) == nil {
		return nil
	}
	return googleChatDetails(t, values)
}

func googleChatGateway(t *testing.T, values map[string]any) *store.Gateway {
	t.Helper()
	raw, err := json.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	stored := string(raw)
	return &store.Gateway{Type: "google_chat", Values: &stored}
}

func TestGoogleChat_LegacySubscriptionOnlyRowStaysOnPubSub(t *testing.T) {
	t.Setenv("WEBUI_FRONTEND_SERVER_URL", "https://sam.example.com")
	legacy := map[string]any{"pubsub_subscription": validGoogleChatSubscription}

	if err := validateGoogleChatValues(legacy); err != nil {
		t.Errorf("a stored Pub/Sub row is now refused: %v", err)
	}
	if details := googleChatDetails(t, map[string]any{"pubsub_subscription": "chat-events"}); len(details["pubsub_subscription"]) == 0 {
		t.Errorf("details = %v, want the subscription still validated as the Pub/Sub one it is", details)
	}

	_, adapterConfig := googleChatAdapterConfig(t, "gc-legacy", legacy)
	if adapterConfig["delivery"] != googlechat.DeliveryPubSub {
		t.Errorf("deployed delivery = %v, want pubsub", adapterConfig["delivery"])
	}
	if endpoint, set := adapterConfig["endpoint_url"]; set {
		t.Errorf("a Pub/Sub row was given an endpoint_url %v", endpoint)
	}
	cfg, err := googlechat.ParseConfig(adapterConfig)
	if err != nil || cfg.Delivery != googlechat.DeliveryPubSub {
		t.Errorf("adapter reads the deployed config as (%+v, %v), want pubsub", cfg, err)
	}
	if requiresIngress(googleChatGateway(t, legacy), slog.New(slog.DiscardHandler)) {
		t.Error("a Pub/Sub row was given a Network tab")
	}
}

func TestGetGoogleChatServesTheResolvedDelivery(t *testing.T) {
	svc := newGatewayServiceWithGoogleChatFlag(t, true)
	for name, tc := range map[string]struct {
		stored map[string]any
		want   string
	}{
		"subscription only":   {stored: map[string]any{"pubsub_subscription": validGoogleChatSubscription}, want: googlechat.DeliveryPubSub},
		"blank delivery":      {stored: map[string]any{"delivery": " ", "pubsub_subscription": validGoogleChatSubscription}, want: googlechat.DeliveryPubSub},
		"neither":             {stored: map[string]any{"endpoint_url": "https://chat.example.com/events"}, want: googlechat.DeliveryHTTPS},
		"explicit over stale": {stored: map[string]any{"delivery": "https", "pubsub_subscription": validGoogleChatSubscription}, want: googlechat.DeliveryHTTPS},
	} {
		t.Run(name, func(t *testing.T) {
			gw := googleChatGateway(t, tc.stored)
			gw.Name = "gc-" + strings.ReplaceAll(name, " ", "-")
			gw.CreatedBy, gw.UpdatedBy = "test-user", "test-user"
			desc := "Google Chat delivery test"
			gw.Description = &desc
			storedJSON := *gw.Values
			if err := svc.gatewayRepo.Create(t.Context(), gw); err != nil {
				t.Fatal(err)
			}

			resp, err := svc.GetByID(t.Context(), gw.ID)
			if err != nil {
				t.Fatal(err)
			}
			values, _ := resp.Values.(map[string]any)
			if values["delivery"] != tc.want {
				t.Errorf("served delivery = %v, want %q", values["delivery"], tc.want)
			}

			row, err := svc.gatewayRepo.GetByID(t.Context(), gw.ID)
			if err != nil {
				t.Fatal(err)
			}
			if *row.Values != storedJSON {
				t.Errorf("stored values rewritten to %s, want %s", *row.Values, storedJSON)
			}
		})
	}
}

func TestNormalizeGatewayValues_GoogleChatAuthoredMatchesServed(t *testing.T) {
	authored := NormalizeGatewayValues("google_chat", map[string]any{"pubsub_subscription": validGoogleChatSubscription})
	if got := authored.(map[string]any)["delivery"]; got != googlechat.DeliveryPubSub {
		t.Errorf("authored delivery = %v, want %q to match the served bag", got, googlechat.DeliveryPubSub)
	}
}

func TestValidateGoogleChatValues_HTTPSAudienceMatchesTheAdapter(t *testing.T) {
	for name, tc := range map[string]struct {
		base       string
		values     map[string]any
		wantFields []string
	}{
		"derived endpoint": {base: "https://sam.example.com", values: map[string]any{}},
		"explicit endpoint, no base": {
			values: map[string]any{"endpoint_url": "https://chat.example.com/events"},
		},
		"plain http endpoint": {
			base: "https://sam.example.com", values: map[string]any{"endpoint_url": "http://chat.example.com/events"}, wantFields: []string{"endpoint_url"},
		},
		"endpoint that is not a URL": {
			base: "https://sam.example.com", values: map[string]any{"endpoint_url": "chat.example.com/events"}, wantFields: []string{"endpoint_url"},
		},
		"project audience without a project number": {
			base: "https://sam.example.com", values: map[string]any{"auth_audience_type": "project_number"}, wantFields: []string{"project_number"},
		},
		"project audience needs no endpoint and no base": {
			values: map[string]any{"auth_audience_type": "project_number", "project_number": "123456789012"},
		},
		"add-on signer under the project audience": {
			base:       "https://sam.example.com",
			values:     map[string]any{"auth_audience_type": "project_number", "project_number": "1", "addon_service_account_email": googleChatAddOnSigner},
			wantFields: []string{"addon_service_account_email"},
		},
		"every audience error at once": {
			values: map[string]any{
				"auth_audience_type": "project_number", "addon_service_account_email": "someone@example.com",
				"min_edit_interval_ms": float64(-1),
			},
			wantFields: []string{"addon_service_account_email", "project_number", "min_edit_interval_ms"},
		},
		"no base to derive from": {values: map[string]any{}, wantFields: []string{"endpoint_url"}},
		"base that is not https": {base: "http://localhost:8000", values: map[string]any{}, wantFields: []string{"endpoint_url"}},
		"unknown audience type": {
			base: "https://sam.example.com", values: map[string]any{"auth_audience_type": "anyone"}, wantFields: []string{"auth_audience_type"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("WEBUI_FRONTEND_SERVER_URL", tc.base)
			details := googleChatDetailsOrNone(t, tc.values)
			fields := make([]string, 0, len(details))
			for field := range details {
				fields = append(fields, field)
			}
			slices.Sort(fields)
			want := slices.Sorted(slices.Values(tc.wantFields))
			if !slices.Equal(fields, want) {
				t.Errorf("error fields = %v (%v), want %v", fields, details, want)
			}

			_, adapterConfig := googleChatAdapterConfig(t, "gc-https", tc.values)
			_, parseErr := googlechat.ParseConfig(adapterConfig)
			audienceOnly := !slices.Contains(tc.wantFields, "min_edit_interval_ms")
			if audienceOnly && (parseErr == nil) != (len(details) == 0) {
				t.Errorf("platform details = %v but the adapter's ParseConfig = %v; they must agree", details, parseErr)
			}
		})
	}
}

func TestValidateGoogleChatValues_NoBaseMessageNamesTheMissingAddress(t *testing.T) {
	t.Setenv("WEBUI_FRONTEND_SERVER_URL", "")
	details := googleChatDetails(t, map[string]any{})
	if len(details["endpoint_url"]) != 1 || !strings.Contains(details["endpoint_url"][0], "WEBUI_FRONTEND_SERVER_URL") {
		t.Errorf("endpoint_url errors = %v, want one naming the missing external address", details["endpoint_url"])
	}
}

func TestGoogleChatRegistryMatchesTheAdapter(t *testing.T) {
	def, ok := platpkg.LookupGatewayType("google_chat")
	if !ok {
		t.Fatal("google_chat not registered")
	}
	field := func(name string) platpkg.FieldDef {
		for _, f := range def.Fields {
			if f.Name == name {
				return f
			}
		}
		t.Fatalf("registry has no %s field", name)
		return platpkg.FieldDef{}
	}
	options := func(f platpkg.FieldDef) []string {
		var values []string
		for _, o := range f.OneOf {
			values = append(values, o.Value)
		}
		slices.Sort(values)
		return values
	}

	delivery := field("delivery")
	if delivery.Default != googlechat.ResolveDelivery(nil) {
		t.Errorf("delivery default = %v, want the adapter's default %q", delivery.Default, googlechat.ResolveDelivery(nil))
	}
	if got, want := options(delivery), slices.Sorted(slices.Values([]string{googlechat.DeliveryHTTPS, googlechat.DeliveryPubSub})); !slices.Equal(got, want) {
		t.Errorf("delivery options = %v, want %v", got, want)
	}

	audience := field("auth_audience_type")
	if audience.Default != googlechat.AudienceEndpointURL {
		t.Errorf("auth_audience_type default = %v, want %q", audience.Default, googlechat.AudienceEndpointURL)
	}
	if got, want := options(audience), slices.Sorted(slices.Values([]string{googlechat.AudienceEndpointURL, googlechat.AudienceProjectNumber})); !slices.Equal(got, want) {
		t.Errorf("auth_audience_type options = %v, want %v", got, want)
	}

	if got := field("addon_service_account_email").Pattern; got != googlechat.AddOnSignerPattern {
		t.Errorf("addon_service_account_email pattern = %q, want the adapter's %q", got, googlechat.AddOnSignerPattern)
	}
	if got := field("pubsub_subscription").ConditionalDisplay["delivery"]; got != googlechat.DeliveryPubSub {
		t.Errorf("pubsub_subscription shows for delivery %v, want %q", got, googlechat.DeliveryPubSub)
	}
	for _, name := range []string{"endpoint_url", "addon_service_account_email"} {
		if got := field(name).ConditionalDisplay; len(got) != 0 {
			t.Errorf("%s has ConditionalDisplay %v, want none: the form does not offer delivery, so a condition on it would hide the field", name, got)
		}
	}
}

func TestTestConnection_GoogleChatHTTPSChecksTheAudienceFirst(t *testing.T) {
	t.Setenv("WEBUI_FRONTEND_SERVER_URL", "https://sam.example.com")
	svc := newGatewayServiceWithGoogleChatFlag(t, true)
	probe := captureGoogleChatProbe(t, googlechat.ProbeResult{Success: true, Note: googlechat.ProbeNoteEndpointUntested})

	resp, err := svc.TestConnection(t.Context(), TestConnectionRequest{GatewayType: "google_chat", Values: map[string]any{
		"delivery": "https", "endpoint_url": "http://chat.example.com/events", "service_account_key": googleChatTestKey,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if probe.called != 0 || resp.Success || resp.ErrorClass != string(googlechat.ProbeClassIncomplete) || !strings.HasPrefix(resp.Message, "endpoint_url ") {
		t.Errorf("response = %+v (probe called %d), want the endpoint refused before probing", *resp, probe.called)
	}

	resp, err = svc.TestConnection(t.Context(), TestConnectionRequest{GatewayType: "google_chat", Values: map[string]any{
		"delivery": "https", "service_account_key": googleChatTestKey,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Success || resp.Message != googlechat.ProbeNoteEndpointUntested {
		t.Errorf("response = %+v, want success that says the endpoint's reachability was not tested", *resp)
	}
}
