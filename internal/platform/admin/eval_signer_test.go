// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	gwauth "github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/oidcauth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/identity"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/eval"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/trust"
)

func newTestTrustSigner(t *testing.T) *trust.Signer {
	t.Helper()
	km, err := trust.NewKeyManager("eval-test")
	if err != nil {
		t.Fatalf("new key manager: %v", err)
	}
	return trust.NewSigner(km, "eval-test", time.Hour, 24*time.Hour)
}

type tokenClaims struct {
	Sub    string   `json:"sub"`
	TaskID string   `json:"task_id"`
	Scopes []string `json:"scopes"`

	PlatformUserID string `json:"platform_user_id"`
}

func decodeTokenClaims(t *testing.T, token string) tokenClaims {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("token = %q, want 3-segment JWT", token)
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	var claims tokenClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		t.Fatalf("unmarshal claims: %v", err)
	}
	return claims
}

type stubDBSource struct {
	mu    sync.Mutex
	state *rbac.DBState
}

func (s *stubDBSource) Snapshot(context.Context) (*rbac.DBState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state, nil
}

func (s *stubDBSource) set(state *rbac.DBState) {
	s.mu.Lock()
	s.state = state
	s.mu.Unlock()
}

func dbSystemUserGrant(subject, roleName string, scopes ...string) *rbac.DBState {
	const roleID = "11111111-1111-4111-8111-111111111111"
	return &rbac.DBState{
		Roles: []rbac.DBRole{{ID: roleID, Name: roleName, Scopes: scopes}},
		UserRoles: []rbac.DBUserRole{{
			UserID:       "22222222-2222-4222-8222-222222222222",
			RoleID:       roleID,
			SubjectType:  store.PendingSubjectSub,
			SubjectValue: subject,
			Issuer:       "sam",
		}},
	}
}

func newEvalComposer(t *testing.T, source *stubDBSource) *rbac.Composer {
	t.Helper()
	yamlState, err := BuildRBACYAMLState(oidcauth.AuthorizationServiceConfig{}, testLogger())
	if err != nil {
		t.Fatalf("build rbac yaml state: %v", err)
	}
	composer := rbac.NewComposer(yamlState, source, testLogger())
	if err := composer.Rebuild(t.Context()); err != nil {
		t.Fatalf("composer rebuild: %v", err)
	}
	return composer
}

func newBoundEvalSigner(t *testing.T, db *rbac.DBState) (*evalRunnerSigner, *stubDBSource) {
	t.Helper()
	source := &stubDBSource{state: db}
	signer, ok := NewEvalRunnerSigner(newTestTrustSigner(t), testLogger()).(*evalRunnerSigner)
	if !ok {
		t.Fatal("NewEvalRunnerSigner did not return *evalRunnerSigner")
	}
	signer.SetPrincipalResolver(newEvalComposer(t, source), nil)
	return signer, source
}

func TestNewEvalRunnerSigner_NilTrustSigner_ReturnsNil(t *testing.T) {
	t.Parallel()
	if got := NewEvalRunnerSigner(nil, testLogger()); got != nil {
		t.Fatalf("NewEvalRunnerSigner(nil, ...) = %v, want nil (community/dev mode publishes without auth token)", got)
	}
}

func TestNewIdentityTokenSigner_NilTrustSigner_ReturnsNil(t *testing.T) {
	t.Parallel()
	if got := NewIdentityTokenSigner(nil); got != nil {
		t.Fatalf("NewIdentityTokenSigner(nil) = %v, want nil", got)
	}
}

func TestNewIdentityTokenSigner_SignServiceToken_HasSubjectAndScopes(t *testing.T) {
	t.Parallel()
	signer := newTestTrustSigner(t)
	s := NewIdentityTokenSigner(signer)
	if s == nil {
		t.Fatal("expected non-nil signer")
	}
	token, err := s.SignServiceToken("corr-xyz")
	if err != nil {
		t.Fatalf("SignServiceToken: %v", err)
	}
	claims := decodeTokenClaims(t, token)
	if claims.Sub != identity.STREnricherAgentName {
		t.Errorf("sub = %q, want %q", claims.Sub, identity.STREnricherAgentName)
	}
	if claims.TaskID != "corr-xyz" {
		t.Errorf("task_id = %q, want corr-xyz", claims.TaskID)
	}
	wantExecute := "tool:*__enrich_authenticated_user:invoke"
	if !slices.Contains(claims.Scopes, wantExecute) {
		t.Errorf("scopes %v missing %q (least-privilege list must include the 5 profile-provider tools)", claims.Scopes, wantExecute)
	}
	for _, sc := range claims.Scopes {
		if sc == "*" || sc == "tool:*:*" {
			t.Errorf("scope %q is too broad — least-privilege regression", sc)
		}
	}
}

