// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package doctor

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type OIDCCheck struct {
	HTTPClient *http.Client
}

func NewOIDCCheck() *OIDCCheck { return &OIDCCheck{HTTPClient: &http.Client{Timeout: 5 * time.Second}} }

func (OIDCCheck) Name() string { return "oidc_provider" }

func (OIDCCheck) DisplayName() string { return "OIDC Provider" }

func (c *OIDCCheck) Run(ctx context.Context, env Env, _ Context) CheckResult {
	enabled := strings.EqualFold(env.AuthenticationEnabled, "true")
	server := strings.TrimRight(env.AuthenticationServer, "/")
	if !enabled || server == "" {
		return CheckResult{
			Name:   c.DisplayName(),
			Status: StatusSkip,
			Reason: "Authentication not enabled (AUTHENTICATION_ENABLED != true or AUTHENTICATION_SERVER not set)",
		}
	}

	url := server + "/.well-known/openid-configuration"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return CheckResult{Name: c.DisplayName(), Status: StatusFail, Detail: server, Reason: fmt.Sprintf("OIDC issuer unreachable: %v", err)}
	}
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		if isTimeout(err) {
			return CheckResult{Name: c.DisplayName(), Status: StatusFail, Detail: server, Reason: fmt.Sprintf("OIDC issuer timed out after 5s: %s", url)}
		}
		return CheckResult{Name: c.DisplayName(), Status: StatusFail, Detail: server, Reason: fmt.Sprintf("OIDC issuer unreachable: %v", err)}
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return CheckResult{Name: c.DisplayName(), Status: StatusFail, Detail: server, Reason: fmt.Sprintf("OIDC discovery endpoint returned HTTP %d", resp.StatusCode)}
	}
	return CheckResult{Name: c.DisplayName(), Status: StatusPass, Detail: server}
}
