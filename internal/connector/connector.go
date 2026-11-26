// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package connector

import (
	"context"
	"fmt"
	"slices"
	"sync"

	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type Connector interface {
	Name() string

	Type() string

	Tool() tool.BuiltinTool

	Start(ctx context.Context) error

	Stop(ctx context.Context) error

	Health() error
}

type Config struct {
	Name string

	Type string

	Subtype string

	Config map[string]any
}

type Factory func(ctx context.Context, cfg Config, services runtime.Services) (Connector, error)

type Registry struct {
	mu        sync.RWMutex
	factories map[string]Factory
}

func NewRegistry() *Registry {
	return &Registry{factories: make(map[string]Factory)}
}

func (r *Registry) Register(connectorType string, factory Factory) error {
	if connectorType == "" {
		return fmt.Errorf("connector type is required")
	}
	if factory == nil {
		return fmt.Errorf("connector factory for type %q is nil", connectorType)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.factories[connectorType]; exists {
		return fmt.Errorf("connector type %q already registered", connectorType)
	}
	r.factories[connectorType] = factory
	return nil
}

func (r *Registry) MustRegister(connectorType string, factory Factory) {
	if err := r.Register(connectorType, factory); err != nil {
		panic(fmt.Sprintf("connector: %v", err))
	}
}

func (r *Registry) Deregister(connectorType string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.factories, connectorType)
}

func (r *Registry) Build(ctx context.Context, cfg Config, services runtime.Services) (Connector, error) {
	if cfg.Name == "" {
		return nil, fmt.Errorf("connector name is required")
	}
	if cfg.Type == "" {
		return nil, fmt.Errorf("connector %q: type is required", cfg.Name)
	}
	r.mu.RLock()
	factory, ok := r.factories[cfg.Type]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("connector %q: unknown type %q (no factory registered)", cfg.Name, cfg.Type)
	}
	conn, err := factory(ctx, cfg, services)
	if err != nil {
		return nil, fmt.Errorf("connector %q (%s): %w", cfg.Name, cfg.Type, err)
	}
	if conn == nil {
		return nil, fmt.Errorf("connector %q (%s): factory returned nil connector", cfg.Name, cfg.Type)
	}
	return conn, nil
}

func (r *Registry) RegisteredTypes() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.factories))
	for k := range r.factories {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

var defaultRegistry = NewRegistry()

func DefaultRegistry() *Registry {
	return defaultRegistry
}
