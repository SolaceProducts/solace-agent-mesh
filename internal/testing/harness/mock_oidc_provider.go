// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import "github.com/SolaceProducts/solace-agent-mesh/internal/devauth/mockoidc"

type (
	MockOIDCProvider       = mockoidc.MockOIDCProvider
	MockOIDCProviderConfig = mockoidc.MockOIDCProviderConfig
	FakeOIDCUser           = mockoidc.FakeOIDCUser
)

var NewMockOIDCProvider = mockoidc.NewMockOIDCProvider
