// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package webhook

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/sacexpr"
)

func buildRequestContext(r *http.Request, route *RouteConfig, body []byte, pathParams map[string]string, logger *slog.Logger) *sacexpr.Context {
	var payload any
	if len(body) > 0 {
		if err := json.Unmarshal(body, &payload); err != nil {
			payload = string(body)
		}
	}

	ctx := sacexpr.NewContext(payload, r.URL.Path, nil)

	if len(pathParams) > 0 {
		if err := ctx.SetData("user_data.path:", toAnyMap(pathParams)); err != nil {
			logger.Warn("failed to set path params in expression context", "error", err)
		}
	}

	if q := r.URL.Query(); len(q) > 0 {
		if route.Auth != nil && route.Auth.Token != nil &&
			strings.EqualFold(route.Auth.Token.Location, "query") && route.Auth.Token.Name != "" {
			q = cloneQueryWithout(q, route.Auth.Token.Name)
		}
		if len(q) > 0 {
			if err := ctx.SetData("user_data.query:", flattenQuery(q)); err != nil {
				logger.Warn("failed to set query params in expression context", "error", err)
			}
		}
	}

	if len(route.Headers) > 0 {
		headers := make(map[string]any, len(route.Headers))
		for alias, headerName := range route.Headers {
			headers[alias] = r.Header.Get(headerName)
		}
		if err := ctx.SetData("user_data.headers:", headers); err != nil {
			logger.Warn("failed to set header aliases in expression context", "error", err)
		}
	}

	return ctx
}

func cloneQueryWithout(q url.Values, key string) url.Values {
	out := make(url.Values, len(q))
	for k, vs := range q {
		if strings.EqualFold(k, key) {
			continue
		}
		out[k] = vs
	}
	return out
}

func flattenQuery(q url.Values) map[string]any {
	out := make(map[string]any, len(q))
	for k, vs := range q {
		if len(vs) > 0 {
			out[k] = vs[0]
		} else {
			out[k] = ""
		}
	}
	return out
}

func toAnyMap(m map[string]string) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
