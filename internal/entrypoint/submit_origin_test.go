// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"maps"
	"testing"

	gwauth "github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const platformGatewayID = "0192f0c4-6a4e-7b1e-9d3c-5f2a8b7e1c41"

func TestSubmitTaskStampsTheEntrypointRowItWasDeployedFrom(t *testing.T) {
	origin := submitAndReadOrigin(t, &recordingAdapter{}, map[string]any{gatewayUUIDKey: platformGatewayID}, gwauth.ForUserIDOnly("user-1"), "user-1", nil)

	if origin.EntrypointUUID != platformGatewayID {
		t.Errorf("entrypoint uuid = %q, want the gateways.id %s", origin.EntrypointUUID, platformGatewayID)
	}
}

func TestSubmitTaskFromABuiltInEntrypointStampsNoEntrypointRow(t *testing.T) {
	if origin := submitAndReadOrigin(t, &recordingAdapter{}, nil, gwauth.ForUserIDOnly("user-1"), "user-1", nil); origin.EntrypointUUID != "" {
		t.Errorf("entrypoint uuid = %q, want none for an entrypoint with no platform row", origin.EntrypointUUID)
	}
}

func TestLoadConfigTakesTheEntrypointRowFromAPlatformAppName(t *testing.T) {
	for _, tc := range []struct {
		name, appName string
		appConfig     map[string]any
		want          string
	}{
		{"a snapshot rendered before gateway_uuid", platformGatewayID, map[string]any{}, platformGatewayID},
		{"the rendered key wins over the app name", platformGatewayID, map[string]any{gatewayUUIDKey: "0192f0c4-6a4e-7b1e-9d3c-5f2a8b7e1c99"}, "0192f0c4-6a4e-7b1e-9d3c-5f2a8b7e1c99"},
		{"a built-in app name is no row id", "webui-gateway", map[string]any{}, ""},
		{"a hand-written key that is not a canonical UUID is ignored", "webui-gateway", map[string]any{gatewayUUIDKey: "{0192F0C4-6A4E-7B1E-9D3C-5F2A8B7E1C99}"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			appConfig := map[string]any{"namespace": "test-ns"}
			maps.Copy(appConfig, tc.appConfig)
			exe := NewGatewayExecutor(testServices(t), fakeGatewayFactory)
			if err := exe.LoadConfig(tc.appName, appConfig); err != nil {
				t.Fatalf("LoadConfig: %v", err)
			}
			cfg, err := ParseGatewayConfig(exe.GetGateway(tc.appName).RawConfig())
			if err != nil {
				t.Fatalf("ParseGatewayConfig: %v", err)
			}
			if cfg.GatewayUUID != tc.want {
				t.Errorf("gateway uuid = %q, want %q", cfg.GatewayUUID, tc.want)
			}
		})
	}
}

func TestSubmitTaskStampsThePlatformUserTheTaskRunsFor(t *testing.T) {
	const platformUserID = "0192f0c4-6a4e-7b1e-9d3c-5f2a8b7e1c40"
	alice := gwauth.NewTestSubject(gwauth.SubjectFields{UserID: "alice@example.com", PlatformUserID: platformUserID})
	for _, tc := range []struct {
		name     string
		identity *gwauth.Subject
		userID   string
		want     string
	}{
		{"the caller's resolved row", alice, "alice@example.com", platformUserID},
		{"a caller the adapter submits under another principal", alice, rbac.DefaultSystemUserSub, rbac.DefaultSystemUserID},
		{"a baked-in system user", nil, rbac.EvalSystemUserSub, rbac.EvalSystemUserID},
		{"a custom system user has no seeded row yet", nil, "system:qa-bot", ""},
		{"a caller with no platform row", gwauth.NewTestSubject(gwauth.SubjectFields{UserID: "sam_dev_user"}), "sam_dev_user", ""},
		{"a baked-in system user's identity", gwauth.SystemUserIdentity(rbac.EvalSystemUserRunAs, ""), rbac.EvalSystemUserSub, rbac.EvalSystemUserID},
		{"a sentinel investigation SAM submits for itself on behalf of a user",
			sentinelIdentity(), "alice@example.com", rbac.DefaultSystemUserID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			caller := tc.identity
			if caller == nil {
				caller = gwauth.ForUserIDOnly(tc.userID)
			}
			signer := &recordingSigner{}
			if got := submitSignedAndReadOrigin(t, &recordingAdapter{}, nil, signer, caller, tc.userID, nil).UserUUID; got != tc.want {
				t.Errorf("user uuid = %q, want %q", got, tc.want)
			}
			if signed := signer.firstIdentity(); tc.identity != nil && (signed == nil || signed.PlatformUserID() != tc.want) {
				t.Errorf("signed identity = %+v, want the token to sign the stamped row %q", signed, tc.want)
			}
		})
	}
}

