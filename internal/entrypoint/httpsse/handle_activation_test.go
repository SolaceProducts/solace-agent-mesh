// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build solace_id

package httpsse

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SolaceProducts/solace-agent-mesh/internal/activation"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/tokenbroker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/solaceid"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spotdraft"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spotdraft/spotdrafttest"
)

const desktopOrigin = "wails://wails"

var (
	ada = activation.SignInResult{
		Subject: "auth0|ada", Email: "ada@example.com", Name: "Ada Lovelace",
		Tokens: activation.FreeModelTokens{RefreshToken: "refresh-ada", AccessToken: "access-ada"},
	}
	signedInAda = activation.Record{
		Account: &activation.Account{Subject: ada.Subject, Email: ada.Email, Name: ada.Name, SignedInAt: time.Now().UTC()},
	}
	activeAda = activation.Record{
		Account: signedInAda.Account,
		EULA:    &activation.Acceptance{Subject: ada.Subject, AgreementID: "agreement-1", AcceptedAt: time.Now().UTC()},
	}
	acceptEULA      = `{"freeModel":false}`
	acceptFreeModel = `{"freeModel":true}`
)

type signInOutcome struct {
	result activation.SignInResult
	err    error
}

type browserSignIn struct {
	outcomes chan signInOutcome
}

func newBrowserSignIn() *browserSignIn {
	return &browserSignIn{outcomes: make(chan signInOutcome, 1)}
}

func (b *browserSignIn) SignIn(ctx context.Context) (activation.SignInResult, error) {
	select {
	case o := <-b.outcomes:
		return o.result, o.err
	case <-ctx.Done():
		return activation.SignInResult{}, ctx.Err()
	}
}

func (b *browserSignIn) finishAs(result activation.SignInResult) {
	b.outcomes <- signInOutcome{result: result}
}

func (b *browserSignIn) fail(err error) {
	b.outcomes <- signInOutcome{err: err}
}

func activationAdapter(t *testing.T, record activation.Record) (*HTTPSSEAdapter, *activation.Service) {
	t.Helper()
	return activationAdapterSigningIn(t, record, nil)
}

func activationAdapterSigningIn(t *testing.T, record activation.Record, client activation.SignInClient, opts ...activation.Option) (*HTTPSSEAdapter, *activation.Service) {
	t.Helper()
	store := activation.NewStore(t.TempDir())
	require.NoError(t, store.Save(record))
	if len(opts) == 0 {
		_, linked := testSpotDraft(t)
		opts = []activation.Option{activation.WithFreeModel(testFreeModel), linked}
	}
	svc := activation.NewService(store, client, slog.New(slog.DiscardHandler), opts...)
	adapter := newCoverageAdapter(t, func(core *entrypoint.GatewayCore) { core.SetActivation(svc) })
	svc.SetFreeModel(platformFreeModel{})
	return adapter, svc
}

const testEULAClickwrap = "eula-1"

func testSpotDraft(t *testing.T) (*spotdrafttest.Fake, activation.Option) {
	t.Helper()
	fake := spotdrafttest.New()
	fake.AddClickwrap(testEULAClickwrap, "End User License Agreement", "LLM Credits Terms")
	server := httptest.NewServer(fake.Handler())
	t.Cleanup(server.Close)
	agreements := activation.Agreements{BaseURL: server.URL, EULAClickwrapID: testEULAClickwrap}
	return fake, activation.WithAgreements(agreements, spotdraft.New(server.URL, spotdraft.WithFirstRetryDelay(time.Millisecond)))
}

var testFreeModel = activation.FreeModelSettings{GrantID: "1c58a105-c275-4401-b9af-9b1af81a80af", Provider: "openai", APIBase: "https://llm.example.test/v1", GeneralModelName: "free", PlanningModelName: "free-planning"}

type platformFreeModel struct{}

func (platformFreeModel) SaveGrant(context.Context, activation.SignInResult) error { return nil }
func (platformFreeModel) Refresh(context.Context) error                            { return nil }
func (platformFreeModel) DeleteGrant(context.Context) error                        { return nil }

