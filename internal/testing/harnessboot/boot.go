// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harnessboot

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/embedded"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samembed"
	"github.com/SolaceProducts/solace-agent-mesh/internal/testing/harness"
	"gopkg.in/yaml.v3"
)

func randomID() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

var bootMu sync.Mutex

type GatewayHandle struct {
	BaseURL string
}

func StartFakeIDPs(t *testing.T, specs []harness.FakeIDPSpec) map[string]*harness.MockOIDCProvider {
	t.Helper()
	out := make(map[string]*harness.MockOIDCProvider, len(specs))
	for _, spec := range specs {
		p, err := harness.NewMockOIDCProvider(harness.MockOIDCProviderConfig{
			ClientID:          spec.ClientID,
			ClientSecret:      spec.ClientSecret,
			Users:             spec.Users,
			IDTokenOnlyClaims: spec.IDTokenOnlyClaims,
		})
		if err != nil {
			t.Fatalf("StartFakeIDPs[%s]: %v", spec.Name, err)
		}
		t.Cleanup(func() { _ = p.Close() })
		out[spec.Name] = p
	}
	return out
}

func BootGateway(
	t *testing.T,
	scenario *harness.Scenario,
	catalog map[string]any,
) (*GatewayHandle, error) {
	t.Helper()
	if scenario == nil || scenario.GatewayAuth == nil {
		return nil, fmt.Errorf("BootGateway: scenario.GatewayAuth required")
	}

	bootMu.Lock()
	t.Cleanup(bootMu.Unlock)

	configDir := t.TempDir()
	keysDir := filepath.Join(configDir, "keys")
	if err := os.MkdirAll(keysDir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir keys: %w", err)
	}

	t.Setenv("SAM_DATA_DIR", configDir)

	gatewayID := "test-gw-" + randomID()
	keyFile := filepath.Join(keysDir, "_agent_"+gatewayID+".key")

	gatewayPort, err := reserveFreePort()
	if err != nil {
		return nil, fmt.Errorf("reserve gateway port: %w", err)
	}
	platformPort, err := reserveFreePort()
	if err != nil {
		return nil, fmt.Errorf("reserve platform port: %w", err)
	}

	appCfg := map[string]any{
		"namespace":                  "solace-agent-mesh",
		"gateway_id":                 gatewayID,
		"fastapi_host":               "127.0.0.1",
		"fastapi_port":               0,
		"system_purpose":             "OIDC declarative test gateway",
		"frontend_welcome_message":   "Test",
		"frontend_app_name":          "Test",
		"frontend_use_authorization": true,
		"external_auth_provider":     scenario.GatewayAuth.ExternalAuthProvider,
		"session_secret_key":         scenario.GatewayAuth.SessionSecretKey,
		"artifact_service":           map[string]any{"type": "memory"},
		"session_service":            map[string]any{"type": "memory", "default_behavior": "PERSISTENT"},
		"frontend_server_url":        fmt.Sprintf("http://127.0.0.1:%d", gatewayPort),
		"model": map[string]any{
			"model":    "openai/gpt-4o",
			"api_base": "http://127.0.0.1:1",
			"api_key":  "test-key",
		},
	}

	gatewayApp := map[string]any{"name": "test_gateway", "app_config": appCfg}
	apps := []any{gatewayApp}
	if scenario.GatewayAuth.AuthorizationService != nil {
		appCfg["authorization_service"] = scenario.GatewayAuth.AuthorizationService
		apps = append(apps, map[string]any{
			"name": "test_platform",
			"app_config": map[string]any{
				"component_type":        "platform",
				"namespace":             "solace-agent-mesh",
				"fastapi_host":          "127.0.0.1",
				"fastapi_port":          platformPort,
				"authorization_service": scenario.GatewayAuth.AuthorizationService,
			},
		})
	}

	fullCfg := map[string]any{
		"trust_manager": map[string]any{
			"enabled":                       true,
			"card_publish_interval_seconds": 5,
			"startup_wait_timeout_seconds":  2,
			"fail_on_timeout":               false,
		},
		"agent_identity": map[string]any{
			"key_mode":        "auto",
			"key_persistence": keyFile,
		},
		"audit_log": map[string]any{"enabled": true},
		"providers": catalog,
		"apps":      apps,
	}

	cfgYAML, err := yaml.Marshal(fullCfg)
	if err != nil {
		return nil, fmt.Errorf("marshal gateway config: %w", err)
	}
	cfgPath := filepath.Join(configDir, "gateway.yaml")
	if writeErr := os.WriteFile(cfgPath, cfgYAML, 0o644); writeErr != nil {
		return nil, fmt.Errorf("write gateway config: %w", writeErr)
	}

	inst := embedded.New(embedded.Config{
		ConfigPaths:  []string{configDir},
		Logger:       slog.New(slog.DiscardHandler),
		Extensions:   samembed.NewProvider(),
		GatewayPort:  gatewayPort,
		PlatformPort: platformPort,
		HealthAddr:   "disabled",
		SystemEnv:    true,
	})

	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer stopCancel()
		_ = inst.Stop(stopCtx)
	})

	if err := inst.Start(t.Context()); err != nil {
		return nil, fmt.Errorf("start embedded gateway: %w", err)
	}

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", gatewayPort)

	if err := waitForLoginReady(t.Context(), baseURL, 5*time.Second); err != nil {
		return nil, fmt.Errorf("login readiness: %w", err)
	}

	return &GatewayHandle{BaseURL: baseURL}, nil
}

func reserveFreePort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("listen on ephemeral port: %w", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	if err := ln.Close(); err != nil {
		return 0, fmt.Errorf("close ephemeral listener: %w", err)
	}
	return port, nil
}

func waitForLoginReady(ctx context.Context, baseURL string, timeout time.Duration) error {
	client := &http.Client{
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	deadline := time.Now().Add(timeout)
	var lastStatus int
	for {
		if time.Now().After(deadline) {
			return fmt.Errorf("login not ready after %v (last status: %d)", timeout, lastStatus)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/v1/auth/login", nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err == nil {
			lastStatus = resp.StatusCode
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusFound {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}
