// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package proxy

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/filesystem"
	artmem "github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/health"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/observability"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/volume"
)

func newTestMonitor() *observability.Monitor {
	return observability.NewRemoteAgentProxyMonitor("test-proxy")
}

type mockServices struct {
	brk     broker.Broker
	arts    *artifact.ScopedService
	metrics runtime.Metrics
	creds   auth.CredentialService
}

func (s *mockServices) Broker() broker.Broker    { return s.brk }
func (s *mockServices) Config() config.Config    { return nil }
func (s *mockServices) KVStore() runtime.KVStore { return nil }
func (s *mockServices) Cache() runtime.Cache     { return nil }
func (s *mockServices) Metrics() runtime.Metrics {
	if s.metrics != nil {
		return s.metrics
	}
	return runtime.NoopMetrics{}
}
func (s *mockServices) Tracer() runtime.Tracer             { return runtime.NoopTracer{} }
func (s *mockServices) LLM(_ string) llm.Client            { return nil }
func (s *mockServices) Artifacts() *artifact.ScopedService { return s.arts }
func (s *mockServices) Sessions() session.Store            { return nil }
func (s *mockServices) Authenticator() auth.Authenticator  { return auth.NoopAuthenticator{} }
func (s *mockServices) ToolAuthorizer() auth.ToolAuthorizer {
	return auth.NoopToolAuthorizer{}
}
func (s *mockServices) AgentAuthorizer() auth.AgentAuthorizer {
	return auth.NoopAgentAuthorizer{}
}
func (s *mockServices) AuditLogger() auth.AuditLogger { return auth.NoopAuditLogger{} }
func (s *mockServices) ControlAuthorizer() auth.ControlAuthorizer {
	return auth.NoopControlAuthorizer{}
}
func (s *mockServices) CredentialService() auth.CredentialService {
	if s.creds != nil {
		return s.creds
	}
	return auth.NoopCredentialService{}
}
func (s *mockServices) Volumes() volume.Service             { return nil }
func (s *mockServices) BudgetGate() *budget.Gate            { return nil }
func (s *mockServices) HTTPClient() *http.Client            { return http.DefaultClient }
func (s *mockServices) FeatureRegistry() *features.Registry { return nil }

func (s *mockServices) UsageReporter() runtime.Component { return nil }
func (s *mockServices) ModelResolver() runtime.ModelAliasResolver {
	return nil
}
func (s *mockServices) Bindings() runtime.ModelBindings                { return nil }
func (s *mockServices) ConnectorSpecStore() runtime.ConnectorSpecStore { return nil }
func (s *mockServices) Logger() *slog.Logger                           { return slog.Default() }

type mockAdapter struct {
	card    *a2a.AgentCard
	cardErr error
	closed  bool
}

func (m *mockAdapter) FetchAgentCard(_ context.Context) (*a2a.AgentCard, error) {
	return m.card, m.cardErr
}

func (m *mockAdapter) ForwardRequest(_ context.Context, _ *TaskContext, _ *a2a.Request) (<-chan ResponseEvent, error) {
	ch := make(chan ResponseEvent)
	close(ch)
	return ch, nil
}

func (m *mockAdapter) CancelTask(_ context.Context, _ *TaskContext) error {
	return nil
}

func (m *mockAdapter) Close() error {
	m.closed = true
	return nil
}

func newTestProxy(pc *ProxyConfig) *ProxyInstance {
	p := NewInstanceFromConfig(pc)
	p.logger = slog.Default()
	return p
}

func newTestProxyWithArtifacts() (*ProxyInstance, *artifact.ScopedService) {
	pc := &ProxyConfig{
		Name:      "test-proxy",
		Namespace: "test-ns",
	}
	p := newTestProxy(pc)

	memStore := artmem.New()
	artSvc := &artifact.ScopedService{
		Inner:     memStore,
		Scope:     "agent",
		Namespace: "test-ns",
	}

	svc := &mockServices{
		brk:  broker.NewDevBroker(),
		arts: artSvc,
	}
	p.svc = svc
	return p, artSvc
}

func TestNewInstanceFromConfig(t *testing.T) {
	pc := &ProxyConfig{
		Name:      "my-proxy",
		Namespace: "ns",
		URL:       "http://example.com",
	}
	inst := NewInstanceFromConfig(pc)

	if inst.Name() != "my-proxy" {
		t.Errorf("Name() = %q, want %q", inst.Name(), "my-proxy")
	}
	if inst.Kind() != "proxy" {
		t.Errorf("Kind() = %q, want %q", inst.Kind(), "proxy")
	}
	if inst.tasks == nil {
		t.Error("tasks store should be initialized")
	}
}

func TestNewInstance_MissingAppConfig(t *testing.T) {
	cfg := config.FromMap(map[string]any{
		"name": "test",
	})
	_, err := NewInstance(cfg)
	if err == nil {
		t.Fatal("expected error for missing app_config")
	}
}

func TestNewInstance_Success(t *testing.T) {
	cfg := config.FromMap(map[string]any{
		"name": "test",
		"app_config": map[string]any{
			"url": "http://example.com",
		},
	})
	inst, err := NewInstance(cfg)
	if err != nil {
		t.Fatalf("NewInstance: %v", err)
	}
	if inst.Name() != "test" {
		t.Errorf("Name() = %q, want %q", inst.Name(), "test")
	}
}

func TestInit_NoAdapterRegistered(t *testing.T) {
	pc := &ProxyConfig{Name: "test", Namespace: "ns", URL: "http://x.com"}
	inst := NewInstanceFromConfig(pc)
	saved := newDefaultAdapter
	newDefaultAdapter = nil
	defer func() { newDefaultAdapter = saved }()

	err := inst.Init(context.Background(), nil, &mockServices{brk: broker.NewDevBroker()})
	if err == nil {
		t.Fatal("expected error when no adapter registered")
	}
}

func TestInit_WithInjectedAdapter(t *testing.T) {
	pc := &ProxyConfig{Name: "test", Namespace: "ns", URL: "http://x.com"}
	inst := NewInstanceFromConfig(pc)
	inst.SetAdapter(&mockAdapter{})

	if err := inst.Init(context.Background(), nil, &mockServices{brk: broker.NewDevBroker()}); err != nil {
		t.Fatalf("Init: %v", err)
	}
}

type trackingBroker struct {
	*broker.DevBroker
	deprovisioned []string
	mu            sync.Mutex
}

func newTrackingBroker() *trackingBroker {
	return &trackingBroker{DevBroker: broker.NewDevBroker()}
}

func (t *trackingBroker) DeprovisionQueue(name string) error {
	t.mu.Lock()
	t.deprovisioned = append(t.deprovisioned, name)
	t.mu.Unlock()
	return t.DevBroker.DeprovisionQueue(name)
}

func TestProxy_RemoveDeprovisionsRequestQueue(t *testing.T) {
	tb := newTrackingBroker()
	pc := &ProxyConfig{Name: "remove-proxy", Namespace: "test-ns"}
	p := newTestProxy(pc)
	p.svc = &mockServices{brk: tb}
	p.queue = &broker.SimpleQueue{QName: "remove-proxy"}
	p.adapter = &mockAdapter{}

	if err := p.Remove(context.Background()); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	tb.mu.Lock()
	defer tb.mu.Unlock()
	if want := []string{"remove-proxy"}; !slices.Equal(tb.deprovisioned, want) {
		t.Errorf("Remove deprovisioned %q, want %q", tb.deprovisioned, want)
	}
}

func TestStop_ClosesAdapter(t *testing.T) {
	pc := &ProxyConfig{Name: "test", Namespace: "ns"}
	inst := newTestProxy(pc)
	ma := &mockAdapter{}
	inst.adapter = ma

	if err := inst.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if !ma.closed {
		t.Error("adapter.Close() was not called")
	}
}

