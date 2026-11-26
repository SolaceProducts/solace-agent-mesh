// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth/toolauth"
)

const legacyPlatformClientName = "MCP Discovery Client"

type clientRegistrationResponse struct {
	ClientID                string
	ClientSecret            string
	TokenEndpointAuthMethod string
}

func registerOAuthClient(ctx context.Context, registrationEndpoint, redirectURI, scopes string, metadata *toolauth.OAuthMetadata, logger *slog.Logger) (*clientRegistrationResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if logger == nil {
		logger = slog.Default()
	}

	var scopeList []string
	if s := strings.TrimSpace(scopes); s != "" {
		scopeList = strings.Fields(s)
	}

	reg, err := toolauth.RegisterClientWithConfig(
		ctx,
		registrationEndpoint,
		redirectURI,
		metadata,
		toolauth.RegistrationRequest{
			Scopes:             scopeList,
			DisableEnvFallback: true,
			ClientName:         legacyPlatformClientName,
		},
		logger,
	)
	if err != nil {
		return nil, err
	}

	return &clientRegistrationResponse{
		ClientID:                reg.ClientID,
		ClientSecret:            reg.ClientSecret,
		TokenEndpointAuthMethod: reg.TokenEndpointAuthMethod,
	}, nil
}

func selectTokenAuthMethod(supported []string) string {
	return toolauth.SelectTokenAuthMethod(supported)
}
