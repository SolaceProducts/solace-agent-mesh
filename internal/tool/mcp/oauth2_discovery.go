// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"context"
	"net/url"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth/toolauth"
)

type OAuthMetadata = toolauth.OAuthMetadata

type ClientRegistration = toolauth.ClientRegistration

var (
	discoveryCache   = &toolauth.DiscoveryCache
	discoveryCacheMu = &toolauth.DiscoveryCacheMu
)

var DiscoverOAuthMetadata = toolauth.DiscoverOAuthMetadata

func RegisterClient(ctx context.Context, registrationURL, redirectURI string, metadata *OAuthMetadata) (*ClientRegistration, error) {
	return toolauth.RegisterClient(ctx, registrationURL, redirectURI, metadata)
}

var parseResourceMetadataURL = toolauth.ParseResourceMetadataURL

func buildWellKnownURLs(authServer, resource *url.URL) []string {
	return toolauth.BuildWellKnownURLs(authServer, resource)
}
