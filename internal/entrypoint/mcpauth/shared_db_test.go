// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcpauth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/atrest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

type stateEchoIDP struct{ fakeIDPClient }

func (stateEchoIDP) BuildAuthURL(_ context.Context, state, _, _, _ string) (string, error) {
	return "https://idp.example.com/authorize?state=" + url.QueryEscape(state), nil
}

func newSharedDBHandler(t *testing.T, db *store.DB) *Handler {
	t.Helper()
	box, err := atrest.New("shared-secret", "mcp-oauth")
	if err != nil {
		t.Fatal(err)
	}
	return New(Config{
		EnableAuth:           true,
		GatewayID:            "mcp-gw",
		IDPClient:            stateEchoIDP{},
		SAMTokenMinter:       &fakeMinter{},
		AuthzResolver:        fakeResolver{},
		RequirePKCE:          true,
		OAuthStateTTLSeconds: 300,
		OAuthCodeTTLSeconds:  300,
		Host:                 "localhost",
		Port:                 8090,
		Records:              store.NewMCPOAuthRepo(db),
		RecordBox:            box,
	})
}

func tokenRequest(t *testing.T, h *Handler, form url.Values) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, PathToken, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	h.HandleToken(rr, req)
	var body map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	return rr.Code, body
}

func TestLoginStepsOnDifferentGWEs(t *testing.T) {
	db := store.TestDB(t)
	a, b := newSharedDBHandler(t, db), newSharedDBHandler(t, db)
	const clientRedirect = "http://localhost:9000/cb"
	clientVerifier := "client-verifier-0123456789-0123456789-0123456789"

	q := url.Values{}
	q.Set("redirect_uri", clientRedirect)
	q.Set("state", "client-state")
	q.Set("code_challenge", ComputeS256Challenge(clientVerifier))
	q.Set("code_challenge_method", "S256")
	rr := httptest.NewRecorder()
	a.HandleAuthorize(rr, httptest.NewRequest(http.MethodGet, PathAuthorize+"?"+q.Encode(), nil))
	if rr.Code != http.StatusFound {
		t.Fatalf("authorize on A = %d %s", rr.Code, rr.Body.String())
	}
	idpURL, _ := url.Parse(rr.Header().Get("Location"))
	internalState := idpURL.Query().Get("state")

	callback := PathCallback + "?code=idp-code&state=" + url.QueryEscape(internalState)
	rr = httptest.NewRecorder()
	b.HandleCallback(rr, httptest.NewRequest(http.MethodGet, callback, nil))
	if rr.Code != http.StatusFound {
		t.Fatalf("callback on B = %d %s", rr.Code, rr.Body.String())
	}
	clientURL, _ := url.Parse(rr.Header().Get("Location"))
	code := clientURL.Query().Get("code")
	if code == "" || clientURL.Query().Get("state") != "client-state" {
		t.Fatalf("callback redirect = %s", clientURL)
	}

	rr = httptest.NewRecorder()
	a.HandleCallback(rr, httptest.NewRequest(http.MethodGet, callback, nil))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("replayed callback on A = %d, want 400", rr.Code)
	}

	status, body := tokenRequest(t, a, url.Values{
		"grant_type": {"authorization_code"}, "code": {code},
		"redirect_uri": {clientRedirect}, "code_verifier": {clientVerifier},
	})
	if status != http.StatusOK || body["access_token"] != "sam-token" {
		t.Fatalf("code exchange on A = %d %v", status, body)
	}
	rt1, _ := body["refresh_token"].(string)

	if again, _ := tokenRequest(t, b, url.Values{
		"grant_type": {"authorization_code"}, "code": {code},
		"redirect_uri": {clientRedirect}, "code_verifier": {clientVerifier},
	}); again != http.StatusBadRequest {
		t.Errorf("second code exchange on B = %d, want 400", again)
	}

	status, body = tokenRequest(t, b, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {rt1}})
	if status != http.StatusOK {
		t.Fatalf("refresh on B = %d %v", status, body)
	}
	rt2, _ := body["refresh_token"].(string)
	if rt2 == "" || rt2 == rt1 {
		t.Fatalf("refresh did not rotate: %q", rt2)
	}

	if status, body := tokenRequest(t, a, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {rt1}}); status != http.StatusBadRequest || body["error"] != "invalid_grant" {
		t.Errorf("old refresh token on A = %d %v, want invalid_grant", status, body)
	}
	if status, _ := tokenRequest(t, a, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {rt2}}); status != http.StatusOK {
		t.Errorf("new refresh token on A = %d", status)
	}
}

