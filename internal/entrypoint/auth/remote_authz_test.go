// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	"github.com/SolaceProducts/solace-agent-mesh/internal/identity"
	"github.com/SolaceProducts/solace-agent-mesh/internal/strutil"
)

func newRemoteAuthzTestBroker(t *testing.T) broker.Broker {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatalf("connect dev broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })
	return brk
}

func startStubResponder(t *testing.T, brk broker.Broker, namespace, gatewayID string, reply func(req map[string]any) *control.Response) *atomic.Int32 {
	t.Helper()
	queue, err := brk.CreateQueue("stub-authz-"+namespace+"-"+gatewayID, broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create stub queue: %v", err)
	}
	if err := brk.Subscribe(queue, a2a.AuthzLookupTopic(namespace, gatewayID)); err != nil {
		t.Fatalf("subscribe stub: %v", err)
	}
	var count atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	go func() {
		for {
			msg, err := brk.Receive(ctx, queue)
			if err != nil || msg == nil {
				return
			}
			count.Add(1)
			var payload map[string]any
			_ = json.Unmarshal(msg.Payload, &payload)
			resp := reply(payload)
			if resp == nil {
				continue
			}
			data, _ := control.MarshalResponse(resp)
			replyTo := msg.ReplyTo
			if replyTo == "" {
				replyTo = a2a.AuthzResponseTopic(namespace, gatewayID)
			}
			_ = brk.Publish(ctx, replyTo, &broker.Message{Payload: data})
		}
	}()

	time.Sleep(20 * time.Millisecond)
	return &count
}

func newRemoteAuthzClient(brk broker.Broker, namespace, gatewayID string) *BrokerAuthorizationClient {
	return NewBrokerAuthorizationClient(brk, BrokerAuthorizationClientConfig{
		Namespace: namespace,
		GatewayID: gatewayID,
		Timeout:   time.Second,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestBrokerAuthorizationClient_DecodesProfile(t *testing.T) {
	const ns = "authzclient-profile"
	const gw = "webui"
	brk := newRemoteAuthzTestBroker(t)
	startStubResponder(t, brk, ns, gw, func(map[string]any) *control.Response {
		return control.SuccessResponse(map[string]any{
			"roles":             []string{"sam_user"},
			"scopes":            []string{"tool:_:read"},
			"enrichment_status": "ok",
			"profile": map[string]any{
				"id":          "u-1",
				"displayName": "Alice",
				"email":       "alice@example.com",
				"jobTitle":    "Engineer",
				"costCenter":  "CC-42",
			},
		})
	})
	client := newRemoteAuthzClient(brk, ns, gw)

	res, err := client.Lookup(context.Background(), "sub-alice", nil)
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if res.EnrichmentStatus != "ok" {
		t.Errorf("EnrichmentStatus = %q, want ok", res.EnrichmentStatus)
	}
	if res.Profile == nil {
		t.Fatal("Profile not decoded from reply")
	}
	if res.Profile.ID != "u-1" || res.Profile.DisplayName != "Alice" || res.Profile.JobTitle != "Engineer" {
		t.Errorf("Profile = %+v", res.Profile)
	}
	if res.Profile.Extra["costCenter"] != "CC-42" || res.Profile.Extra["email"] != "alice@example.com" {
		t.Errorf("custom fields did not pass through into Extra: %+v", res.Profile.Extra)
	}
}

func TestBrokerAuthorizationClient_MalformedProfileDowngrades(t *testing.T) {
	const ns = "authzclient-badprofile"
	const gw = "webui"
	brk := newRemoteAuthzTestBroker(t)
	startStubResponder(t, brk, ns, gw, func(map[string]any) *control.Response {
		return control.SuccessResponse(map[string]any{
			"roles":             []string{"sam_user"},
			"scopes":            []string{"sam:read"},
			"enrichment_status": "ok",
			"profile":           map[string]any{"displayName": "No ID Here"},
		})
	})
	client := newRemoteAuthzClient(brk, ns, gw)

	res, err := client.Lookup(context.Background(), "sub-alice", nil)
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if res.Profile != nil {
		t.Errorf("Profile = %+v, want nil on decode failure", res.Profile)
	}
	if res.EnrichmentStatus != identity.EnrichmentUnavailable {
		t.Errorf("EnrichmentStatus = %q, want unavailable (must not stay ok and poison the cache)", res.EnrichmentStatus)
	}
}

func TestBuildBrokerLazyEnrichmentLookup_HappyPath(t *testing.T) {
	const ns = "authzclient-lazy"
	const gw = "webui"
	brk := newRemoteAuthzTestBroker(t)
	startStubResponder(t, brk, ns, gw, func(req map[string]any) *control.Response {
		gctx, _ := req["gateway_context"].(map[string]any)
		claims, _ := gctx["idp_claims"].(map[string]any)
		if claims["email"] != "alice@example.com" {
			return control.ErrorResponse(1, "missing idp_claims.email")
		}
		return control.SuccessResponse(map[string]any{
			"roles":             []string{"sam_user"},
			"scopes":            []string{"tool:_:read"},
			"enrichment_status": "ok",
			"profile":           map[string]any{"id": "u-1", "displayName": "Alice"},
		})
	})

	fn := BuildBrokerLazyEnrichmentLookup(brk, ns, gw, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if fn == nil {
		t.Fatal("BuildBrokerLazyEnrichmentLookup returned nil for a valid broker/gatewayID")
	}
	got, err := fn(context.Background(), &Subject{userID: "sub-alice", email: "alice@example.com"})
	if err != nil {
		t.Fatalf("lazy lookup: %v", err)
	}
	if got.Profile == nil || got.Profile.ID != "u-1" {
		t.Errorf("Profile = %+v", got.Profile)
	}
	if got.EnrichmentStatus != "ok" {
		t.Errorf("EnrichmentStatus = %q, want ok", got.EnrichmentStatus)
	}
}

func TestBuildBrokerLazyEnrichmentLookup_NilBrokerOrEmptyGateway(t *testing.T) {
	if fn := BuildBrokerLazyEnrichmentLookup(nil, "ns", "webui", nil); fn != nil {
		t.Error("expected nil LazyEnrichmentLookup when broker is nil")
	}
	brk := broker.NewDevBroker()
	if fn := BuildBrokerLazyEnrichmentLookup(brk, "ns", "", nil); fn != nil {
		t.Error("expected nil LazyEnrichmentLookup when gatewayID is empty")
	}
}

func TestBuildBrokerLazyEnrichmentLookup_PropagatesLookupError(t *testing.T) {
	const ns = "authzclient-lazy-err"
	const gw = "webui"
	brk := newRemoteAuthzTestBroker(t)
	startStubResponder(t, brk, ns, gw, func(map[string]any) *control.Response {
		return control.ErrorResponse(500, "boom")
	})

	fn := BuildBrokerLazyEnrichmentLookup(brk, ns, gw, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if fn == nil {
		t.Fatal("BuildBrokerLazyEnrichmentLookup returned nil")
	}
	_, err := fn(context.Background(), &Subject{userID: "sub-alice", email: "alice@example.com"})
	if err == nil {
		t.Fatal("expected error from lazy lookup when platform returns error envelope")
	}
}

func TestBrokerAuthorizationClient_HappyPath(t *testing.T) {
	const ns = "authzclient-happy"
	const gw = "webui"
	brk := newRemoteAuthzTestBroker(t)
	startStubResponder(t, brk, ns, gw, func(req map[string]any) *control.Response {
		return control.SuccessResponse(map[string]any{
			"roles":  []string{"sam_admin"},
			"scopes": []string{"*"},
		})
	})
	client := newRemoteAuthzClient(brk, ns, gw)

	res, err := client.Lookup(context.Background(), "sub-alice", map[string]any{"gateway_id": gw})
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if !slices.Equal(res.Roles, []string{"sam_admin"}) {
		t.Errorf("roles = %v", res.Roles)
	}
	if !slices.Equal(res.Scopes, []string{"*"}) {
		t.Errorf("scopes = %v", res.Scopes)
	}
}

func TestBrokerAuthorizationClient_ResolutionDecode(t *testing.T) {
	const gw = "wa-gw"
	for _, tt := range []struct {
		name  string
		ns    string
		reply map[string]any
		want  identity.LookupResolution
	}{
		{"absent stays unknown", "authzclient-res-absent",
			map[string]any{"roles": []string{"r"}, "scopes": []string{"*"}}, identity.LookupUnknown},
		{"a shipped value passes through", "authzclient-res-shipped",
			map[string]any{"resolution": "no_match"}, identity.LookupNoMatch},
		{"a value from a newer platform is held as degraded", "authzclient-res-newer",
			map[string]any{"resolution": "quarantined", "roles": []string{"r"}}, identity.LookupDegraded},
		{"a typo is held as degraded", "authzclient-res-typo",
			map[string]any{"resolution": "mached"}, identity.LookupDegraded},
	} {
		t.Run(tt.name, func(t *testing.T) {
			brk := newRemoteAuthzTestBroker(t)
			startStubResponder(t, brk, tt.ns, gw, func(map[string]any) *control.Response {
				return control.SuccessResponse(tt.reply)
			})
			client := newRemoteAuthzClient(brk, tt.ns, gw)

			res, err := client.Lookup(context.Background(), "sub-alice", map[string]any{"gateway_id": gw})
			if err != nil {
				t.Fatalf("lookup: %v", err)
			}
			if res.Resolution != tt.want {
				t.Errorf("resolution = %q, want %q", res.Resolution, tt.want)
			}
		})
	}
}

func TestBrokerAuthorizationClient_RolesSourceDecode(t *testing.T) {
	const gw = "wa-gw"
	for _, tt := range []struct {
		name  string
		ns    string
		reply map[string]any
		want  identity.RolesSource
	}{
		{"absent stays unknown", "authzclient-src-absent",
			map[string]any{"roles": []string{"r"}}, identity.RolesSourceUnknown},
		{"assigned", "authzclient-src-assigned",
			map[string]any{"roles_source": "assigned"}, identity.RolesSourceAssigned},
		{"defaults", "authzclient-src-defaults",
			map[string]any{"roles_source": "defaults"}, identity.RolesSourceDefaults},
	} {
		t.Run(tt.name, func(t *testing.T) {
			brk := newRemoteAuthzTestBroker(t)
			startStubResponder(t, brk, tt.ns, gw, func(map[string]any) *control.Response {
				return control.SuccessResponse(tt.reply)
			})
			client := newRemoteAuthzClient(brk, tt.ns, gw)

			res, err := client.Lookup(context.Background(), "sub-alice", map[string]any{"gateway_id": gw})
			if err != nil {
				t.Fatalf("lookup: %v", err)
			}
			if res.RolesSource != tt.want {
				t.Errorf("RolesSource = %q, want %q", res.RolesSource, tt.want)
			}
		})
	}
}

func TestBrokerAuthorizationClient_SystemUserRowDecode(t *testing.T) {
	const gw = "wa-gw"
	type systemUser struct {
		ID  string
		Row SystemUserRow
	}
	for _, tt := range []struct {
		name  string
		ns    string
		reply map[string]any
		want  systemUser
	}{
		{"a reported row passes through with its id", "authzclient-sur-found",
			map[string]any{"system_user_row": "found", "system_user_id": "row-1"}, systemUser{"row-1", SystemUserRowFound}},
		{"a missing row passes through", "authzclient-sur-missing",
			map[string]any{"system_user_row": "missing"}, systemUser{"", SystemUserRowMissing}},
		{"an absent field leaves the state unset", "authzclient-sur-absent",
			map[string]any{"roles": []string{"r"}}, systemUser{}},
		{"a non-string row is unavailable, not missing", "authzclient-sur-nonstring",
			map[string]any{"system_user_row": 5}, systemUser{"", SystemUserRowUnavailable}},
		{"a state this build does not know is unavailable, not passed through", "authzclient-sur-unknown",
			map[string]any{"system_user_row": "archived"}, systemUser{"", SystemUserRowUnavailable}},
		{"a non-string id is ignored", "authzclient-sur-nonstring-id",
			map[string]any{"system_user_row": "found", "system_user_id": 5}, systemUser{"", SystemUserRowFound}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			brk := newRemoteAuthzTestBroker(t)
			startStubResponder(t, brk, tt.ns, gw, func(map[string]any) *control.Response {
				return control.SuccessResponse(tt.reply)
			})
			client := newRemoteAuthzClient(brk, tt.ns, gw)

			res, err := client.Lookup(context.Background(), "system:nightly", map[string]any{"gateway_id": gw})
			if err != nil {
				t.Fatalf("lookup: %v", err)
			}
			if diff := cmp.Diff(tt.want, systemUser{res.SystemUserID, res.SystemUserRow}); diff != "" {
				t.Errorf("service account (-want +got):\n%s", diff)
			}
		})
	}
}

func TestBrokerAuthorizationClient_ErrorEnvelopeFailsClosed(t *testing.T) {
	const ns = "authzclient-err"
	const gw = "webui"
	brk := newRemoteAuthzTestBroker(t)
	startStubResponder(t, brk, ns, gw, func(map[string]any) *control.Response {
		return control.ErrorResponse(control.ErrOperationFailed, "lookup failed")
	})
	client := newRemoteAuthzClient(brk, ns, gw)

	_, err := client.Lookup(context.Background(), "sub-alice", nil)
	if err == nil {
		t.Fatal("expected error when platform returns error envelope")
	}
}

func TestBrokerAuthorizationClient_OnlyTransientFailuresAreUnavailable(t *testing.T) {
	const gw = "webui"
	for _, tt := range []struct {
		name            string
		ns              string
		reply           *control.Response
		answers         bool
		wantUnavailable bool
	}{
		{"no platform answers", "authzclient-unavail-silent", nil, false, true},
		{"the platform reports its lookup failed", "authzclient-unavail-failed",
			control.ErrorResponse(control.ErrOperationFailed, "lookup failed"), true, true},
		{"the platform rejects the request", "authzclient-unavail-invalid",
			control.ErrorResponse(control.ErrInvalidRequest, "user_id is required"), true, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			brk := newRemoteAuthzTestBroker(t)
			if tt.answers {
				startStubResponder(t, brk, tt.ns, gw, func(map[string]any) *control.Response { return tt.reply })
			}
			client := NewBrokerAuthorizationClient(brk, BrokerAuthorizationClientConfig{
				Namespace:    tt.ns,
				GatewayID:    gw,
				Timeout:      100 * time.Millisecond,
				RetryBackoff: 5 * time.Millisecond,
			}, slog.New(slog.NewTextHandler(io.Discard, nil)))

			_, err := client.Lookup(context.Background(), "sub-alice", nil)

			if err == nil {
				t.Fatal("lookup succeeded, want a failure")
			}
			if got := errors.Is(err, ErrIdentityLookupUnavailable); got != tt.wantUnavailable {
				t.Errorf("lookup unavailable = %v, want %v (err: %v)", got, tt.wantUnavailable, err)
			}
			if got := errors.Is(err, ErrIdentityPlatformUnreachable); got != tt.wantUnavailable {
				t.Errorf("platform unreachable = %v, want %v (err: %v)", got, tt.wantUnavailable, err)
			}
		})
	}
}

func TestBrokerAuthorizationClient_TimeoutFailsClosed(t *testing.T) {
	const ns = "authzclient-timeout"
	const gw = "webui"
	brk := newRemoteAuthzTestBroker(t)
	client := NewBrokerAuthorizationClient(brk, BrokerAuthorizationClientConfig{
		Namespace:    ns,
		GatewayID:    gw,
		Timeout:      100 * time.Millisecond,
		RetryBackoff: 5 * time.Millisecond,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	_, err := client.Lookup(context.Background(), "sub-alice", nil)
	if err == nil {
		t.Fatal("expected timeout error when platform is unreachable even after retries")
	}
}

func TestBrokerAuthorizationClient_RetriesTransientGap(t *testing.T) {
	const ns = "authzclient-retry"
	const gw = "webui"
	brk := newRemoteAuthzTestBroker(t)
	var served atomic.Int32
	startStubResponder(t, brk, ns, gw, func(map[string]any) *control.Response {
		if served.Add(1) == 1 {
			return nil
		}
		return control.SuccessResponse(map[string]any{
			"roles":  []string{"sam_user"},
			"scopes": []string{"sam:read"},
		})
	})
	client := NewBrokerAuthorizationClient(brk, BrokerAuthorizationClientConfig{
		Namespace:    ns,
		GatewayID:    gw,
		Timeout:      150 * time.Millisecond,
		RetryBackoff: 10 * time.Millisecond,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	res, err := client.Lookup(context.Background(), "sub-alice", nil)
	if err != nil {
		t.Fatalf("lookup should succeed after retrying past the gap: %v", err)
	}
	if !slices.Equal(res.Roles, []string{"sam_user"}) {
		t.Errorf("roles = %v", res.Roles)
	}
	if got := served.Load(); got < 2 {
		t.Errorf("expected at least 2 attempts (1 dropped + 1 served), got %d", got)
	}
}

func TestBrokerAuthorizationClient_DenialNotRetried(t *testing.T) {
	const ns = "authzclient-denial"
	const gw = "webui"
	brk := newRemoteAuthzTestBroker(t)
	count := startStubResponder(t, brk, ns, gw, func(map[string]any) *control.Response {
		return control.ErrorResponse(control.ErrOperationFailed, "denied")
	})
	client := newRemoteAuthzClient(brk, ns, gw)

	if _, err := client.Lookup(context.Background(), "sub-alice", nil); err == nil {
		t.Fatal("expected denial error")
	}
	if got := count.Load(); got != 1 {
		t.Errorf("a definitive denial must not be retried: got %d requests, want 1", got)
	}
}

func TestBrokerAuthorizationClient_SingleflightCoalesces(t *testing.T) {
	const ns = "authzclient-sf"
	const gw = "webui"
	brk := newRemoteAuthzTestBroker(t)
	count := startStubResponder(t, brk, ns, gw, func(map[string]any) *control.Response {
		time.Sleep(50 * time.Millisecond)
		return control.SuccessResponse(map[string]any{
			"roles":  []string{"sam_user"},
			"scopes": []string{"tool:_:read"},
		})
	})
	client := newRemoteAuthzClient(brk, ns, gw)

	var wg sync.WaitGroup
	for range 5 {
		wg.Go(func() {
			_, err := client.Lookup(context.Background(), "sub-alice", nil)
			if err != nil {
				t.Errorf("lookup err: %v", err)
			}
		})
	}
	wg.Wait()
	if got := count.Load(); got != 1 {
		t.Errorf("stub got %d requests, want 1 (singleflight should coalesce)", got)
	}
}

func TestAuthzLookupLogsFingerprintNotTheIdentity(t *testing.T) {
	const userID = "whatsapp:15551234567"

	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	client := NewBrokerAuthorizationClient(brk, BrokerAuthorizationClientConfig{
		Namespace:    "test-ns",
		GatewayID:    "wa-gw",
		Timeout:      50 * time.Millisecond,
		MaxAttempts:  2,
		RetryBackoff: time.Millisecond,
	}, logger)

	if _, err := client.Lookup(context.Background(), userID, nil); err == nil {
		t.Fatal("expected the lookup to fail with no responder")
	}

	out := buf.String()
	if !strings.Contains(out, "exhausted retries") {
		t.Fatalf("expected the exhausted-retries line, got: %s", out)
	}
	if strings.Contains(out, userID) {
		t.Errorf("the authz client log leaks the raw identity %q: %s", userID, out)
	}
	if !strings.Contains(out, "userFP="+strutil.PIIFingerprint(userID)) {
		t.Errorf("expected userFP=%s in: %s", strutil.PIIFingerprint(userID), out)
	}
}
