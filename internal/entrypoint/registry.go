// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"fmt"
	"slices"
	"sync"
)

type AdapterFactory func(adapterCfg map[string]any) (GatewayAdapter, error)

var (
	adapterRegistryMu sync.Mutex
	adapterFactories  = map[string]AdapterFactory{}
)

func RegisterAdapterFactory(name string, factory AdapterFactory) {
	adapterRegistryMu.Lock()
	defer adapterRegistryMu.Unlock()
	adapterFactories[name] = factory
}

func CreateAdapter(name string, adapterCfg map[string]any) (GatewayAdapter, error) {
	adapterRegistryMu.Lock()
	factory, ok := adapterFactories[name]
	adapterRegistryMu.Unlock()

	if !ok {
		return nil, fmt.Errorf("unknown gateway adapter: %q (registered: %v)", name, RegisteredAdapters())
	}
	return factory(adapterCfg)
}

func RegisteredAdapters() []string {
	adapterRegistryMu.Lock()
	defer adapterRegistryMu.Unlock()

	names := make([]string, 0, len(adapterFactories))
	for name := range adapterFactories {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

type ExternalQueueRemover func(ctx context.Context, appConfig map[string]any) error

var (
	externalQueueRemoversMu sync.Mutex
	externalQueueRemovers   = map[string]ExternalQueueRemover{}
)

func RegisterExternalQueueRemover(gatewayType string, remove ExternalQueueRemover) {
	externalQueueRemoversMu.Lock()
	defer externalQueueRemoversMu.Unlock()
	externalQueueRemovers[gatewayType] = remove
}

func RemoveExternalQueues(ctx context.Context, appConfig map[string]any) error {
	gatewayType, _ := appConfig["gateway_type"].(string)
	externalQueueRemoversMu.Lock()
	remove := externalQueueRemovers[gatewayType]
	externalQueueRemoversMu.Unlock()
	if remove == nil {
		return nil
	}
	return remove(ctx, appConfig)
}
