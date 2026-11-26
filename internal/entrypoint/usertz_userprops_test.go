// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"encoding/json"
	"io"
	"log/slog"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
)

func newTZCore(defaultTZ string) *GatewayCore {
	return &GatewayCore{
		cfg:    &GatewayConfig{DefaultUserTimezone: defaultTZ},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func decodeUserConfig(t *testing.T, blob string) map[string]any {
	t.Helper()
	if blob == "" {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(blob), &m); err != nil {
		t.Fatalf("a2aUserConfig is not a JSON object: %v (%q)", err, blob)
	}
	return m
}

func TestApplyUserTimezoneFromCaller(t *testing.T) {
	core := newTZCore("UTC")
	props := &a2a.UserProperties{}
	core.applyUserTimezoneToUserProps(props, "America/Toronto")

	if got := decodeUserConfig(t, props.A2AUserConfig)["user_timezone"]; got != "America/Toronto" {
		t.Errorf("user_timezone = %v, want America/Toronto", got)
	}
}

func TestApplyUserTimezoneWithoutIdentity(t *testing.T) {
	core := newTZCore("America/Denver")
	props := &a2a.UserProperties{}

	core.applyUserTimezoneToUserProps(props, "")

	if got := decodeUserConfig(t, props.A2AUserConfig)["user_timezone"]; got != "America/Denver" {
		t.Errorf("user_timezone = %v, want the configured default America/Denver", got)
	}
}

func TestApplyUserTimezoneCallerBeatsConfigDefault(t *testing.T) {
	core := newTZCore("America/Denver")
	props := &a2a.UserProperties{}
	core.applyUserTimezoneToUserProps(props, "Europe/Berlin")

	if got := decodeUserConfig(t, props.A2AUserConfig)["user_timezone"]; got != "Europe/Berlin" {
		t.Errorf("user_timezone = %v, want the request zone Europe/Berlin", got)
	}
}

func TestApplyUserTimezoneAbsentWhenNoZoneAnywhere(t *testing.T) {
	core := newTZCore("")
	props := &a2a.UserProperties{}

	core.applyUserTimezoneToUserProps(props, "")

	if props.A2AUserConfig != "" {
		t.Errorf("a2aUserConfig = %q, want it left empty so the agent uses UTC", props.A2AUserConfig)
	}
}

func TestApplyUserTimezonePreservesExistingKeys(t *testing.T) {
	core := newTZCore("UTC")
	props := &a2a.UserProperties{
		A2AUserConfig: `{"_enterprise_capabilities":["tool:time:read"],"user_profile":{"name":"Ada"}}`,
	}
	core.applyUserTimezoneToUserProps(props, "Asia/Tokyo")

	cfg := decodeUserConfig(t, props.A2AUserConfig)
	if got := cfg["user_timezone"]; got != "Asia/Tokyo" {
		t.Errorf("user_timezone = %v, want Asia/Tokyo", got)
	}
	caps, ok := cfg["_enterprise_capabilities"].([]any)
	if !ok || len(caps) != 1 || caps[0] != "tool:time:read" {
		t.Errorf("_enterprise_capabilities = %v, want it preserved", cfg["_enterprise_capabilities"])
	}
	if _, ok := cfg["user_profile"]; !ok {
		t.Error("user_profile was dropped")
	}
}

func TestApplyUserTimezoneLeavesNonObjectBlobAlone(t *testing.T) {
	core := newTZCore("UTC")
	props := &a2a.UserProperties{A2AUserConfig: `["not","an","object"]`}
	core.applyUserTimezoneToUserProps(props, "Asia/Tokyo")

	if props.A2AUserConfig != `["not","an","object"]` {
		t.Errorf("a2aUserConfig = %q, want it left untouched", props.A2AUserConfig)
	}
}
