// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"log/slog"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/googlechat"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

var assistantContextKeys = []string{"systemPurpose", "responseFormat"}

func googleChatSampleValues(t *testing.T) map[string]any {
	t.Helper()
	return map[string]any{
		"pubsub_subscription":           "projects/chat-proj/subscriptions/chat-events",
		"service_account_key":           googleChatServiceAccountKey(t),
		"default_agent_name":            "HelpDesk",
		"allowed_email_domains":         "example.com, @Example.org",
		"use_user_identity_in_channels": true,
		"feedback_enabled":              true,
		"delivery":                      "pubsub",
		"auth_audience_type":            "endpoint_url",
		"endpoint_url":                  "https://chat.example.com/gw/gc/events",
		"addon_service_account_email":   "service-123456789012@gcp-sa-gsuiteaddons.iam.gserviceaccount.com",
		"project_number":                "123456789012",
		"run_as":                        "chat-ops",
		"identity_claim_key":            "upn",
		"min_edit_interval_ms":          float64(2500),
		"thread_history_limit":          float64(20),
		"artifact_drive_folder_id":      "0AExampleSharedDrive",
		"artifact_drive_access_days":    float64(14),
		"initial_status_message":        "Thinking...",
		"systemPurpose":                 "Answer HR questions.",
		"responseFormat":                "Plain text.",
	}
}

func googleChatAdapterConfig(t *testing.T, id string, values map[string]any) (appConfig, adapterConfig map[string]any) {
	t.Helper()
	t.Setenv("OBJECT_STORAGE_TYPE", "")
	t.Setenv("S3_BUCKET_NAME", "")

	raw, err := json.Marshal(values)
	if err != nil {
		t.Fatalf("marshal values: %v", err)
	}
	stored := string(raw)
	built, err := NewYAMLService("test-ns", slog.Default()).BuildGatewayConfig(&store.Gateway{
		ID:          id,
		Name:        "Google Chat GW",
		Description: strPtr("google chat fixture"),
		Type:        "google_chat",
		Values:      &stored,
	})
	if err != nil {
		t.Fatalf("BuildGatewayConfig: %v", err)
	}
	appConfig = appConfigFromBuilt(t, built)
	adapter, ok := appConfig["gateway_adapter"].(map[string]any)
	if !ok {
		t.Fatalf("gateway_adapter missing: %v", appConfig)
	}
	if adapter["type"] != "google_chat" {
		t.Errorf("gateway_adapter.type = %v, want google_chat", adapter["type"])
	}
	adapterConfig, ok = adapter["adapter_config"].(map[string]any)
	if !ok {
		t.Fatalf("adapter_config missing: %v", adapter)
	}
	return appConfig, adapterConfig
}

