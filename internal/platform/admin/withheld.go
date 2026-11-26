// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"log/slog"

	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

var withheldServices []func(core *platpkg.PlatformCore, db *store.DB, deps *RouteDeps, logger *slog.Logger)

func (d *RouteDeps) SetWithheld(key, value any) {
	if d.withheld == nil {
		d.withheld = map[any]any{}
	}
	d.withheld[key] = value
}

func (d *RouteDeps) Withheld(key any) any {
	return d.withheld[key]
}
