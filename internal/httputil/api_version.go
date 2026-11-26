// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httputil //nolint:revive // internal package, no conflict risk

import (
	"net/http"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/samversion"
)

const HeaderAPIVersion = "SAM-Version"

const apiVersionLayout = time.DateOnly

func SetAPIVersion(req *http.Request) {
	req.Header.Set(HeaderAPIVersion, samversion.APIVersion)
}

func APIVersionLogMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if declared := r.Header.Get(HeaderAPIVersion); declared != "" {
			logger := LoggerFromContext(r.Context())
			if _, err := time.Parse(apiVersionLayout, declared); err != nil {
				logger.Debug("malformed client API version header", "length", len(declared))
			} else {
				logger.Debug("client API version declared", "clientAPIVersion", declared, "serverAPIVersion", samversion.APIVersion)
			}
		}
		next.ServeHTTP(w, r)
	})
}
