// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"

var withheldRouteGroups []withheldRouteGroup

type withheldRouteGroup struct {
	name     string
	build    func(deps *entplatform.RouteDeps) []Route
	describe func(deps *entplatform.RouteDeps)
}
