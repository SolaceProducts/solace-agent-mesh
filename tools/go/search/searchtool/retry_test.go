// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package searchtool

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
