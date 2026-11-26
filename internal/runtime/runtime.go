// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package runtime

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/volume"
)

type Component interface {
	Init(ctx context.Context, cfg config.Config) error

	Start(ctx context.Context) error

	Stop(ctx context.Context) error

	Health() error
}

var ErrDegraded = errors.New("degraded")

var ErrRestartRequired = errors.New("restart required")

type shutdownComponent struct {
	shutdown func(context.Context) error
}

func (c shutdownComponent) Init(context.Context, config.Config) error { return nil }

func (c shutdownComponent) Start(context.Context) error { return nil }

func (c shutdownComponent) Stop(ctx context.Context) error { return c.shutdown(ctx) }

func (c shutdownComponent) Health() error { return nil }

type ModelAliasResolver interface {
	Resolve(ctx context.Context, alias string) (map[string]any, error)
}

type ModelBindings interface {
	ModelClient(alias string) (llm.Client, error)
	ResolveOrFetch(ctx context.Context, alias string) (llm.Client, error)
	ResolveRawOrFetch(ctx context.Context, alias string) (map[string]any, error)
	GrantTokens() llm.GrantTokens
}

type ConnectorSpecStore interface {
	Get(ctx context.Context, key string) (io.ReadCloser, error)
}

type Services interface {
	Broker() broker.Broker

	Config() config.Config

	KVStore() KVStore

	Cache() Cache

	Metrics() Metrics

	Tracer() Tracer

	LLM(model string) llm.Client

	Artifacts() *artifact.ScopedService

	ConnectorSpecStore() ConnectorSpecStore

	Sessions() session.Store

	Authenticator() auth.Authenticator

	ToolAuthorizer() auth.ToolAuthorizer

	AgentAuthorizer() auth.AgentAuthorizer

	AuditLogger() auth.AuditLogger

	CredentialService() auth.CredentialService

	ControlAuthorizer() auth.ControlAuthorizer

	FeatureRegistry() *features.Registry

	UsageReporter() Component

	ModelResolver() ModelAliasResolver

	Bindings() ModelBindings

	Volumes() volume.Service

	BudgetGate() *budget.Gate

	HTTPClient() *http.Client

	Logger() *slog.Logger
}

type KVStore interface {
	Get(key string) (any, bool)
	Set(key string, value any)
	Delete(key string)
}

type Cache interface {
	Get(key string) (any, bool)
	Set(key string, value any, opts ...CacheOption)
	Delete(key string)
	GetAndDelete(key string) (any, bool)
	SetIfAbsent(key string, value any, opts ...CacheOption) bool
	DeleteIf(key string, pred func(value any) bool) bool
	Swap(key string, value any, opts ...CacheOption) (any, bool)
}

type CacheOption func(*cacheEntryConfig)

type cacheEntryConfig struct {
	ttl      time.Duration
	onExpiry func(key string, value any)
}
