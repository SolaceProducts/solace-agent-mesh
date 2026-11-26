// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	"github.com/SolaceProducts/solace-agent-mesh/internal/identity"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func newAuthzTestComposer(t *testing.T) *rbac.Composer {
	t.Helper()
	yaml := rbac.YAMLState{
		Roles: rbac.RolesConfig{Roles: map[string]rbac.RoleDefinition{
			"sam_admin": {Scopes: []string{"*"}},
			"sam_user":  {Scopes: []string{"agent_builder:*:read"}},
		}},
		Users: rbac.UsersConfig{Users: map[string]rbac.UserAssignment{
			"alice@example.org": {Roles: []string{"sam_admin"}},
		}},
		DefaultRoles: []string{"sam_user"},
	}
	c := rbac.NewComposer(yaml, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := c.Rebuild(context.Background()); err != nil {
		t.Fatalf("composer rebuild: %v", err)
	}
	return c
}

func roundTrip(t *testing.T, h *AuthzLookupHandler, brk broker.Broker, namespace, userID string, ctx map[string]any) *control.Response {
	t.Helper()
	payload, err := json.Marshal(lookupRequest{UserID: userID, GatewayContext: ctx})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	reqCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	gatewayID, _ := ctx["gateway_id"].(string)
	if gatewayID == "" {
		gatewayID = "test-gw"
	}
	reply, err := brk.Request(reqCtx, a2a.AuthzLookupTopic(namespace, gatewayID), &broker.Message{
		Payload: payload,
		ReplyTo: a2a.AuthzResponseTopic(namespace, gatewayID),
	}, time.Second)
	if err != nil {
		t.Fatalf("broker request: %v", err)
	}
	resp, err := control.UnmarshalResponse(reply.Payload)
	if err != nil {
		t.Fatalf("unmarshal reply: %v", err)
	}
	return resp
}

func startAuthzHandler(t *testing.T, brk broker.Broker, composer *rbac.Composer, namespace string) *AuthzLookupHandler {
	t.Helper()
	h := NewAuthzLookupHandler(composer, nil, brk, AuthzLookupConfig{Namespace: namespace, CacheTTL: time.Minute},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := h.Start(context.Background()); err != nil {
		t.Fatalf("start handler: %v", err)
	}
	t.Cleanup(func() { _ = h.Stop(context.Background()) })
	time.Sleep(20 * time.Millisecond)
	return h
}

func newTestBroker(t *testing.T) broker.Broker {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatalf("connect dev broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })
	return brk
}

func TestAuthzLookupHandler_HappyPath(t *testing.T) {
	const ns = "rbac-test"
	brk := newTestBroker(t)
	composer := newAuthzTestComposer(t)
	startAuthzHandler(t, brk, composer, ns)

	resp := roundTrip(t, nil, brk, ns, "alice@example.org", map[string]any{"gateway_id": "webui"})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	res, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T", resp.Result)
	}
	if !equalAny(res["roles"], []string{"sam_admin"}) {
		t.Errorf("roles = %v, want [sam_admin]", res["roles"])
	}
	if !equalAny(res["scopes"], []string{"*"}) {
		t.Errorf("scopes = %v, want [sam:*]", res["scopes"])
	}
	if res["roles_source"] != "assigned" {
		t.Errorf("roles_source = %v, want assigned", res["roles_source"])
	}
}

func TestAuthzLookupHandler_DefaultFallback(t *testing.T) {
	const ns = "rbac-default"
	brk := newTestBroker(t)
	composer := newAuthzTestComposer(t)
	startAuthzHandler(t, brk, composer, ns)

	resp := roundTrip(t, nil, brk, ns, "stranger@example.org", map[string]any{"gateway_id": "webui"})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	res := resp.Result.(map[string]any)
	if !equalAny(res["roles"], []string{"sam_user"}) {
		t.Errorf("default roles = %v, want [sam_user]", res["roles"])
	}
	if res["roles_source"] != "defaults" {
		t.Errorf("roles_source = %v, want defaults", res["roles_source"])
	}
}

func TestAuthzLookupHandler_ValidationError(t *testing.T) {
	const ns = "rbac-validation"
	brk := newTestBroker(t)
	startAuthzHandler(t, brk, newAuthzTestComposer(t), ns)

	resp := roundTrip(t, nil, brk, ns, "", nil)
	if resp.Error == nil || resp.Error.Code != control.ErrInvalidRequest {
		t.Fatalf("expected invalid-request error, got %+v", resp)
	}
}

func TestAuthzLookupHandler_EchoesCorrelationID(t *testing.T) {
	const ns = "rbac-corr"
	const gw = "webui"
	for _, tc := range []struct {
		name   string
		userID string
	}{
		{"success", "alice@example.org"},
		{"validation_error", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			brk := newTestBroker(t)
			startAuthzHandler(t, brk, newAuthzTestComposer(t), ns)

			replyTopic := a2a.AuthzResponseTopic(ns, gw)
			replyQ, err := brk.CreateQueue("corr-inspect", broker.QueueOptions{Named: true})
			if err != nil {
				t.Fatalf("create queue: %v", err)
			}
			if err = brk.Subscribe(replyQ, replyTopic); err != nil {
				t.Fatalf("subscribe: %v", err)
			}
			time.Sleep(20 * time.Millisecond)

			const corr = "fixed-correlation-123"
			payload, err := json.Marshal(lookupRequest{UserID: tc.userID, GatewayContext: map[string]any{"gateway_id": gw}})
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if err = brk.Publish(t.Context(), a2a.AuthzLookupTopic(ns, gw), &broker.Message{
				Payload: payload, ReplyTo: replyTopic, CorrelationID: corr,
			}); err != nil {
				t.Fatalf("publish: %v", err)
			}

			recvCtx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			reply, err := brk.Receive(recvCtx, replyQ)
			if err != nil {
				t.Fatalf("receive reply: %v", err)
			}
			if reply.CorrelationID != corr {
				t.Errorf("reply CorrelationID = %q, want %q (handler must echo request correlation id)", reply.CorrelationID, corr)
			}
		})
	}
}

