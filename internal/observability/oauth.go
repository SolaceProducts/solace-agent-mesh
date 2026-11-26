// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package observability

import "github.com/SolaceProducts/solace-agent-mesh/internal/runtime"

const (
	OAuthOpValidateToken = "validate_token"
	OAuthOpGetUserInfo   = "get_user_info"
	OAuthOpDiscovery     = "discovery"
	OAuthOpGetJWKS       = "get_jwks"
)

func NewOAuthRemoteMonitor(op string) *Monitor {
	return newMonitor(
		runtime.MetricProvider(),
		metricOutboundRequestDuration,
		classifyRemoteError,
		"service.peer.name", "oauth_service",
		"operation.name", op,
	)
}
