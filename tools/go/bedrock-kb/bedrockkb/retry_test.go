// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package bedrockkb

import (
	"context"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/awsretry"
)

func TestLoadAWSConfig_KeepsLegacyRetryer(t *testing.T) {
	cfg, err := loadAWSConfig(context.Background(), awsAuth{
		region:          "us-east-1",
		accessKeyID:     "AKIAIOSFODNN7EXAMPLE",
		secretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
	})
	if err != nil {
		t.Fatalf("loadAWSConfig: %v", err)
	}
	if cfg.Retryer == nil || !awsretry.IsLegacy(cfg.Retryer()) {
		t.Error("config does not carry the legacy retryer")
	}
}

func TestNewBedrockClients_KeepLegacyRetryer(t *testing.T) {
	clients, err := newBedrockClients(context.Background(), awsAuth{
		region:          "us-east-1",
		accessKeyID:     "AKIAIOSFODNN7EXAMPLE",
		secretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
	})
	if err != nil {
		t.Fatalf("newBedrockClients: %v", err)
	}
	if !awsretry.IsLegacy(clients.runtime.Options().Retryer) {
		t.Error("bedrock-agent-runtime client does not use the legacy retryer")
	}
	if !awsretry.IsLegacy(clients.agent.Options().Retryer) {
		t.Error("bedrock-agent client does not use the legacy retryer")
	}
}