func equalAny(got any, want []string) bool {
	switch v := got.(type) {
	case []any:
		s := make([]string, 0, len(v))
		for _, item := range v {
			if str, ok := item.(string); ok {
				s = append(s, str)
			}
		}
		return slices.Equal(s, want)
	case []string:
		return slices.Equal(v, want)
	}
	return false
}

type fakeIdentity struct {
	resolveCalls atomic.Int32
	block        atomic.Bool
	fail         atomic.Bool
}

func (f *fakeIdentity) Resolve(ctx context.Context, _ map[string]any) (*identity.Resolution, error) {
	f.resolveCalls.Add(1)
	if f.block.Load() {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if f.fail.Load() {
		return &identity.Resolution{EnrichmentStatus: identity.EnrichmentUnavailable, EnrichmentReason: "provider down"}, nil
	}
	return &identity.Resolution{EnrichmentStatus: identity.EnrichmentOK, Profile: &identity.Profile{}}, nil
}

func (f *fakeIdentity) SearchUsers(context.Context, string, int) ([]*identity.Profile, error) {
	return nil, nil
}
func (f *fakeIdentity) GetUserProfile(context.Context, string) (map[string]any, error) {
	return nil, nil
}
func (f *fakeIdentity) GetTimeOff(context.Context, string, string, string) ([]identity.TimeOffEntry, error) {
	return nil, nil
}
func (f *fakeIdentity) GetUserProfilePicture(context.Context, string) ([]byte, string, error) {
	return nil, "", nil
}

func uniqueUser(i int) string { return "user" + string(rune('a'+i)) + "@solace.com" }

func TestAuthzLookupHandler_EnrichmentBreakerFreesWorkerPool(t *testing.T) {
	const ns = "rbac-enrich-breaker"
	const threshold = 3
	brk := newTestBroker(t)
	composer := newAuthzTestComposer(t)
	id := &fakeIdentity{}
	id.block.Store(true)
	h := NewAuthzLookupHandler(composer, id, brk, AuthzLookupConfig{
		Namespace:                  ns,
		CacheTTL:                   time.Minute,
		Workers:                    1,
		EnrichmentTimeout:          100 * time.Millisecond,
		EnrichmentBreakerThreshold: threshold,
		EnrichmentBreakerCooldown:  time.Hour,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := h.Start(context.Background()); err != nil {
		t.Fatalf("start handler: %v", err)
	}
	t.Cleanup(func() { _ = h.Stop(context.Background()) })
	time.Sleep(20 * time.Millisecond)

	const requests = 8
	for i := range requests {
		resp := roundTrip(t, h, brk, ns, uniqueUser(i), map[string]any{"gateway_id": "webui"})
		if resp.Error != nil {
			t.Fatalf("request %d: unexpected error: %+v", i, resp.Error)
		}
		res := resp.Result.(map[string]any)
		if !equalAny(res["roles"], []string{"sam_user"}) {
			t.Errorf("request %d roles = %v, want [sam_user]", i, res["roles"])
		}
		if res["enrichment_status"] != string(identity.EnrichmentUnavailable) {
			t.Errorf("request %d enrichment_status = %v, want unavailable", i, res["enrichment_status"])
		}
	}
	if got := id.resolveCalls.Load(); got != threshold {
		t.Errorf("resolve calls = %d, want %d (breaker should short-circuit enrichment after the threshold)", got, threshold)
	}
}

func TestAuthzLookupHandler_EnrichmentBreakerRecovers(t *testing.T) {
	const ns = "rbac-enrich-recover"
	const threshold = 2
	const cooldown = 50 * time.Millisecond
	brk := newTestBroker(t)
	composer := newAuthzTestComposer(t)
	id := &fakeIdentity{}
	id.fail.Store(true)
	h := NewAuthzLookupHandler(composer, id, brk, AuthzLookupConfig{
		Namespace:                  ns,
		CacheTTL:                   time.Minute,
		Workers:                    1,
		EnrichmentTimeout:          time.Second,
		EnrichmentBreakerThreshold: threshold,
		EnrichmentBreakerCooldown:  cooldown,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := h.Start(context.Background()); err != nil {
		t.Fatalf("start handler: %v", err)
	}
	t.Cleanup(func() { _ = h.Stop(context.Background()) })
	time.Sleep(20 * time.Millisecond)

	user := 0
	for range threshold {
		resp := roundTrip(t, h, brk, ns, uniqueUser(user), map[string]any{"gateway_id": "webui"})
		user++
		if resp.Result.(map[string]any)["enrichment_status"] != string(identity.EnrichmentUnavailable) {
			t.Fatalf("expected unavailable while provider failing")
		}
	}

	id.fail.Store(false)
	time.Sleep(cooldown + 20*time.Millisecond)

	resp := roundTrip(t, h, brk, ns, uniqueUser(user), map[string]any{"gateway_id": "webui"})
	if got := resp.Result.(map[string]any)["enrichment_status"]; got != string(identity.EnrichmentOK) {
		t.Errorf("after recovery enrichment_status = %v, want ok (breaker should have closed)", got)
	}
}

func TestAuthzLookupHandler_AcksEveryPath(t *testing.T) {
	const ns = "rbac-ack"
	brk := newTestBroker(t)
	h := NewAuthzLookupHandler(newAuthzTestComposer(t), nil, brk,
		AuthzLookupConfig{Namespace: ns, CacheTTL: time.Minute},
		slog.New(slog.NewTextHandler(io.Discard, nil)))

	replyTo := a2a.AuthzResponseTopic(ns, "webui")
	marshal := func(v any) []byte {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal request: %v", err)
		}
		return b
	}
	valid := marshal(lookupRequest{UserID: "alice@example.org", GatewayContext: map[string]any{"gateway_id": "webui"}})

	cases := []struct {
		name    string
		replyTo string
		payload []byte
	}{
		{"missing_reply_to", "", valid},
		{"bad_payload", replyTo, []byte("not json")},
		{"empty_user_id", replyTo, marshal(lookupRequest{})},
		{"happy_path", replyTo, valid},
		{"cache_hit", replyTo, valid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var acked atomic.Bool
			msg := broker.NewDeliveredMessage("id-1", a2a.AuthzLookupTopic(ns, "webui"), tc.payload,
				func() { acked.Store(true) }, func() {})
			msg.ReplyTo = tc.replyTo
			h.handle(context.Background(), msg)
			if !acked.Load() {
				t.Errorf("%s: inbound message was not acked", tc.name)
			}
		})
	}
}

func TestSystemUserAttr(t *testing.T) {
	t.Parallel()
	if got := systemUserAttr("system:deploy-bot"); !slices.Equal(got, []any{"systemUser", "system:deploy-bot"}) {
		t.Errorf("system principal must be named in logs, got %v", got)
	}
	for _, human := range []string{"alice@corp.com", "whatsapp:15551230000", "channel-user"} {
		if got := systemUserAttr(human); got != nil {
			t.Errorf("systemUserAttr(%q) = %v, want nil — a human identity attribute is PII", human, got)
		}
	}
}

func TestAuthzLookupHandler_WarnsOnBudgetBelowEnrichmentTimeout(t *testing.T) {
	t.Parallel()
	const warning = "the enrichment circuit breaker will never trip"

	logger, buf := captureLogger()
	NewAuthzLookupHandler(newAuthzTestComposer(t), nil, newTestBroker(t), AuthzLookupConfig{
		Namespace:         "rbac-budget-warn",
		DependencyBudget:  time.Second,
		EnrichmentTimeout: 4 * time.Second,
	}, logger)
	if !strings.Contains(buf.String(), warning) {
		t.Errorf("a budget below the enrichment timeout must warn; log = %q", buf.String())
	}

	defaults, defaultBuf := captureLogger()
	NewAuthzLookupHandler(newAuthzTestComposer(t), nil, newTestBroker(t), AuthzLookupConfig{
		Namespace: "rbac-budget-defaults",
	}, defaults)
	if strings.Contains(defaultBuf.String(), warning) {
		t.Errorf("the default budget/timeout pair must not warn; log = %q", defaultBuf.String())
	}
}

type blockingResolver struct{}

func (blockingResolver) Resolve(ctx context.Context, _ identityLookupRequest) (identityResolution, error) {
	<-ctx.Done()
	return identityResolution{Outcome: identity.LookupDegraded}, ctx.Err()
}

type slowResolver struct{ delay time.Duration }

func (r slowResolver) Resolve(ctx context.Context, _ identityLookupRequest) (identityResolution, error) {
	select {
	case <-time.After(r.delay):
		return identityResolution{Outcome: identity.LookupNoMatch}, nil
	case <-ctx.Done():
		return identityResolution{Outcome: identity.LookupDegraded}, ctx.Err()
	}
}

func TestAuthzLookupHandler_SlowResolveDoesNotTripEnrichmentBreaker(t *testing.T) {
	const ns = "rbac-slow-resolve-breaker"
	const threshold = 2
	const requests = 4
	brk := newTestBroker(t)
	id := &fakeIdentity{}
	id.fail.Store(true)
	h := NewAuthzLookupHandler(newAuthzTestComposer(t), id, brk, AuthzLookupConfig{
		Namespace:                  ns,
		CacheTTL:                   time.Minute,
		Workers:                    1,
		DependencyBudget:           200 * time.Millisecond,
		EnrichmentTimeout:          time.Second,
		EnrichmentBreakerThreshold: threshold,
		EnrichmentBreakerCooldown:  time.Hour,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	metrics := newCounterMetrics()
	h.SetMetrics(metrics)
	h.SetUserIdentityResolver(slowResolver{delay: 150 * time.Millisecond})
	if err := h.Start(context.Background()); err != nil {
		t.Fatalf("start handler: %v", err)
	}
	t.Cleanup(func() { _ = h.Stop(context.Background()) })
	time.Sleep(20 * time.Millisecond)

	for i := range requests {
		resp := roundTrip(t, h, brk, ns, uniqueUser(i), map[string]any{"gateway_id": "slack"})
		if resp.Error != nil {
			t.Fatalf("request %d: unexpected error: %+v", i, resp.Error)
		}
		res := resp.Result.(map[string]any)
		if res["enrichment_status"] != string(identity.EnrichmentUnavailable) {
			t.Errorf("request %d enrichment_status = %v, want unavailable", i, res["enrichment_status"])
		}
	}
	if got := id.resolveCalls.Load(); got != requests {
		t.Errorf("resolve calls = %d, want %d — a slow DB must not open the breaker on a healthy provider", got, requests)
	}
	if got := metrics.get(metricAuthzEnrichmentUncounted); got != requests {
		t.Errorf("%s = %d, want %d — every starved failure must be counted, not just dropped",
			metricAuthzEnrichmentUncounted, got, requests)
	}
}

func TestAuthzLookupHandler_UserResolveTimeoutBoundsAStalledDB(t *testing.T) {
	const ns = "rbac-user-resolve-timeout"
	brk := newTestBroker(t)
	h := NewAuthzLookupHandler(newAuthzTestComposer(t), nil, brk, AuthzLookupConfig{
		Namespace:          ns,
		CacheTTL:           time.Minute,
		Workers:            1,
		UserResolveTimeout: 100 * time.Millisecond,
		DependencyBudget:   time.Hour,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.SetUserIdentityResolver(blockingResolver{})
	if err := h.Start(context.Background()); err != nil {
		t.Fatalf("start handler: %v", err)
	}
	t.Cleanup(func() { _ = h.Stop(context.Background()) })
	time.Sleep(20 * time.Millisecond)

	resp := roundTrip(t, h, brk, ns, "stalled@example.com", map[string]any{"gateway_id": "slack"})
	if resp.Error != nil {
		t.Fatalf("a stalled resolve must still reply: %+v", resp.Error)
	}
	res := resp.Result.(map[string]any)
	if !equalAny(res["roles"], []string{"sam_user"}) {
		t.Errorf("roles = %v, want [sam_user] (defaults after a stalled resolve)", res["roles"])
	}
}

func TestAuthzLookupHandler_ResolveFailureCounted(t *testing.T) {
	const ns = "rbac-resolve-failure-counted"
	brk := newTestBroker(t)
	h := NewAuthzLookupHandler(newAuthzTestComposer(t), nil, brk,
		AuthzLookupConfig{Namespace: ns, CacheTTL: time.Minute},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	metrics := newCounterMetrics()
	h.SetMetrics(metrics)
	h.SetUserIdentityResolver(erroringResolver{})
	if err := h.Start(context.Background()); err != nil {
		t.Fatalf("start handler: %v", err)
	}
	t.Cleanup(func() { _ = h.Stop(context.Background()) })
	time.Sleep(20 * time.Millisecond)

	if resp := roundTrip(t, h, brk, ns, "chat@example.com", map[string]any{"gateway_id": "slack"}); resp.Error != nil {
		t.Fatalf("a degraded resolve must still reply: %+v", resp.Error)
	}

	if got := metrics.get(metricAuthzResolveFailures); got != 1 {
		t.Errorf("%s = %d, want 1", metricAuthzResolveFailures, got)
	}
	if got := metrics.labelsFor(metricAuthzResolveFailures); len(got) != 2 || got[0] != "reason" || got[1] != resolveFailureDBError {
		t.Errorf("labels = %v, want [reason %s]", got, resolveFailureDBError)
	}
}

func TestAuthzLookupHandler_DefaultBudgetStillTripsEnrichmentBreaker(t *testing.T) {
	const ns = "rbac-default-budget-breaker"
	const threshold = 2
	const requests = 4
	brk := newTestBroker(t)
	id := &fakeIdentity{}
	id.fail.Store(true)
	h := NewAuthzLookupHandler(newAuthzTestComposer(t), id, brk, AuthzLookupConfig{
		Namespace:                  ns,
		CacheTTL:                   time.Minute,
		Workers:                    1,
		EnrichmentBreakerThreshold: threshold,
		EnrichmentBreakerCooldown:  time.Hour,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.SetUserIdentityResolver(slowResolver{})
	if err := h.Start(context.Background()); err != nil {
		t.Fatalf("start handler: %v", err)
	}
	t.Cleanup(func() { _ = h.Stop(context.Background()) })
	time.Sleep(20 * time.Millisecond)

	for i := range requests {
		if resp := roundTrip(t, h, brk, ns, uniqueUser(i), map[string]any{"gateway_id": "slack"}); resp.Error != nil {
			t.Fatalf("request %d: unexpected error: %+v", i, resp.Error)
		}
	}
	if got := id.resolveCalls.Load(); got != threshold {
		t.Errorf("resolve calls = %d, want %d — under the default budget==timeout config the breaker must still open", got, threshold)
	}
}

func TestAuthzLookupHandler_FastResolveStillTripsEnrichmentBreaker(t *testing.T) {
	const ns = "rbac-fast-resolve-breaker"
	const threshold = 2
	const requests = 5
	brk := newTestBroker(t)
	id := &fakeIdentity{}
	id.fail.Store(true)
	h := NewAuthzLookupHandler(newAuthzTestComposer(t), id, brk, AuthzLookupConfig{
		Namespace:                  ns,
		CacheTTL:                   time.Minute,
		Workers:                    1,
		EnrichmentTimeout:          100 * time.Millisecond,
		EnrichmentBreakerThreshold: threshold,
		EnrichmentBreakerCooldown:  time.Hour,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.SetUserIdentityResolver(slowResolver{})
	if err := h.Start(context.Background()); err != nil {
		t.Fatalf("start handler: %v", err)
	}
	t.Cleanup(func() { _ = h.Stop(context.Background()) })
	time.Sleep(20 * time.Millisecond)

	for i := range requests {
		if resp := roundTrip(t, h, brk, ns, uniqueUser(i), map[string]any{"gateway_id": "slack"}); resp.Error != nil {
			t.Fatalf("request %d: unexpected error: %+v", i, resp.Error)
		}
	}
	if got := id.resolveCalls.Load(); got != threshold {
		t.Errorf("resolve calls = %d, want %d — a failing provider must still open the breaker", got, threshold)
	}
}

func TestAuthzLookupHandler_DependencyBudgetStillReplies(t *testing.T) {
	const ns = "rbac-dep-budget"
	brk := newTestBroker(t)
	id := &fakeIdentity{}
	id.block.Store(true)
	h := NewAuthzLookupHandler(newAuthzTestComposer(t), id, brk, AuthzLookupConfig{
		Namespace:        ns,
		CacheTTL:         time.Minute,
		Workers:          1,
		DependencyBudget: 300 * time.Millisecond,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.SetUserIdentityResolver(blockingResolver{})
	if err := h.Start(context.Background()); err != nil {
		t.Fatalf("start handler: %v", err)
	}
	t.Cleanup(func() { _ = h.Stop(context.Background()) })
	time.Sleep(20 * time.Millisecond)

	resp := roundTrip(t, h, brk, ns, "stalled@example.com", map[string]any{"gateway_id": "slack"})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	res := resp.Result.(map[string]any)
	if !equalAny(res["roles"], []string{"sam_user"}) {
		t.Errorf("roles = %v, want [sam_user] (defaults after a stalled resolve)", res["roles"])
	}
	if res["enrichment_status"] != string(identity.EnrichmentUnavailable) {
		t.Errorf("enrichment_status = %v, want unavailable", res["enrichment_status"])
	}
}

type gatedIdentity struct {
	fakeIdentity
	entered chan struct{}
	release chan struct{}
}

func (g *gatedIdentity) Resolve(ctx context.Context, claims map[string]any) (*identity.Resolution, error) {
	close(g.entered)
	<-g.release
	return g.fakeIdentity.Resolve(ctx, claims)
}

func TestAuthzLookupHandler_ClearDuringLookupIsNotUndoneByItsCacheStore(t *testing.T) {
	const ns = "rbac-clear-during-lookup"
	brk := newTestBroker(t)
	id := &gatedIdentity{entered: make(chan struct{}), release: make(chan struct{})}
	h := NewAuthzLookupHandler(newAuthzTestComposer(t), id, brk, AuthzLookupConfig{Namespace: ns, CacheTTL: time.Minute},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := h.Start(t.Context()); err != nil {
		t.Fatalf("start handler: %v", err)
	}
	t.Cleanup(func() { _ = h.Stop(context.Background()) })
	time.Sleep(20 * time.Millisecond)

	gwCtx := map[string]any{"gateway_id": "webui"}
	done := make(chan *control.Response, 1)
	go func() { done <- roundTrip(t, h, brk, ns, "alice@example.org", gwCtx) }()
	<-id.entered
	h.InvalidateAll()
	close(id.release)
	if resp := <-done; resp.Error != nil {
		t.Fatalf("lookup: %+v", resp.Error)
	}

	if _, ok := h.cache.Get(cacheKeyFor("alice@example.org", "webui", gwCtx)); ok {
		t.Fatal("the answer read before InvalidateAll was cached after it")
	}
}
