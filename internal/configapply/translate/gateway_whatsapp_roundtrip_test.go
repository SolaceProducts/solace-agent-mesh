// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package translate

import (
	"encoding/json"
	"log/slog"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/whatsapp"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func TestWhatsAppValues_SurviveThePullApplyRoundTrip(t *testing.T) {
	t.Setenv("OBJECT_STORAGE_TYPE", "")
	t.Setenv("S3_BUCKET_NAME", "")

	uiValues := map[string]any{
		"phone_number_id":      "1234567890",
		"access_token":         "EAAtoken",
		"app_secret":           "appsecret",
		"verify_token":         "verifytoken",
		"default_agent_name":   "Manager",
		"disable_file_uploads": true,
		"run_as":               "ops",
	}

	slug := "whatsapp-support"
	spec, err := ReverseEntrypointV2(wire.EntrypointResponse{
		ID:             "wa-roundtrip",
		Type:           "entrypoint",
		Slug:           &slug,
		Name:           "WhatsApp Support",
		EntrypointType: "whatsapp",
		Values:         uiValues,
	}, EntrypointV2Hooks{})
	if err != nil {
		t.Fatalf("ReverseEntrypointV2: %v", err)
	}
	onDisk, err := yaml.Marshal(spec)
	if err != nil {
		t.Fatalf("marshal pulled spec: %v", err)
	}

	var reapplied map[string]any
	if err = yaml.Unmarshal(onDisk, &reapplied); err != nil {
		t.Fatalf("unmarshal pulled spec: %v\n%s", err, onDisk)
	}
	req, err := ForwardEntrypointV2(reapplied, EntrypointV2Hooks{})
	if err != nil {
		t.Fatalf("ForwardEntrypointV2: %v", err)
	}

	stored, err := json.Marshal(req.Values)
	if err != nil {
		t.Fatalf("marshal applied values: %v", err)
	}
	storedValues := string(stored)
	description := "round-trip fixture"
	gw := &store.Gateway{
		ID:          "wa-roundtrip",
		Name:        "WhatsApp Support",
		Description: &description,
		Type:        "whatsapp",
		Values:      &storedValues,
	}

	runtimeYAML, err := entplatform.NewYAMLService("test-ns", slog.Default()).GenerateGatewayYAML(gw)
	if err != nil {
		t.Fatalf("GenerateGatewayYAML: %v", err)
	}
	adapterCfg := adapterConfigOf(t, runtimeYAML)

	cfg, err := whatsapp.ParseWhatsAppConfig(adapterCfg)
	if err != nil {
		t.Fatalf("ParseWhatsAppConfig on the generated runtime config: %v\n%s", err, runtimeYAML)
	}

	if !cfg.DisableFileUploads {
		t.Error("disable_file_uploads did not survive the round trip — the adapter would still accept uploads the operator turned off")
	}
	if cfg.PhoneNumberID != "1234567890" || cfg.DefaultAgentName != "Manager" {
		t.Errorf("phone_number_id/default_agent_name = %q/%q, want 1234567890/Manager", cfg.PhoneNumberID, cfg.DefaultAgentName)
	}
	if cfg.AccessToken != "EAAtoken" {
		t.Errorf("access_token = %q, want the stored credential", cfg.AccessToken)
	}
	if cfg.RunAs != "ops" {
		t.Errorf("run_as = %q, want %q — the entrypoint would authorize senders as themselves despite the config", cfg.RunAs, "ops")
	}
}

func TestWhatsAppValues_ExplicitFalseSurvivesTheRoundTrip(t *testing.T) {
	t.Setenv("OBJECT_STORAGE_TYPE", "")
	t.Setenv("S3_BUCKET_NAME", "")

	values := map[string]any{
		"phone_number_id":      "1234567890",
		"access_token":         "EAAtoken",
		"app_secret":           "appsecret",
		"verify_token":         "verifytoken",
		"disable_file_uploads": false,
	}
	slug := "wa-false"
	spec, err := ReverseEntrypointV2(wire.EntrypointResponse{
		ID: "wa-false", Type: "entrypoint", Slug: &slug, Name: "WhatsApp Off", EntrypointType: "whatsapp", Values: values,
	}, EntrypointV2Hooks{})
	if err != nil {
		t.Fatalf("ReverseEntrypointV2: %v", err)
	}
	onDisk, err := yaml.Marshal(spec)
	if err != nil {
		t.Fatalf("marshal pulled spec: %v", err)
	}
	var reapplied map[string]any
	if err = yaml.Unmarshal(onDisk, &reapplied); err != nil {
		t.Fatalf("unmarshal pulled spec: %v", err)
	}
	req, err := ForwardEntrypointV2(reapplied, EntrypointV2Hooks{})
	if err != nil {
		t.Fatalf("ForwardEntrypointV2: %v", err)
	}
	stored, err := json.Marshal(req.Values)
	if err != nil {
		t.Fatalf("marshal applied values: %v", err)
	}
	storedValues := string(stored)
	description := "round-trip fixture"
	runtimeYAML, err := entplatform.NewYAMLService("test-ns", slog.Default()).GenerateGatewayYAML(&store.Gateway{
		ID: "wa-false", Name: "WhatsApp Off", Description: &description, Type: "whatsapp", Values: &storedValues,
	})
	if err != nil {
		t.Fatalf("GenerateGatewayYAML: %v", err)
	}
	adapterCfg := adapterConfigOf(t, runtimeYAML)
	if got, present := adapterCfg["disable_file_uploads"]; !present || got != false {
		t.Fatalf("adapter_config.disable_file_uploads = %v (present=%v), want an explicit false", got, present)
	}
	cfg, err := whatsapp.ParseWhatsAppConfig(adapterCfg)
	if err != nil {
		t.Fatalf("ParseWhatsAppConfig: %v", err)
	}
	if cfg.DisableFileUploads {
		t.Error("an explicitly stored false arrived as true")
	}
}

func adapterConfigOf(t *testing.T, runtimeYAML string) map[string]any {
	t.Helper()
	var parsed struct {
		Apps []struct {
			AppConfig struct {
				GatewayAdapter struct {
					Type          string         `yaml:"type"`
					AdapterConfig map[string]any `yaml:"adapter_config"`
				} `yaml:"gateway_adapter"`
			} `yaml:"app_config"`
		} `yaml:"apps"`
	}
	if err := yaml.Unmarshal([]byte(runtimeYAML), &parsed); err != nil {
		t.Fatalf("parse generated runtime YAML: %v\n%s", err, runtimeYAML)
	}
	if len(parsed.Apps) == 0 {
		t.Fatalf("generated runtime YAML has no apps:\n%s", runtimeYAML)
	}
	adapter := parsed.Apps[0].AppConfig.GatewayAdapter
	if adapter.Type != "whatsapp" {
		t.Fatalf("gateway_adapter.type = %q, want whatsapp", adapter.Type)
	}
	return adapter.AdapterConfig
}
