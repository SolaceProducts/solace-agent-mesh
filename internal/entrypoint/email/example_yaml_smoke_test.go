// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"os"
	"regexp"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/SolaceProducts/solace-agent-mesh/internal/testing/sourcetree"
)

var expandVarsPatternForTest = regexp.MustCompile(`\$\{([^}:\s]+)(?:\s*,\s*((?:[^$}]|\$[^{}]|\$\{[^}]*\}|\$)*))?\}`)

func expandVarsForTest(text string, lookup func(string) (string, bool)) string {
	return expandVarsPatternForTest.ReplaceAllStringFunc(text, func(match string) string {
		groups := expandVarsPatternForTest.FindStringSubmatch(match)
		varName := groups[1]
		defaultVal := groups[2]
		if val, ok := lookup(varName); ok {
			return val
		}
		return expandVarsForTest(defaultVal, lookup)
	})
}

func TestExampleYAML_ParsesIntoEmailConfig(t *testing.T) {
	sourcetree.RequireInternal(t)
	t.Setenv("GMAIL_POC_USERNAME", "poc@gmail.com")
	t.Setenv("GMAIL_POC_APP_PASSWORD", "abcdefghijklmnop")

	raw, err := os.ReadFile("../../../examples/entrypoints/email_entrypoint_example.yaml")
	if err != nil {
		t.Fatalf("read example yaml: %v", err)
	}
	expanded := expandVarsForTest(string(raw), os.LookupEnv)

	var top map[string]any
	if err := yaml.Unmarshal([]byte(expanded), &top); err != nil {
		t.Fatalf("yaml unmarshal: %v", err)
	}

	apps, _ := top["apps"].([]any)
	if len(apps) == 0 {
		t.Fatalf("apps array missing or empty")
	}
	app, _ := apps[0].(map[string]any)
	appCfg, _ := app["app_config"].(map[string]any)
	ga, ok := appCfg["gateway_adapter"].(map[string]any)
	if !ok {
		t.Fatalf("gateway_adapter block missing")
	}
	if got := ga["type"]; got != "email" {
		t.Fatalf("gateway_adapter.type = %v, want email", got)
	}
	adapterCfg, _ := ga["adapter_config"].(map[string]any)

	if _, err := ParseEmailConfig(adapterCfg); err != nil {
		t.Fatalf("ParseEmailConfig rejected example yaml: %v", err)
	}
}