func TestRefreshSurvivesRestart(t *testing.T) {
	db := store.TestDB(t)
	before := newSharedDBHandler(t, db)
	rt, err := before.storeMCPRefreshToken(t.Context(), "idp-rt")
	if err != nil {
		t.Fatal(err)
	}
	after := newSharedDBHandler(t, db)
	if status, body := tokenRequest(t, after, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {rt}}); status != http.StatusOK {
		t.Errorf("refresh after restart = %d %v", status, body)
	}
}

type flakyRefreshIDP struct {
	stateEchoIDP
	fail bool
}

func (f *flakyRefreshIDP) RefreshToken(ctx context.Context, rt string) (*IDPTokens, error) {
	if f.fail {
		return nil, errors.New("idp unavailable")
	}
	return f.stateEchoIDP.RefreshToken(ctx, rt)
}

func TestRefreshKeepsMappingWhenIDPFails(t *testing.T) {
	db := store.TestDB(t)
	h := newSharedDBHandler(t, db)
	idp := &flakyRefreshIDP{fail: true}
	h.idp = idp
	rt, err := h.storeMCPRefreshToken(t.Context(), "idp-rt")
	if err != nil {
		t.Fatal(err)
	}
	form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {rt}}
	if status, body := tokenRequest(t, h, form); status != http.StatusBadRequest || body["error"] != "invalid_grant" {
		t.Fatalf("refresh with IdP down = %d %v, want invalid_grant", status, body)
	}
	idp.fail = false
	if status, body := tokenRequest(t, h, form); status != http.StatusOK {
		t.Errorf("refresh after IdP recovered = %d %v, want 200", status, body)
	}
}

func TestRefreshAfterSecretChangeAsksForLogin(t *testing.T) {
	db := store.TestDB(t)
	rt, err := newSharedDBHandler(t, db).storeMCPRefreshToken(t.Context(), "idp-rt")
	if err != nil {
		t.Fatal(err)
	}
	after := newSharedDBHandler(t, db)
	other, _ := atrest.New("new-secret", "mcp-oauth")
	after.store.(*dbStore).box = other
	if status, body := tokenRequest(t, after, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {rt}}); status != http.StatusBadRequest || body["error"] != "invalid_grant" {
		t.Errorf("refresh under a new secret = %d %v, want invalid_grant", status, body)
	}
}

type failingRepo struct{ RecordRepo }

func (failingRepo) CountLiveStates(context.Context, string, int64) (int, error) {
	return 0, context.DeadlineExceeded
}
func (failingRepo) TakeState(context.Context, string, string, int64) (string, bool, error) {
	return "", false, context.DeadlineExceeded
}
func (failingRepo) TakeCode(context.Context, string, string, int64) (string, bool, error) {
	return "", false, context.DeadlineExceeded
}
func (failingRepo) GetRefresh(context.Context, string, string, int64) (string, bool, error) {
	return "", false, context.DeadlineExceeded
}

func TestStoreErrorsFailClosed(t *testing.T) {
	box, _ := atrest.New("s", "mcp-oauth")
	h := New(Config{EnableAuth: true, GatewayID: "gw", IDPClient: stateEchoIDP{}, SAMTokenMinter: &fakeMinter{},
		Host: "localhost", Port: 8090, Records: failingRepo{}, RecordBox: box})

	q := url.Values{"redirect_uri": {"http://localhost:9000/cb"}}
	rr := httptest.NewRecorder()
	h.HandleAuthorize(rr, httptest.NewRequest(http.MethodGet, PathAuthorize+"?"+q.Encode(), nil))
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("authorize = %d, want 503", rr.Code)
	}
	rr = httptest.NewRecorder()
	h.HandleCallback(rr, httptest.NewRequest(http.MethodGet, PathCallback+"?code=c&state=s", nil))
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("callback = %d, want 503", rr.Code)
	}
	if status, _ := tokenRequest(t, h, url.Values{"grant_type": {"authorization_code"}, "code": {"c"}}); status != http.StatusServiceUnavailable {
		t.Errorf("code exchange = %d, want 503", status)
	}
	if status, _ := tokenRequest(t, h, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {"r"}}); status != http.StatusServiceUnavailable {
		t.Errorf("refresh = %d, want 503", status)
	}
}
