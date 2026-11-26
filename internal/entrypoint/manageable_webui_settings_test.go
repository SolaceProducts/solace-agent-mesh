// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/webuisettings"
)

func newWebuiSettingsExec(t *testing.T, servesWebUI bool, registry *webuisettings.Registry, opts ...runtime.Option) *GatewayExecutor {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatalf("connect dev broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	rt := runtime.NewRuntime(brk, nil, opts...)
	factory := func(appConfig map[string]any, s runtime.Services) (*GatewayCore, error) {
		core := NewGatewayCore(&recordingAdapter{}, appConfig)
		core.SetServices(s)
		core.SetServesWebUI(servesWebUI)
		core.SetWebuiSettingsRegistry(registry)
		return core, nil
	}
	exe := NewGatewayExecutor(rt.Services(), factory)
	cfg := map[string]any{
		"namespace":                "test-ns",
		"gateway_id":               "webui-gw",
		"system_purpose":           "The configured purpose.",
		"response_format":          "The configured format.",
		"frontend_welcome_message": "Configured welcome.",
	}
	if err := exe.LoadConfig("webui-gw", cfg); err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if err := exe.Init(t.Context(), nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { _ = exe.Stop(context.Background()) })
	return exe
}

func webuiSettingsReaderIdentity() *auth.Identity {
	return &auth.Identity{UserID: "admin", Scopes: []string{rbac.ScopeWebuiSettingsRead}}
}

func TestExecutorWebuiSettings_NoWebUICore_ErrorsAndNotAdvertised(t *testing.T) {
	exe := newWebuiSettingsExec(t, false, webuisettings.NewRegistry(nil, nil))

	for _, ep := range exe.ManagementEndpoints() {
		if ep.Path == "webuiSettings" {
			t.Error("no webUI-serving core in this process, but webuiSettings is advertised")
		}
	}

	_, err := exe.HandleManagement(context.Background(), "get", []string{"webuiSettings"}, nil, webuiSettingsReaderIdentity())
	if err == nil || !strings.Contains(err.Error(), "no gateway instance in this process serves the web UI") {
		t.Errorf("error = %v, want 'no gateway instance ... serves the web UI'", err)
	}
}

func TestExecutorWebuiSettings_NoRegistry_Errors(t *testing.T) {
	exe := newWebuiSettingsExec(t, true, nil)

	var advertised bool
	for _, ep := range exe.ManagementEndpoints() {
		if ep.Path == "webuiSettings" {
			advertised = true
		}
	}
	if !advertised {
		t.Error("webUI-serving core exists but webuiSettings is not advertised")
	}

	_, err := exe.HandleManagement(context.Background(), "get", []string{"webuiSettings"}, nil, webuiSettingsReaderIdentity())
	if err == nil || !strings.Contains(err.Error(), "no settings registry configured") {
		t.Errorf("error = %v, want 'no settings registry configured'", err)
	}
}

func TestExecutorWebuiSettings_UnsupportedMethod(t *testing.T) {
	exe := newWebuiSettingsExec(t, true, webuisettings.NewRegistry(nil, nil))

	_, err := exe.HandleManagement(context.Background(), "post", []string{"webuiSettings"}, nil, webuiSettingsReaderIdentity())
	if err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Errorf("error = %v, want 'unsupported'", err)
	}
}

func TestExecutorWebuiSettings_FailsClosedOnPullError(t *testing.T) {
	failing := webuisettings.NewRegistry(func(context.Context) (*webuisettings.Settings, error) {
		return nil, errors.New("broker unreachable")
	}, nil)
	exe := newWebuiSettingsExec(t, true, failing)

	_, err := exe.HandleManagement(context.Background(), "get", []string{"webuiSettings"}, nil, webuiSettingsReaderIdentity())
	if err == nil || !strings.Contains(err.Error(), "resolve webui settings") {
		t.Errorf("error = %v, want a wrapped 'resolve webui settings' error rather than a degraded response", err)
	}
}

func TestExecutorWebuiSettings_ReturnsResolvedFieldsWithSources(t *testing.T) {
	appOverride := "Acme Assistant"
	reg := webuisettings.NewRegistry(func(context.Context) (*webuisettings.Settings, error) {
		return &webuisettings.Settings{AppName: &appOverride}, nil
	}, nil)
	exe := newWebuiSettingsExec(t, true, reg)

	res, err := exe.HandleManagement(context.Background(), "get", []string{"webuiSettings"}, nil, webuiSettingsReaderIdentity())
	if err != nil {
		t.Fatalf("HandleManagement: %v", err)
	}
	fields, ok := res.(map[string]webuiSettingsFieldResponse)
	if !ok {
		t.Fatalf("result type = %T, want map[string]webuiSettingsFieldResponse", res)
	}

	if fields["appName"].Value != appOverride || fields["appName"].Source != "override" {
		t.Errorf("appName = %+v, want {%q, override}", fields["appName"], appOverride)
	}
	if fields["systemPurpose"].Value != "The configured purpose." || fields["systemPurpose"].Source != "config" {
		t.Errorf("systemPurpose = %+v, want {%q, config}", fields["systemPurpose"], "The configured purpose.")
	}
	if fields["welcomeMessage"].Value != "Configured welcome." || fields["welcomeMessage"].Source != "config" {
		t.Errorf("welcomeMessage = %+v, want {%q, config}", fields["welcomeMessage"], "Configured welcome.")
	}
	if fields["disclaimerText"].Value != "" || fields["disclaimerText"].Source != "default" {
		t.Errorf("disclaimerText = %+v, want {\"\", default}", fields["disclaimerText"])
	}
	if len(fields) != 10 {
		t.Errorf("got %d fields, want all 10 settings keys", len(fields))
	}
}

func TestExecutorWebuiSettings_ScopeGate(t *testing.T) {
	reg := webuisettings.NewRegistry(nil, nil)
	exe := newWebuiSettingsExec(t, true, reg, runtime.WithControlAuthorizer(auth.DenyAllControlAuthorizer{}))
	path := []string{"webuiSettings"}

	if _, err := exe.HandleManagement(context.Background(), "get", path, nil, nil); err == nil ||
		!strings.Contains(err.Error(), "insufficient permissions") {
		t.Errorf("nil identity error = %v, want 'insufficient permissions'", err)
	}

	nonAdmin := &auth.Identity{UserID: "carol", Scopes: []string{"agent:research:invoke"}}
	if _, err := exe.HandleManagement(context.Background(), "get", path, nil, nonAdmin); err == nil ||
		!strings.Contains(err.Error(), "insufficient permissions") {
		t.Errorf("wrong-scope identity error = %v, want 'insufficient permissions'", err)
	}

	if _, err := exe.HandleManagement(context.Background(), "get", path, nil, webuiSettingsReaderIdentity()); err != nil {
		t.Errorf("reader-scope identity: unexpected error %v", err)
	}
}
