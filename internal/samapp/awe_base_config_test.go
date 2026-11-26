// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samapp

import (
	"context"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/settings"
)

func TestAWEBaseConfig_ResolvesViaAWEStartupPath(t *testing.T) {
	loader := &config.YAMLLoader{}
	cfg, err := loader.LoadBytes(context.Background(), settings.AWEClusterBaseConfigYAML())
	if err != nil {
		t.Fatalf("load AWE base config: %v", err)
	}

	entries, err := LoadAppEntriesFromConfig(cfg)
	if err != nil {
		t.Fatalf("LoadAppEntriesFromConfig: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected no app entries from the base config, got %d", len(entries))
	}

	if got := FirstBrokerConfig(entries); got != nil {
		t.Errorf("FirstBrokerConfig = %v, want nil (base config declares no app)", got)
	}
	brokerCfg := ExtractBrokerConfigFromRaw(cfg)
	if brokerCfg == nil {
		t.Fatal("ExtractBrokerConfigFromRaw returned nil; AWE would have no broker on cluster")
	}
	if _, ok := brokerCfg["broker_url"]; !ok {
		t.Errorf("top-level broker config missing broker_url: %v", brokerCfg)
	}
}
