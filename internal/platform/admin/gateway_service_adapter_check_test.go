// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"

	_ "github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/eventmesh"
	_ "github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/httpsse"
	_ "github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/slack"
	_ "github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/teams"
)

func TestGatewayService_Create_AcceptsTypeWithRegisteredAdapter(t *testing.T) {
	t.Parallel()
	svc := newGatewayServiceForAdapterCheckTest(t)

	cases := []struct {
		name   string
		typ    string
		values map[string]any
	}{
		{
			name: "slack",
			typ:  "slack",
			values: map[string]any{
				"bot_token": "xoxb-test",
				"app_token": "xapp-test",
			},
		},
		{
			name: "teams",
			typ:  "teams",
			values: map[string]any{
				"microsoft_app_id":       "12345678-1234-1234-1234-123456789abc",
				"microsoft_app_password": "secret",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := svc.Create(context.Background(), CreateGatewayRequest{
				Description: "E2E test gateway for fixture purposes",
				Name:        tc.name + "-ok",
				Type:        tc.typ,
				Values:      tc.values,
			}, "test-user")
			if err != nil {
				t.Fatalf("type=%s should be accepted when the adapter is registered: %v", tc.typ, err)
			}
		})
	}
}

func newGatewayServiceForAdapterCheckTest(t *testing.T) *GatewayService {
	t.Helper()
	db := testOpenDB(t)
	return NewGatewayService(
		store.NewGatewayRepo(db),
		store.NewGatewayDeploymentRepo(db),
		nil,
		nil,
		nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
}
