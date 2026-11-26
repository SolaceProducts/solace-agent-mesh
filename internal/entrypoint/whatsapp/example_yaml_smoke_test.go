// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package whatsapp

import (
	"os"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/testing/sourcetree"
)

func exampleAdapterConfig(t *testing.T) map[string]any {
	t.Helper()
	sourcetree.RequireInternal(t)
	raw, err := os.ReadFile("../../../examples/entrypoints/whatsapp_entrypoint_example.yaml")
	if err != nil {
		t.Fatalf("read example yaml: %v", err)
	}
	var top map[string]any
	if err := yaml.Unmarshal([]byte(config.ExpandVarsEnv(string(raw))), &top); err != nil {
		t.Fatalf("yaml unmarshal: %v", err)
	}
	apps, _ := top["apps"].([]any)
	if len(apps) == 0 {
		t.Fatal("apps array missing or empty")
	}
	app, _ := apps[0].(map[string]any)
	appCfg, _ := app["app_config"].(map[string]any)
	ga, ok := appCfg["gateway_adapter"].(map[string]any)
	if !ok {
		t.Fatal("gateway_adapter block missing")
	}
	if got := ga["type"]; got != "whatsapp" {
		t.Fatalf("gateway_adapter.type = %v, want whatsapp", got)
	}
	adapterCfg, _ := ga["adapter_config"].(map[string]any)
	return adapterCfg
}

func TestExampleYAML_ParsesIntoWhatsAppConfig(t *testing.T) {
	t.Setenv("WHATSAPP_ACCESS_TOKEN", "EAAtoken")
	t.Setenv("WHATSAPP_PHONE_NUMBER_ID", "1234567890")
	t.Setenv("WHATSAPP_APP_SECRET", "appsecret")
	t.Setenv("WHATSAPP_VERIFY_TOKEN", "verifytoken")

	if _, err := ParseWhatsAppConfig(exampleAdapterConfig(t)); err != nil {
		t.Fatalf("ParseWhatsAppConfig rejected example yaml: %v", err)
	}
}

func TestExampleYAML_RunAsIsReachableFromTheConfigFile(t *testing.T) {
	t.Setenv("WHATSAPP_ACCESS_TOKEN", "EAAtoken")
	t.Setenv("WHATSAPP_PHONE_NUMBER_ID", "1234567890")
	t.Setenv("WHATSAPP_APP_SECRET", "appsecret")
	t.Setenv("WHATSAPP_VERIFY_TOKEN", "verifytoken")

	if _, present := exampleAdapterConfig(t)["run_as"]; !present {
		t.Fatal("the example config does not mention run_as; a key absent from the canonical file is one nobody copying it discovers")
	}

	unset, err := ParseWhatsAppConfig(exampleAdapterConfig(t))
	if err != nil {
		t.Fatalf("ParseWhatsAppConfig: %v", err)
	}
	if unset.RunAs != "" {
		t.Errorf("RunAs = %q with WHATSAPP_RUN_AS unset, want empty — copying the example must not swap the principal", unset.RunAs)
	}

	t.Setenv("WHATSAPP_RUN_AS", "ops")
	set, err := ParseWhatsAppConfig(exampleAdapterConfig(t))
	if err != nil {
		t.Fatalf("ParseWhatsAppConfig with run_as set: %v", err)
	}
	if set.RunAs != "ops" {
		t.Errorf("RunAs = %q, want %q — run_as did not reach the adapter from the config file", set.RunAs, "ops")
	}
}
