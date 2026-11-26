// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"maps"
	"slices"
)

var apiKeyRoutes = map[string]bool{
	removedV1Pattern: true,
}

func APIKeyRoutePatterns() []string {
	return slices.Sorted(maps.Keys(apiKeyRoutes))
}

func (p *PlatformCore) AllowAPIKeys(patterns ...string) {
	p.apiKeyPatterns = append(p.apiKeyPatterns, patterns...)
}

func (p *PlatformCore) allowedAPIKeyRoutes() map[string]bool {
	allowed := maps.Clone(apiKeyRoutes)
	for _, pattern := range p.apiKeyPatterns {
		allowed[pattern] = true
	}
	return allowed
}
