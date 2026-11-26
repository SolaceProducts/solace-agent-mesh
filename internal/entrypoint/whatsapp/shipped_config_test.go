// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package whatsapp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/testing/sourcetree"
)

func TestShippedE2EHarnessConfigStarts(t *testing.T) {
	sourcetree.RequireInternal(t)
	path := filepath.Join("..", "..", "..", "configs-e2e-harness", "whatsapp-gateway.yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	expanded := config.ExpandVars(string(raw), func(string) (string, bool) { return "", false })

	var doc struct {
		Apps []struct {
			AppConfig struct {
				GatewayAdapter struct {
					Type          string         `yaml:"type"`
					AdapterConfig map[string]any `yaml:"adapter_config"`
				} `yaml:"gateway_adapter"`
			} `yaml:"app_config"`
		} `yaml:"apps"`
	}
	if err = yaml.Unmarshal([]byte(expanded), &doc); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}

	var adapterCfg map[string]any
	for _, app := range doc.Apps {
		if app.AppConfig.GatewayAdapter.Type == "whatsapp" {
			adapterCfg = app.AppConfig.GatewayAdapter.AdapterConfig
		}
	}
	if adapterCfg == nil {
		t.Fatalf("no whatsapp gateway_adapter in %s", path)
	}

	cfg, err := ParseWhatsAppConfig(adapterCfg)
	if err != nil {
		t.Fatalf("ParseWhatsAppConfig: %v", err)
	}
	if err := checkTestOnlyConfig(cfg); err != nil {
		t.Fatalf("the shipped harness config is refused at startup with no environment set: %v", err)
	}
	if cfg.GraphAPIBaseURL == defaultGraphAPIBaseURL {
		t.Errorf("GraphAPIBaseURL fell back to the production host %q; a config that disables rate limiting must never point there", defaultGraphAPIBaseURL)
	}
}

func TestExampleConfigCarriesTheSameResponseFormat(t *testing.T) {
	sourcetree.RequireInternal(t)
	path := filepath.Join("..", "..", "..", "examples", "entrypoints", "whatsapp_entrypoint_example.yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var doc struct {
		Apps []struct {
			AppConfig struct {
				ResponseFormat string `yaml:"response_format"`
			} `yaml:"app_config"`
		} `yaml:"apps"`
	}
	if err = yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	if len(doc.Apps) == 0 {
		t.Fatalf("%s declares no apps", path)
	}
	got := strings.TrimRight(doc.Apps[0].AppConfig.ResponseFormat, "\n")
	if got != DefaultResponseFormat {
		t.Errorf("the example's response_format has drifted from DefaultResponseFormat.\n got: %q\nwant: %q", got, DefaultResponseFormat)
	}
}

func TestExampleConfigDocumentsEveryOptionalKey(t *testing.T) {
	sourcetree.RequireInternal(t)
	documented := map[string]any{
		"identity_lookup_key":               "phone",
		"identity_claim_key":                "phone_number",
		"contact_request_template":          "request_contact_info_1",
		"contact_request_template_language": "en_US",
		"disable_file_uploads":              false,
		"max_media_size_mb":                 25,
		"default_agent_name":                "Orchestrator",
		"graph_api_version":                 "v26.0",
	}

	path := filepath.Join("..", "..", "..", "examples", "entrypoints", "whatsapp_entrypoint_example.yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	text := string(raw)

	cfg := map[string]any{
		"access_token":    "EAAtoken",
		"phone_number_id": "1234567890",
		"app_secret":      "appsecret",
		"verify_token":    "verifytoken",
	}
	for key, value := range documented {
		if !strings.Contains(text, key+":") {
			t.Errorf("%s no longer appears in %s, so the example stopped documenting a key the adapter still reads", key, path)
		}
		cfg[key] = value
	}

	if _, err := ParseWhatsAppConfig(cfg); err != nil {
		t.Fatalf("the example's own keys do not parse: %v", err)
	}
}
