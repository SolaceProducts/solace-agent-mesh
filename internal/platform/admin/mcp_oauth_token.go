// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"fmt"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth/toolauth"
)

func exchangeOAuthCode(ctx context.Context, tokenURL, code, codeVerifier, redirectURI, clientID, clientSecret, authMethod string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	scheme := &toolauth.OAuthScheme{
		TokenURL:                tokenURL,
		TokenEndpointAuthMethod: authMethod,
	}
	cred := &toolauth.AuthCredential{ClientID: clientID, ClientSecret: clientSecret}

	tok, err := toolauth.ExchangeCode(ctx, scheme, cred, code, codeVerifier, redirectURI)
	if err != nil {
		return "", err
	}
	if tok == nil || tok.AccessToken == "" {
		return "", fmt.Errorf("token response missing access_token")
	}
	return tok.AccessToken, nil
}