func TestEvalSignerResolvesEmbeddedEvalRoleScopes(t *testing.T) {
	t.Parallel()
	s, _ := newBoundEvalSigner(t, nil)
	token, err := s.SignTask(t.Context(), eval.TaskPrincipal{Subject: rbac.EvalSystemUserSub}, "task-embedded")
	if err != nil {
		t.Fatalf("SignTask: %v", err)
	}
	claims := decodeTokenClaims(t, token)
	if claims.Sub != rbac.EvalSystemUserSub {
		t.Errorf("sub = %q, want %q", claims.Sub, rbac.EvalSystemUserSub)
	}
	if claims.TaskID != "task-embedded" {
		t.Errorf("task_id = %q, want task-embedded", claims.TaskID)
	}
	if claims.PlatformUserID != rbac.EvalSystemUserID {
		t.Errorf("platform_user_id = %q, want system:eval's seeded row %s", claims.PlatformUserID, rbac.EvalSystemUserID)
	}
	for _, want := range []string{rbac.ScopeAgentInvokeAny, rbac.ScopeWorkflowInvokeAny, rbac.ScopeToolAny} {
		if !slices.Contains(claims.Scopes, want) {
			t.Errorf("scopes %v missing embedded system_eval_invoke grant %q", claims.Scopes, want)
		}
	}
	if slices.Contains(claims.Scopes, "*") {
		t.Errorf("scopes %v carry the universal wildcard — the embedded role must stay bounded", claims.Scopes)
	}
}

func TestEvalSignerResolvesDefaultSystemUser(t *testing.T) {
	t.Parallel()
	s, _ := newBoundEvalSigner(t, nil)
	token, err := s.SignTask(t.Context(), eval.TaskPrincipal{Subject: rbac.DefaultSystemUserSub}, "task-default")
	if err != nil {
		t.Fatalf("SignTask(%s): %v", rbac.DefaultSystemUserSub, err)
	}
	claims := decodeTokenClaims(t, token)
	if claims.Sub != rbac.DefaultSystemUserSub {
		t.Errorf("sub = %q, want %q", claims.Sub, rbac.DefaultSystemUserSub)
	}
	if claims.PlatformUserID != rbac.DefaultSystemUserID {
		t.Errorf("platform_user_id = %q, want system:default's seeded row %s", claims.PlatformUserID, rbac.DefaultSystemUserID)
	}
	if !slices.Contains(claims.Scopes, rbac.ScopeAgentInvokeAny) {
		t.Errorf("scopes %v missing the embedded default-system-user grant %q", claims.Scopes, rbac.ScopeAgentInvokeAny)
	}
	if slices.Contains(claims.Scopes, rbac.ScopeToolAny) {
		t.Errorf("scopes %v carry %q, which system_agent_invoke does not grant", claims.Scopes, rbac.ScopeToolAny)
	}
}

func TestEvalSignerResolvesDBProvisionedPrincipal(t *testing.T) {
	t.Parallel()
	const (
		principal = "system:qa-bot"
		narrow    = "agent:JiraAgent:invoke"
	)
	s, _ := newBoundEvalSigner(t, dbSystemUserGrant(principal, "eval_qa_bot", narrow))
	token, err := s.SignTask(t.Context(), eval.TaskPrincipal{Subject: principal}, "task-db")
	if err != nil {
		t.Fatalf("SignTask(%s): %v", principal, err)
	}
	claims := decodeTokenClaims(t, token)
	if claims.Sub != principal {
		t.Errorf("sub = %q, want %q", claims.Sub, principal)
	}
	if got := claims.Scopes; !slices.Equal(got, []string{narrow}) {
		t.Errorf("scopes = %v, want exactly [%q] — the run must carry the role's grant and nothing wider", got, narrow)
	}
	if claims.PlatformUserID != "" {
		t.Errorf("platform_user_id = %q, want none for a custom system user with no seeded row, so its spend is booked by name", claims.PlatformUserID)
	}
}

type capturingTaskSigner struct {
	signed *gwauth.Subject
}

func (c *capturingTaskSigner) SignTask(identity *gwauth.Subject, _ string) (string, error) {
	c.signed = identity
	return "signed", nil
}

