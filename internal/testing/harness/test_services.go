// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/volume"
)

type memoryCredentialService struct {
	mu    sync.Mutex
	store map[string]string
}

func newMemoryCredentialService() *memoryCredentialService {
	return &memoryCredentialService{store: make(map[string]string)}
}

func (m *memoryCredentialService) credKey(agentName, appName, userID, key string) string {
	return agentName + "|" + appName + "|" + userID + "|" + key
}

func (m *memoryCredentialService) Get(_ context.Context, agentName, appName, userID, key string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.store[m.credKey(agentName, appName, userID, key)], nil
}

func (m *memoryCredentialService) Set(_ context.Context, agentName, appName, userID, key, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.store[m.credKey(agentName, appName, userID, key)] = value
	return nil
}

func (m *memoryCredentialService) Delete(_ context.Context, agentName, appName, userID, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.store, m.credKey(agentName, appName, userID, key))
	return nil
}

func (m *memoryCredentialService) List(_ context.Context, agentName, appName, userID string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	prefix := m.credKey(agentName, appName, userID, "")
	var keys []string
	for k := range m.store {
		if len(k) > len(prefix) && k[:len(prefix)] == prefix {
			keys = append(keys, k[len(prefix):])
		}
	}
	return keys, nil
}

var _ auth.CredentialService = (*memoryCredentialService)(nil)

type testServices struct {
	budgetGate *budget.Gate
	logger     *slog.Logger

	brk      broker.Broker
	llmMap   map[string]llm.Client
	bindings runtime.ModelBindings
	sessions session.Store
	arts     *artifact.ScopedService

	volSvc volume.Service

	metricsOverride runtime.Metrics

	authenticatorOverride auth.Authenticator
	toolAuthOverride      auth.ToolAuthorizer
	agentAuthOverride     auth.AgentAuthorizer
	auditLoggerOverride   auth.AuditLogger

	featuresOverride *features.Registry

	credSvcOnce sync.Once
	credSvc     auth.CredentialService
}

func (s *testServices) Broker() broker.Broker    { return s.brk }
func (s *testServices) Config() config.Config    { return nil }
func (s *testServices) KVStore() runtime.KVStore { return nil }
func (s *testServices) Cache() runtime.Cache     { return nil }
func (s *testServices) Metrics() runtime.Metrics {
	if s.metricsOverride != nil {
		return s.metricsOverride
	}
	return runtime.NoopMetrics{}
}
func (s *testServices) Tracer() runtime.Tracer             { return runtime.NoopTracer{} }
func (s *testServices) Artifacts() *artifact.ScopedService { return s.arts }
func (s *testServices) Sessions() session.Store            { return s.sessions }

func (s *testServices) LLM(model string) llm.Client {
	if c, ok := s.llmMap[model]; ok {
		return c
	}
	return s.llmMap[""]
}

func (s *testServices) Authenticator() auth.Authenticator {
	if s.authenticatorOverride != nil {
		return s.authenticatorOverride
	}
	return auth.NoopAuthenticator{}
}
func (s *testServices) ToolAuthorizer() auth.ToolAuthorizer {
	if s.toolAuthOverride != nil {
		return s.toolAuthOverride
	}
	return auth.NoopToolAuthorizer{}
}
func (s *testServices) AgentAuthorizer() auth.AgentAuthorizer {
	if s.agentAuthOverride != nil {
		return s.agentAuthOverride
	}
	return auth.NoopAgentAuthorizer{}
}
func (s *testServices) AuditLogger() auth.AuditLogger {
	if s.auditLoggerOverride != nil {
		return s.auditLoggerOverride
	}
	return auth.NoopAuditLogger{}
}
func (s *testServices) ControlAuthorizer() auth.ControlAuthorizer {
	return auth.NoopControlAuthorizer{}
}
func (s *testServices) CredentialService() auth.CredentialService {
	s.credSvcOnce.Do(func() {
		s.credSvc = newMemoryCredentialService()
	})
	return s.credSvc
}
func (s *testServices) Volumes() volume.Service             { return s.volSvc }
func (s *testServices) BudgetGate() *budget.Gate            { return s.budgetGate }
func (s *testServices) HTTPClient() *http.Client            { return http.DefaultClient }
func (s *testServices) FeatureRegistry() *features.Registry { return s.featuresOverride }

func withFlagEnabled(reg *features.Registry, key string) *features.Registry {
	if reg == nil {
		reg = features.NewRegistry()
	}
	reg.Register(features.Definition{Key: key, Default: true})
	return reg
}

func (s *testServices) UsageReporter() runtime.Component { return nil }
func (s *testServices) ModelResolver() runtime.ModelAliasResolver {
	return nil
}
func (s *testServices) Bindings() runtime.ModelBindings                { return s.bindings }
func (s *testServices) ConnectorSpecStore() runtime.ConnectorSpecStore { return nil }
func (s *testServices) Logger() *slog.Logger {
	if s.logger != nil {
		return s.logger
	}
	return slog.Default()
}

type testWriter struct {
	t    *testing.T
	done atomic.Bool

	mu      sync.Mutex
	capture bytes.Buffer
}

func (w *testWriter) Write(p []byte) (n int, err error) {
	w.mu.Lock()
	w.capture.Write(p)
	w.mu.Unlock()

	if w.done.Load() {
		return len(p), nil
	}
	w.t.Log(string(p))
	return len(p), nil
}

func (w *testWriter) captured() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.capture.String()
}