func TestAddAndStartTakesTheEntrypointRowFromAPlatformAppName(t *testing.T) {
	exe := NewGatewayExecutor(testServices(t), fakeGatewayFactory)
	ctx := t.Context()
	if err := exe.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := exe.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = exe.Stop(context.Background()) })

	if _, err := exe.AddAndStart(ctx, platformGatewayID, map[string]any{"namespace": "test-ns"}); err != nil {
		t.Fatalf("AddAndStart: %v", err)
	}

	cfg, err := ParseGatewayConfig(exe.GetGateway(platformGatewayID).RawConfig())
	if err != nil {
		t.Fatalf("ParseGatewayConfig: %v", err)
	}
	if cfg.GatewayUUID != platformGatewayID {
		t.Errorf("gateway uuid = %q, want the platform app name %s for a snapshot rendered before gateway_uuid", cfg.GatewayUUID, platformGatewayID)
	}
}

func sentinelIdentity() *gwauth.Subject {
	return (&SentinelMonitorService{cfg: SentinelMonitorConfig{SystemUser: "sentinel-monitor"}}).systemIdentity()
}

func TestASentinelInvestigationSignsSystemDefaultsRow(t *testing.T) {
	if got := sentinelIdentity().TaskSignClaims("task-1").PlatformUserID; got != rbac.DefaultSystemUserID {
		t.Errorf("signed platform user = %q, want system:default's row %s", got, rbac.DefaultSystemUserID)
	}
}

func TestSubmitTaskStampsTheRenderedRowOfACustomRunAsSystemUser(t *testing.T) {
	const qaBotID = "0192f0c4-6a4e-7b1e-9d3c-5f2a8b7e1c42"
	appConfig := map[string]any{runAsUserUUIDsKey: map[string]any{
		"system:qa-bot":     qaBotID,
		"alice@example.com": "0192f0c4-6a4e-7b1e-9d3c-5f2a8b7e1c43",
		"system:bad-row":    "not-a-row-id",
	}}
	for _, tc := range []struct {
		name     string
		identity *gwauth.Subject
		userID   string
		want     string
	}{
		{"a channel swapped to the run_as system user", gwauth.SystemUserIdentity("qa-bot", "slack"), "system:qa-bot", qaBotID},
		{"a run_as system user the platform rendered no row for", gwauth.SystemUserIdentity("other-bot", "webhook"), "system:other-bot", ""},
		{"a rendered key that names no system principal", nil, "alice@example.com", ""},
		{"a baked-in system user keeps its seeded row", gwauth.SystemUserIdentity("", "webhook"), rbac.DefaultSystemUserSub, rbac.DefaultSystemUserID},
		{"a rendered row id that is not a canonical UUID", gwauth.SystemUserIdentity("bad-row", "webhook"), "system:bad-row", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			caller := tc.identity
			if caller == nil {
				caller = gwauth.ForUserIDOnly(tc.userID)
			}
			signer := &recordingSigner{}
			if got := submitSignedAndReadOrigin(t, &recordingAdapter{}, appConfig, signer, caller, tc.userID, nil).UserUUID; got != tc.want {
				t.Errorf("user uuid = %q, want %q", got, tc.want)
			}
			if signed := signer.firstIdentity(); tc.identity != nil && (signed == nil || signed.PlatformUserID() != tc.want) {
				t.Errorf("signed identity = %+v, want the token to sign the stamped row %q", signed, tc.want)
			}
		})
	}
}
