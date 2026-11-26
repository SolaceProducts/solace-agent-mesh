// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
)

func TestParseRemoveInstanceBody_Gateway(t *testing.T) {
	cases := []struct {
		name     string
		body     json.RawMessage
		wantDepr bool
		wantErr  bool
	}{
		{"empty body", nil, true, false},
		{"empty object", json.RawMessage(`{}`), true, false},
		{"deprovision null", json.RawMessage(`{"deprovision":null}`), true, false},
		{"deprovision false", json.RawMessage(`{"deprovision":false}`), false, false},
		{"deprovision true", json.RawMessage(`{"deprovision":true}`), true, false},
		{"malformed JSON", json.RawMessage(`not-json`), false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseRemoveInstanceBody(tc.body)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.wantDepr {
				t.Errorf("deprovision = %v, want %v", got, tc.wantDepr)
			}
		})
	}
}

func TestHandleManagement_DeleteBody_RoutesDeprovisionFlag(t *testing.T) {
	svc, brk := testServicesWithTracking(t)
	exe := NewGatewayExecutor(svc, fakeGatewayFactory)
	exe.AllowRuntimeManagement = true

	ctx := context.Background()

	_, err := exe.HandleManagement(ctx, "delete",
		[]string{"instances", "ghost-gw"}, json.RawMessage(`{"deprovision":false}`), nil)
	if err != nil {
		t.Fatalf("delete with deprovision:false: %v", err)
	}
	if names := brk.deprovisionedNames(); len(names) != 0 {
		t.Errorf("deprovision:false must not touch broker queues; DeprovisionQueue calls = %v", names)
	}

	_, err = exe.HandleManagement(ctx, "delete",
		[]string{"instances", "ghost-gw"}, nil, nil)
	if err != nil {
		t.Fatalf("delete with nil body: %v", err)
	}
	want := "solace-agent-mesh/q/gdk/gateway/ghost-gw"
	if names := brk.deprovisionedNames(); !slices.Contains(names, want) {
		t.Errorf("bodyless delete must keep undeploy semantics; DeprovisionQueue calls = %v, want %q", names, want)
	}
}

func TestHandleManagement_DeleteMalformedBody_ReturnsError_Gateway(t *testing.T) {
	svc, _ := testServicesWithTracking(t)
	exe := NewGatewayExecutor(svc, fakeGatewayFactory)
	exe.AllowRuntimeManagement = true

	_, err := exe.HandleManagement(context.Background(), "delete",
		[]string{"instances", "ghost-gw"}, json.RawMessage(`{"deprovision":`), nil)
	if err == nil {
		t.Fatal("expected error for malformed delete body")
	}
}
