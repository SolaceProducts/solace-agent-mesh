// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package graphdb

import (
	"context"
	"testing"
	"time"
)

func TestAuthFromConfig(t *testing.T) {
	config := map[string]any{
		"region":                "eu-west-1",
		"aws_access_key_id":     "AKID",
		"aws_secret_access_key": "SECRET",
		"role_arn":              "arn:aws:iam::123456789012:role/TestRole",
		"external_id":           "ext-123",
		"session_name":          "test-session",
	}
	auth := authFromConfig(config)
	if auth.region != "eu-west-1" {
		t.Errorf("region = %q, want %q", auth.region, "eu-west-1")
	}
	if auth.accessKeyID != "AKID" {
		t.Errorf("accessKeyID = %q", auth.accessKeyID)
	}
	if auth.secretAccessKey != "SECRET" {
		t.Errorf("secretAccessKey = %q", auth.secretAccessKey)
	}
	if auth.roleARN != "arn:aws:iam::123456789012:role/TestRole" {
		t.Errorf("roleARN = %q", auth.roleARN)
	}
}

func TestAuthFromConfig_Defaults(t *testing.T) {
	auth := authFromConfig(map[string]any{})
	if auth.region != "us-east-1" {
		t.Errorf("default region = %q, want %q", auth.region, "us-east-1")
	}
}

func TestNewNeptuneBackend_Config(t *testing.T) {
	config := map[string]any{
		"connection_string": "bolt+s://cluster.neptune.amazonaws.com:8182",
		"region":            "us-west-2",
	}
	nb := newNeptuneBackend(config)
	if nb.connStr != "bolt+s://cluster.neptune.amazonaws.com:8182" {
		t.Errorf("connStr = %q", nb.connStr)
	}
}

func TestNewNeptuneBackend_ConnectRequiresConnectionString(t *testing.T) {
	nb := newNeptuneBackend(map[string]any{
		"region": "us-east-1",
	})
	err := nb.Connect(t.Context())
	if err == nil {
		t.Fatal("expected error for missing connection_string")
	}
}

func TestNewNeptuneBackend_ConnectUnreachable(t *testing.T) {
	nb := newNeptuneBackend(map[string]any{
		"connection_string":     "bolt+s://127.0.0.1:1",
		"region":                "us-east-1",
		"aws_access_key_id":     "AKIAIOSFODNN7EXAMPLE",
		"aws_secret_access_key": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
	})

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	if err := nb.Connect(ctx); err == nil {
		_ = nb.Close(context.Background())
		t.Fatal("expected connect error for unreachable neptune endpoint")
	}
}
