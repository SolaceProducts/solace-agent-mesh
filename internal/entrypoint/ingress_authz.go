// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
)

func (g *GatewayCore) SetIdentityAuthorizer(a auth.SenderAuthorizer) {
	if impl, ok := a.(*auth.BrokerSenderAuthorizer); ok && impl == nil {
		g.identityAuthorizer = nil
		return
	}
	g.identityAuthorizer = a
}

func (g *GatewayCore) IdentityAuthorizer() auth.SenderAuthorizer { return g.identityAuthorizer }