func callActivation(t *testing.T, adapter *HTTPSSEAdapter, method, path, origin, body string) (*httptest.ResponseRecorder, ActivationResponse) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	rec := httptest.NewRecorder()
	adapter.Handler().ServeHTTP(rec, req)
	var resp ActivationResponse
	if rec.Code/100 == 2 {
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	}
	return rec, resp
}

func awaitSignIn(t *testing.T, svc *activation.Service) {
	t.Helper()
	require.Eventually(t, func() bool { return !svc.Status().SigningIn }, 5*time.Second, 10*time.Millisecond)
}

func TestActivationReportsAFreshInstallAsSignedOut(t *testing.T) {
	adapter, _ := activationAdapter(t, activation.Record{})

	rec, resp := callActivation(t, adapter, http.MethodGet, pathActivation, desktopOrigin, "")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, ActivationResponse{
		State:              string(activation.StateSignedOut),
		Agreements:         ActivationAgreements{EulaClickwrapID: "eula-1"},
		FreeModelAvailable: true,
		FreeModel:          &ActivationFreeModel{GrantID: testFreeModel.GrantID, Provider: "openai", APIBase: testFreeModel.APIBase, GeneralModelName: "free", PlanningModelName: "free-planning"},
	}, resp)
	assert.JSONEq(t, `{"state":"signed_out","signingIn":false,`+
		`"agreements":{"eulaClickwrapId":"eula-1"},`+
		`"freeModelAvailable":true,"freeModelChosen":false,`+
		`"freeModel":{"grantId":"`+testFreeModel.GrantID+`","provider":"openai","apiBase":"`+testFreeModel.APIBase+`","generalModelName":"free","planningModelName":"free-planning"}}`, rec.Body.String())
}

func TestActivationNamesTheSignedInAccount(t *testing.T) {
	adapter, _ := activationAdapter(t, signedInAda)

	rec, resp := callActivation(t, adapter, http.MethodGet, pathActivation, desktopOrigin, "")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, &ActivationAccount{Subject: ada.Subject, Email: ada.Email, Name: ada.Name}, resp.Account)
	assert.Contains(t, rec.Body.String(), `"account":{"subject":"auth0|ada","email":"ada@example.com","name":"Ada Lovelace"}`)
}

func TestConfigTellsTheWebUIToShowTheGate(t *testing.T) {
	adapter, _ := activationAdapter(t, activation.Record{})

	rec := httptest.NewRecorder()
	adapter.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/config", nil))

	require.Equal(t, http.StatusOK, rec.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, true, resp["activationGateEnabled"])
}

func TestSigningInThenAcceptingFromTheAppActivates(t *testing.T) {
	browser := newBrowserSignIn()
	adapter, svc := activationAdapterSigningIn(t, activation.Record{}, browser)

	rec, resp := callActivation(t, adapter, http.MethodPost, pathActivationSignIn, desktopOrigin, `{}`)
	require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())
	assert.Equal(t, string(activation.StateSignedOut), resp.State)
	assert.True(t, resp.SigningIn)

	browser.finishAs(ada)
	awaitSignIn(t, svc)
	rec, resp = callActivation(t, adapter, http.MethodGet, pathActivation, desktopOrigin, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, string(activation.StateEULAPending), resp.State)
	assert.Nil(t, resp.SignInFailure)
	assert.False(t, svc.Active())

	rec, resp = callActivation(t, adapter, http.MethodPost, pathActivationAccept, desktopOrigin, acceptEULA)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, string(activation.StateActive), resp.State)
	assert.True(t, svc.Active())
}

