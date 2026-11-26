// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"fmt"
	"log/slog"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

type testModelConfigEnv struct {
	svc     *ModelConfigService
	handler *modelConfigHandler
}

func setupModelConfigMux(t *testing.T) *testModelConfigEnv {
	t.Helper()
	svc := NewModelConfigService(store.NewModelConfigRepo(testOpenDB(t)), slog.Default())
	svc.SetFeatureRegistry(decisionNodesOn())
	return &testModelConfigEnv{svc: svc, handler: &modelConfigHandler{svc: svc, logger: slog.Default()}}
}

func TestIsServiceError(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"ValidationError", &ValidationError{Message: "bad"}, true},
		{"NotFoundError", &NotFoundError{Resource: "x", ID: "y"}, true},
		{"ConflictError", &ConflictError{Message: "dup"}, true},
		{"ForbiddenError", &ForbiddenError{Message: "no"}, true},
		{"ExternalServiceError", &ExternalServiceError{Message: "down"}, true},
		{"plain error", fmt.Errorf("random"), false},
		{"nil", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isServiceError(tc.err); got != tc.want {
				t.Errorf("isServiceError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
