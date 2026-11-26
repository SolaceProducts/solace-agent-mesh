// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package config

import (
	"context"
	"path/filepath"
	"testing"
)

func TestAsMap_LoaderOutputIsAsMapable(t *testing.T) {
	loader := &YAMLLoader{Lookup: mockLookup(map[string]string{})}
	dir := t.TempDir()
	writeTemp(t, dir, "app.yaml", "management_server:\n  port: 9090\nlog:\n  level: info\n")

	cfg, err := loader.Load(context.Background(), filepath.Join(dir, "app.yaml"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	m := AsMap(cfg)
	if m == nil {
		t.Fatal("AsMap(loaded config) = nil: ResolveManagementAddr / Build*RuntimeOptions would silently lose all YAML config — YAMLLoader.Load must return a *mapConfig")
	}
	if _, ok := m["management_server"].(map[string]any); !ok {
		t.Fatalf("AsMap dropped the management_server section: %#v", m)
	}
}

func TestAsMap_NonMapConfigReturnsNil(t *testing.T) {
	if got := AsMap(nil); got != nil {
		t.Errorf("AsMap(nil) = %v, want nil", got)
	}
}

func TestAsMap_ClonesUnderlyingData(t *testing.T) {
	cfg := FromMap(map[string]any{"management_server": map[string]any{"port": 9090}})
	m := AsMap(cfg)
	if m == nil {
		t.Fatal("AsMap(FromMap(...)) = nil")
	}
	m["injected"] = "tampered"

	again := AsMap(cfg)
	if _, leaked := again["injected"]; leaked {
		t.Error("AsMap returned a live reference, not a clone: caller mutation leaked back into the config")
	}
}
