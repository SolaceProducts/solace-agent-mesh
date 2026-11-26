// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"fmt"
	"net/http"
	"sync"
)

func MountExecutor(proxy *GatewayProxy, exe *GatewayExecutor) error {
	var staticPrefixes sync.Map

	for _, gwCore := range exe.Gateways() {
		if err := mountAdapter(proxy, gwCore, &staticPrefixes); err != nil {
			return fmt.Errorf("mount %s: %w", gwCore.GatewayID(), err)
		}
	}
	exe.OnGatewayReady = proxy.RegisterHandler
	exe.OnGatewayRemoved = func(name string) {
		if prefix, ok := staticPrefixes.LoadAndDelete(name); ok {
			proxy.UnregisterPrefixHandler(prefix.(string))
			return
		}
		proxy.UnregisterHandler(name)
	}
	return nil
}

func mountAdapter(proxy *GatewayProxy, gwCore *GatewayCore, staticPrefixes *sync.Map) error {
	adapter := gwCore.Adapter()
	spec := adapter.MountIntent()
	switch spec.Kind {
	case MountNone:
		return nil
	case MountAtPrefix:
		if spec.Prefix == "" {
			return fmt.Errorf("adapter declares MountAtPrefix with empty Prefix")
		}
		staticPrefixes.Store(gwCore.GatewayID(), spec.Prefix)
		return bindDeferred(adapter, func(h http.Handler) {
			proxy.RegisterPrefixHandler(spec.Prefix, h)
		})
	case MountNamed:
		return bindDeferred(adapter, func(h http.Handler) {
			proxy.RegisterHandler(gwCore.GatewayID(), h)
		})
	default:
		return fmt.Errorf("unknown MountKind %v", spec.Kind)
	}
}

func bindDeferred(adapter GatewayAdapter, register func(http.Handler)) error {
	d, ok := adapter.(DeferredHTTPAdapter)
	if !ok {
		return fmt.Errorf("adapter declares HTTP mount intent but does not implement DeferredHTTPAdapter")
	}
	d.SetOnHandlerReady(register)
	return nil
}