func TestEvalSignerSignsThePrincipalsPlatformUser(t *testing.T) {
	t.Parallel()
	const (
		principal = "system:qa-bot"
		rowID     = "0192f0c4-6a4e-7b1e-9d3c-5f2a8b7e1c45"
	)
	s, _ := newBoundEvalSigner(t, dbSystemUserGrant(principal, "eval_qa_bot", "agent:JiraAgent:invoke"))
	captured := &capturingTaskSigner{}
	s.signer = captured

	if _, err := s.SignTask(t.Context(), eval.TaskPrincipal{Subject: principal, UserUUID: rowID}, "task-row"); err != nil {
		t.Fatalf("SignTask: %v", err)
	}
	if got := captured.signed.PlatformUserID(); got != rowID {
		t.Errorf("signed PlatformUserID = %q, want the principal's row %q", got, rowID)
	}
	if got := captured.signed.UserID(); got != principal {
		t.Errorf("signed subject = %q, want %q", got, principal)
	}
}

func TestEvalSignerRefusesUnprovisionedPrincipal(t *testing.T) {
	t.Parallel()
	s, _ := newBoundEvalSigner(t, nil)
	token, err := s.SignTask(t.Context(), eval.TaskPrincipal{Subject: "system:never-provisioned"}, "task-deny")
	if err == nil {
		t.Fatalf("SignTask(unprovisioned) = %q, want a refusal", token)
	}
	if !errors.Is(err, ErrEvalPrincipalUnauthorized) {
		t.Errorf("err = %v, want ErrEvalPrincipalUnauthorized", err)
	}
	if !strings.Contains(err.Error(), "system:never-provisioned") {
		t.Errorf("err = %v, want the principal named so the run's error message is actionable", err)
	}
	if token != "" {
		t.Errorf("token = %q, want empty on refusal", token)
	}
}

func TestEvalSignerRefusesWhenResolverUnbound(t *testing.T) {
	t.Parallel()
	s := NewEvalRunnerSigner(newTestTrustSigner(t), testLogger())
	if _, err := s.SignTask(t.Context(), eval.TaskPrincipal{Subject: rbac.EvalSystemUserSub}, "task-unbound"); !errors.Is(err, ErrEvalPrincipalUnauthorized) {
		t.Errorf("err = %v, want ErrEvalPrincipalUnauthorized when no resolver is bound", err)
	}
}

func TestEvalSignerLookupFailureIsNotReportedAsMissingGrant(t *testing.T) {
	t.Parallel()
	yamlState, err := BuildRBACYAMLState(oidcauth.AuthorizationServiceConfig{}, testLogger())
	if err != nil {
		t.Fatalf("build rbac yaml state: %v", err)
	}
	signer, ok := NewEvalRunnerSigner(newTestTrustSigner(t), testLogger()).(*evalRunnerSigner)
	if !ok {
		t.Fatal("NewEvalRunnerSigner did not return *evalRunnerSigner")
	}
	signer.SetPrincipalResolver(rbac.NewComposer(yamlState, &stubDBSource{}, testLogger()), nil)
	_, err = signer.SignTask(t.Context(), eval.TaskPrincipal{Subject: rbac.EvalSystemUserSub}, "task-transient")
	if !errors.Is(err, ErrEvalPrincipalUnauthorized) {
		t.Fatalf("err = %v, want ErrEvalPrincipalUnauthorized on a lookup failure (fail closed)", err)
	}
	if !strings.Contains(err.Error(), "authorization lookup failed") {
		t.Errorf("err = %v, want it to name the lookup failure rather than an absent grant", err)
	}
}

