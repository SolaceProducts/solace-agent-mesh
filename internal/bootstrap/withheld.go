// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package bootstrap

import (
	"log/slog"
	"maps"
	"slices"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/leader"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/trust"
)

type WithheldDeps struct {
	PlatformAppConfig map[string]any
	Registry          *features.Registry
	Metrics           runtime.Metrics
	Broker            broker.Broker
	CallSigner        *trust.Signer
	Gateway           *entrypoint.GatewayExecutor
	Namespace         string
	Logger            *slog.Logger
	Platform          *platform.PlatformCore
	Leader            *leader.Elector
}

type WithheldComponent struct {
	Name      string
	Component runtime.Component
}

var withheldComponents = map[string]func(WithheldDeps) (*WithheldComponent, error){}

func AddWithheldComponent(name string, build func(WithheldDeps) (*WithheldComponent, error)) {
	if _, taken := withheldComponents[name]; taken {
		panic("bootstrap: withheld component " + name + " registered twice")
	}
	withheldComponents[name] = build
}

func WithheldComponents(d WithheldDeps) ([]WithheldComponent, error) {
	var out []WithheldComponent
	for _, name := range slices.Sorted(maps.Keys(withheldComponents)) {
		c, err := withheldComponents[name](d)
		if err != nil {
			return nil, err
		}
		if c != nil {
			out = append(out, *c)
		}
	}
	return out, nil
}
