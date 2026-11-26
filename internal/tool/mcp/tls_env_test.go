// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"os"
	"testing"
)

func withEnvVar(t *testing.T, key string, set bool, val string) {
	t.Helper()
	t.Setenv(key, val)
	if !set {
		_ = os.Unsetenv(key)
	}
}

func TestTLSVerifyFromEnv(t *testing.T) {
	tests := []struct {
		name string
		set  bool
		val  string
		want bool
	}{
		{"unset", false, "", true},
		{"empty", true, "", true},
		{"whitespace", true, "   ", true},
		{"true", true, "true", true},
		{"false", true, "false", false},
		{"FALSE", true, "FALSE", false},
		{"False", true, "False", false},
		{"surrounding whitespace false", true, "  false  ", false},
		{"zero is not false", true, "0", true},
		{"no is not false", true, "no", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			withEnvVar(t, EnvMCPConnectorTLSVerify, tc.set, tc.val)
			if got := TLSVerifyFromEnv(); got != tc.want {
				t.Errorf("TLSVerifyFromEnv() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestEffectiveSSLVerify_EnvFallback(t *testing.T) {
	trueV, falseV := true, false
	tests := []struct {
		name   string
		cfg    ToolsetConfig
		envSet bool
		envVal string
		want   bool
	}{
		{"sse, no per-connector, env unset", ToolsetConfig{ConnectionType: connTypeSSE}, false, "", true},
		{"streamable-http, no per-connector, env unset", ToolsetConfig{ConnectionType: connTypeStreamableHTTP}, false, "", true},
		{"sse, no per-connector, env=false", ToolsetConfig{ConnectionType: connTypeSSE}, true, "false", false},
		{"streamable-http, no per-connector, env=false", ToolsetConfig{ConnectionType: connTypeStreamableHTTP}, true, "false", false},
		{"stdio, no per-connector, env=false (ignored)", ToolsetConfig{ConnectionType: connTypeStdio}, true, "false", true},
		{"sse, verify=true, env=false (per-connector wins)", ToolsetConfig{ConnectionType: connTypeSSE, SSLVerify: &trueV}, true, "false", true},
		{"sse, verify=false, env unset", ToolsetConfig{ConnectionType: connTypeSSE, SSLVerify: &falseV}, false, "", false},
		{"sse, verify=false, env=true (per-connector wins)", ToolsetConfig{ConnectionType: connTypeSSE, SSLVerify: &falseV}, true, "true", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			withEnvVar(t, EnvMCPConnectorTLSVerify, tc.envSet, tc.envVal)
			if got := tc.cfg.EffectiveSSLVerify(); got != tc.want {
				t.Errorf("EffectiveSSLVerify() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestHasSSLConfig_EnvFallback(t *testing.T) {
	tests := []struct {
		name   string
		cfg    ToolsetConfig
		envSet bool
		envVal string
		want   bool
	}{
		{"sse, env=false, no per-connector", ToolsetConfig{ConnectionType: connTypeSSE}, true, "false", true},
		{"streamable-http, env=false, no per-connector", ToolsetConfig{ConnectionType: connTypeStreamableHTTP}, true, "false", true},
		{"sse, env unset, no per-connector", ToolsetConfig{ConnectionType: connTypeSSE}, false, "", false},
		{"stdio, env=false, no per-connector (ignored)", ToolsetConfig{ConnectionType: connTypeStdio}, true, "false", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			withEnvVar(t, EnvMCPConnectorTLSVerify, tc.envSet, tc.envVal)
			if got := tc.cfg.HasSSLConfig(); got != tc.want {
				t.Errorf("HasSSLConfig() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestBuildHTTPClient_EnvFallback(t *testing.T) {
	t.Run("env=false, no per-connector skips verify", func(t *testing.T) {
		withEnvVar(t, EnvMCPConnectorTLSVerify, true, "false")
		cfg := &ToolsetConfig{ConnectionType: connTypeSSE}
		client, err := cfg.BuildHTTPClient()
		if err != nil {
			t.Fatalf("BuildHTTPClient: %v", err)
		}
		tc := clientTLSConfig(t, client)
		if !tc.InsecureSkipVerify {
			t.Errorf("InsecureSkipVerify = false, want true (env override should apply)")
		}
	})

	t.Run("env=false, per-connector verify=true keeps verify on", func(t *testing.T) {
		withEnvVar(t, EnvMCPConnectorTLSVerify, true, "false")
		trueV := true
		cfg := &ToolsetConfig{ConnectionType: connTypeSSE, SSLVerify: &trueV}
		client, err := cfg.BuildHTTPClient()
		if err != nil {
			t.Fatalf("BuildHTTPClient: %v", err)
		}
		tc := clientTLSConfig(t, client)
		if tc.InsecureSkipVerify {
			t.Errorf("InsecureSkipVerify = true, want false (per-connector verify=true must win)")
		}
	})

	t.Run("env unset, no per-connector keeps verify on", func(t *testing.T) {
		withEnvVar(t, EnvMCPConnectorTLSVerify, false, "")
		cfg := &ToolsetConfig{ConnectionType: connTypeSSE}
		client, err := cfg.BuildHTTPClient()
		if err != nil {
			t.Fatalf("BuildHTTPClient: %v", err)
		}
		tc := clientTLSConfig(t, client)
		if tc.InsecureSkipVerify {
			t.Errorf("InsecureSkipVerify = true, want false (default verify on)")
		}
	})
}