func TestEvalSignerDoesNotCacheRefusal(t *testing.T) {
	t.Parallel()
	const principal = "system:qa-bot"
	source := &stubDBSource{}
	yamlState, buildErr := BuildRBACYAMLState(oidcauth.AuthorizationServiceConfig{}, testLogger())
	if buildErr != nil {
		t.Fatalf("build rbac yaml state: %v", buildErr)
	}
	composer := rbac.NewComposer(yamlState, source, testLogger())
	if rebuildErr := composer.Rebuild(t.Context()); rebuildErr != nil {
		t.Fatalf("composer rebuild: %v", rebuildErr)
	}
	signer, ok := NewEvalRunnerSigner(newTestTrustSigner(t), testLogger()).(*evalRunnerSigner)
	if !ok {
		t.Fatal("NewEvalRunnerSigner did not return *evalRunnerSigner")
	}
	signer.SetPrincipalResolver(composer, nil)

	if _, denied := signer.SignTask(t.Context(), eval.TaskPrincipal{Subject: principal}, "task-before-grant"); !errors.Is(denied, ErrEvalPrincipalUnauthorized) {
		t.Fatalf("err = %v, want a refusal before the grant exists", denied)
	}

	source.set(dbSystemUserGrant(principal, "eval_qa_bot", "agent:JiraAgent:invoke"))
	if rebuildErr := composer.Rebuild(t.Context()); rebuildErr != nil {
		t.Fatalf("composer rebuild after grant: %v", rebuildErr)
	}

	token, err := signer.SignTask(t.Context(), eval.TaskPrincipal{Subject: principal}, "task-after-grant")
	if err != nil {
		t.Fatalf("SignTask after grant: %v — the refusal was cached, so remediation needs a restart", err)
	}
	if got := decodeTokenClaims(t, token).Scopes; !slices.Equal(got, []string{"agent:JiraAgent:invoke"}) {
		t.Errorf("scopes = %v, want the newly granted role's scopes", got)
	}
}

func TestEvalSignerConcurrentSignTask(t *testing.T) {
	t.Parallel()
	s, _ := newBoundEvalSigner(t, dbSystemUserGrant("system:qa-bot", "eval_qa_bot", "agent:JiraAgent:invoke"))
	principals := []string{rbac.EvalSystemUserSub, "system:qa-bot", "system:never-provisioned"}

	var wg sync.WaitGroup
	subjects := make([]string, 24)
	errs := make([]error, 24)
	for i := range subjects {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			token, err := s.SignTask(t.Context(), eval.TaskPrincipal{Subject: principals[i%len(principals)]}, fmt.Sprintf("task-%d", i))
			if err != nil {
				errs[i] = err
				return
			}
			subjects[i] = decodeTokenClaims(t, token).Sub
		}(i)
	}
	wg.Wait()

	for i := range subjects {
		want := principals[i%len(principals)]
		if want == "system:never-provisioned" {
			if !errors.Is(errs[i], ErrEvalPrincipalUnauthorized) {
				t.Errorf("task-%d (%s): err = %v, want ErrEvalPrincipalUnauthorized", i, want, errs[i])
			}
			continue
		}
		if errs[i] != nil {
			t.Errorf("task-%d (%s): %v", i, want, errs[i])
			continue
		}
		if subjects[i] != want {
			t.Errorf("task-%d sub = %q, want %q", i, subjects[i], want)
		}
	}
}

func TestRegisterRoutesBindsEvalSignerToComposer(t *testing.T) {
	t.Parallel()
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	core := platpkg.NewPlatformCore(
		map[string]any{
			"namespace":    "test-ns",
			"database_url": platformCoreDBURL(t),
		},
		platpkg.ExtensionPoints{},
	)
	rt := runtime.NewRuntime(brk, nil, runtime.WithLogger(testLogger()))
	core.SetServices(rt.Services())
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatalf("init platform core: %v", err)
	}

	yamlState, err := BuildRBACYAMLState(oidcauth.AuthorizationServiceConfig{}, testLogger())
	if err != nil {
		t.Fatalf("build rbac yaml state: %v", err)
	}
	deps := &RouteDeps{
		IsRBACAuthority: true,
		RBACYAMLState:   yamlState,
		EvalTaskSigner:  NewEvalRunnerSigner(newTestTrustSigner(t), testLogger()),
	}
	RegisterRoutesWithDeps(http.NewServeMux(), core, deps)

	token, err := deps.EvalTaskSigner.SignTask(t.Context(), eval.TaskPrincipal{Subject: rbac.EvalSystemUserSub}, "task-wired")
	if err != nil {
		t.Fatalf("SignTask after route registration: %v — the composer was never bound to the eval signer", err)
	}
	if got := decodeTokenClaims(t, token); got.Sub != rbac.EvalSystemUserSub {
		t.Errorf("sub = %q, want %q", got.Sub, rbac.EvalSystemUserSub)
	}
}

type countingMetrics struct {
	mu    sync.Mutex
	calls []string
}

func (m *countingMetrics) Counter(name string, _ int64, labels ...string) {
	m.mu.Lock()
	m.calls = append(m.calls, name+"|"+strings.Join(labels, ","))
	m.mu.Unlock()
}

func (m *countingMetrics) recorded() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.calls...)
}

