// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcpauth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/atrest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/trust"
	"github.com/SolaceProducts/solace-agent-mesh/internal/trust/trusttest"
)

var errInjected = errors.New("injected")

type flakyRepo struct {
	RecordRepo
	failPutState, failTakeState, failTakeByVerifier, failPutCode, failPutRefresh, failRotate, rotateLost bool
}

func (f *flakyRepo) PutState(ctx context.Context, gw, sh, vh, d string, exp, now int64) error {
	if f.failPutState {
		return errInjected
	}
	return f.RecordRepo.PutState(ctx, gw, sh, vh, d, exp, now)
}

func (f *flakyRepo) TakeState(ctx context.Context, gw, sh string, now int64) (string, bool, error) {
	if f.failTakeState {
		return "", false, errInjected
	}
	return f.RecordRepo.TakeState(ctx, gw, sh, now)
}

func (f *flakyRepo) TakeStateByVerifier(ctx context.Context, gw, vh string, now int64) (string, bool, error) {
	if f.failTakeByVerifier {
		return "", false, errInjected
	}
	return f.RecordRepo.TakeStateByVerifier(ctx, gw, vh, now)
}

func (f *flakyRepo) PutCode(ctx context.Context, gw, ch, d string, exp, now int64) error {
	if f.failPutCode {
		return errInjected
	}
	return f.RecordRepo.PutCode(ctx, gw, ch, d, exp, now)
}

func (f *flakyRepo) PutRefresh(ctx context.Context, gw, th, d string, exp, now int64) error {
	if f.failPutRefresh {
		return errInjected
	}
	return f.RecordRepo.PutRefresh(ctx, gw, th, d, exp, now)
}

func (f *flakyRepo) RotateRefresh(ctx context.Context, gw, oh, nh, d string, exp, now int64) (bool, error) {
	if f.failRotate {
		return false, errInjected
	}
	if f.rotateLost {
		return false, nil
	}
	return f.RecordRepo.RotateRefresh(ctx, gw, oh, nh, d, exp, now)
}

type failingAuthURLIDP struct{ stateEchoIDP }

func (failingAuthURLIDP) BuildAuthURL(context.Context, string, string, string, string) (string, error) {
	return "", errInjected
}

func flakyHandler(t *testing.T, idp IDPClient) (*Handler, *flakyRepo) {
	t.Helper()
	box, _ := atrest.New("s", "mcp-oauth")
	repo := &flakyRepo{RecordRepo: store.NewMCPOAuthRepo(store.TestDB(t))}
	return New(Config{EnableAuth: true, GatewayID: "gw", IDPClient: idp, SAMTokenMinter: &fakeMinter{},
		Host: "localhost", Port: 8090, Records: repo, RecordBox: box}), repo
}

func authorize(t *testing.T, h *Handler) (int, string) {
	t.Helper()
	q := url.Values{"redirect_uri": {"http://localhost:9000/cb"}}
	rr := httptest.NewRecorder()
	h.HandleAuthorize(rr, httptest.NewRequest(http.MethodGet, PathAuthorize+"?"+q.Encode(), nil))
	loc, _ := url.Parse(rr.Header().Get("Location"))
	return rr.Code, loc.Query().Get("state")
}

func callback(h *Handler, state string) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	h.HandleCallback(rr, httptest.NewRequest(http.MethodGet, PathCallback+"?code=idp&state="+url.QueryEscape(state), nil))
	return rr
}

func TestAuthorizeStoreFailures(t *testing.T) {
	h, repo := flakyHandler(t, stateEchoIDP{})
	repo.failPutState = true
	if code, _ := authorize(t, h); code != http.StatusServiceUnavailable {
		t.Errorf("authorize with a failing state write = %d, want 503", code)
	}
}

func TestAuthorizeIdPURLFailureWithAFailingStateDrop(t *testing.T) {
	h, repo := flakyHandler(t, failingAuthURLIDP{})
	repo.failTakeState = true
	if code, _ := authorize(t, h); code != http.StatusServiceUnavailable {
		t.Errorf("authorize with a failing IdP URL = %d, want 503", code)
	}
}

func TestCallbackCodeWriteFailureRedirectsWithServerError(t *testing.T) {
	h, repo := flakyHandler(t, stateEchoIDP{})
	_, state := authorize(t, h)
	repo.failPutCode = true
	rr := callback(h, state)
	if rr.Code != http.StatusFound || !strings.Contains(rr.Header().Get("Location"), "error=server_error") {
		t.Errorf("callback = %d %s, want a redirect with server_error", rr.Code, rr.Header().Get("Location"))
	}
}

func codeFromLogin(t *testing.T, h *Handler) string {
	t.Helper()
	_, state := authorize(t, h)
	loc, _ := url.Parse(callback(h, state).Header().Get("Location"))
	return loc.Query().Get("code")
}

func TestTokenStoreFailures(t *testing.T) {
	h, repo := flakyHandler(t, stateEchoIDP{})
	code := codeFromLogin(t, h)
	repo.failPutRefresh = true
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {"http://localhost:9000/cb"}}
	if status, _ := tokenRequest(t, h, form); status != http.StatusServiceUnavailable {
		t.Errorf("code exchange with a failing refresh write = %d, want 503", status)
	}

	repo.failPutRefresh = false
	rt, err := h.storeMCPRefreshToken(t.Context(), "idp-rt")
	if err != nil {
		t.Fatal(err)
	}
	refresh := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {rt}}
	repo.failRotate = true
	if status, _ := tokenRequest(t, h, refresh); status != http.StatusServiceUnavailable {
		t.Errorf("refresh with a failing rotation = %d, want 503", status)
	}
	repo.failRotate, repo.rotateLost = false, true
	if status, body := tokenRequest(t, h, refresh); status != http.StatusBadRequest || body["error"] != "invalid_grant" {
		t.Errorf("refresh that lost the rotation = %d %v, want invalid_grant", status, body)
	}
}

func TestFinishStoreFailureFailsClosed(t *testing.T) {
	km, err := trust.NewKeyManager("mcp-a")
	if err != nil {
		t.Fatal(err)
	}
	signer := trust.NewSigner(km, "mcp-a", time.Hour, 2*time.Hour)
	box, _ := atrest.New("s", "mcp-oauth")
	repo := &flakyRepo{RecordRepo: store.NewMCPOAuthRepo(store.TestDB(t)), failTakeByVerifier: true}
	h := New(Config{
		EnableAuth: true, GatewayID: "mcp-a", StateRedirectionEnabled: true,
		OAuthDispatcherURL: "https://example.com/api/v1/auth/callback",
		IDPClient:          fakeIDPClient{}, SAMTokenMinter: &fakeMinter{},
		StateSigner:   trust.NewStateSigner(signer),
		StateVerifier: trusttest.NewStateVerifier(km, "mcp-a", map[string]bool{"example.com": true}),
		Records:       repo, RecordBox: box,
	})
	state := mintStateForFinish(t, signer, "mcp-a", verifierHash("v"))
	rr := httptest.NewRecorder()
	h.HandleFinish(rr, httptest.NewRequest(http.MethodGet, PathFinish+"?code=c&state="+url.QueryEscape(state), nil))
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("finish with a failing state read = %d, want 503", rr.Code)
	}
}
