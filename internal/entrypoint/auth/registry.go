// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package auth

import (
	"fmt"
	"slices"
	"strings"
	"sync"
)

type ProviderFactory func(cfg map[string]any) (AuthProvider, error)

var (
	registryMu sync.Mutex
	factories  = map[string]ProviderFactory{}
)

func RegisterProviderFactory(name string, factory ProviderFactory) {
	registryMu.Lock()
	defer registryMu.Unlock()
	factories[name] = factory
}

func CreateProvider(name string, cfg map[string]any) (AuthProvider, error) {
	registryMu.Lock()
	factory, ok := factories[name]
	registryMu.Unlock()

	if !ok {
		return nil, fmt.Errorf("unknown auth provider: %q (registered: %v)", name, RegisteredProviders())
	}
	return factory(cfg)
}

func UnregisterProviderFactory(name string) {
	registryMu.Lock()
	defer registryMu.Unlock()
	delete(factories, name)
}

func UnregisterProviderFactoriesForScope(scope string) {
	prefix := scope + ":"
	registryMu.Lock()
	defer registryMu.Unlock()
	for name := range factories {
		if name == scope || strings.HasPrefix(name, prefix) {
			delete(factories, name)
		}
	}
}

func RegisteredProviders() []string {
	registryMu.Lock()
	defer registryMu.Unlock()

	names := make([]string, 0, len(factories))
	for name := range factories {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}
