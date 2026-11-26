// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth/toolauth"
)

type OAuthToken = toolauth.OAuthToken

var GenerateVerifier = toolauth.GenerateVerifier

var GenerateState = toolauth.GenerateState

var BuildAuthorizationURL = toolauth.BuildAuthorizationURL

var ExchangeCode = toolauth.ExchangeCode

var RefreshToken = toolauth.RefreshToken

var ResolveRedirectURI = toolauth.ResolveRedirectURI
