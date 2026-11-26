// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package auth

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
)

type TokenValidator interface {
	Validate(ctx context.Context, token string) (*Subject, error)
}

type TokenValidatorFunc func(ctx context.Context, token string) (*Subject, error)

func (f TokenValidatorFunc) Validate(ctx context.Context, token string) (*Subject, error) {
	return f(ctx, token)
}

type ValidatorFactory func(cfg map[string]any) (TokenValidator, error)

var (
	validatorMu        sync.RWMutex
	validatorFactories = map[string]ValidatorFactory{}
)

func RegisterValidatorFactory(name string, factory ValidatorFactory) {
	validatorMu.Lock()
	defer validatorMu.Unlock()
	validatorFactories[name] = factory
}

func CreateValidator(name string, cfg map[string]any) (TokenValidator, error) {
	validatorMu.RLock()
	factory, ok := validatorFactories[name]
	validatorMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("token validator: no factory registered for %q (registered: %v)", name, RegisteredValidators())
	}
	return factory(cfg)
}

func RegisteredValidators() []string {
	validatorMu.RLock()
	defer validatorMu.RUnlock()
	names := make([]string, 0, len(validatorFactories))
	for n := range validatorFactories {
		names = append(names, n)
	}
	slices.Sort(names)
	return names
}

func UnregisterValidatorFactory(name string) {
	validatorMu.Lock()
	defer validatorMu.Unlock()
	delete(validatorFactories, name)
}

func UnregisterValidatorFactoriesForScope(scope string) {
	prefix := scope + ":"
	validatorMu.Lock()
	defer validatorMu.Unlock()
	for name := range validatorFactories {
		if name == scope || strings.HasPrefix(name, prefix) {
			delete(validatorFactories, name)
		}
	}
}

func unregisterValidatorFactory(name string) { UnregisterValidatorFactory(name) }