func TestEvalSignerRefreshesStaleSnapshotBeforeRefusing(t *testing.T) {
	t.Parallel()
	const principal = "system:qa-bot"
	signer, source := newBoundEvalSigner(t, nil)

	if _, denied := signer.SignTask(t.Context(), eval.TaskPrincipal{Subject: principal}, "task-ungranted"); !errors.Is(denied, ErrEvalPrincipalUnauthorized) {
		t.Fatalf("err = %v, want a refusal while the principal genuinely holds nothing", denied)
	}

	source.set(dbSystemUserGrant(principal, "eval_qa_bot", "agent:JiraAgent:invoke"))

	token, err := signer.SignTask(t.Context(), eval.TaskPrincipal{Subject: principal}, "task-granted-elsewhere")
	if err != nil {
		t.Fatalf("SignTask: %v — a grant committed without a local rebuild must still resolve, or an operator on the wrong replica is stuck until a restart", err)
	}
	if got := decodeTokenClaims(t, token).Scopes; !slices.Equal(got, []string{"agent:JiraAgent:invoke"}) {
		t.Errorf("scopes = %v, want the newly granted role's scopes", got)
	}
}

func TestEvalSignerCountsRefusals(t *testing.T) {
	t.Parallel()
	metrics := &countingMetrics{}
	source := &stubDBSource{}
	signer, ok := NewEvalRunnerSigner(newTestTrustSigner(t), testLogger()).(*evalRunnerSigner)
	if !ok {
		t.Fatal("NewEvalRunnerSigner did not return *evalRunnerSigner")
	}
	signer.SetPrincipalResolver(newEvalComposer(t, source), metrics)

	if _, err := signer.SignTask(t.Context(), eval.TaskPrincipal{Subject: "system:never-provisioned"}, "task-deny"); !errors.Is(err, ErrEvalPrincipalUnauthorized) {
		t.Fatalf("err = %v, want a refusal", err)
	}
	want := metricEvalPrincipalRefusals + "|reason,no_scopes"
	if got := metrics.recorded(); !slices.Contains(got, want) {
		t.Errorf("counters = %v, want one containing %q", got, want)
	}

	metrics.mu.Lock()
	metrics.calls = nil
	metrics.mu.Unlock()
	if _, err := signer.SignTask(t.Context(), eval.TaskPrincipal{Subject: rbac.EvalSystemUserSub}, "task-ok"); err != nil {
		t.Fatalf("SignTask(system:eval): %v", err)
	}
	if got := metrics.recorded(); len(got) != 0 {
		t.Errorf("counters after a successful sign = %v, want none", got)
	}
}

type rebuildCountingResolver struct {
	inner    evalPrincipalResolver
	mu       sync.Mutex
	rebuilds int
}

func (r *rebuildCountingResolver) Lookup(userID, gatewayID string, gatewayContext map[string]any) (rbac.Resolution, error) {
	return r.inner.Lookup(userID, gatewayID, gatewayContext)
}

func (r *rebuildCountingResolver) Rebuild(ctx context.Context) error {
	r.mu.Lock()
	r.rebuilds++
	r.mu.Unlock()
	return r.inner.Rebuild(ctx)
}

func (r *rebuildCountingResolver) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.rebuilds
}

func TestEvalSignerTeardownDoesNotRebuild(t *testing.T) {
	t.Parallel()
	const principal = "system:never-provisioned"
	resolver := &rebuildCountingResolver{inner: newEvalComposer(t, &stubDBSource{})}
	signer, ok := NewEvalRunnerSigner(newTestTrustSigner(t), testLogger()).(*evalRunnerSigner)
	if !ok {
		t.Fatal("NewEvalRunnerSigner did not return *evalRunnerSigner")
	}
	signer.SetPrincipalResolver(resolver, nil)

	if _, err := signer.SignTask(t.Context(), eval.TaskPrincipal{Subject: principal}, "task-publish"); !errors.Is(err, ErrEvalPrincipalUnauthorized) {
		t.Fatalf("SignTask err = %v, want a refusal", err)
	}
	if got := resolver.count(); got != 1 {
		t.Fatalf("rebuilds after SignTask = %d, want 1 — the cross-replica refresh is the point of the publish path", got)
	}

	for i := range 5 {
		if _, err := signer.SignTeardown(t.Context(), principal, fmt.Sprintf("task-cancel-%d", i)); !errors.Is(err, ErrEvalPrincipalUnauthorized) {
			t.Fatalf("SignTeardown[%d] err = %v, want the same refusal", i, err)
		}
	}
	if got := resolver.count(); got != 1 {
		t.Errorf("rebuilds after 5 teardowns = %d, want 1 — each straggler cancel would otherwise pay a whole-RBAC snapshot", got)
	}
}

