// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package control

import (
	"context"
	"encoding/json"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
)

type Manageable interface {
	ManagementEndpoints() []Endpoint

	HandleManagement(ctx context.Context, method string, path []string, body json.RawMessage, identity *auth.Identity) (any, error)
}

type Endpoint struct {
	Method      string `json:"method"`
	Path        string `json:"path"`
	Description string `json:"description"`
}
