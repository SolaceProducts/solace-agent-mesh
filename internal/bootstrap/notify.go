// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package bootstrap

import (
	"net/http"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/notify"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func NewNotifyCore(svc runtime.Services, appConfig map[string]any, ext notify.NotifyExtensions) *notify.NotifyCore {
	core := notify.NewNotifyCore(appConfig, ext)
	core.SetServices(svc)
	return core
}

type NotifyMountable interface {
	SetOnHandlerReady(cb func(http.Handler))
}

func MountNotify(proxy *entrypoint.GatewayProxy, core NotifyMountable) {
	core.SetOnHandlerReady(func(h http.Handler) {
		proxy.RegisterPrefixHandler("/api/v1/notify", h)
	})
}
