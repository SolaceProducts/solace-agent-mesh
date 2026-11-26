// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httputil //nolint:revive // internal package, no conflict risk

import (
	"net/http"
	"slices"
)

const HeaderClientSurface = "X-Sam-Client-Surface"

const (
	SurfaceWebUI = "ui"
	SurfaceCLI   = "cli"
	SurfaceAPI   = "api"
)

const (
	SurfaceSlack      = "slack"
	SurfaceTeams      = "teams"
	SurfaceEmail      = "email"
	SurfaceWhatsApp   = "whatsapp"
	SurfaceGoogleChat = "google_chat"
)

var RelayedSurfaces = []string{SurfaceSlack, SurfaceTeams, SurfaceEmail, SurfaceWhatsApp, SurfaceGoogleChat}

var SubmitterSurfaces = append([]string{SurfaceWebUI, SurfaceCLI, SurfaceAPI}, RelayedSurfaces...)

const SurfaceSystem = "system"

var WriterSurfaces = slices.Concat(SubmitterSurfaces, []string{SurfaceSystem})

func ClientSurface(r *http.Request) string {
	return ParseClientSurface(r.Header.Get(HeaderClientSurface))
}

func ParseClientSurface(value string) string {
	switch value {
	case SurfaceWebUI:
		return SurfaceWebUI
	case SurfaceCLI:
		return SurfaceCLI
	default:
		return SurfaceAPI
	}
}