func TestAFailedSignInIsReportedByTheStatus(t *testing.T) {
	browser := newBrowserSignIn()
	adapter, svc := activationAdapterSigningIn(t, activation.Record{}, browser)
	rec, _ := callActivation(t, adapter, http.MethodPost, pathActivationSignIn, desktopOrigin, `{}`)
	require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())

	browser.fail(solaceid.ErrDenied)
	awaitSignIn(t, svc)
	rec, resp := callActivation(t, adapter, http.MethodGet, pathActivation, desktopOrigin, "")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, string(activation.StateSignedOut), resp.State)
	assert.False(t, resp.SigningIn)
	assert.Equal(t, &ActivationSignInFailure{Code: "denied", Message: "Solace ID did not complete the sign-in. Try again."}, resp.SignInFailure)
	assert.Contains(t, rec.Body.String(), `"signInFailure":{"code":"denied","message":"Solace ID did not complete the sign-in. Try again."}`)
}

func TestABuildWithoutTheFreeModelOffersOnlyYourOwn(t *testing.T) {
	_, linked := testSpotDraft(t)
	adapter, svc := activationAdapterSigningIn(t, signedInAda, nil, linked)

	rec, resp := callActivation(t, adapter, http.MethodGet, pathActivation, desktopOrigin, "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.False(t, resp.FreeModelAvailable)
	assert.Contains(t, rec.Body.String(), `"freeModelAvailable":false`)
	assert.Equal(t, testEULAClickwrap, resp.Agreements.EulaClickwrapID)

	rec, _ = callActivation(t, adapter, http.MethodPost, pathActivationAccept, desktopOrigin, acceptFreeModel)
	assert.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "not available in this build")
	assert.False(t, svc.Active())

	rec, _ = callActivation(t, adapter, http.MethodPost, pathActivationAccept, desktopOrigin, acceptEULA)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.True(t, svc.Active())
}

func TestAcceptingTheFreeModelTermsFromTheApp(t *testing.T) {
	adapter, svc := activationAdapter(t, signedInAda)

	rec, _ := callActivation(t, adapter, http.MethodPost, pathActivationAccept, desktopOrigin, acceptFreeModel)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.True(t, svc.Active())
	assert.True(t, svc.Status().FreeModelChosen)
}

func TestActivationChangesAreRefusedFromAnotherSite(t *testing.T) {
	routes := []struct {
		path, body string
		record     activation.Record
		signingIn  bool
		unchanged  func(activation.Status) bool
	}{
		{path: pathActivationSignIn, body: `{}`, unchanged: func(s activation.Status) bool { return s.State == activation.StateSignedOut && !s.SigningIn }},
		{path: pathActivationCancelSignIn, body: `{}`, signingIn: true, unchanged: func(s activation.Status) bool { return s.SigningIn }},
		{path: pathActivationAccept, body: acceptEULA, record: signedInAda, unchanged: func(s activation.Status) bool { return s.State == activation.StateEULAPending }},
	}
	requests := map[string]struct {
		shape  func(*http.Request)
		status int
	}{
		"json from another origin": {func(r *http.Request) { r.Header.Set("Origin", "https://attacker.example") }, http.StatusForbidden},
		"plain text from another origin": {func(r *http.Request) {
			r.Header.Set("Origin", "https://attacker.example")
			r.Header.Set("Content-Type", "text/plain")
		}, http.StatusUnsupportedMediaType},
		"origin that only looks local": {func(r *http.Request) { r.Header.Set("Origin", "http://wails.localhost.attacker.example") }, http.StatusForbidden},
		"opaque origin":                {func(r *http.Request) { r.Header.Set("Origin", "null") }, http.StatusForbidden},
		"no origin, cross-site":        {func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") }, http.StatusForbidden},
		"name rebound to this machine": {func(r *http.Request) {
			r.Header.Set("Origin", "http://evil.example:8800")
			r.Host = "evil.example:8800"
		}, http.StatusForbidden},
		"another local page": {func(r *http.Request) { r.Header.Set("Origin", "http://127.0.0.1:9999"); r.Host = "127.0.0.1:8800" }, http.StatusForbidden},
	}
	for _, route := range routes {
		for name, req := range requests {
			t.Run(route.path+" "+name, func(t *testing.T) {
				adapter, svc := activationAdapterSigningIn(t, route.record, newBrowserSignIn())
				t.Cleanup(svc.Close)
				if route.signingIn {
					rec, _ := callActivation(t, adapter, http.MethodPost, pathActivationSignIn, desktopOrigin, `{}`)
					require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())
				}
				r := httptest.NewRequest(http.MethodPost, route.path, strings.NewReader(route.body))
				r.Header.Set("Content-Type", "application/json")
				req.shape(r)
				rec := httptest.NewRecorder()

				adapter.Handler().ServeHTTP(rec, r)

				assert.Equal(t, req.status, rec.Code, rec.Body.String())
				assert.True(t, route.unchanged(svc.Status()), "the request must change nothing")
			})
		}
	}
}