func TestBuildGoogleChatGatewayConfig_ThreadsEveryRegistryField(t *testing.T) {
	def, ok := platpkg.LookupGatewayType("google_chat")
	if !ok {
		t.Fatal("google_chat is not registered")
	}
	sample := googleChatSampleValues(t)
	for _, f := range def.Fields {
		if _, ok := sample[f.Name]; !ok {
			t.Fatalf("registry field %q has no sample value; add one so its threading is asserted", f.Name)
		}
	}
	if err := validateGoogleChatValues(sample); err != nil {
		t.Fatalf("sample values must be valid: %s", describeValidationError(err))
	}

	appConfig, adapterConfig := googleChatAdapterConfig(t, "gc-gw-1", sample)
	for _, f := range def.Fields {
		if slices.Contains(assistantContextKeys, f.Name) {
			continue
		}
		if _, ok := adapterConfig[f.Name]; !ok {
			t.Errorf("registry field %q never reaches adapter_config", f.Name)
		}
		if _, leaked := appConfig[f.Name]; leaked {
			t.Errorf("%s leaked to the app_config root, where the adapter parser never looks", f.Name)
		}
	}
	if appConfig["system_purpose"] != "Answer HR questions." || appConfig["response_format"] != "Plain text." {
		t.Errorf("assistant context not stamped: system_purpose=%v response_format=%v",
			appConfig["system_purpose"], appConfig["response_format"])
	}

	cfg, err := googlechat.ParseConfig(adapterConfig)
	if err != nil {
		t.Fatalf("the adapter refuses the generated config: %v", err)
	}
	want := googlechat.Config{
		Delivery:                  googlechat.DeliveryPubSub,
		PubSubSubscription:        "projects/chat-proj/subscriptions/chat-events",
		ProjectNumber:             "123456789012",
		ServiceAccountKey:         sample["service_account_key"].(string),
		DefaultAgentName:          "HelpDesk",
		IdentityClaimKey:          "upn",
		AllowedEmailDomains:       []string{"example.com", "example.org"},
		RunAs:                     "chat-ops",
		UseUserIdentityInChannels: true,
		FeedbackEnabled:           true,
		AudienceType:              googlechat.AudienceEndpointURL,
		EndpointURL:               "https://chat.example.com/gw/gc/events",
		AddOnServiceAccountEmail:  "service-123456789012@gcp-sa-gsuiteaddons.iam.gserviceaccount.com",
		MinEditInterval:           2500 * time.Millisecond,
		InitialStatusMessage:      "Thinking...",
		ThreadHistoryLimit:        20,
		ArtifactDriveFolderID:     "0AExampleSharedDrive",
		ArtifactDriveAccessDays:   14,
	}
	got := *cfg
	got.ChatAPIBaseURL, got.PubSubAPIBaseURL, got.DriveAPIBaseURL = "", "", ""
	got.DriveMaxArtifactBytes = 0
	got.DriveRemoveBackoff = 0
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parsed config = %+v\nwant %+v", got, want)
	}
}

func TestBuildGoogleChatGatewayConfig_SecretFallsBackToEnvVar(t *testing.T) {
	t.Setenv("GOOGLE_CHAT_SERVICE_ACCOUNT_KEY_GC_GW_2", `{"type":"service_account"}`)

	_, adapterConfig := googleChatAdapterConfig(t, "gc-gw-2", map[string]any{
		"delivery": "pubsub", "pubsub_subscription": "projects/p/subscriptions/s",
	})
	if got := adapterConfig["service_account_key"]; got != `{"type":"service_account"}` {
		t.Errorf("adapter_config.service_account_key = %v, want the cluster-injected env var value", got)
	}
}

func TestBuildGoogleChatGatewayConfig_NullsAreOmitted(t *testing.T) {
	values := map[string]any{
		"delivery": "pubsub", "pubsub_subscription": "projects/p/subscriptions/s",
		"identity_claim_key":   nil,
		"min_edit_interval_ms": nil,
		"run_as":               nil,
	}
	if err := validateGoogleChatValues(values); err != nil {
		t.Fatalf("nulls must validate as absent: %s", describeValidationError(err))
	}
	_, adapterConfig := googleChatAdapterConfig(t, "gc-gw-3", values)
	if _, err := googlechat.ParseConfig(adapterConfig); err != nil {
		t.Fatalf("the adapter refuses a config built from null values: %v", err)
	}
}

func TestBuildGoogleChatGatewayConfig_DerivesEndpointURL(t *testing.T) {
	t.Setenv("WEBUI_FRONTEND_SERVER_URL", "https://sam.example.com/")
	_, adapterConfig := googleChatAdapterConfig(t, "gc-gw-https", map[string]any{"delivery": "https"})
	if got := adapterConfig["endpoint_url"]; got != "https://sam.example.com/gw/gc-gw-https/events" {
		t.Errorf("endpoint_url = %v, want the derived events URL", got)
	}

	_, explicit := googleChatAdapterConfig(t, "gc-gw-https", map[string]any{"delivery": "https", "endpoint_url": "https://chat.example.com/events"})
	if got := explicit["endpoint_url"]; got != "https://chat.example.com/events" {
		t.Errorf("endpoint_url = %v, want the configured value kept", got)
	}

	_, pubsub := googleChatAdapterConfig(t, "gc-gw-pubsub", map[string]any{"delivery": "pubsub", "pubsub_subscription": "projects/p/subscriptions/s"})
	if _, set := pubsub["endpoint_url"]; set {
		t.Errorf("pubsub delivery got an endpoint_url: %v", pubsub["endpoint_url"])
	}
}