func TestStop_NilAdapter(t *testing.T) {
	pc := &ProxyConfig{Name: "test", Namespace: "ns"}
	inst := newTestProxy(pc)
	inst.adapter = nil

	if err := inst.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

func receiveInBackground(t *testing.T, brk broker.Broker, q broker.Queue) <-chan *broker.Message {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	received := make(chan *broker.Message, 1)
	go func() {
		msg, _ := brk.Receive(ctx, q)
		if msg != nil {
			received <- msg
		}
	}()
	return received
}

func TestStop_PublishesRemovalCard(t *testing.T) {
	pc := &ProxyConfig{Name: "removal-probe", Namespace: "test-ns"}
	inst := newTestProxy(pc)
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatalf("connect dev broker: %v", err)
	}
	inst.svc = &mockServices{brk: brk}
	received := receiveInBackground(t, brk, subscribeTopic(t, brk, "removal-probe", a2a.DiscoveryAgentCardsTopic(pc.Namespace)))

	if stopErr := inst.Stop(context.Background()); stopErr != nil {
		t.Fatalf("Stop: %v", stopErr)
	}

	select {
	case msg := <-received:
		if msg.UserProperties["removed"] != "true" {
			t.Errorf("UserProperties[removed] = %q, want true", msg.UserProperties["removed"])
		}
		if got := msg.UserProperties["instance_id"]; got == "" || got != inst.instanceID {
			t.Errorf("UserProperties[instance_id] = %q, want this proxy's %q", got, inst.instanceID)
		}
		var card a2a.AgentCard
		if err := json.Unmarshal(msg.Payload, &card); err != nil {
			t.Fatalf("unmarshal card: %v", err)
		}
		if card.Name != pc.Name {
			t.Errorf("card.Name = %q, want %q", card.Name, pc.Name)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("removal card not published within 2s")
	}
}

func TestFetchAndPublishCard_RecoveryPublishesHealthyImmediately(t *testing.T) {
	pc := &ProxyConfig{Name: "recovery-probe", Namespace: "test-ns"}
	inst := newTestProxy(pc)
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatalf("connect dev broker: %v", err)
	}
	inst.svc = &mockServices{brk: brk}
	inst.adapter = &mockAdapter{card: &a2a.AgentCard{Name: "downstream"}}
	inst.health.Set(healthKeyRemoteFetch, health.Degraded, health.DetailRemoteFetchFailed)
	received := receiveInBackground(t, brk, subscribeTopic(t, brk, "recovery-probe", a2a.DiscoveryAgentCardsTopic(pc.Namespace)))

	if err := inst.fetchAndPublishCard(context.Background()); err != nil {
		t.Fatalf("fetchAndPublishCard: %v", err)
	}

	select {
	case msg := <-received:
		if got := msg.UserProperties["instance_id"]; got == "" || got != inst.instanceID {
			t.Errorf("UserProperties[instance_id] = %q, want this proxy's %q", got, inst.instanceID)
		}
		var card a2a.AgentCard
		if err := json.Unmarshal(msg.Payload, &card); err != nil {
			t.Fatalf("unmarshal card: %v", err)
		}
		for _, ext := range card.Capabilities.Extensions {
			if ext.URI == a2a.ExtensionURIHealth {
				t.Errorf("published card still carries a health extension after a successful fetch: %+v", ext)
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("card not published within 2s")
	}

	if problems := inst.health.Snapshot(); len(problems) != 0 {
		t.Errorf("health.Snapshot() = %v, want empty after a successful fetch", problems)
	}
}

type switchableAdapter struct {
	mu      sync.Mutex
	card    *a2a.AgentCard
	cardErr error

	fetches   atomic.Int32
	forwarded atomic.Int32
}

func (a *switchableAdapter) set(card *a2a.AgentCard, err error) {
	a.mu.Lock()
	a.card, a.cardErr = card, err
	a.mu.Unlock()
}

func (a *switchableAdapter) FetchAgentCard(_ context.Context) (*a2a.AgentCard, error) {
	a.fetches.Add(1)
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cardErr != nil {
		return nil, a.cardErr
	}
	card := *a.card
	card.Capabilities.Extensions = slices.Clone(a.card.Capabilities.Extensions)
	return &card, nil
}

func (a *switchableAdapter) ForwardRequest(_ context.Context, _ *TaskContext, _ *a2a.Request) (<-chan ResponseEvent, error) {
	a.forwarded.Add(1)
	ch := make(chan ResponseEvent)
	close(ch)
	return ch, nil
}

func (a *switchableAdapter) CancelTask(_ context.Context, _ *TaskContext) error { return nil }
func (a *switchableAdapter) Close() error                                       { return nil }

func verifiedRemoteCard() *a2a.AgentCard {
	return &a2a.AgentCard{Name: "downstream", Description: "remote", Capabilities: a2a.AgentCapabilities{
		Extensions: []a2a.AgentExtension{a2a.RemoteProtocolExtension(a2a.RemoteProtocol{Version: "1.0", Binding: "JSONRPC", Pin: "auto", CardSignature: a2a.CardSignatureVerified})},
	}}
}

func remoteFetchDetail(p *ProxyInstance) string {
	for _, problem := range p.health.Snapshot() {
		if problem.ID == healthKeyRemoteFetch {
			return problem.Detail
		}
	}
	return ""
}

func publishedCardSignature(t *testing.T, p *ProxyInstance) any {
	t.Helper()
	card := p.CachedCard()
	if card == nil {
		t.Fatal("no card is published")
	}
	rp, ok := a2a.RemoteProtocolFromCard(*card)
	if !ok {
		t.Fatal("the published card has no sam/remote-protocol extension")
	}
	return rp.CardSignature
}

func TestStart_NamesTheCardFetchFailure(t *testing.T) {
	tests := []struct {
		name         string
		err          error
		wantDetail   string
		wantForwards bool
	}{
		{"untrusted card", fmt.Errorf("check card: %w", ErrAgentCardUntrusted), string(health.DetailRemoteCardUntrusted), false},
		{"trusted keys unavailable", fmt.Errorf("check card: %w", ErrAgentCardKeysUnavailable), string(health.DetailRemoteCardKeysUnavailable), true},
		{"unreachable agent", errors.New("fetch agent card: network connection failed"), string(health.DetailRemoteFetchFailed), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newBrokerProxy(t)
			p.adapter = &errorAdapter{err: tt.err}
			p.cfg.DiscoveryIntervalSeconds = 0
			p.cfg.TaskCleanupIntervalMinutes = 0
			if err := p.Start(t.Context()); err != nil {
				t.Fatalf("Start: %v", err)
			}
			<-p.started
			defer p.Stop(context.Background()) //nolint:errcheck // test cleanup

			if got := remoteFetchDetail(p); got != tt.wantDetail {
				t.Errorf("health detail = %q, want %q", got, tt.wantDetail)
			}
			if got := p.forwardsTasks(); got != tt.wantForwards {
				t.Errorf("forwardsTasks = %v, want %v", got, tt.wantForwards)
			}
		})
	}
}

func TestDiscoveryTimer_RefusedCardStopsTasksAndWithdrawsItsSignature(t *testing.T) {
	p := newBrokerProxy(t)
	adapter := &switchableAdapter{}
	adapter.set(verifiedRemoteCard(), nil)
	p.adapter = adapter
	p.cfg.DiscoveryIntervalSeconds = 1
	p.cfg.TaskCleanupIntervalMinutes = 0
	if err := p.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	<-p.started
	defer p.Stop(context.Background()) //nolint:errcheck // test cleanup

	adapter.set(nil, fmt.Errorf("check card: %w", ErrAgentCardUntrusted))
	waitFor(t, func() bool { return publishedCardSignature(t, p) == a2a.CardSignatureRefused && !p.forwardsTasks() })
	if got := remoteFetchDetail(p); got != string(health.DetailRemoteCardUntrusted) {
		t.Errorf("health detail = %q, want the untrusted-card detail", got)
	}
	p.forwardAndProcess(t.Context(), &TaskContext{TaskID: "refused"}, &a2a.Request{ID: "refused", Method: "message/send"})
	if got := adapter.forwarded.Load(); got != 0 {
		t.Errorf("forwarded %d requests while the card is refused, want 0", got)
	}

	adapter.set(verifiedRemoteCard(), nil)
	waitFor(t, func() bool { return publishedCardSignature(t, p) == a2a.CardSignatureVerified && p.forwardsTasks() })
	if got := remoteFetchDetail(p); got != "" {
		t.Errorf("health detail after the card passes again = %q, want none", got)
	}
	p.forwardAndProcess(t.Context(), &TaskContext{TaskID: "accepted"}, &a2a.Request{ID: "accepted", Method: "message/send"})
	if got := adapter.forwarded.Load(); got != 1 {
		t.Errorf("forwarded %d requests once the card passes again, want 1", got)
	}
}

func TestStart_RetriesARefusedCardWithTheTimerOff(t *testing.T) {
	p := newBrokerProxy(t)
	adapter := &switchableAdapter{}
	adapter.set(nil, fmt.Errorf("check card: %w", ErrAgentCardUntrusted))
	p.adapter = adapter
	p.cfg.DiscoveryIntervalSeconds = 0
	p.cfg.TaskCleanupIntervalMinutes = 0
	p.cardRetryInterval = 20 * time.Millisecond
	if err := p.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	<-p.started
	defer p.Stop(context.Background()) //nolint:errcheck // test cleanup
	if p.forwardsTasks() {
		t.Fatal("forwardsTasks = true with the card refused at start")
	}

	adapter.set(verifiedRemoteCard(), nil)
	waitFor(t, func() bool { return p.forwardsTasks() && p.CachedCard() != nil && p.Health() == nil })
}

func TestForwardsTasks_RequireSignedCardWaitsForACard(t *testing.T) {
	p := newBrokerProxy(t)
	p.cfg.RequireSignedCard = true
	if p.forwardsTasks() {
		t.Error("forwardsTasks = true before any card was adopted, want false")
	}
	p.cachedCard.Store(verifiedRemoteCard())
	if !p.forwardsTasks() {
		t.Error("forwardsTasks = false with an adopted card, want true")
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met within 10s")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestHealth(t *testing.T) {
	tests := []struct {
		name        string
		cardHealthy bool
		wantErr     error
	}{
		{"a healthy card", true, nil},
		{"no healthy card is degraded", false, runtime.ErrDegraded},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inst := newTestProxy(&ProxyConfig{Name: "test", Namespace: "ns"})
			inst.cardHealthy.Store(tt.cardHealthy)

			if err := inst.Health(); !errors.Is(err, tt.wantErr) {
				t.Errorf("Health() = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestCachedCard(t *testing.T) {
	pc := &ProxyConfig{Name: "test", Namespace: "ns"}
	inst := newTestProxy(pc)

	if inst.CachedCard() != nil {
		t.Error("expected nil before any card is stored")
	}

	card := &a2a.AgentCard{Name: "downstream"}
	inst.cachedCard.Store(card)

	got := inst.CachedCard()
	if got == nil || got.Name != "downstream" {
		t.Errorf("CachedCard() = %v, want card with name 'downstream'", got)
	}
}

func TestExtractSessionID(t *testing.T) {
	tests := []struct {
		name   string
		params any
		want   string
	}{
		{"valid contextId", map[string]any{"message": map[string]any{"contextId": "sess-123"}}, "sess-123"},
		{"missing contextId", map[string]any{"message": map[string]any{}}, ""},
		{"missing message", map[string]any{}, ""},
		{"nil params", nil, ""},
		{"params not a map", "string-params", ""},
		{"message not a map", map[string]any{"message": "not-a-map"}, ""},
		{"contextId not a string", map[string]any{"message": map[string]any{"contextId": 42}}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := newTestProxy(&ProxyConfig{Name: "test", Namespace: "ns"})
			if got := p.extractSessionID(&a2a.Request{Params: tc.params}); got != tc.want {
				t.Errorf("extractSessionID() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestIsTextOnlyArtifact(t *testing.T) {
	tests := []struct {
		name  string
		parts []a2a.Part
		want  bool
	}{
		{"empty parts", []a2a.Part{}, false},
		{"single text part", []a2a.Part{{Kind: "text", Text: "hello"}}, true},
		{"multiple text parts", []a2a.Part{{Kind: "text", Text: "hello"}, {Kind: "text", Text: "world"}}, true},
		{"mixed parts", []a2a.Part{{Kind: "text", Text: "hello"}, {Kind: "file", File: &a2a.FileContent{URI: "http://x"}}}, false},
		{"file only", []a2a.Part{{Kind: "file", File: &a2a.FileContent{URI: "http://x"}}}, false},
		{"data part only", []a2a.Part{{Kind: "data", Data: map[string]any{"type": "test"}}}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isTextOnlyArtifact(a2a.Artifact{Parts: tc.parts}); got != tc.want {
				t.Errorf("isTextOnlyArtifact() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestConvertToProgressUpdate(t *testing.T) {
	tests := []struct {
		name       string
		su         *a2a.TaskStatusUpdateEvent
		wantParts  int
		wantIsData bool
	}{
		{name: "nil message", su: &a2a.TaskStatusUpdateEvent{Status: a2a.TaskStatus{State: a2a.TaskStateWorking, Message: nil}}, wantParts: 0},
		{
			name: "text part converted to data",
			su: &a2a.TaskStatusUpdateEvent{Status: a2a.TaskStatus{State: a2a.TaskStateWorking,
				Message: &a2a.Message{Parts: []a2a.Part{{Kind: "text", Text: "Processing..."}}}}},
			wantParts:  1,
			wantIsData: true,
		},
		{
			name: "empty text part not converted",
			su: &a2a.TaskStatusUpdateEvent{Status: a2a.TaskStatus{State: a2a.TaskStateWorking,
				Message: &a2a.Message{Parts: []a2a.Part{{Kind: "text", Text: ""}}}}},
			wantParts:  1,
			wantIsData: false,
		},
		{
			name: "non-text part preserved",
			su: &a2a.TaskStatusUpdateEvent{Status: a2a.TaskStatus{State: a2a.TaskStateWorking,
				Message: &a2a.Message{Parts: []a2a.Part{{Kind: "file", File: &a2a.FileContent{URI: "x"}}}}}},
			wantParts:  1,
			wantIsData: false,
		},
		{
			name: "mixed parts",
			su: &a2a.TaskStatusUpdateEvent{Status: a2a.TaskStatus{State: a2a.TaskStateWorking,
				Message: &a2a.Message{Parts: []a2a.Part{{Kind: "text", Text: "Status update"}, {Kind: "file", File: &a2a.FileContent{URI: "y"}}}}}},
			wantParts:  2,
			wantIsData: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := newTestProxy(&ProxyConfig{Name: "test", Namespace: "ns"})
			p.convertToProgressUpdate(tc.su)

			if tc.su.Status.Message == nil {
				if tc.wantParts != 0 {
					t.Errorf("expected %d parts but message is nil", tc.wantParts)
				}
				return
			}
			got := len(tc.su.Status.Message.Parts)
			if got != tc.wantParts {
				t.Errorf("got %d parts, want %d", got, tc.wantParts)
			}
			if tc.wantIsData && got > 0 {
				part := tc.su.Status.Message.Parts[0]
				if part.Kind != "data" {
					t.Errorf("first part kind = %q, want %q", part.Kind, "data")
				}
				raw, err := json.Marshal(part.Data)
				if err != nil {
					t.Fatalf("marshal data: %v", err)
				}
				var pd map[string]any
				if err := json.Unmarshal(raw, &pd); err != nil {
					t.Fatalf("unmarshal data: %v", err)
				}
				if pd["type"] != string(a2a.SignalAgentProgressUpdate) {
					t.Errorf("data type = %v, want %v", pd["type"], a2a.SignalAgentProgressUpdate)
				}
			}
		})
	}
}

func TestBuildResponseUserProps(t *testing.T) {
	tests := []struct {
		name    string
		tc      *TaskContext
		cfgName string
		want    map[string]string
	}{
		{
			name:    "basic fields",
			cfgName: "proxy-A",
			tc:      &TaskContext{ClientID: "client-1", UserID: "user-1"},
			want:    map[string]string{"delegating_agent_name": "proxy-A", "clientId": "client-1", "userId": "user-1"},
		},
		{
			name:    "missing client and user",
			cfgName: "proxy-B",
			tc:      &TaskContext{},
			want:    map[string]string{"delegating_agent_name": "proxy-B"},
		},
		{
			name:    "preserves a2aUserConfig and authToken",
			cfgName: "proxy-C",
			tc: &TaskContext{UserProperties: map[string]string{
				"a2aUserConfig": `{"key":"val"}`,
				"authToken":     "tok-123",
				"other":         "ignored",
			}},
			want: map[string]string{"delegating_agent_name": "proxy-C", "a2aUserConfig": `{"key":"val"}`, "authToken": "tok-123"},
		},
		{
			name:    "empty authToken not preserved",
			cfgName: "proxy-D",
			tc:      &TaskContext{UserProperties: map[string]string{"authToken": ""}},
			want:    map[string]string{"delegating_agent_name": "proxy-D"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := newTestProxy(&ProxyConfig{Name: tc.cfgName, Namespace: "ns"})
			got := p.buildResponseUserProps(tc.tc)

			if got["timestamp"] == "" {
				t.Error("props[timestamp] should not be empty")
			}
			delete(got, "timestamp")
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("buildResponseUserProps() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestConsolidateTextArtifacts(t *testing.T) {
	const consolidated = "The following text-only artifacts were returned and have been consolidated into this message:\n\n---\n\n"
	tests := []struct {
		name string
		task *a2a.Task
		want *a2a.Task
	}{
		{
			name: "no artifacts",
			task: &a2a.Task{Status: a2a.TaskStatus{State: a2a.TaskStateCompleted}},
			want: &a2a.Task{Status: a2a.TaskStatus{State: a2a.TaskStateCompleted}},
		},
		{
			name: "text-only artifacts consolidated into nil message",
			task: &a2a.Task{
				Status:    a2a.TaskStatus{State: a2a.TaskStateCompleted},
				Artifacts: []a2a.Artifact{{ArtifactID: "a1", Parts: []a2a.Part{{Kind: "text", Text: "Hello from artifact"}}}},
			},
			want: &a2a.Task{Status: a2a.TaskStatus{State: a2a.TaskStateCompleted,
				Message: &a2a.Message{Role: "agent", Parts: []a2a.Part{{Kind: "text", Text: consolidated + "Hello from artifact"}}}}},
		},
		{
			name: "text-only artifacts appended to existing message",
			task: &a2a.Task{
				Status: a2a.TaskStatus{State: a2a.TaskStateCompleted,
					Message: &a2a.Message{MessageID: "msg-1", Role: "agent", Parts: []a2a.Part{{Kind: "text", Text: "Original"}}}},
				Artifacts: []a2a.Artifact{{ArtifactID: "a1", Parts: []a2a.Part{{Kind: "text", Text: "Consolidated text"}}}},
			},
			want: &a2a.Task{Status: a2a.TaskStatus{State: a2a.TaskStateCompleted,
				Message: &a2a.Message{MessageID: "msg-1", Role: "agent", Parts: []a2a.Part{
					{Kind: "text", Text: "Original"},
					{Kind: "text", Text: consolidated + "Consolidated text"},
				}}}},
		},
		{
			name: "mixed artifacts - only text removed",
			task: &a2a.Task{
				Status: a2a.TaskStatus{State: a2a.TaskStateCompleted},
				Artifacts: []a2a.Artifact{
					{ArtifactID: "text-art", Parts: []a2a.Part{{Kind: "text", Text: "Remove me"}}},
					{ArtifactID: "file-art", Parts: []a2a.Part{{Kind: "file", File: &a2a.FileContent{URI: "http://x"}}}},
				},
			},
			want: &a2a.Task{
				Status: a2a.TaskStatus{State: a2a.TaskStateCompleted,
					Message: &a2a.Message{Role: "agent", Parts: []a2a.Part{{Kind: "text", Text: consolidated + "Remove me"}}}},
				Artifacts: []a2a.Artifact{{ArtifactID: "file-art", Parts: []a2a.Part{{Kind: "file", File: &a2a.FileContent{URI: "http://x"}}}}},
			},
		},
		{
			name: "no text content in text parts",
			task: &a2a.Task{
				Status:    a2a.TaskStatus{State: a2a.TaskStateCompleted},
				Artifacts: []a2a.Artifact{{ArtifactID: "a1", Parts: []a2a.Part{{Kind: "text", Text: ""}}}},
			},
			want: &a2a.Task{
				Status:    a2a.TaskStatus{State: a2a.TaskStateCompleted},
				Artifacts: []a2a.Artifact{{ArtifactID: "a1", Parts: []a2a.Part{{Kind: "text", Text: ""}}}},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newTestProxy(&ProxyConfig{Name: "test", Namespace: "ns"})

			p.consolidateTextArtifacts(tt.task)

			if diff := cmp.Diff(tt.want, tt.task, cmpopts.IgnoreFields(a2a.Message{}, "MessageID")); diff != "" {
				t.Errorf("consolidateTextArtifacts() task mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestResolveInboundArtifacts_NoArtifactService(t *testing.T) {
	p := newTestProxy(&ProxyConfig{Name: "test", Namespace: "ns"})
	p.svc = &mockServices{brk: broker.NewDevBroker(), arts: nil}

	req := &a2a.Request{
		Params: map[string]any{
			"message": map[string]any{
				"parts": []any{
					map[string]any{
						"kind": "file",
						"file": map[string]any{"uri": "artifact://app/user/sess/file.txt"},
					},
				},
			},
		},
	}
	p.resolveInboundArtifacts(context.Background(), req)
}

func TestResolveInboundArtifacts(t *testing.T) {
	type stored struct{ filename, content, mimeType string }
	tests := []struct {
		name      string
		stored    []stored
		parts     []any
		wantParts []any
	}{
		{
			name:      "a uri that is not an artifact uri is left alone",
			parts:     []any{map[string]any{"kind": "file", "file": map[string]any{"uri": "https://example.com/file.txt"}}},
			wantParts: []any{map[string]any{"kind": "file", "file": map[string]any{"uri": "https://example.com/file.txt"}}},
		},
		{
			name:      "an artifact uri is replaced by the artifact's bytes and mime type",
			stored:    []stored{{"test.txt", "hello world", "text/plain"}},
			parts:     []any{map[string]any{"kind": "file", "file": map[string]any{"uri": "artifact://myapp/user1/sess1/test.txt"}}},
			wantParts: []any{map[string]any{"kind": "file", "file": map[string]any{"bytes": "aGVsbG8gd29ybGQ=", "mimeType": "text/plain"}}},
		},
		{
			name:      "a version in the uri reads that version",
			stored:    []stored{{"versioned.txt", "v0", "text/plain"}, {"versioned.txt", "v1", "text/plain"}},
			parts:     []any{map[string]any{"kind": "file", "file": map[string]any{"uri": "artifact://myapp/user1/sess1/versioned.txt?version=0"}}},
			wantParts: []any{map[string]any{"kind": "file", "file": map[string]any{"bytes": "djA=", "mimeType": "text/plain"}}},
		},
		{
			name: "parts that are not files are left alone",
			parts: []any{
				map[string]any{"kind": "text", "text": "hello"},
				map[string]any{"kind": "data", "data": map[string]any{"x": 1}},
			},
			wantParts: []any{
				map[string]any{"kind": "text", "text": "hello"},
				map[string]any{"kind": "data", "data": map[string]any{"x": 1}},
			},
		},
		{
			name:      "a missing artifact keeps its uri",
			parts:     []any{map[string]any{"kind": "file", "file": map[string]any{"uri": "artifact://app/user/sess/nonexistent.txt"}}},
			wantParts: []any{map[string]any{"kind": "file", "file": map[string]any{"uri": "artifact://app/user/sess/nonexistent.txt"}}},
		},
		{
			name:      "a uri with too few path segments keeps its uri",
			parts:     []any{map[string]any{"kind": "file", "file": map[string]any{"uri": "artifact://app/user/sess"}}},
			wantParts: []any{map[string]any{"kind": "file", "file": map[string]any{"uri": "artifact://app/user/sess"}}},
		},
		{
			name:      "a mime type the part already has is kept",
			stored:    []stored{{"data.bin", "binary", "application/octet-stream"}},
			parts:     []any{map[string]any{"kind": "file", "file": map[string]any{"uri": "artifact://myapp/user1/sess1/data.bin", "mimeType": "image/png"}}},
			wantParts: []any{map[string]any{"kind": "file", "file": map[string]any{"bytes": "YmluYXJ5", "mimeType": "image/png"}}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, artSvc := newTestProxyWithArtifacts()
			ctx := context.Background()
			for _, s := range tt.stored {
				if _, err := artSvc.Inner.Save(ctx, artSvc.Resolve("myapp", "user1", "sess1", s.filename), []byte(s.content), s.mimeType); err != nil {
					t.Fatalf("save %s: %v", s.filename, err)
				}
			}
			req := &a2a.Request{Params: map[string]any{"message": map[string]any{"parts": tt.parts}}}

			p.resolveInboundArtifacts(ctx, req)

			if diff := cmp.Diff(tt.wantParts, req.Params.(map[string]any)["message"].(map[string]any)["parts"]); diff != "" {
				t.Errorf("resolveInboundArtifacts parts mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestResolveInboundArtifacts_BadParams(t *testing.T) {
	p, _ := newTestProxyWithArtifacts()

	tests := []struct {
		name   string
		params any
	}{
		{"nil params", nil},
		{"string params", "not-a-map"},
		{"no message key", map[string]any{"other": "stuff"}},
		{"message not map", map[string]any{"message": "string"}},
		{"no parts key", map[string]any{"message": map[string]any{}}},
		{"parts not array", map[string]any{"message": map[string]any{"parts": "str"}}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := &a2a.Request{Params: tc.params}
			p.resolveInboundArtifacts(context.Background(), req)
		})
	}
}

func TestProcessOutboundArtifacts_NoArtifactService(t *testing.T) {
	p := newTestProxy(&ProxyConfig{Name: "test", Namespace: "ns"})
	p.svc = &mockServices{brk: broker.NewDevBroker(), arts: nil}

	tc := &TaskContext{TaskID: "t1", UserID: "u1", SessionID: "s1"}
	result := p.processOutboundArtifacts(context.Background(), tc, []a2a.Artifact{{ArtifactID: "a1", Parts: []a2a.Part{
		{Kind: "file", File: &a2a.FileContent{Bytes: base64.StdEncoding.EncodeToString([]byte("data")), Name: "f.txt"}},
	}}})
	if result != nil {
		t.Errorf("processOutboundArtifacts without an artifact service = %v, want nil", result)
	}
}

func TestProcessOutboundArtifacts(t *testing.T) {
	tests := []struct {
		name         string
		parts        []a2a.Part
		wantManifest []map[string]any
		wantParts    []a2a.Part
	}{
		{
			name: "file bytes are saved and replaced by an artifact uri",
			parts: []a2a.Part{{Kind: "file", File: &a2a.FileContent{
				Bytes: base64.StdEncoding.EncodeToString([]byte("outbound data")), Name: "output.txt", MIMEType: "text/plain"}}},
			wantManifest: []map[string]any{{"filename": "output.txt", "version": 0}},
			wantParts: []a2a.Part{{Kind: "file", File: &a2a.FileContent{
				Name: "output.txt", MIMEType: "text/plain", URI: "artifact://test-proxy/user1/sess1/output.txt?version=0"}}},
		},
		{
			name:      "text parts are not saved",
			parts:     []a2a.Part{{Kind: "text", Text: "just text"}},
			wantParts: []a2a.Part{{Kind: "text", Text: "just text"}},
		},
		{
			name: "a file that already has a uri is not saved",
			parts: []a2a.Part{{Kind: "file", File: &a2a.FileContent{
				URI: "artifact://existing", Bytes: base64.StdEncoding.EncodeToString([]byte("data"))}}},
			wantParts: []a2a.Part{{Kind: "file", File: &a2a.FileContent{URI: "artifact://existing", Bytes: "ZGF0YQ=="}}},
		},
		{
			name:         "a file without a name is named after its artifact",
			parts:        []a2a.Part{{Kind: "file", File: &a2a.FileContent{Bytes: base64.StdEncoding.EncodeToString([]byte("data"))}}},
			wantManifest: []map[string]any{{"filename": "artifact_art-1", "version": 0}},
			wantParts:    []a2a.Part{{Kind: "file", File: &a2a.FileContent{URI: "artifact://test-proxy/user1/sess1/artifact_art-1?version=0"}}},
		},
		{
			name: "a file next to a text part is saved",
			parts: []a2a.Part{
				{Kind: "text", Text: "Description from text"},
				{Kind: "file", File: &a2a.FileContent{Bytes: base64.StdEncoding.EncodeToString([]byte("data")), Name: "desc-test.txt", MIMEType: "text/plain"}},
			},
			wantManifest: []map[string]any{{"filename": "desc-test.txt", "version": 0}},
			wantParts: []a2a.Part{
				{Kind: "text", Text: "Description from text"},
				{Kind: "file", File: &a2a.FileContent{Name: "desc-test.txt", MIMEType: "text/plain", URI: "artifact://test-proxy/user1/sess1/desc-test.txt?version=0"}},
			},
		},
		{
			name:      "bytes that are not base64 are skipped",
			parts:     []a2a.Part{{Kind: "file", File: &a2a.FileContent{Bytes: "not-valid-base64!!!", Name: "bad.txt"}}},
			wantParts: []a2a.Part{{Kind: "file", File: &a2a.FileContent{Bytes: "not-valid-base64!!!", Name: "bad.txt"}}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, _ := newTestProxyWithArtifacts()
			tc := &TaskContext{TaskID: "t1", UserID: "user1", SessionID: "sess1"}
			artifacts := []a2a.Artifact{{ArtifactID: "art-1", Parts: tt.parts}}

			manifest := p.processOutboundArtifacts(context.Background(), tc, artifacts)

			if diff := cmp.Diff(tt.wantManifest, manifest); diff != "" {
				t.Errorf("processOutboundArtifacts manifest mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantParts, artifacts[0].Parts); diff != "" {
				t.Errorf("processOutboundArtifacts parts mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestProcessOutboundArtifacts_DefaultMimeType(t *testing.T) {
	p, artSvc := newTestProxyWithArtifacts()
	ctx := context.Background()
	tc := &TaskContext{TaskID: "t1", UserID: "user1", SessionID: "sess1"}
	artifacts := []a2a.Artifact{{ArtifactID: "art-1", Parts: []a2a.Part{
		{Kind: "file", File: &a2a.FileContent{Bytes: base64.StdEncoding.EncodeToString([]byte("data")), Name: "noext"}},
	}}}

	p.processOutboundArtifacts(ctx, tc, artifacts)

	loc := artSvc.Resolve("test-proxy", "user1", "sess1", "noext")
	_, mime, err := artSvc.Inner.Load(ctx, loc, nil)
	if err != nil {
		t.Fatalf("load artifact: %v", err)
	}
	if mime != "application/octet-stream" {
		t.Errorf("mime = %q, want %q", mime, "application/octet-stream")
	}
}

func TestSetAgentNameMetadata(t *testing.T) {
	p := newTestProxy(&ProxyConfig{Name: "my-proxy", Namespace: "ns"})

	t.Run("nil metadata", func(t *testing.T) {
		task := &a2a.Task{}
		p.setAgentNameMetadata(task)
		if task.Metadata["agent_name"] != "my-proxy" {
			t.Errorf("agent_name = %v", task.Metadata["agent_name"])
		}
	})

	t.Run("existing metadata", func(t *testing.T) {
		task := &a2a.Task{Metadata: map[string]any{"other": "value"}}
		p.setAgentNameMetadata(task)
		if task.Metadata["agent_name"] != "my-proxy" {
			t.Errorf("agent_name = %v", task.Metadata["agent_name"])
		}
		if task.Metadata["other"] != "value" {
			t.Error("existing metadata should be preserved")
		}
	})
}

func TestRewriteCard(t *testing.T) {
	p := newTestProxy(&ProxyConfig{Name: "my-proxy", Namespace: "test-ns", DisplayName: "My Proxy Display"})
	card := &a2a.AgentCard{Name: "original-name", URL: "http://original.example.com", Description: "Original description"}

	p.rewriteCard(card)

	if card.Name != "my-proxy" {
		t.Errorf("Name = %q, want %q", card.Name, "my-proxy")
	}
	wantURL := "solace:" + a2a.AgentRequestTopic("test-ns", "my-proxy")
	if card.URL != wantURL {
		t.Errorf("URL = %q, want %q", card.URL, wantURL)
	}
	if card.Version != "1.0.0" {
		t.Errorf("Version = %q, want %q", card.Version, "1.0.0")
	}
	if card.ProtocolVersion != "0.3.0" {
		t.Errorf("ProtocolVersion = %q, want %q", card.ProtocolVersion, "0.3.0")
	}
	foundAgentType := false
	foundDisplayName := false
	for _, ext := range card.Capabilities.Extensions {
		if ext.URI == "https://solace.com/a2a/extensions/agent-type" {
			foundAgentType = true
			if ext.Params["type"] != "proxy" {
				t.Errorf("agent-type = %v, want %q", ext.Params["type"], "proxy")
			}
		}
		if ext.URI == "https://solace.com/a2a/extensions/display-name" {
			foundDisplayName = true
			if ext.Params["display_name"] != "My Proxy Display" {
				t.Errorf("display_name = %v, want %q", ext.Params["display_name"], "My Proxy Display")
			}
		}
	}
	if !foundAgentType {
		t.Error("agent-type extension not found")
	}
	if !foundDisplayName {
		t.Error("display-name extension not found")
	}
	if len(card.DefaultInputModes) != 1 || card.DefaultInputModes[0] != "text" {
		t.Errorf("DefaultInputModes = %v, want [text]", card.DefaultInputModes)
	}
	if len(card.DefaultOutputModes) != 1 || card.DefaultOutputModes[0] != "text" {
		t.Errorf("DefaultOutputModes = %v, want [text]", card.DefaultOutputModes)
	}
	if card.Skills == nil {
		t.Error("Skills should be initialized to empty slice")
	}
}

func TestRewriteCard_PreservesExistingValues(t *testing.T) {
	p := newTestProxy(&ProxyConfig{Name: "my-proxy", Namespace: "test-ns"})
	card := &a2a.AgentCard{
		Name:               "original",
		URL:                "http://original.example.com",
		Version:            "2.0.0",
		ProtocolVersion:    "0.5.0",
		DefaultInputModes:  []string{"text", "file"},
		DefaultOutputModes: []string{"text", "data"},
		Skills:             []a2a.AgentSkill{{ID: "skill-1"}},
	}

	p.rewriteCard(card)

	if card.Version != "2.0.0" {
		t.Errorf("Version should be preserved, got %q", card.Version)
	}
	if card.ProtocolVersion != "0.5.0" {
		t.Errorf("ProtocolVersion should be preserved, got %q", card.ProtocolVersion)
	}
	if len(card.DefaultInputModes) != 2 {
		t.Errorf("DefaultInputModes should be preserved, got %v", card.DefaultInputModes)
	}
	if len(card.Skills) != 1 {
		t.Errorf("Skills should be preserved, got %v", card.Skills)
	}
}

func TestRewriteCard_UpdatesExistingAgentTypeExtension(t *testing.T) {
	p := newTestProxy(&ProxyConfig{Name: "my-proxy", Namespace: "test-ns"})
	card := &a2a.AgentCard{Capabilities: a2a.AgentCapabilities{Extensions: []a2a.AgentExtension{
		{URI: "https://solace.com/a2a/extensions/agent-type", Params: map[string]any{"type": "agent"}},
	}}}

	p.rewriteCard(card)

	count := 0
	for _, ext := range card.Capabilities.Extensions {
		if ext.URI == "https://solace.com/a2a/extensions/agent-type" {
			count++
			if ext.Params["type"] != "proxy" {
				t.Errorf("agent-type = %v, should be updated to proxy", ext.Params["type"])
			}
		}
	}
	if count != 1 {
		t.Errorf("agent-type extensions = %d, want 1", count)
	}
}

func TestRewriteCard_NoHealthExtensionWhenNoProblems(t *testing.T) {
	p := newTestProxy(&ProxyConfig{Name: "my-proxy", Namespace: "test-ns"})
	card := &a2a.AgentCard{}

	p.rewriteCard(card)

	if got := a2a.HealthProblemsFromCard(*card); got != nil {
		t.Errorf("HealthProblemsFromCard() = %v, want nil", got)
	}
}

func TestRewriteCard_HealthExtension(t *testing.T) {
	p := newTestProxy(&ProxyConfig{Name: "my-proxy", Namespace: "test-ns"})
	p.health.Set("remote:fetch", health.Degraded, "dial tcp: connection refused")
	card := &a2a.AgentCard{}

	p.rewriteCard(card)

	problems := a2a.HealthProblemsFromCard(*card)
	if len(problems) != 1 || problems[0].Severity != health.Degraded {
		t.Fatalf("HealthProblemsFromCard() = %v, want one Degraded problem", problems)
	}

	p.health.Clear("remote:fetch")
	p.rewriteCard(card)
	if got := a2a.HealthProblemsFromCard(*card); got != nil {
		t.Errorf("HealthProblemsFromCard() after Clear = %v, want nil", got)
	}
}

func TestRewriteCard_HealthExtensionNeverMergesRemoteCard(t *testing.T) {
	p := newTestProxy(&ProxyConfig{Name: "my-proxy", Namespace: "test-ns"})
	p.health.Set("remote:fetch", health.Fatal, "sam's own view")
	card := &a2a.AgentCard{Capabilities: a2a.AgentCapabilities{Extensions: []a2a.AgentExtension{
		a2a.HealthProblemsExtension([]health.Problem{{ID: "not-sams", Detail: "remote's own claim"}}),
	}}}

	p.rewriteCard(card)

	problems := a2a.HealthProblemsFromCard(*card)
	if len(problems) != 1 || problems[0].ID != "remote:fetch" {
		t.Fatalf("HealthProblemsFromCard() = %+v, want only SAM's own remote:fetch problem", problems)
	}
}

func TestRepublishCachedCardHealth_NoCachedCard_NoPanic(t *testing.T) {
	p := newTestProxy(&ProxyConfig{Name: "my-proxy", Namespace: "test-ns"})
	p.republishCachedCardHealth(context.Background())
}

func TestIsAuthResponse(t *testing.T) {
	tests := []struct {
		name   string
		params any
		want   bool
	}{
		{"nil params", nil, false},
		{"not a map", "string", false},
		{"no data parts", map[string]any{"message": map[string]any{"parts": []any{map[string]any{"kind": "text"}}}}, false},
		{
			"auth_response data part",
			map[string]any{"message": map[string]any{"parts": []any{
				map[string]any{"kind": "data", "data": map[string]any{"type": string(a2a.SignalAuthResponse)}},
			}}},
			true,
		},
		{
			"different data type",
			map[string]any{"message": map[string]any{"parts": []any{
				map[string]any{"kind": "data", "data": map[string]any{"type": "other_signal"}},
			}}},
			false,
		},
		{
			"data not a map",
			map[string]any{"message": map[string]any{"parts": []any{map[string]any{"kind": "data", "data": "string-data"}}}},
			false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := newTestProxy(&ProxyConfig{Name: "test", Namespace: "ns"})
			if got := p.isAuthResponse(&a2a.Request{Params: tc.params}); got != tc.want {
				t.Errorf("isAuthResponse() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestLoadArtifactByURI(t *testing.T) {
	p, artSvc := newTestProxyWithArtifacts()
	ctx := context.Background()
	if _, err := artSvc.Inner.Save(ctx, artSvc.Resolve("myapp", "user1", "sess1", "file.txt"), []byte("content"), "text/plain"); err != nil {
		t.Fatalf("save: %v", err)
	}

	tests := []struct {
		name    string
		uri     string
		want    string
		wantErr bool
	}{
		{name: "valid URI", uri: "artifact://myapp/user1/sess1/file.txt", want: "content"},
		{name: "non-artifact scheme", uri: "https://example.com/file", wantErr: true},
		{name: "missing host", uri: "artifact:///user1/sess1/file.txt", wantErr: true},
		{name: "too few path segments", uri: "artifact://myapp/user1", wantErr: true},
		{name: "nonexistent artifact", uri: "artifact://myapp/user1/sess1/missing.txt", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			content, _, err := p.loadArtifactByURI(ctx, artSvc, tc.uri)
			if (err != nil) != tc.wantErr {
				t.Errorf("loadArtifactByURI(%q) error = %v, want error %v", tc.uri, err, tc.wantErr)
			}
			if string(content) != tc.want {
				t.Errorf("loadArtifactByURI(%q) = %q, want %q", tc.uri, content, tc.want)
			}
		})
	}
}

func TestLoadArtifactByURI_ReadsTheLocationArtifactParseURIReads(t *testing.T) {
	p, _ := newTestProxyWithArtifacts()
	artSvc := &artifact.ScopedService{Inner: filesystem.New(t.TempDir()), Scope: "agent", Namespace: "test-ns"}
	ctx := context.Background()
	save := func(user, session, filename, content string) {
		t.Helper()
		if _, err := artSvc.Inner.Save(ctx, artSvc.Resolve("myapp", user, session, filename), []byte(content), "text/plain"); err != nil {
			t.Fatalf("save: %v", err)
		}
	}
	save("alice", "s", "report.txt", "alice-secret")
	save("bob", "x", "report.txt", "bob-own")
	save("alice/bob", "s", "report.txt", "other-owner-with-a-slash")

	tests := []struct {
		name string
		uri  string
		want string
	}{
		{"extra leading segments: the last three name the location", "artifact://myapp/alice/s/bob/x/report.txt", "bob-own"},
		{"percent-encoded owner", "artifact://myapp/%62ob/x/report.txt", "bob-own"},
		{"an encoded slash stays inside its segment", "artifact://myapp/alice%2Fbob/s/report.txt", "other-owner-with-a-slash"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			content, _, err := p.loadArtifactByURI(ctx, artSvc, tc.uri)
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			if string(content) != tc.want {
				t.Errorf("read %q, want %q", content, tc.want)
			}
		})
	}

	t.Run("a version that is not a number reads nothing", func(t *testing.T) {
		if _, _, err := p.loadArtifactByURI(ctx, artSvc, "artifact://myapp/bob/x/report.txt?version=abc"); err == nil {
			t.Error("expected an error for a malformed version")
		}
	})
}

func TestRouteMessage_UnknownMethod(t *testing.T) {
	p := newTestProxy(&ProxyConfig{Name: "test", Namespace: "ns"})
	p.svc = &mockServices{brk: broker.NewDevBroker()}
	p.adapter = &mockAdapter{}
	data, _ := json.Marshal(a2a.Request{Method: "unknown/method", ID: "t1"})
	acked := false
	msg := broker.NewDeliveredMessage("m1", "topic", data, func() { acked = true }, nil)

	p.routeMessage(context.Background(), msg)

	if !acked {
		t.Error("message should be acked for unknown method")
	}
}

func TestRouteMessage_InvalidJSON(t *testing.T) {
	p := newTestProxy(&ProxyConfig{Name: "test", Namespace: "ns"})
	p.svc = &mockServices{brk: broker.NewDevBroker()}
	acked := false
	msg := broker.NewDeliveredMessage("m1", "topic", []byte("not json"), func() { acked = true }, nil)

	p.routeMessage(context.Background(), msg)

	if !acked {
		t.Error("message should be acked for invalid JSON")
	}
}

func TestHandleCancel_KnownTask(t *testing.T) {
	p := newBrokerProxy(t)
	p.tasks.Add(&TaskContext{TaskID: "t1", SessionID: "s1"})
	req := a2a.Request{Method: "tasks/cancel", ID: "t1"}
	data, _ := json.Marshal(req)

	p.handleCancel(context.Background(), broker.NewDeliveredMessage("m1", "topic", data, func() {}, nil), &req)

	if p.tasks.Get("t1") != nil {
		t.Error("task should be removed after cancel")
	}
}

func TestHandleCancel_UnknownTask(t *testing.T) {
	p := newBrokerProxy(t)
	req := a2a.Request{Method: "tasks/cancel", ID: "unknown"}
	data, _ := json.Marshal(req)

	p.handleCancel(context.Background(), broker.NewDeliveredMessage("m1", "topic", data, func() {}, nil), &req)
}

func TestHandleRequest_Basic(t *testing.T) {
	p := newBrokerProxy(t)
	ctx := context.Background()
	brk := p.svc.Broker()
	replyQ := subscribeTopic(t, brk, "reply-q", "reply/topic")
	p.adapter = &completedTaskAdapter{taskID: "downstream-1"}
	reqBody := a2a.Request{
		Method: "message/send",
		ID:     "t1",
		Params: map[string]any{
			"message": map[string]any{
				"contextId": "sess-1",
				"parts":     []any{map[string]any{"kind": "text", "text": "hello"}},
			},
		},
	}
	data, _ := json.Marshal(reqBody)
	msg := broker.NewDeliveredMessage("m1", "topic", data, func() {}, nil)
	msg.UserProperties = map[string]string{
		"replyTo":        "reply/topic",
		"a2aStatusTopic": "status/topic",
		"userId":         "user1",
		"clientId":       "client1",
	}

	p.handleRequest(ctx, msg, &reqBody)

	receiveMessage(ctx, t, brk, replyQ)
}

func TestForwardAndProcess_AdapterError(t *testing.T) {
	p := newBrokerProxy(t)
	ctx := context.Background()
	brk := p.svc.Broker()
	q := subscribeReplyTo(t, brk)
	p.adapter = &errorAdapter{err: fmt.Errorf("connection refused")}
	tc := &TaskContext{TaskID: "t1", SessionID: "s1", ReplyToTopic: "reply/topic"}
	p.tasks.Add(tc)

	p.forwardAndProcess(ctx, tc, &a2a.Request{Method: "message/send", ID: "t1"})

	if p.tasks.Get("t1") != nil {
		t.Error("task should be removed after forward error")
	}
	receiveMessage(ctx, t, brk, q)
}

func TestForwardAndProcess_RecordsMetricOnAdapterError(t *testing.T) {
	p, rec := newProxyWithRecorder(t)
	p.SetAdapter(&recordingAdapter{err: fmt.Errorf("HTTP 503 bad gateway")})
	tc := &TaskContext{TaskID: "t1", SessionID: "s1"}
	p.tasks.Add(tc)

	p.forwardAndProcess(t.Context(), tc, &a2a.Request{ID: "t1", Method: "message/send"})

	if c := rec.HistogramCount("operation.duration", "type", "a2a_agent"); c != 1 {
		t.Fatalf("operation.duration histograms of type a2a_agent = %d, want 1", c)
	}
	if !rec.HasHistogram("operation.duration",
		"component.name", "test-agent",
		"operation.name", "forward_request",
		"error.type", "5xx_error",
	) {
		t.Errorf("operation.duration histograms = %+v, want one with error.type 5xx_error", rec.Histograms("operation.duration"))
	}
}

func TestForwardAndProcess_RecordsTheForwardMetric(t *testing.T) {
	tests := []struct {
		name          string
		method        string
		event         ResponseEvent
		wantErrorType string
	}{
		{
			name:   "a completed task records no error type",
			method: "message/send",
			event:  ResponseEvent{Task: &a2a.Task{ID: "d1", Kind: "task", Status: a2a.TaskStatus{State: a2a.TaskStateCompleted}}},
		},
		{
			name:          "a stream error records a JSON-RPC error",
			method:        "message/stream",
			event:         ResponseEvent{Error: fmt.Errorf("downstream error -32603: internal")},
			wantErrorType: "jsonrpc_error",
		},
		{
			name:          "a failed task records a task failure",
			method:        "message/stream",
			event:         ResponseEvent{Task: &a2a.Task{ID: "d1", Kind: "task", Status: a2a.TaskStatus{State: a2a.TaskStateFailed}}},
			wantErrorType: "task_failed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, rec := newProxyWithRecorder(t)
			p.SetAdapter(&recordingAdapter{events: responseEvents(tt.event)})
			tc := &TaskContext{TaskID: "t1", SessionID: "s1"}
			p.tasks.Add(tc)

			p.forwardAndProcess(t.Context(), tc, &a2a.Request{ID: "t1", Method: tt.method})

			got := rec.Histograms("operation.duration")
			if len(got) != 1 || !rec.HasHistogram("operation.duration",
				"type", "a2a_agent",
				"component.name", "test-agent",
				"operation.name", "forward_request",
				"error.type", tt.wantErrorType,
			) {
				t.Errorf("operation.duration histograms = %+v, want one a2a_agent forward_request with error.type %q", got, tt.wantErrorType)
			}
		})
	}
}

func TestBuildOAuthScheme_NilAuth(t *testing.T) {
	p := newTestProxy(&ProxyConfig{Name: "test", Namespace: "ns"})
	scheme := p.buildOAuthScheme()
	if scheme != nil {
		t.Error("expected nil scheme when no task auth configured")
	}
}

func TestBuildOAuthScheme_WithAuth(t *testing.T) {
	p := newTestProxy(&ProxyConfig{
		Name:      "test",
		Namespace: "ns",
		TaskAuth: &AuthConfig{
			Type:                    "oauth2_authorization_code",
			AuthorizationURL:        "https://auth.example.com/authorize",
			TokenURL:                "https://auth.example.com/token",
			Scopes:                  []string{"read", "write"},
			TokenEndpointAuthMethod: "client_secret_basic",
		},
	})
	scheme := p.buildOAuthScheme()
	if scheme == nil {
		t.Fatal("expected non-nil scheme")
	}
	if scheme.AuthorizationURL != "https://auth.example.com/authorize" {
		t.Errorf("AuthorizationURL = %q", scheme.AuthorizationURL)
	}
	if scheme.TokenURL != "https://auth.example.com/token" {
		t.Errorf("TokenURL = %q", scheme.TokenURL)
	}
	if len(scheme.Scopes) != 2 {
		t.Errorf("Scopes = %v", scheme.Scopes)
	}
}

func TestBuildOAuthCred_NilAuth(t *testing.T) {
	p := newTestProxy(&ProxyConfig{Name: "test", Namespace: "ns"})
	cred := p.buildOAuthCred()
	if cred != nil {
		t.Error("expected nil cred when no task auth configured")
	}
}

func TestBuildOAuthCred_WithAuth(t *testing.T) {
	p := newTestProxy(&ProxyConfig{
		Name:      "test",
		Namespace: "ns",
		TaskAuth: &AuthConfig{
			Type:         "oauth2_authorization_code",
			ClientID:     "my-client",
			ClientSecret: "my-secret",
		},
	})
	cred := p.buildOAuthCred()
	if cred == nil {
		t.Fatal("expected non-nil cred")
	}
	if cred.ClientID != "my-client" {
		t.Errorf("ClientID = %q", cred.ClientID)
	}
	if cred.ClientSecret != "my-secret" {
		t.Errorf("ClientSecret = %q", cred.ClientSecret)
	}
}

func TestExtractAuthResponse(t *testing.T) {
	p := newTestProxy(&ProxyConfig{Name: "test", Namespace: "ns"})

	t.Run("valid auth response", func(t *testing.T) {
		req := &a2a.Request{
			Params: map[string]any{
				"message": map[string]any{
					"parts": []any{
						map[string]any{
							"kind": "data",
							"data": map[string]any{
								"type":                 string(a2a.SignalAuthResponse),
								"target_agent":         "proxy-1",
								"target_agent_task_id": "task-123",
								"code":                 "auth-code-456",
								"code_verifier":        "verifier-789",
							},
						},
					},
				},
			},
		}
		got := p.extractAuthResponse(req)
		if got == nil {
			t.Fatal("expected non-nil response")
		}
		if got.Code != "auth-code-456" {
			t.Errorf("Code = %q", got.Code)
		}
		if got.TargetAgentTaskID != "task-123" {
			t.Errorf("TargetAgentTaskID = %q", got.TargetAgentTaskID)
		}
	})

	t.Run("no auth response", func(t *testing.T) {
		req := &a2a.Request{
			Params: map[string]any{
				"message": map[string]any{
					"parts": []any{
						map[string]any{"kind": "text", "text": "hello"},
					},
				},
			},
		}
		got := p.extractAuthResponse(req)
		if got != nil {
			t.Errorf("extractAuthResponse() = %v, want nil", got)
		}
	})

	t.Run("nil params", func(t *testing.T) {
		req := &a2a.Request{Params: nil}
		got := p.extractAuthResponse(req)
		if got != nil {
			t.Errorf("extractAuthResponse() = %v, want nil", got)
		}
	})
}

func newBrokerProxy(t *testing.T) *ProxyInstance {
	t.Helper()
	pc := &ProxyConfig{
		Name:      "test-proxy",
		Namespace: "test-ns",
	}
	p := newTestProxy(pc)

	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}

	memStore := artmem.New()
	artSvc := &artifact.ScopedService{
		Inner:     memStore,
		Scope:     "agent",
		Namespace: "test-ns",
	}

	p.svc = &mockServices{brk: brk, arts: artSvc}
	p.adapter = &mockAdapter{}
	return p
}

func subscribeTopic(t *testing.T, brk broker.Broker, queue, topic string) broker.Queue {
	t.Helper()
	q, err := brk.CreateQueue(queue, broker.QueueOptions{})
	if err != nil {
		t.Fatalf("CreateQueue(%q): %v", queue, err)
	}
	if subErr := brk.Subscribe(q, topic); subErr != nil {
		t.Fatalf("Subscribe(%q): %v", topic, subErr)
	}
	return q
}

func subscribeReplyTo(t *testing.T, brk broker.Broker) broker.Queue {
	t.Helper()
	return subscribeTopic(t, brk, "test-q", "reply/topic")
}

func receiveMessage(ctx context.Context, t *testing.T, brk broker.Broker, q broker.Queue) *broker.Message {
	t.Helper()
	msg, err := brk.Receive(ctx, q)
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if msg == nil {
		t.Fatal("Receive returned no message")
	}
	return msg
}

func responseEvents(events ...ResponseEvent) <-chan ResponseEvent {
	ch := make(chan ResponseEvent, len(events))
	for _, evt := range events {
		ch <- evt
	}
	close(ch)
	return ch
}

func TestFinalStatusUpdateWithoutStatusTopic(t *testing.T) {
	p, rec := newPublishRecordingProxy(t)
	brk := p.svc.Broker()
	q := subscribeReplyTo(t, brk)
	tc := &TaskContext{TaskID: "t1", SessionID: "s1", ReplyToTopic: "reply/topic"}
	p.tasks.Add(tc)

	p.processResponseEvents(context.Background(), tc, responseEvents(
		ResponseEvent{Task: &a2a.Task{ID: "d1", Kind: "task", Status: a2a.TaskStatus{State: a2a.TaskStateSubmitted}}},
		ResponseEvent{StatusUpdate: &a2a.TaskStatusUpdateEvent{TaskID: "d1", Kind: "status-update", Final: true,
			Status: a2a.TaskStatus{State: a2a.TaskStateCompleted, Message: &a2a.Message{Role: a2a.RoleAgent, Parts: []a2a.Part{{Kind: "text", Text: "done"}}}}}},
	), newTestMonitor())

	task := receiveFinalTask(t, brk, q)
	if task.ID != "t1" || task.Status.State != a2a.TaskStateCompleted {
		t.Errorf("response task = %+v, want t1 completed", task)
	}
	if msg := task.Status.Message; msg == nil || msg.TaskID != "t1" || msg.ContextID != "s1" || msg.MessageID == "" {
		t.Errorf("final message = %+v, want the mesh task and session ids and a message id", msg)
	}
	if got := rec.published(); len(got) != 1 || got[0] != "reply/topic" {
		t.Errorf("published to %q, want exactly one publish, to replyTo", got)
	}
	if p.tasks.Get("t1") != nil {
		t.Error("task still tracked after its final response")
	}
}

func TestProcessResponseEvents_StreamedArtifactTextWithoutStatusTopic(t *testing.T) {
	p := newBrokerProxy(t)
	brk := p.svc.Broker()
	q := subscribeReplyTo(t, brk)
	tc := &TaskContext{TaskID: "t1", SessionID: "s1", ReplyToTopic: "reply/topic"}
	p.tasks.Add(tc)

	p.processResponseEvents(context.Background(), tc, responseEvents(
		textArtifactUpdate("report", "The full report.", false),
		finalStatus(a2a.TaskStateCompleted),
	), newTestMonitor())

	task := receiveFinalTask(t, brk, q)
	if task.Status.Message == nil || len(task.Status.Message.Parts) != 1 || task.Status.Message.Parts[0].Text != "The full report." {
		t.Errorf("final message = %+v, want the streamed report as the answer", task.Status.Message)
	}
}

func TestProcessResponseEvents_FoldsStreamedArtifactText(t *testing.T) {
	const consolidatedPrefix = "The following text-only artifacts"
	tests := []struct {
		name        string
		statusTopic string
		events      []ResponseEvent
		wantTexts   []string
	}{
		{
			name: "chunks of one artifact join without a separator",
			events: []ResponseEvent{
				textArtifactUpdate("a1", "Hel", false), textArtifactUpdate("a1", "lo", true),
				textArtifactUpdate("a2", "World", false), finalStatus(a2a.TaskStateCompleted),
			},
			wantTexts: []string{"Hello\nWorld"},
		},
		{
			name: "updates of one artifact not marked append stay separate lines",
			events: []ResponseEvent{
				textArtifactUpdate("a1", "line one", false), textArtifactUpdate("a1", "line two", false), finalStatus(a2a.TaskStateCompleted),
			},
			wantTexts: []string{"line one\nline two"},
		},
		{
			name: "a final Task repeating a streamed artifact carries its text once",
			events: []ResponseEvent{
				textArtifactUpdate("report", "The full report.", false),
				{Task: &a2a.Task{ID: "d1", Kind: "task", Status: a2a.TaskStatus{State: a2a.TaskStateCompleted},
					Artifacts: []a2a.Artifact{{ArtifactID: "report", Parts: []a2a.Part{{Kind: "text", Text: "The full report."}}}}}},
			},
			wantTexts: []string{consolidatedPrefix},
		},
		{
			name:        "a final's own text is the answer, for a caller that saw the stream",
			statusTopic: "status/topic",
			events:      []ResponseEvent{textArtifactUpdate("a1", "streamed", false), finalStatus(a2a.TaskStateCompleted, a2a.Part{Kind: "text", Text: "answer"})},
			wantTexts:   []string{"answer"},
		},
		{
			name:      "a final's own text is the answer, for a caller without a status topic",
			events:    []ResponseEvent{textArtifactUpdate("a1", "streamed", false), finalStatus(a2a.TaskStateCompleted, a2a.Part{Kind: "text", Text: "answer"})},
			wantTexts: []string{"answer"},
		},
		{
			name:        "an input-required signal is left as it is",
			statusTopic: "status/topic",
			events:      []ResponseEvent{textArtifactUpdate("a1", "streamed", false), finalStatus(a2a.TaskStateInputNeeded, a2a.Part{Kind: "data", Data: map[string]any{"ask": "region"}})},
			wantTexts:   nil,
		},
		{
			name:      "an auth-required signal is left as it is, for a caller without a status topic",
			events:    []ResponseEvent{textArtifactUpdate("a1", "streamed", false), finalStatus(a2a.TaskStateAuthRequired, a2a.Part{Kind: "data", Data: map[string]any{"a2a_signal_type": "authentication_required"}})},
			wantTexts: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newBrokerProxy(t)
			brk := p.svc.Broker()
			q := subscribeReplyTo(t, brk)
			tc := &TaskContext{TaskID: "t1", SessionID: "s1", ReplyToTopic: "reply/topic", StatusTopic: tt.statusTopic}
			p.tasks.Add(tc)

			p.processResponseEvents(context.Background(), tc, responseEvents(tt.events...), newTestMonitor())

			task := receiveFinalTask(t, brk, q)
			var texts []string
			if task.Status.Message != nil {
				for _, part := range task.Status.Message.Parts {
					if part.Kind == "text" {
						texts = append(texts, part.Text)
					}
				}
			}
			if len(texts) != len(tt.wantTexts) {
				t.Fatalf("final text parts = %q, want %d starting %q", texts, len(tt.wantTexts), tt.wantTexts)
			}
			for i, want := range tt.wantTexts {
				if !strings.HasPrefix(texts[i], want) {
					t.Errorf("text part %d = %q, want it to start with %q", i, texts[i], want)
				}
			}
			if all := strings.Join(texts, "|"); strings.Count(all, "The full report.") > 1 {
				t.Errorf("final text %q repeats the report", all)
			}
		})
	}
}

func TestProcessResponseEvents_InterruptedTaskKeepsArtifacts(t *testing.T) {
	for _, state := range []a2a.TaskState{a2a.TaskStateRejected, a2a.TaskStateInputNeeded, a2a.TaskStateAuthRequired} {
		t.Run(string(state), func(t *testing.T) {
			p := newBrokerProxy(t)
			brk := p.svc.Broker()
			q := subscribeReplyTo(t, brk)
			tc := &TaskContext{TaskID: "t1", SessionID: "s1", ReplyToTopic: "reply/topic", StatusTopic: "status/topic"}
			p.tasks.Add(tc)

			p.processResponseEvents(context.Background(), tc, responseEvents(ResponseEvent{Task: &a2a.Task{ID: "d1", Kind: "task",
				Status:    a2a.TaskStatus{State: state, Message: &a2a.Message{Role: a2a.RoleAgent, Parts: []a2a.Part{{Kind: "text", Text: "Which region?"}}}},
				Artifacts: []a2a.Artifact{{ArtifactID: "plan", Name: "plan.json", Parts: []a2a.Part{{Kind: "data", Data: map[string]any{"steps": 3}}}}},
			}}), newTestMonitor())

			task := receiveFinalTask(t, brk, q)
			if task.Status.State != state || len(task.Artifacts) != 1 || task.Artifacts[0].ArtifactID != "plan" {
				t.Errorf("final task = %+v, want state %q with its artifact", task, state)
			}
			if p.tasks.Get("t1") != nil {
				t.Error("task still tracked after its final response")
			}
		})
	}
}

func TestProcessResponseEvents_EndOfTurnRemovesTheTask(t *testing.T) {
	type subscription struct{ queue, topic string }
	replyOnly := []subscription{{"test-q", "reply/topic"}}
	tests := []struct {
		name             string
		subscriptions    []subscription
		statusTopic      string
		userID           string
		events           []ResponseEvent
		wantDownstreamID string
	}{
		{
			name:             "a terminal task",
			subscriptions:    replyOnly,
			events:           []ResponseEvent{{Task: &a2a.Task{ID: "downstream-1", Kind: "task", Status: a2a.TaskStatus{State: a2a.TaskStateCompleted}}}},
			wantDownstreamID: "downstream-1",
		},
		{
			name:          "a downstream error",
			subscriptions: replyOnly,
			events:        []ResponseEvent{{Error: fmt.Errorf("downstream failure")}},
		},
		{
			name:          "a stream closed without a terminal task",
			subscriptions: replyOnly,
		},
		{
			name:          "a non-terminal task followed by a terminal one",
			subscriptions: []subscription{{"reply-q", "reply/topic"}},
			events: []ResponseEvent{
				{Task: &a2a.Task{ID: "downstream-1", Kind: "task", Status: a2a.TaskStatus{State: a2a.TaskStateWorking}}},
				{Task: &a2a.Task{ID: "downstream-1", Kind: "task", Status: a2a.TaskStatus{State: a2a.TaskStateCompleted}}},
			},
			wantDownstreamID: "downstream-1",
		},
		{
			name:          "a file artifact update followed by a terminal task",
			subscriptions: []subscription{{"status-q", "status/topic"}, {"reply-q", "reply/topic"}},
			statusTopic:   "status/topic",
			userID:        "u1",
			events: []ResponseEvent{
				{ArtifactUpdate: &a2a.TaskArtifactUpdateEvent{TaskID: "downstream-1", Kind: "artifact-update", Artifact: a2a.Artifact{ArtifactID: "a1",
					Parts: []a2a.Part{{Kind: "file", File: &a2a.FileContent{Bytes: base64.StdEncoding.EncodeToString([]byte("data")), Name: "file.bin", MIMEType: "application/octet-stream"}}}}}},
				{Task: &a2a.Task{ID: "downstream-1", Kind: "task", Status: a2a.TaskStatus{State: a2a.TaskStateCompleted}}},
			},
			wantDownstreamID: "downstream-1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newBrokerProxy(t)
			for _, s := range tt.subscriptions {
				subscribeTopic(t, p.svc.Broker(), s.queue, s.topic)
			}
			tc := &TaskContext{TaskID: "t1", SessionID: "s1", StatusTopic: tt.statusTopic, ReplyToTopic: "reply/topic", UserID: tt.userID}
			p.tasks.Add(tc)

			p.processResponseEvents(context.Background(), tc, responseEvents(tt.events...), newTestMonitor())

			if p.tasks.Get("t1") != nil {
				t.Error("processResponseEvents left task t1 tracked, want it removed")
			}
			if tc.DownstreamTaskID != tt.wantDownstreamID {
				t.Errorf("DownstreamTaskID = %q, want %q", tc.DownstreamTaskID, tt.wantDownstreamID)
			}
		})
	}
}

func TestProcessResponseEvents_StatusUpdate(t *testing.T) {
	p := newBrokerProxy(t)
	p.cfg.ConvertProgressUpdates = false
	ctx := context.Background()
	brk := p.svc.Broker()
	statusQ := subscribeTopic(t, brk, "status-q", "status/topic")
	subscribeTopic(t, brk, "reply-q", "reply/topic")
	tc := &TaskContext{TaskID: "t1", SessionID: "s1", StatusTopic: "status/topic", ReplyToTopic: "reply/topic"}
	p.tasks.Add(tc)

	p.processResponseEvents(ctx, tc, responseEvents(
		ResponseEvent{StatusUpdate: &a2a.TaskStatusUpdateEvent{TaskID: "downstream-1", Kind: "status-update",
			Status: a2a.TaskStatus{State: a2a.TaskStateWorking, Message: &a2a.Message{Parts: []a2a.Part{{Kind: "text", Text: "Working..."}}}}}},
		ResponseEvent{Task: &a2a.Task{ID: "downstream-1", Kind: "task", Status: a2a.TaskStatus{State: a2a.TaskStateCompleted}}},
	), newTestMonitor())

	var received a2a.TaskStatusUpdateEvent
	decodeStatusEnvelope(t, receiveMessage(ctx, t, brk, statusQ).Payload, "t1", &received)
	if got := received.Status.Message.Parts; len(got) != 1 || got[0].Kind != "text" || got[0].Text != "Working..." {
		t.Errorf("published status parts = %+v, want the text part unconverted", got)
	}
}

func TestProcessResponseEvents_ConvertProgressUpdates(t *testing.T) {
	tests := []struct {
		name     string
		event    ResponseEvent
		wantKind string
		wantText string
	}{
		{
			name: "a status update's text becomes a progress data part",
			event: ResponseEvent{StatusUpdate: &a2a.TaskStatusUpdateEvent{TaskID: "downstream-id", Kind: "status-update",
				Status: a2a.TaskStatus{State: a2a.TaskStateWorking, Message: &a2a.Message{Parts: []a2a.Part{{Kind: "text", Text: "Processing step 1..."}}}}}},
			wantKind: "data",
		},
		{
			name: "a text-only artifact stays the answer's text",
			event: ResponseEvent{ArtifactUpdate: &a2a.TaskArtifactUpdateEvent{TaskID: "downstream-id", Kind: "artifact-update",
				Artifact: a2a.Artifact{ArtifactID: "a1", Parts: []a2a.Part{{Kind: "text", Text: "The sum of 2 and 3 is 5."}}}}},
			wantKind: "text",
			wantText: "The sum of 2 and 3 is 5.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newBrokerProxy(t)
			p.cfg.ConvertProgressUpdates = true
			ctx := context.Background()
			brk := p.svc.Broker()
			q := subscribeTopic(t, brk, "status-q", "status/topic")
			tc := &TaskContext{TaskID: "t1", SessionID: "s1", StatusTopic: "status/topic"}
			p.tasks.Add(tc)

			p.processResponseEvents(ctx, tc, responseEvents(tt.event), newTestMonitor())

			var received a2a.TaskStatusUpdateEvent
			decodeStatusEnvelope(t, receiveMessage(ctx, t, brk, q).Payload, "t1", &received)
			if len(received.Status.Message.Parts) == 0 {
				t.Fatal("published status update has no parts")
			}
			if got := received.Status.Message.Parts[0]; got.Kind != tt.wantKind || got.Text != tt.wantText {
				t.Errorf("first part = %s %q, want %s %q", got.Kind, got.Text, tt.wantKind, tt.wantText)
			}
		})
	}
}

func assertSingleTaskReply(ctx context.Context, t *testing.T, brk broker.Broker, q broker.Queue) {
	t.Helper()
	var resp a2a.Response
	if err := json.Unmarshal(receiveMessage(ctx, t, brk, q).Payload, &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("first reply was an error: code=%d msg=%q", resp.Error.Code, resp.Error.Message)
	}
	shortCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	if extra, _ := brk.Receive(shortCtx, q); extra != nil {
		t.Fatalf("proxy published a spurious second reply after the final response: %s", string(extra.Payload))
	}
}

func TestProcessResponseEvents_FinalStatusUpdateNoSpuriousError(t *testing.T) {
	p := newBrokerProxy(t)
	ctx := context.Background()
	brk := p.svc.Broker()
	replyQ := subscribeTopic(t, brk, "reply-q", "reply/topic")
	tc := &TaskContext{TaskID: "t1", SessionID: "s1", StatusTopic: "status/topic", ReplyToTopic: "reply/topic", UserID: "u1", IsStreaming: true}
	p.tasks.Add(tc)

	p.processResponseEvents(ctx, tc, responseEvents(ResponseEvent{StatusUpdate: &a2a.TaskStatusUpdateEvent{
		TaskID: "downstream-1", Kind: "status-update", Final: true, Status: a2a.TaskStatus{State: a2a.TaskStateCompleted},
	}}), newTestMonitor())

	assertSingleTaskReply(ctx, t, brk, replyQ)
	if got := p.tasks.Get("t1"); got != nil {
		t.Error("task was not removed after final status-update")
	}
}

func TestProcessResponseEvents_FinalFailedStatusUpdateNoSpuriousError(t *testing.T) {
	p := newBrokerProxy(t)
	ctx := t.Context()
	brk := p.svc.Broker()
	replyQ := subscribeTopic(t, brk, "reply-q", "reply/topic")
	tc := &TaskContext{TaskID: "t1", SessionID: "s1", StatusTopic: "status/topic", ReplyToTopic: "reply/topic", UserID: "u1", IsStreaming: true}
	p.tasks.Add(tc)

	p.processResponseEvents(ctx, tc, responseEvents(ResponseEvent{StatusUpdate: &a2a.TaskStatusUpdateEvent{
		TaskID: "downstream-1", Kind: "status-update", Final: true, Status: a2a.TaskStatus{State: a2a.TaskStateFailed},
	}}), newTestMonitor())

	assertSingleTaskReply(ctx, t, brk, replyQ)
	if got := p.tasks.Get("t1"); got != nil {
		t.Error("task was not removed after final failed status-update")
	}
}

func TestPublishErrorResponse(t *testing.T) {
	p := newBrokerProxy(t)
	ctx := context.Background()
	brk := p.svc.Broker()
	q := subscribeReplyTo(t, brk)
	tc := &TaskContext{TaskID: "t1", SessionID: "s1", ReplyToTopic: "reply/topic", ClientID: "c1", UserID: "u1"}

	p.publishErrorResponse(ctx, tc, -32603, "something went wrong")

	var resp a2a.Response
	if err := json.Unmarshal(receiveMessage(ctx, t, brk, q).Payload, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Error == nil {
		t.Fatal("published response has no error")
	}
	if resp.Error.Code != -32603 || resp.Error.Message != "something went wrong" {
		t.Errorf("error = %d %q, want -32603 %q", resp.Error.Code, resp.Error.Message, "something went wrong")
	}
}

func TestPublishErrorResponse_NoReplyTopic(t *testing.T) {
	p := newBrokerProxy(t)
	tc := &TaskContext{TaskID: "t1", ReplyToTopic: ""}
	p.publishErrorResponse(context.Background(), tc, -32603, "error")
}

func TestPublishFinalResponse(t *testing.T) {
	p := newBrokerProxy(t)
	ctx := context.Background()
	brk := p.svc.Broker()
	q := subscribeTopic(t, brk, "test-q", "reply/final")
	tc := &TaskContext{TaskID: "t1", SessionID: "s1", ReplyToTopic: "reply/final", ClientID: "c1"}

	p.publishFinalResponse(ctx, tc, &a2a.Task{ID: "t1", Kind: "task", Status: a2a.TaskStatus{State: a2a.TaskStateCompleted}})

	if got := receiveMessage(ctx, t, brk, q).UserProperties["delegating_agent_name"]; got != "test-proxy" {
		t.Errorf("delegating_agent_name = %q, want %q", got, "test-proxy")
	}
}

func TestPublishFinalResponse_NoReplyTopic(t *testing.T) {
	p := newBrokerProxy(t)
	tc := &TaskContext{TaskID: "t1", ReplyToTopic: ""}
	task := &a2a.Task{ID: "t1", Kind: "task", Status: a2a.TaskStatus{State: a2a.TaskStateCompleted}}
	p.publishFinalResponse(context.Background(), tc, task)
}

func TestHandleFinalTask(t *testing.T) {
	tests := []struct {
		name                  string
		replyTo               string
		userID                string
		task                  *a2a.Task
		wantProducedArtifacts bool
	}{
		{
			name:    "the ids are remapped to the mesh task",
			replyTo: "reply/final",
			userID:  "u1",
			task: &a2a.Task{ID: "downstream-id", Kind: "task", Status: a2a.TaskStatus{State: a2a.TaskStateCompleted,
				Message: &a2a.Message{MessageID: "msg-1", Role: "agent", Parts: []a2a.Part{{Kind: "text", Text: "Done"}}}}},
		},
		{
			name:    "a file artifact is listed as produced",
			replyTo: "reply/topic",
			userID:  "u1",
			task: &a2a.Task{ID: "downstream-id", Kind: "task",
				Status: a2a.TaskStatus{State: a2a.TaskStateCompleted,
					Message: &a2a.Message{MessageID: "msg-1", Role: "agent", Parts: []a2a.Part{{Kind: "text", Text: "Done"}}}},
				Artifacts: []a2a.Artifact{{ArtifactID: "a1", Parts: []a2a.Part{{Kind: "file", File: &a2a.FileContent{
					Bytes: base64.StdEncoding.EncodeToString([]byte("artifact data")), Name: "result.txt", MIMEType: "text/plain"}}}}},
			},
			wantProducedArtifacts: true,
		},
		{
			name:    "a message without an id gets one",
			replyTo: "reply/topic",
			task: &a2a.Task{ID: "downstream-id", Kind: "task", Status: a2a.TaskStatus{State: a2a.TaskStateCompleted,
				Message: &a2a.Message{Role: "agent", Parts: []a2a.Part{{Kind: "text", Text: "Done"}}}}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newBrokerProxy(t)
			subscribeTopic(t, p.svc.Broker(), "test-q", tt.replyTo)
			tc := &TaskContext{TaskID: "t1", SessionID: "s1", ReplyToTopic: tt.replyTo, UserID: tt.userID}

			p.handleFinalTask(context.Background(), tc, tt.task)

			task := tt.task
			if task.ID != "t1" || task.ContextID != "s1" {
				t.Errorf("task ids = %q, %q, want t1, s1", task.ID, task.ContextID)
			}
			if task.Metadata["agent_name"] != "test-proxy" {
				t.Errorf("agent_name = %v, want test-proxy", task.Metadata["agent_name"])
			}
			if got := task.Metadata["produced_artifacts"] != nil; got != tt.wantProducedArtifacts {
				t.Errorf("produced_artifacts present = %v, want %v", got, tt.wantProducedArtifacts)
			}
			if msg := task.Status.Message; msg.MessageID == "" || msg.TaskID != "t1" || msg.ContextID != "s1" {
				t.Errorf("message ids = %q, %q, %q, want a message id, t1, s1", msg.MessageID, msg.TaskID, msg.ContextID)
			}
		})
	}
}

func TestPublishStatusUpdate_RemapsTheTaskAndKeepsTheText(t *testing.T) {
	tests := []struct {
		name string
		text string
	}{
		{"the update is published under the mesh task id", "Working..."},
		{"with progress conversion off the text stays text", "Still working..."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newBrokerProxy(t)
			p.cfg.ConvertProgressUpdates = false
			ctx := context.Background()
			brk := p.svc.Broker()
			q := subscribeTopic(t, brk, "test-q", "status/topic")
			tc := &TaskContext{TaskID: "t1", SessionID: "s1", StatusTopic: "status/topic"}

			p.publishStatusUpdate(ctx, tc, &a2a.TaskStatusUpdateEvent{TaskID: "downstream-id", Kind: "status-update",
				Status: a2a.TaskStatus{State: a2a.TaskStateWorking, Message: &a2a.Message{Parts: []a2a.Part{{Kind: "text", Text: tt.text}}}}})

			var received a2a.TaskStatusUpdateEvent
			decodeStatusEnvelope(t, receiveMessage(ctx, t, brk, q).Payload, "t1", &received)
			if received.TaskID != "t1" {
				t.Errorf("TaskID = %q, want %q", received.TaskID, "t1")
			}
			if got := received.Status.Message.Parts; len(got) != 1 || got[0].Kind != "text" || got[0].Text != tt.text {
				t.Errorf("parts = %+v, want one text part %q", got, tt.text)
			}
		})
	}
}

func TestHandleStatusUpdate_Final(t *testing.T) {
	p := newBrokerProxy(t)
	ctx := context.Background()
	brk := p.svc.Broker()
	q := subscribeReplyTo(t, brk)
	tc := &TaskContext{TaskID: "t1", SessionID: "s1", StatusTopic: "status/topic", ReplyToTopic: "reply/topic"}
	p.tasks.Add(tc)

	p.publishStatusUpdate(ctx, tc, &a2a.TaskStatusUpdateEvent{TaskID: "downstream-id", Kind: "status-update", Final: true,
		Status: a2a.TaskStatus{State: a2a.TaskStateCompleted, Message: &a2a.Message{MessageID: "msg-1", Role: "agent", Parts: []a2a.Part{{Kind: "text", Text: "Final"}}}}})

	if p.tasks.Get("t1") != nil {
		t.Error("task should be removed after final status update")
	}
	receiveMessage(ctx, t, brk, q)
}

func TestHandleArtifactUpdate_TextOnlyConvertedToStatus(t *testing.T) {
	p := newBrokerProxy(t)
	ctx := context.Background()
	brk := p.svc.Broker()
	q := subscribeTopic(t, brk, "test-q", "status/topic")
	tc := &TaskContext{TaskID: "t1", SessionID: "s1", StatusTopic: "status/topic"}

	p.handleArtifactUpdate(ctx, tc, &a2a.TaskArtifactUpdateEvent{TaskID: "downstream-id", Kind: "artifact-update",
		Artifact: a2a.Artifact{ArtifactID: "a1", Parts: []a2a.Part{{Kind: "text", Text: "Text artifact"}}}})

	var su a2a.TaskStatusUpdateEvent
	decodeStatusEnvelope(t, receiveMessage(ctx, t, brk, q).Payload, "t1", &su)
	if su.TaskID != "t1" {
		t.Errorf("TaskID = %q, want %q", su.TaskID, "t1")
	}
}

func TestHandleArtifactUpdate_FileArtifactPublished(t *testing.T) {
	p := newBrokerProxy(t)
	ctx := context.Background()
	brk := p.svc.Broker()
	q := subscribeTopic(t, brk, "test-q", "status/topic")
	tc := &TaskContext{TaskID: "t1", SessionID: "s1", StatusTopic: "status/topic", UserID: "u1"}

	p.handleArtifactUpdate(ctx, tc, &a2a.TaskArtifactUpdateEvent{TaskID: "downstream-id", Kind: "artifact-update",
		Artifact: a2a.Artifact{ArtifactID: "a1", Parts: []a2a.Part{{Kind: "file", File: &a2a.FileContent{
			Bytes: base64.StdEncoding.EncodeToString([]byte("file data")), Name: "test.bin", MIMEType: "application/octet-stream"}}}}})

	var received a2a.TaskArtifactUpdateEvent
	decodeStatusEnvelope(t, receiveMessage(ctx, t, brk, q).Payload, "t1", &received)
	if received.TaskID != "t1" {
		t.Errorf("TaskID = %q, want %q", received.TaskID, "t1")
	}
}

func TestPublishAuthRequired(t *testing.T) {
	p := newBrokerProxy(t)
	ctx := context.Background()
	brk := p.svc.Broker()
	q := subscribeTopic(t, brk, "test-q", "status/topic")
	tc := &TaskContext{TaskID: "t1", SessionID: "s1", StatusTopic: "status/topic", UserID: "u1", ClientID: "c1"}

	p.publishAuthRequired(ctx, tc, &a2a.AuthenticationRequiredData{
		Type:        string(a2a.SignalAuthenticationRequired),
		TargetAgent: "test-proxy",
		AuthURI:     "https://auth.example.com/authorize?state=xyz",
	})

	var su a2a.TaskStatusUpdateEvent
	decodeStatusEnvelope(t, receiveMessage(ctx, t, brk, q).Payload, "t1", &su)
	if su.Status.State != a2a.TaskStateInputNeeded {
		t.Errorf("state = %q, want %q", su.Status.State, a2a.TaskStateInputNeeded)
	}
}

func TestPublishAuthRequired_NoStatusTopic(t *testing.T) {
	p := newBrokerProxy(t)
	tc := &TaskContext{TaskID: "t1", StatusTopic: ""}
	p.publishAuthRequired(context.Background(), tc, &a2a.AuthenticationRequiredData{Type: "test"})
}

func decodeStatusEnvelope(t *testing.T, payload []byte, taskID string, out any) {
	t.Helper()
	var envelope struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      string          `json:"id"`
		Result  json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	if envelope.JSONRPC != "2.0" || envelope.ID != taskID || len(envelope.Result) == 0 {
		t.Fatalf("status payload is not a JSON-RPC response for task %q: %s", taskID, payload)
	}
	if err := json.Unmarshal(envelope.Result, out); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
}

type publishRecordingBroker struct {
	*broker.DevBroker
	mu     sync.Mutex
	topics []string
}

func (b *publishRecordingBroker) Publish(ctx context.Context, topic string, msg *broker.Message) error {
	b.mu.Lock()
	b.topics = append(b.topics, topic)
	b.mu.Unlock()
	return b.DevBroker.Publish(ctx, topic, msg)
}

func (b *publishRecordingBroker) published() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.topics)
}

func newPublishRecordingProxy(t *testing.T) (*ProxyInstance, *publishRecordingBroker) {
	t.Helper()
	p := newBrokerProxy(t)
	rec := &publishRecordingBroker{DevBroker: p.svc.Broker().(*broker.DevBroker)}
	p.svc.(*mockServices).brk = rec
	return p, rec
}

func TestHandleStatusUpdate_NoStatusTopic(t *testing.T) {
	p, rec := newPublishRecordingProxy(t)
	tc := &TaskContext{TaskID: "t1", ReplyToTopic: "reply/topic"}
	p.tasks.Add(tc)

	p.publishStatusUpdate(context.Background(), tc, &a2a.TaskStatusUpdateEvent{Status: a2a.TaskStatus{State: a2a.TaskStateWorking}})
	p.handleArtifactUpdate(context.Background(), tc, &a2a.TaskArtifactUpdateEvent{
		Artifact: a2a.Artifact{ArtifactID: "a1", Parts: []a2a.Part{{Kind: "data", Data: map[string]any{"rows": 1}}}},
	})
	p.handleArtifactUpdate(context.Background(), tc, &a2a.TaskArtifactUpdateEvent{
		Artifact: a2a.Artifact{ArtifactID: "a2", Parts: []a2a.Part{{Kind: "text", Text: "partial answer"}}},
	})

	if got := rec.published(); len(got) != 0 {
		t.Errorf("published to %q, want nothing for a caller without a status topic", got)
	}
	if len(tc.streamedArtifactText) != 1 || tc.streamedArtifactText[0] != "partial answer" {
		t.Errorf("streamed artifact text = %q, want it kept for the final response", tc.streamedArtifactText)
	}
}

func receiveFinalTask(t *testing.T, brk broker.Broker, q broker.Queue) *a2a.Task {
	t.Helper()
	rctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	msg, err := brk.Receive(rctx, q)
	if err != nil || msg == nil {
		t.Fatalf("no final response published to replyTo: %v", err)
	}
	var resp struct {
		Result a2a.Task `json:"result"`
	}
	if err := json.Unmarshal(msg.Payload, &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return &resp.Result
}

func textArtifactUpdate(id, text string, appendChunk bool) ResponseEvent {
	return ResponseEvent{ArtifactUpdate: &a2a.TaskArtifactUpdateEvent{TaskID: "d1", Append: appendChunk,
		Artifact: a2a.Artifact{ArtifactID: id, Parts: []a2a.Part{{Kind: "text", Text: text}}}}}
}

func finalStatus(state a2a.TaskState, parts ...a2a.Part) ResponseEvent {
	status := a2a.TaskStatus{State: state}
	if len(parts) > 0 {
		status.Message = &a2a.Message{Role: a2a.RoleAgent, Parts: parts}
	}
	return ResponseEvent{StatusUpdate: &a2a.TaskStatusUpdateEvent{TaskID: "d1", Final: true, Status: status}}
}

func TestHandleArtifactUpdate_StoresFileWithoutStatusTopic(t *testing.T) {
	p, rec := newPublishRecordingProxy(t)
	tc := &TaskContext{TaskID: "t1", SessionID: "s1", UserID: "u1", ReplyToTopic: "reply/topic"}
	au := &a2a.TaskArtifactUpdateEvent{Artifact: a2a.Artifact{ArtifactID: "a1", Parts: []a2a.Part{{Kind: "file",
		File: &a2a.FileContent{Name: "report.csv", MIMEType: "text/csv", Bytes: base64.StdEncoding.EncodeToString([]byte("a,b\n1,2\n"))}}}}}

	p.handleArtifactUpdate(context.Background(), tc, au)

	if f := au.Artifact.Parts[0].File; f.Bytes != "" || !strings.HasPrefix(f.URI, "artifact://") {
		t.Errorf("file part = %+v, want it stored and referenced by an artifact:// uri", f)
	}
	if got := rec.published(); len(got) != 0 {
		t.Errorf("published to %q, want nothing", got)
	}
}

func TestFetchAndPublishCard(t *testing.T) {
	p := newBrokerProxy(t)
	ctx := context.Background()
	brk := p.svc.Broker()
	p.adapter = &mockAdapter{card: &a2a.AgentCard{Name: "downstream-agent", Description: "A downstream agent"}}
	q := subscribeTopic(t, brk, "test-q", a2a.DiscoveryAgentCardsTopic("test-ns"))

	if fetchErr := p.fetchAndPublishCard(ctx); fetchErr != nil {
		t.Fatalf("fetchAndPublishCard: %v", fetchErr)
	}

	cached := p.CachedCard()
	if cached == nil {
		t.Fatal("CachedCard() = nil after fetchAndPublishCard, want the rewritten card")
	}
	if cached.Name != "test-proxy" {
		t.Errorf("cached card name = %q, want %q", cached.Name, "test-proxy")
	}
	receiveMessage(ctx, t, brk, q)
}

func TestFetchAndPublishCard_NilCard(t *testing.T) {
	p := newBrokerProxy(t)
	p.adapter = &mockAdapter{card: nil}

	if err := p.fetchAndPublishCard(context.Background()); err == nil {
		t.Fatal("expected error for nil card")
	}
}

func TestPublishCard(t *testing.T) {
	p := newBrokerProxy(t)
	ctx := context.Background()
	brk := p.svc.Broker()
	q := subscribeTopic(t, brk, "test-q", a2a.DiscoveryAgentCardsTopic("test-ns"))

	if pubErr := p.publishCard(ctx); pubErr == nil {
		t.Fatal("expected error when no card cached")
	}

	p.cachedCard.Store(&a2a.AgentCard{Name: "test-proxy", Description: "test"})
	if pubErr := p.publishCard(ctx); pubErr != nil {
		t.Fatalf("publishCard: %v", pubErr)
	}
	receiveMessage(ctx, t, brk, q)
}

func TestRegisterDefaultAdapter(t *testing.T) {
	saved := newDefaultAdapter
	defer func() { newDefaultAdapter = saved }()

	called := false
	RegisterDefaultAdapter(func(_ *ProxyConfig, _ *slog.Logger) (ProtocolAdapter, error) {
		called = true
		return &mockAdapter{}, nil
	})

	if newDefaultAdapter == nil {
		t.Fatal("expected newDefaultAdapter to be set")
	}

	_, _ = newDefaultAdapter(&ProxyConfig{}, slog.Default())
	if !called {
		t.Error("factory should have been called")
	}
}

type errorAdapter struct {
	err error
}

func (a *errorAdapter) FetchAgentCard(_ context.Context) (*a2a.AgentCard, error) {
	return nil, a.err
}

func (a *errorAdapter) ForwardRequest(_ context.Context, _ *TaskContext, _ *a2a.Request) (<-chan ResponseEvent, error) {
	return nil, a.err
}

func (a *errorAdapter) CancelTask(_ context.Context, _ *TaskContext) error {
	return a.err
}

func (a *errorAdapter) Close() error {
	return nil
}

type completedTaskAdapter struct {
	taskID string
}

func (a *completedTaskAdapter) FetchAgentCard(_ context.Context) (*a2a.AgentCard, error) {
	return nil, nil
}

func (a *completedTaskAdapter) ForwardRequest(_ context.Context, _ *TaskContext, _ *a2a.Request) (<-chan ResponseEvent, error) {
	ch := make(chan ResponseEvent, 1)
	ch <- ResponseEvent{
		Task: &a2a.Task{
			ID:   a.taskID,
			Kind: "task",
			Status: a2a.TaskStatus{
				State: a2a.TaskStateCompleted,
				Message: &a2a.Message{
					MessageID: "msg-1",
					Role:      "agent",
					Parts:     []a2a.Part{{Kind: "text", Text: "Done"}},
				},
			},
		},
	}
	close(ch)
	return ch, nil
}

func (a *completedTaskAdapter) CancelTask(_ context.Context, _ *TaskContext) error {
	return nil
}

func (a *completedTaskAdapter) Close() error {
	return nil
}

func TestStart_Success(t *testing.T) {
	p := newBrokerProxy(t)
	p.adapter = &mockAdapter{card: &a2a.AgentCard{Name: "downstream", Description: "test agent"}}
	p.cfg.DiscoveryIntervalSeconds = 0
	p.cfg.TaskCleanupIntervalMinutes = 0

	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	<-p.started
	defer p.Stop(context.Background()) //nolint:errcheck // test cleanup

	if p.CachedCard() == nil {
		t.Error("expected cached card after Start")
	}
	if p.Health() != nil {
		t.Error("expected healthy after successful Start")
	}
}

func TestStart_ClearsHealth(t *testing.T) {
	p := newBrokerProxy(t)
	p.adapter = &mockAdapter{card: &a2a.AgentCard{Name: "downstream", Description: "test agent"}}
	p.cfg.DiscoveryIntervalSeconds = 0
	p.cfg.TaskCleanupIntervalMinutes = 0
	p.health.Set("queue:leftover", health.Fatal, "queue no longer exists")

	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	<-p.started
	defer p.Stop(context.Background()) //nolint:errcheck // test cleanup

	if problems := p.health.Snapshot(); problems != nil {
		t.Errorf("health.Snapshot() after Start = %v, want nil", problems)
	}
}

func TestStart_CardFetchFailure(t *testing.T) {
	p := newBrokerProxy(t)
	p.adapter = &errorAdapter{err: fmt.Errorf("connection refused")}
	p.cfg.DiscoveryIntervalSeconds = 0
	p.cfg.TaskCleanupIntervalMinutes = 0

	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("Start should not fail on card fetch error: %v", err)
	}
	<-p.started
	defer p.Stop(context.Background()) //nolint:errcheck // test cleanup

	if p.Health() == nil {
		t.Error("expected unhealthy when card fetch failed")
	}
}

func TestStartCleanupTimer(t *testing.T) {
	p := newBrokerProxy(t)
	p.cfg.TaskCleanupIntervalMinutes = 0
	ctx, cancel := context.WithCancel(t.Context())
	p.startCleanupTimer(ctx)
	cancel()
}

func TestStartDiscoveryTimer_Disabled(t *testing.T) {
	p := newBrokerProxy(t)
	p.cfg.DiscoveryIntervalSeconds = 0
	ctx, cancel := context.WithCancel(t.Context())
	p.startDiscoveryTimer(ctx)
	cancel()
}

func TestStartCleanupTimer_Runs(t *testing.T) {
	p := newBrokerProxy(t)
	p.cfg.TaskCleanupIntervalMinutes = 1
	p.cfg.TaskStateTTLMinutes = 0
	ctx, cancel := context.WithCancel(t.Context())
	p.tasks.Add(&TaskContext{TaskID: "stale-1"})
	p.startCleanupTimer(ctx)
	cancel()
}

func TestStartDiscoveryTimer_Runs(t *testing.T) {
	p := newBrokerProxy(t)
	p.cfg.DiscoveryIntervalSeconds = 1
	p.adapter = &mockAdapter{card: &a2a.AgentCard{Name: "test"}}

	ctx, cancel := context.WithCancel(t.Context())
	p.startDiscoveryTimer(ctx)
	cancel()
}

func TestRewriteCard_DisplayNameUpdate(t *testing.T) {
	p := newTestProxy(&ProxyConfig{
		Name:        "my-proxy",
		Namespace:   "test-ns",
		DisplayName: "Updated Display",
	})

	card := &a2a.AgentCard{
		Capabilities: a2a.AgentCapabilities{
			Extensions: []a2a.AgentExtension{
				{
					URI:    "https://solace.com/a2a/extensions/display-name",
					Params: map[string]any{"display_name": "Old Display"},
				},
			},
		},
	}

	p.rewriteCard(card)

	for _, ext := range card.Capabilities.Extensions {
		if ext.URI == "https://solace.com/a2a/extensions/display-name" {
			if ext.Params["display_name"] != "Updated Display" {
				t.Errorf("display_name = %v, want %q", ext.Params["display_name"], "Updated Display")
			}
			return
		}
	}
	t.Error("display-name extension not found")
}

type recordingAdapter struct {
	events <-chan ResponseEvent
	err    error
}

func (a *recordingAdapter) FetchAgentCard(context.Context) (*a2a.AgentCard, error) {
	return nil, nil
}
func (a *recordingAdapter) ForwardRequest(context.Context, *TaskContext, *a2a.Request) (<-chan ResponseEvent, error) {
	return a.events, a.err
}
func (a *recordingAdapter) CancelTask(context.Context, *TaskContext) error { return nil }
func (a *recordingAdapter) Close() error                                   { return nil }

func newProxyWithRecorder(t *testing.T) (*ProxyInstance, *observability.RecordingMetrics) {
	t.Helper()
	pc := &ProxyConfig{
		Name:      "test-agent",
		Namespace: "test-ns",
	}
	p := newTestProxy(pc)
	rec := observability.NewRecordingMetrics()
	runtime.Init(rec)
	t.Cleanup(func() { runtime.Init(runtime.NoopMetrics{}) })
	p.svc = &mockServices{
		brk:     broker.NewDevBroker(),
		metrics: rec,
	}
	return p, rec
}

type failingReceiveBroker struct {
	queueName string
	err       error
	calls     atomic.Int64
}

func (f *failingReceiveBroker) Connect(context.Context) error    { return nil }
func (f *failingReceiveBroker) Disconnect(context.Context) error { return nil }
func (f *failingReceiveBroker) IsConnected() bool                { return true }
func (f *failingReceiveBroker) CreateQueue(string, broker.QueueOptions) (broker.Queue, error) {
	return &broker.SimpleQueue{QName: f.queueName}, nil
}
func (f *failingReceiveBroker) CreateTemporaryQueue() (broker.Queue, error) { return nil, nil }
func (f *failingReceiveBroker) RequestReplyInbox(context.Context, string, *broker.Message, time.Duration) (*broker.Message, error) {
	return nil, nil
}
func (f *failingReceiveBroker) CreateDirectSubscription() (broker.Queue, error) {
	return &broker.SimpleQueue{QName: "direct", DirectSubscription: true}, nil
}
func (f *failingReceiveBroker) DeleteQueue(string) error               { return nil }
func (f *failingReceiveBroker) DeprovisionQueue(string) error          { return nil }
func (f *failingReceiveBroker) Subscribe(broker.Queue, string) error   { return nil }
func (f *failingReceiveBroker) Unsubscribe(broker.Queue, string) error { return nil }
func (f *failingReceiveBroker) Publish(context.Context, string, *broker.Message) error {
	return nil
}
func (f *failingReceiveBroker) PublishGuaranteed(context.Context, string, *broker.Message) error {
	return nil
}
func (f *failingReceiveBroker) Receive(ctx context.Context, _ broker.Queue) (*broker.Message, error) {
	f.calls.Add(1)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, f.err
}
func (f *failingReceiveBroker) Request(context.Context, string, *broker.Message, time.Duration) (*broker.Message, error) {
	return nil, errors.New("not implemented")
}

func TestProxyReceiveLoop_ExitsOnQueueNotFound(t *testing.T) {
	t.Parallel()

	b := &failingReceiveBroker{queueName: "missing", err: fmt.Errorf("devbroker: queue %q: %w", "missing", broker.ErrQueueNotFound)}
	p := newTestProxy(&ProxyConfig{Name: "test", Namespace: "ns"})
	p.svc = &mockServices{brk: b}
	p.queue = &broker.SimpleQueue{QName: "missing"}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		p.receiveLoop(ctx, p.queue)
	}()

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("receive loop did not exit on ErrQueueNotFound within 500ms (after %d attempts); it is retrying a terminal error", b.calls.Load())
	}

	if got := b.calls.Load(); got > 2 {
		t.Errorf("Receive attempts on ErrQueueNotFound = %d, want at most 2", got)
	}
}

func TestProxyReceiveLoop_BackoffOnError(t *testing.T) {
	t.Parallel()

	fb := &failingReceiveBroker{queueName: "fail", err: errors.New("simulated: No More Non-Durable Queue or Topic Endpoint")}
	p := newTestProxy(&ProxyConfig{Name: "test", Namespace: "ns"})
	p.svc = &mockServices{brk: fb}
	p.queue = &broker.SimpleQueue{QName: "fail"}

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		p.receiveLoop(ctx, p.queue)
	}()

	<-ctx.Done()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("receiveLoop did not return after context cancel (it may be sleeping too long or ignoring ctx)")
	}

	calls := fb.calls.Load()
	const spinThreshold = 10
	if calls > spinThreshold {
		t.Fatalf("receive loop appears to be spinning: %d Receive() calls in 300ms; expected ≤ %d (exponential backoff)",
			calls, spinThreshold)
	}
	if calls == 0 {
		t.Fatal("receive loop did not call Broker.Receive() at all")
	}
}

func startRetryProxy(t *testing.T, interval int, err error) (*ProxyInstance, *switchableAdapter) {
	t.Helper()
	p := newBrokerProxy(t)
	adapter := &switchableAdapter{}
	adapter.set(verifiedRemoteCard(), err)
	p.adapter = adapter
	p.cfg.DiscoveryIntervalSeconds = interval
	p.cfg.TaskCleanupIntervalMinutes = 0
	p.cardRetryInterval = 10 * time.Millisecond
	if startErr := p.Start(t.Context()); startErr != nil {
		t.Fatalf("Start: %v", startErr)
	}
	<-p.started
	t.Cleanup(func() { _ = p.Stop(context.Background()) })
	return p, adapter
}

func fetchesSettle(t *testing.T, a *switchableAdapter) int32 {
	t.Helper()
	time.Sleep(150 * time.Millisecond)
	before := a.fetches.Load()
	time.Sleep(150 * time.Millisecond)
	if after := a.fetches.Load(); after != before {
		t.Fatalf("card fetches still running: %d then %d", before, after)
	}
	return before
}

func TestCardRetry_RunsUntilAFetchSucceedsThenStops(t *testing.T) {
	_, adapter := startRetryProxy(t, 0, errors.New("fetch agent card: network connection failed"))
	waitFor(t, func() bool { return adapter.fetches.Load() >= 3 })
	adapter.set(verifiedRemoteCard(), nil)
	waitFor(t, func() bool { return adapter.fetches.Load() >= 4 })
	fetchesSettle(t, adapter)
}

func TestCardRetry_StopsWithTheProxy(t *testing.T) {
	p, adapter := startRetryProxy(t, 0, errors.New("fetch agent card: network connection failed"))
	waitFor(t, func() bool { return adapter.fetches.Load() >= 2 })
	if err := p.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	fetchesSettle(t, adapter)
}

func TestCardRetry_NotStartedWhenUnneeded(t *testing.T) {
	t.Run("first fetch succeeded", func(t *testing.T) {
		_, adapter := startRetryProxy(t, 0, nil)
		if got := fetchesSettle(t, adapter); got != 1 {
			t.Errorf("card fetches = %d, want only the one at start", got)
		}
	})
	t.Run("the discovery timer is on", func(t *testing.T) {
		_, adapter := startRetryProxy(t, 60, errors.New("fetch agent card: network connection failed"))
		time.Sleep(150 * time.Millisecond)
		if got := adapter.fetches.Load(); got != 1 {
			t.Errorf("card fetches = %d within the timer's first interval, want only the one at start", got)
		}
	})
}

func TestStart_ForgetsTheLastRunsRefusal(t *testing.T) {
	p, adapter := startRetryProxy(t, 0, fmt.Errorf("check card: %w", ErrAgentCardUntrusted))
	if p.forwardsTasks() {
		t.Fatal("forwardsTasks = true with the card refused")
	}
	if err := p.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	adapter.set(nil, errors.New("fetch agent card: network connection failed"))
	if err := p.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	<-p.started
	if !p.forwardsTasks() {
		t.Error("forwardsTasks = false after a restart whose fetch failed for another reason")
	}
	if got := remoteFetchDetail(p); got != string(health.DetailRemoteFetchFailed) {
		t.Errorf("health detail = %q, want the unreachable detail", got)
	}
}

func TestSetCardFetchFailed_RefusalOutlastsOtherFailures(t *testing.T) {
	p := newBrokerProxy(t)
	p.setCardFetchFailed(fmt.Errorf("check card: %w", ErrAgentCardUntrusted))
	for _, err := range []error{fmt.Errorf("check card: %w", ErrAgentCardKeysUnavailable), errors.New("fetch agent card: network connection failed")} {
		p.setCardFetchFailed(err)
		if got := remoteFetchDetail(p); got != string(health.DetailRemoteCardUntrusted) {
			t.Errorf("after %v: health detail = %q, want the untrusted-card detail", err, got)
		}
	}
}

type heldCardAdapter struct {
	*switchableAdapter
	release chan struct{}
}

func (a *heldCardAdapter) FetchAgentCard(ctx context.Context) (*a2a.AgentCard, error) {
	select {
	case <-a.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return a.switchableAdapter.FetchAgentCard(ctx)
}

func TestStart_ReturnsWhileTheFirstCardFetchHangsAndServesOnlyAfterIt(t *testing.T) {
	p := newBrokerProxy(t)
	inner := &switchableAdapter{}
	inner.set(verifiedRemoteCard(), nil)
	held := &heldCardAdapter{switchableAdapter: inner, release: make(chan struct{})}
	p.adapter = held
	p.cfg.TaskCleanupIntervalMinutes = 0

	returned := make(chan error, 1)
	go func() { returned <- p.Start(t.Context()) }()
	select {
	case err := <-returned:
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Start waited for the card fetch")
	}
	t.Cleanup(func() { _ = p.Stop(context.Background()) })
	select {
	case <-p.started:
		t.Fatal("the proxy started serving before its first card fetch ended")
	case <-time.After(100 * time.Millisecond):
	}

	request := []byte(`{"jsonrpc":"2.0","id":"held-task","method":"message/send","params":{"message":{"role":"user","messageId":"m1","parts":[{"kind":"text","text":"hi"}]}}}`)
	if err := p.svc.Broker().Publish(t.Context(), a2a.AgentRequestTopic("test-ns", "test-proxy"), &broker.Message{Payload: request}); err != nil {
		t.Fatalf("publish a task while the first fetch is held: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	if got := inner.forwarded.Load(); got != 0 {
		t.Fatalf("forwarded %d tasks before the card was checked, want 0", got)
	}

	close(held.release)
	<-p.started
	if p.CachedCard() == nil || p.Health() != nil {
		t.Fatalf("after the first fetch: card %v, health %v, want the card cached and healthy", p.CachedCard(), p.Health())
	}
	waitFor(t, func() bool { return inner.forwarded.Load() == 1 })
}

func TestStop_DuringTheFirstCardFetchStartsNothingAndRecordsNoFailure(t *testing.T) {
	p := newBrokerProxy(t)
	inner := &switchableAdapter{}
	inner.set(verifiedRemoteCard(), nil)
	held := &heldCardAdapter{switchableAdapter: inner, release: make(chan struct{})}
	p.adapter = held
	p.cfg.DiscoveryIntervalSeconds = 1
	p.cfg.TaskCleanupIntervalMinutes = 0
	p.cardRetryInterval = time.Millisecond
	if err := p.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if err := p.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	<-p.started
	time.Sleep(50 * time.Millisecond)

	if got := remoteFetchDetail(p); got != "" {
		t.Fatalf("health detail %q after a stop during the first fetch, want none: the cancelled fetch is not a failure of the remote agent", got)
	}
	if got := inner.fetches.Load(); got != 0 {
		t.Fatalf("fetched the card %d times after the stop, want 0: no timer starts once the proxy is stopped", got)
	}
}