func TestActivationIsUnreadableFromAnotherSite(t *testing.T) {
	adapter, _ := activationAdapter(t, activeAda)

	rec, _ := callActivation(t, adapter, http.MethodGet, pathActivation, "https://attacker.example", "")
	assert.Equal(t, http.StatusForbidden, rec.Code)

	req := httptest.NewRequest(http.MethodGet, pathActivation, nil)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	rec = httptest.NewRecorder()
	adapter.Handler().ServeHTTP(rec, req)
	assert.Equal(t, http.StatusForbidden, rec.Code)

	req = httptest.NewRequest(http.MethodGet, pathActivation, nil)
	req.Host = "evil.example:8800"
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec = httptest.NewRecorder()
	adapter.Handler().ServeHTTP(rec, req)
	assert.Equal(t, http.StatusForbidden, rec.Code)
	for _, accountValue := range []string{ada.Subject, ada.Email, ada.Name} {
		assert.NotContains(t, rec.Body.String(), accountValue)
	}
}

func TestActivationAnswersTheAppItsOwnPageAndNonBrowsers(t *testing.T) {
	adapter, _ := activationAdapter(t, activeAda)

	for name, shape := range map[string]func(*http.Request){
		"the desktop":            func(r *http.Request) { r.Header.Set("Origin", "wails://wails") },
		"the desktop on windows": func(r *http.Request) { r.Header.Set("Origin", "http://wails.localhost") },
		"own page at 127.0.0.1":  func(r *http.Request) { r.Header.Set("Origin", "http://127.0.0.1:8800"); r.Host = "127.0.0.1:8800" },
		"own page at localhost":  func(r *http.Request) { r.Header.Set("Origin", "http://localhost:8800"); r.Host = "localhost:8800" },
		"own page at [::1]":      func(r *http.Request) { r.Header.Set("Origin", "http://[::1]:8800"); r.Host = "[::1]:8800" },
		"not a browser":          func(r *http.Request) { r.Host = "127.0.0.1:8800" },
	} {
		t.Run(name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, pathActivation, nil)
			shape(r)
			rec := httptest.NewRecorder()

			adapter.Handler().ServeHTTP(rec, r)

			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			var resp ActivationResponse
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
			assert.Equal(t, string(activation.StateActive), resp.State)
		})
	}
}