func TestEvalSignerTeardownStillSignsGrantedPrincipal(t *testing.T) {
	t.Parallel()
	const principal = "system:qa-bot"
	signer, _ := newBoundEvalSigner(t, dbSystemUserGrant(principal, "eval_qa_bot", "agent:JiraAgent:invoke"))

	token, err := signer.SignTeardown(t.Context(), principal, "task-cancel")
	if err != nil {
		t.Fatalf("SignTeardown: %v", err)
	}
	claims := decodeTokenClaims(t, token)
	if claims.Sub != principal {
		t.Errorf("sub = %q, want %q — the agent authorizes tasks/cancel on this", claims.Sub, principal)
	}
	if got := claims.Scopes; !slices.Equal(got, []string{"agent:JiraAgent:invoke"}) {
		t.Errorf("scopes = %v, want the granted role's scopes", got)
	}
}

func rawTokenPayload(t *testing.T, token string) map[string]any {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("token has %d parts, want 3", len(parts))
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	var claims map[string]any
	if err := json.Unmarshal(raw, &claims); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	return claims
}

func TestTrustTaskSigner_SignsTheClaimsTaskSignClaimsNames(t *testing.T) {
	t.Parallel()
	signer := newTestTrustSigner(t)
	identity := gwauth.NewTestSubject(gwauth.SubjectFields{
		UserID:       "system:ci-deployer",
		Email:        "",
		Name:         "system:ci-deployer",
		AuthMethod:   gwauth.APIKeyAuthMethod,
		CredentialID: "key-1",
		Roles:        []string{"ci"},
		Scopes:       []string{"agent:*:invoke"},
	})

	token, err := gatewayTaskSignerFromTrust(signer).SignTask(identity, "task-42")
	if err != nil {
		t.Fatalf("SignTask: %v", err)
	}
	want, err := signer.Sign(identity.TaskSignClaims("task-42"))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	got, wantClaims := rawTokenPayload(t, token), rawTokenPayload(t, want)
	if got["auth_method"] != "api_key" || got["credential_id"] != "key-1" {
		t.Errorf("auth_method, credential_id = %v, %v, want api_key, key-1: the key is dropped from every run it triggers",
			got["auth_method"], got["credential_id"])
	}
	for _, perToken := range []string{"iat", "exp", "nbf", "jti"} {
		delete(got, perToken)
		delete(wantClaims, perToken)
	}
	if !reflect.DeepEqual(got, wantClaims) {
		t.Errorf("claims differ from TaskSignClaims:\n got %v\nwant %v", got, wantClaims)
	}
}

type failingRebuildResolver struct {
	inner evalPrincipalResolver
}

func (r failingRebuildResolver) Lookup(userID, gatewayID string, gatewayContext map[string]any) (rbac.Resolution, error) {
	return r.inner.Lookup(userID, gatewayID, gatewayContext)
}

func (failingRebuildResolver) Rebuild(context.Context) error {
	return errors.New("rbac source unreachable")
}

func TestEvalSignerRefreshFailureKeepsTheActionableRefusal(t *testing.T) {
	t.Parallel()
	var logs bytes.Buffer
	metrics := &countingMetrics{}
	signer, ok := NewEvalRunnerSigner(newTestTrustSigner(t), slog.New(slog.NewTextHandler(&logs, nil))).(*evalRunnerSigner)
	if !ok {
		t.Fatal("NewEvalRunnerSigner did not return *evalRunnerSigner")
	}
	signer.SetPrincipalResolver(failingRebuildResolver{inner: newEvalComposer(t, &stubDBSource{})}, metrics)

	_, err := signer.SignTask(t.Context(), eval.TaskPrincipal{Subject: "system:never-provisioned"}, "task-deny")
	if !errors.Is(err, ErrEvalPrincipalUnauthorized) || !strings.Contains(err.Error(), "holds no RBAC scopes") {
		t.Fatalf("err = %v, want the actionable no-scopes refusal", err)
	}
	got := metrics.recorded()
	if !slices.Contains(got, metricEvalPrincipalRefusals+"|reason,no_scopes") || slices.Contains(got, metricEvalPrincipalRefusals+"|reason,lookup_failed") {
		t.Errorf("counters = %v, want no_scopes and not lookup_failed", got)
	}
	if !strings.Contains(logs.String(), "authorization snapshot refresh failed") {
		t.Errorf("logs = %q, want the refresh failure named", logs.String())
	}
}
