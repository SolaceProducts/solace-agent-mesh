// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import "net/http"

const APIV2Prefix = "/api/v2/platform"

const V1Prefix = "/api/v1/platform"

const InternalPrefix = "/api/internal/platform"

func HandleWebUIRoute(mux *http.ServeMux, method, suffix string, h http.HandlerFunc) {
	for _, prefix := range []string{V1Prefix, InternalPrefix} {
		mux.HandleFunc(method+" "+prefix+suffix, h)
	}
}
