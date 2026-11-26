// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tokenbroker

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

var ErrInvalidGrant = errors.New("invalid OAuth grant")

func validateNewGrant(g *store.ModelOAuthGrant, loopbackHTTP bool) error {
	var problems []string
	if strings.TrimSpace(g.AccountSubject) == "" {
		problems = append(problems, "it names no account")
	}
	if !secureEndpoint(g.TokenEndpoint, loopbackHTTP) {
		problems = append(problems, "its token endpoint is not an https URL")
	}
	if !secureEndpoint(g.APIBase, loopbackHTTP) {
		problems = append(problems, "its API base is not an https URL")
	}
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrInvalidGrant, strings.Join(problems, "; "))
}

func secureEndpoint(raw string, loopbackHTTP bool) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil {
		return false
	}
	switch u.Scheme {
	case "https":
		return true
	case "http":
		return loopbackHTTP && isLoopback(u.Hostname())
	default:
		return false
	}
}

func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