func TestSigningInWithoutASolaceIDApplicationIsUnavailable(t *testing.T) {
	adapter, _ := activationAdapter(t, activation.Record{})

	rec, _ := callActivation(t, adapter, http.MethodPost, pathActivationSignIn, desktopOrigin, `{}`)

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

func TestAcceptingValidatesTheRequest(t *testing.T) {
	for body, want := range map[string]int{
		`{"freeModel":false,"extra":true}`:                http.StatusBadRequest,
		`{"freeModel":"yes"}`:                             http.StatusBadRequest,
		`{"eulaVersion":"placeholder"}`:                   http.StatusBadRequest,
		`{"freeModel":false,"agreementId":"agreement-1"}`: http.StatusBadRequest,
		`{"freeModel":false,"openedDocumentIds":[""]}`:    http.StatusBadRequest,
		`{"freeModel":false,"openedDocumentIds":["` + strings.Repeat("1", maxOpenedDocumentIDLen+1) + `"]}`: http.StatusBadRequest,
	} {
		t.Run(body, func(t *testing.T) {
			adapter, svc := activationAdapter(t, signedInAda)

			rec, _ := callActivation(t, adapter, http.MethodPost, pathActivationAccept, desktopOrigin, body)

			assert.Equal(t, want, rec.Code)
			assert.False(t, svc.Active())
		})
	}
}

func TestAcceptingWithoutOpeningTheDocumentsActivates(t *testing.T) {
	adapter, svc := activationAdapter(t, signedInAda)

	rec, _ := callActivation(t, adapter, http.MethodPost, pathActivationAccept, desktopOrigin, `{"freeModel":false}`)

	assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.True(t, svc.Active())
}

func TestAcceptingBeforeSigningInIsAConflict(t *testing.T) {
	adapter, _ := activationAdapter(t, activation.Record{})

	rec, _ := callActivation(t, adapter, http.MethodPost, pathActivationAccept, desktopOrigin, acceptEULA)

	assert.Equal(t, http.StatusConflict, rec.Code)
}

func TestAcceptingReportsARecordThatCouldNotBeSaved(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a directory the process cannot write to")
	}
	home := t.TempDir()
	store := activation.NewStore(home)
	require.NoError(t, store.Save(signedInAda))
	_, linked := testSpotDraft(t)
	svc := activation.NewService(store, nil, slog.New(slog.DiscardHandler), linked)
	adapter := newCoverageAdapter(t, func(core *entrypoint.GatewayCore) { core.SetActivation(svc) })
	require.NoError(t, os.Chmod(home, 0o500))
	t.Cleanup(func() { _ = os.Chmod(home, 0o700) })

	rec, _ := callActivation(t, adapter, http.MethodPost, pathActivationAccept, desktopOrigin, acceptEULA)

	assert.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "could not save")
	assert.False(t, svc.Active())
}

