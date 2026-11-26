// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import "net/http"

const (
	apiV1PlatformPrefix = V1Prefix + "/"

	removedV1Pattern = apiV1PlatformPrefix

	UpgradeCLIMessage = "This API version has been removed. Call /api/v2/platform instead. If a sam CLI sent this request, upgrade it to the version released with this server."
)

func registerRemovedV1Routes(mux *http.ServeMux) {
	mux.HandleFunc(removedV1Pattern, handleRemovedV1)
}

func handleRemovedV1(w http.ResponseWriter, _ *http.Request) {
	writeError(w, http.StatusGone, UpgradeCLIMessage)
}
