// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"fmt"
	"net/url"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
)

func authorizePrompt(authData *a2a.AuthenticationRequiredData) (string, bool) {
	u, err := url.Parse(authData.AuthURI)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return "", false
	}
	service := authData.ToolDisplayName
	if service == "" {
		service = u.Host
	}
	return fmt.Sprintf("🔐 The agent needs your permission to access %s on your behalf.", service), true
}
