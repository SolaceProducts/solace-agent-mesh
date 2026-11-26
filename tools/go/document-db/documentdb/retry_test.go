// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package documentdb

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"

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

func TestDynamoDBClientKeepsThreeAttemptsAndLegacyRetryer(t *testing.T) {
	cfg, err := loadAWSConfig(context.Background(), awsAuth{
		region:          "us-east-1",
		accessKeyID:     "AKIAIOSFODNN7EXAMPLE",
		secretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
	})
	if err != nil {
		t.Fatalf("loadAWSConfig: %v", err)
	}
	client := dynamodb.NewFromConfig(cfg, legacyRetryerOption)
	retryer := client.Options().Retryer
	if !awsretry.IsLegacy(retryer) {
		t.Fatal("dynamodb client does not use the legacy retryer")
	}
	if got := retryer.MaxAttempts(); got != 3 {
		t.Errorf("MaxAttempts = %d, want 3 (DynamoDB v1.74.0 would default to 4)", got)
	}
}