func TestAnAcceptanceThatCouldNotBeSavedIsLoggedUnderItsErrorID(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	rec := httptest.NewRecorder()
	samHome := "/Users/ada/.sam"
	err := fmt.Errorf("%w: activation: create %s: %w", activation.ErrRecordNotSaved, samHome, fs.ErrPermission)

	writeActivationError(rec, logger, err)

	var body struct {
		ErrorID string `json:"errorId"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.NotEmpty(t, body.ErrorID)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Contains(t, logs.String(), `"errorId":"`+body.ErrorID+`"`)
	assert.Contains(t, logs.String(), `"code":"not_saved"`)
	assert.Contains(t, logs.String(), `"cause":"permission_denied"`)
	assert.NotContains(t, logs.String(), samHome)
}

func TestASignInThatCouldNotBeSavedSaysSoInTheStatus(t *testing.T) {
	failure := signInFailure(fmt.Errorf("%w: disk full", activation.ErrRecordNotSaved))

	require.NotNil(t, failure)
	assert.Equal(t, "not_saved", failure.Code)
	assert.Equal(t, msgSignInNotSaved, failure.Message)
}

func TestASignInWithoutARefreshTokenIsNotReportedAsRetryable(t *testing.T) {
	failure := signInFailure(fmt.Errorf("%w: %w", activation.ErrFreeModelGrantNotSaved, activation.ErrNoRefreshToken))

	require.NotNil(t, failure)
	assert.Equal(t, "no_refresh_token", failure.Code)
	assert.NotContains(t, failure.Message, "Try again")
}

func TestASignInTheFreeModelsCannotTakeIsNotReportedAsRetryable(t *testing.T) {
	failure := signInFailure(fmt.Errorf("%w: save OAuth grant: %w", activation.ErrFreeModelGrantNotSaved, tokenbroker.ErrGrantInUse))

	require.NotNil(t, failure)
	assert.Equal(t, "grant_in_use", failure.Code)
	assert.NotContains(t, failure.Message, "Try again")
}

func getTerms(t *testing.T, adapter *HTTPSSEAdapter, origin string) (*httptest.ResponseRecorder, ActivationTermsResponse) {
	t.Helper()
	rec, _ := callActivation(t, adapter, http.MethodGet, pathActivationTerms, origin, "")
	var terms ActivationTermsResponse
	if rec.Code == http.StatusOK {
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &terms))
	}
	return rec, terms
}

func TestTheTermsAreOneCheckboxWithEveryDocumentAndWhetherItIsAgreed(t *testing.T) {
	fake, linked := testSpotDraft(t)
	adapter, _ := activationAdapterSigningIn(t, signedInAda, nil, activation.WithFreeModel(testFreeModel), linked)
	fake.MarkAgreed(testEULAClickwrap, ada.Subject)

	rec, terms := getTerms(t, adapter, desktopOrigin)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.True(t, terms.Agreed)
	assert.Equal(t, spotdrafttest.CheckboxText, terms.CheckboxText)
	require.Len(t, terms.Documents, 2)
	assert.Equal(t, "LLM Credits Terms", terms.Documents[1].Name)
	assert.Contains(t, rec.Body.String(), `"checkboxText":"I agree to {#agreement_list#}."`)
	assert.Contains(t, rec.Body.String(), `"version":"1.0"`)
}

func TestABuildWithoutTheFreeModelListsTheSameCheckbox(t *testing.T) {
	_, linked := testSpotDraft(t)
	adapter, _ := activationAdapterSigningIn(t, signedInAda, nil, linked)

	rec, terms := getTerms(t, adapter, desktopOrigin)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Len(t, terms.Documents, 2)
}

func TestTheTermsAreUnreadableFromAnotherSite(t *testing.T) {
	adapter, _ := activationAdapter(t, signedInAda)

	rec, _ := getTerms(t, adapter, "https://attacker.example")

	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestAcceptingRecordsTheOpenedDocumentsInSpotDraft(t *testing.T) {
	fake, linked := testSpotDraft(t)
	adapter, svc := activationAdapterSigningIn(t, signedInAda, nil, activation.WithFreeModel(testFreeModel), linked)
	_, terms := getTerms(t, adapter, desktopOrigin)

	rec, _ := callActivation(t, adapter, http.MethodPost, pathActivationAccept, desktopOrigin, `{"freeModel":false,"openedDocumentIds":["`+terms.Documents[0].ID+`"]}`)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.True(t, svc.Active())
	executions := fake.Executions()
	require.Len(t, executions, 1)
	assert.Equal(t, ada.Subject, executions[0].UserIdentifier)
	assert.True(t, executions[0].Agreements[0].HasClicked)
}

func TestSpotDraftOutagesAreReportedAsRetryable(t *testing.T) {
	fake, linked := testSpotDraft(t)
	adapter, svc := activationAdapterSigningIn(t, signedInAda, nil, activation.WithFreeModel(testFreeModel), linked)
	fake.SetDown(true)

	rec, _ := getTerms(t, adapter, desktopOrigin)
	assert.Equal(t, http.StatusBadGateway, rec.Code)
	assert.Contains(t, rec.Body.String(), "couldn't load the license agreement")

	rec, _ = callActivation(t, adapter, http.MethodPost, pathActivationAccept, desktopOrigin, acceptFreeModel)
	assert.Equal(t, http.StatusBadGateway, rec.Code)
	assert.Contains(t, rec.Body.String(), "couldn't record your agreement")
	assert.False(t, svc.Active())
}

func TestASpotDraftOutageIsLoggedAsAWarning(t *testing.T) {
	for code, err := range map[string]error{
		"terms_unavailable":      fmt.Errorf("%w: spotdraft: clickwrap answered 503", activation.ErrTermsUnavailable),
		"agreement_not_recorded": fmt.Errorf("%w: spotdraft: execute answered 503", activation.ErrAgreementNotRecorded),
	} {
		t.Run(code, func(t *testing.T) {
			var logs bytes.Buffer

			writeActivationError(httptest.NewRecorder(), slog.New(slog.NewJSONHandler(&logs, nil)), err)

			assert.Contains(t, logs.String(), `"code":"`+code+`"`)
			assert.Contains(t, logs.String(), `"level":"WARN"`)
		})
	}
}
