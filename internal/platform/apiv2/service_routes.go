// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/SolaceProducts/solace-agent-mesh/internal/samversion"
)

const publicReasonHealthProbe = "Kubernetes probes and the chart's install notes call it without a credential, and it returns two fixed strings."

const publicReasonVersionProbe = "the sam CLI and scheduled jobs read it before signing in to check compatibility, and it returns only build versions."

type Health struct {
	Status  string `json:"status" example:"healthy"`
	Service string `json:"service" example:"Platform Service"`
}

type Version struct {
	Products         []VersionProduct `json:"products"`
	MinClientVersion *string          `json:"minClientVersion" example:"v2.561.0"`
	APIVersion       *string          `json:"apiVersion" example:"2026-11-27"`
}

type VersionProduct struct {
	Key     string `json:"key" example:"sam-gateway-go"`
	Name    string `json:"name" example:"Solace Agent Mesh"`
	Version string `json:"version" example:"v2.455.1"`
}

func serviceRoutes(_ *slog.Logger) []Route {
	return []Route{
		{Method: http.MethodGet, Path: "/health", Gate: GatePublic, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "getHealth", Summary: "Get the platform's health",
				Text: "Returns 200 while the platform is up, for use as a health probe.", Tag: healthTag},
			Handler: Handle(getHealth, "Unable to get health")},
		{Method: http.MethodGet, Path: "/version", Gate: GatePublic, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "getVersion", Summary: "Get the platform's version",
				Text: "Returns the version of each product in the build and the oldest sam CLI version the platform supports. The sam CLI checks that version before it plans or applies a configuration.", Tag: versionTag},
			Handler: Handle(getVersion, "Unable to get version")},
	}
}

func getHealth(context.Context, struct{}) (Health, error) {
	return Health{Status: "healthy", Service: "Platform Service"}, nil
}

func getVersion(context.Context, struct{}) (Version, error) {
	return Version{
		Products: []VersionProduct{
			{Key: "sam-gateway-go", Name: "Solace Agent Mesh", Version: samversion.Version},
			{Key: "sam-str-go", Name: "Solace Agent Mesh Secure Tool Runtime", Version: samversion.STRVersion},
		},
		MinClientVersion: NullIfZero(samversion.MinClientVersion),
		APIVersion:       NullIfZero(samversion.APIVersion),
	}, nil
}
