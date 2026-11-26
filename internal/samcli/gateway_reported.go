// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samcli

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/target"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samcli/clianalytics"
)

var addressedGateway atomic.Pointer[clianalytics.Gateway]

func recordAddressedGateway(baseURL, fallbackURL, token string, allowPlainHTTP, skipTLSVerify bool) {
	if baseURL == "" || baseURL == fallbackURL {
		return
	}
	addressedGateway.Store(&clianalytics.Gateway{BaseURL: baseURL, Token: token,
		AllowPlainHTTP: allowPlainHTTP, SkipTLSVerify: skipTLSVerify})
}

func recordAddressedPlatform(ctx context.Context, tg target.Target) {
	if tg.URL == "" {
		return
	}
	resolver, err := defaultOAuthResolver()
	if err != nil {
		return
	}
	resolved, err := tg.Resolve(ctx, resolver)
	if err != nil {
		if !errors.Is(err, target.ErrNotLoggedIn) {
			return
		}
		resolved = target.Resolved{URL: strings.TrimRight(tg.URL, "/")}
	}
	recordAddressedGateway(resolved.URL, "", resolved.Token, false, false)
}

func recordAddressedPlatformURL(ctx context.Context, url string) {
	recordAddressedPlatform(ctx, target.Target{
		URL:  url,
		Auth: target.AuthSpec{Type: target.AuthOAuth},
	})
}

func recordedGateway() clianalytics.Gateway {
	if gw := addressedGateway.Load(); gw != nil {
		return *gw
	}
	return clianalytics.Gateway{}
}
