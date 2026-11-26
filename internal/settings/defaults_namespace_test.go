// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package settings

import (
	"maps"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"gopkg.in/yaml.v3"
)

func TestBuiltInConfigsHonourNamespaceEnv(t *testing.T) {
	builtIn := maps.Clone(DefaultConfigYAML())
	maps.Copy(builtIn, AdditionalConfigYAML())

	cases := []struct {
		name   string
		lookup func(string) (string, bool)
		want   string
	}{
		{"set", lookupOnly("NAMESPACE", "sam-aa"), "sam-aa"},
		{"unset", func(string) (string, bool) { return "", false }, config.DefaultNamespace},
	}
	for _, tc := range cases {
		for file, raw := range builtIn {
			t.Run(tc.name+"/"+file, func(t *testing.T) {
				namespaces := appNamespaces(t, config.ExpandVars(string(raw), tc.lookup))
				if len(namespaces) == 0 {
					t.Fatalf("%s declares no app with an app_config.namespace", file)
				}
				for app, ns := range namespaces {
					if ns != tc.want {
						t.Errorf("%s app %q namespace = %q, want %q", file, app, ns, tc.want)
					}
				}
			})
		}
	}
}

func lookupOnly(key, value string) func(string) (string, bool) {
	return func(k string) (string, bool) {
		if k == key {
			return value, true
		}
		return "", false
	}
}

func appNamespaces(t *testing.T, expanded string) map[string]string {
	t.Helper()
	var doc struct {
		Apps []struct {
			Name      string `yaml:"name"`
			AppConfig struct {
				Namespace string `yaml:"namespace"`
			} `yaml:"app_config"`
		} `yaml:"apps"`
	}
	if err := yaml.Unmarshal([]byte(expanded), &doc); err != nil {
		t.Fatalf("parse expanded config: %v", err)
	}
	namespaces := make(map[string]string, len(doc.Apps))
	for _, app := range doc.Apps {
		namespaces[app.Name] = app.AppConfig.Namespace
	}
	return namespaces
}
