// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/testing/sourcetree"
)

func TestExampleYAML_ParsesIntoConfig(t *testing.T) {
	t.Setenv("GOOGLE_CHAT_SUBSCRIPTION", "projects/p/subscriptions/s")
	t.Setenv("GOOGLE_CHAT_RUN_AS", "chat-bot")

	cfg, err := ParseConfig(exampleAdapterConfig(t))
	if err != nil {
		t.Fatalf("ParseConfig rejected the example: %v", err)
	}
	if cfg.PubSubSubscription != "projects/p/subscriptions/s" || cfg.RunAs != "chat-bot" || cfg.ServiceAccountKey != "" {
		t.Errorf("example parsed to %+v", cfg)
	}
}

func TestExampleYAML_CarriesServiceAccountKeyThroughSubstitution(t *testing.T) {
	compact := googleServiceAccountKey(t)
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, []byte(compact), "", "  "); err != nil {
		t.Fatal(err)
	}
	for name, key := range map[string]string{"one line": compact, "pretty printed": pretty.String()} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("GOOGLE_CHAT_SUBSCRIPTION", "projects/p/subscriptions/s")
			t.Setenv("GOOGLE_CHAT_SERVICE_ACCOUNT_KEY", key)

			cfg, err := ParseConfig(exampleAdapterConfig(t))
			if err != nil {
				t.Fatalf("ParseConfig rejected the example with a real key: %v", err)
			}
			var parsed struct {
				ClientEmail string `json:"client_email"`
			}
			if err := json.Unmarshal([]byte(cfg.ServiceAccountKey), &parsed); err != nil {
				t.Fatalf("service_account_key is no longer JSON after substitution: %v", err)
			}
			if parsed.ClientEmail != testServiceAccountEmail {
				t.Errorf("client_email = %q, want %q", parsed.ClientEmail, testServiceAccountEmail)
			}
		})
	}
}

func exampleAdapterConfig(t *testing.T) map[string]any {
	t.Helper()
	sourcetree.RequireInternal(t)
	raw, err := os.ReadFile("../../../examples/entrypoints/google_chat_entrypoint_example.yaml")
	if err != nil {
		t.Fatalf("read example yaml: %v", err)
	}
	var top struct {
		Apps []struct {
			AppConfig struct {
				GatewayAdapter struct {
					Type          string         `yaml:"type"`
					AdapterConfig map[string]any `yaml:"adapter_config"`
				} `yaml:"gateway_adapter"`
			} `yaml:"app_config"`
		} `yaml:"apps"`
	}
	if yamlErr := yaml.Unmarshal([]byte(config.ExpandVarsEnv(string(raw))), &top); yamlErr != nil {
		t.Fatalf("yaml unmarshal: %v", yamlErr)
	}
	if len(top.Apps) == 0 || top.Apps[0].AppConfig.GatewayAdapter.Type != EntrypointType {
		t.Fatalf("example is not a %s gateway: %+v", EntrypointType, top)
	}
	return top.Apps[0].AppConfig.GatewayAdapter.AdapterConfig
}
