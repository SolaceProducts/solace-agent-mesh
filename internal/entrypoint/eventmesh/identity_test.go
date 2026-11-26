// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eventmesh

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/sacexpr"
)

func newTestLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})), &buf
}

func TestResolveUserID_HandlerDefault(t *testing.T) {
	exprCtx := sacexpr.NewContext(nil, "", nil)
	h := &EventHandlerConfig{DefaultUserIdentity: "alice"}
	logger, _ := newTestLogger()

	userID, forced := resolveUserID(exprCtx, h, "gw-default", "", logger)

	if userID != "alice" {
		t.Errorf("userID = %q, want alice", userID)
	}
	if forced {
		t.Error("forced = true, want false")
	}
}

func TestResolveUserID_GatewayDefaultWhenHandlerEmpty(t *testing.T) {
	exprCtx := sacexpr.NewContext(nil, "", nil)
	h := &EventHandlerConfig{}
	logger, _ := newTestLogger()

	userID, _ := resolveUserID(exprCtx, h, "gw-default", "", logger)

	if userID != "gw-default" {
		t.Errorf("userID = %q, want gw-default", userID)
	}
}

func TestResolveUserID_ExpressionWinsOverDefault(t *testing.T) {
	exprCtx := sacexpr.NewContext(map[string]any{"u": "bob"}, "", nil)
	h := &EventHandlerConfig{
		DefaultUserIdentity:    "alice",
		UserIdentityExpression: "input.payload:u",
	}
	logger, _ := newTestLogger()

	userID, _ := resolveUserID(exprCtx, h, "gw-default", "", logger)

	if userID != "bob" {
		t.Errorf("userID = %q, want bob", userID)
	}
}

func TestResolveUserID_ExpressionEmptyFallsBackToDefault(t *testing.T) {
	exprCtx := sacexpr.NewContext(map[string]any{}, "", nil)
	h := &EventHandlerConfig{
		DefaultUserIdentity:    "alice",
		UserIdentityExpression: "input.payload:missing",
	}
	logger, _ := newTestLogger()

	userID, _ := resolveUserID(exprCtx, h, "gw-default", "", logger)

	if userID != "alice" {
		t.Errorf("userID = %q, want alice", userID)
	}
}

func TestResolveUserID_NothingResolves(t *testing.T) {
	exprCtx := sacexpr.NewContext(map[string]any{}, "", nil)
	h := &EventHandlerConfig{}
	logger, _ := newTestLogger()

	userID, forced := resolveUserID(exprCtx, h, "", "", logger)

	if userID != "" {
		t.Errorf("userID = %q, want empty", userID)
	}
	if forced {
		t.Error("forced = true, want false")
	}
}

func TestResolveUserID_ForceOverridesAll(t *testing.T) {
	exprCtx := sacexpr.NewContext(map[string]any{"u": "bob"}, "", nil)
	h := &EventHandlerConfig{
		DefaultUserIdentity:    "alice",
		UserIdentityExpression: "input.payload:u",
	}
	logger, buf := newTestLogger()

	userID, forced := resolveUserID(exprCtx, h, "gw-default", "dev-user", logger)

	if userID != "dev-user" {
		t.Errorf("userID = %q, want dev-user", userID)
	}
	if !forced {
		t.Error("forced = false, want true")
	}
	if !strings.Contains(buf.String(), "DEVELOPMENT MODE") {
		t.Errorf("expected DEVELOPMENT MODE warn log, got: %s", buf.String())
	}
	if !strings.Contains(buf.String(), "original=bob") || !strings.Contains(buf.String(), "forced=dev-user") {
		t.Errorf("expected original/forced values in warn log, got: %s", buf.String())
	}
}
