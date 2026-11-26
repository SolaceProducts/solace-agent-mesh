// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build solace_id

package httpsse

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SolaceProducts/solace-agent-mesh/internal/activation"
	"github.com/SolaceProducts/solace-agent-mesh/internal/freellm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

var activeAdaOnFreeModel = activation.Record{
	Account:        activeAda.Account,
	EULA:           activeAda.EULA,
	FreeModelTerms: &activation.Acceptance{Subject: ada.Subject, AcceptedAt: time.Now().UTC()},
}

type fixedAllowance struct {
	allowance freellm.Allowance
	err       error
	models    []freellm.ModelDisplay
	modelsErr error
	known     []freellm.ModelDisplay
}

func (f fixedAllowance) Get(context.Context) (freellm.Allowance, error) { return f.allowance, f.err }

func (f fixedAllowance) Models(context.Context) ([]freellm.ModelDisplay, error) {
	return f.models, f.modelsErr
}

func (f fixedAllowance) KnownModels() []freellm.ModelDisplay { return f.known }

var knownSonnet = []freellm.ModelDisplay{{ModelName: "solace-free-general", DisplayName: "Sonnet 5.5"}}

func freeModelHandler(t *testing.T, record activation.Record, allowances freeModelAllowances) *activationHandler {
	t.Helper()
	return freeModelHandlerOn(t, record, allowances, platformFreeModel{})
}

func freeModelHandlerOn(t *testing.T, record activation.Record, allowances freeModelAllowances, platform activation.FreeModel) *activationHandler {
	t.Helper()
	store := activation.NewStore(t.TempDir())
	require.NoError(t, store.Save(record))
	svc := activation.NewService(store, nil, slog.New(slog.DiscardHandler), activation.WithFreeModel(testFreeModel))
	t.Cleanup(svc.Close)
	svc.SetFreeModel(platform)
	return &activationHandler{svc: svc, logger: slog.New(slog.DiscardHandler), allowances: allowances}
}

func getFreeModel(h *activationHandler, origin string) *httptest.ResponseRecorder {
	return serveFreeModel(h.handleGetFreeModel, http.MethodGet, pathFreeModel, origin)
}

func serveFreeModel(handler http.HandlerFunc, method, path, origin string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	rec := httptest.NewRecorder()
	handler(rec, req)
	return rec
}

func TestFreeModelReportsTheAllowanceLeft(t *testing.T) {
	left := 42.5
	h := freeModelHandler(t, activeAdaOnFreeModel, fixedAllowance{allowance: freellm.Allowance{Status: freellm.AllowanceAvailable, RemainingPercent: &left}})

	rec := getFreeModel(h, desktopOrigin)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{"status":"available","remainingPercent":42.5,"models":[]}`, rec.Body.String())
}

func TestFreeModelReportsASignInToRedoWithNoPercent(t *testing.T) {
	h := freeModelHandler(t, activeAdaOnFreeModel, fixedAllowance{allowance: freellm.Allowance{Status: freellm.AllowanceReauthorizationRequired}})

	rec := getFreeModel(h, desktopOrigin)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{"status":"reauthorization_required","remainingPercent":null,"models":[]}`, rec.Body.String())
}

func TestFreeModelNamesItsModels(t *testing.T) {
	left := 80.0
	h := freeModelHandler(t, activeAdaOnFreeModel, fixedAllowance{
		allowance: freellm.Allowance{Status: freellm.AllowanceAvailable, RemainingPercent: &left},
		models:    []freellm.ModelDisplay{{ModelName: "solace-free-general", DisplayName: "Sonnet 5.5"}},
	})

	rec := getFreeModel(h, desktopOrigin)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{"status":"available","remainingPercent":80,"models":[{"modelName":"solace-free-general","displayName":"Sonnet 5.5"}]}`, rec.Body.String())
}

func TestFreeModelKeepsTheNamesItKnowsWhileTheFreeTierIsNotAvailable(t *testing.T) {
	for _, status := range []freellm.AllowanceStatus{freellm.AllowancePaused, freellm.AllowanceReauthorizationRequired, freellm.AllowanceUnavailable} {
		t.Run(string(status), func(t *testing.T) {
			h := freeModelHandler(t, activeAdaOnFreeModel, fixedAllowance{
				allowance: freellm.Allowance{Status: status},
				modelsErr: errors.New("names must not be fetched while the free tier is not available"),
				known:     knownSonnet,
			})

			rec := getFreeModel(h, desktopOrigin)

			require.Equal(t, http.StatusOK, rec.Code)
			assert.JSONEq(t, `{"status":"`+string(status)+`","remainingPercent":null,"models":[{"modelName":"solace-free-general","displayName":"Sonnet 5.5"}]}`, rec.Body.String())
		})
	}
}

func TestFreeModelReportsTheAllowanceWithoutNamesThatCouldNotBeRead(t *testing.T) {
	left := 80.0
	h := freeModelHandler(t, activeAdaOnFreeModel, fixedAllowance{
		allowance: freellm.Allowance{Status: freellm.AllowanceAvailable, RemainingPercent: &left},
		modelsErr: errors.New("bad gateway"),
	})

	rec := getFreeModel(h, desktopOrigin)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{"status":"available","remainingPercent":80,"models":[]}`, rec.Body.String())
}

func TestFreeModelReportsAnAllowanceThatCouldNotBeRead(t *testing.T) {
	h := freeModelHandler(t, activeAdaOnFreeModel, fixedAllowance{allowance: freellm.Allowance{Status: freellm.AllowanceUnavailable, UpstreamStatus: 502}, err: errors.New("bad gateway")})

	rec := getFreeModel(h, desktopOrigin)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{"status":"unavailable","remainingPercent":null,"models":[]}`, rec.Body.String())
}

func TestFreeModelIsNotFoundForAnInstallOnItsOwnModel(t *testing.T) {
	h := freeModelHandler(t, activeAda, fixedAllowance{})

	assert.Equal(t, http.StatusNotFound, getFreeModel(h, desktopOrigin).Code)
}

func TestFreeModelIsNotFoundInABuildWithoutIt(t *testing.T) {
	h := freeModelHandler(t, activeAdaOnFreeModel, nil)

	assert.Equal(t, http.StatusNotFound, getFreeModel(h, desktopOrigin).Code)
}

func TestFreeModelRefusesAnotherSite(t *testing.T) {
	h := freeModelHandler(t, activeAdaOnFreeModel, fixedAllowance{})

	assert.Equal(t, http.StatusForbidden, getFreeModel(h, "https://evil.example").Code)
}

func TestFreeModelRouteIsServed(t *testing.T) {
	adapter, _ := activationAdapter(t, activeAdaOnFreeModel)

	rec, _ := callActivation(t, adapter, http.MethodGet, pathFreeModel, desktopOrigin, "")

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), "The free model is not set up.")
}

func TestRefreshingTheFreeModelAnswersWithItsState(t *testing.T) {
	left := 10.0
	h := freeModelHandler(t, activeAdaOnFreeModel, fixedAllowance{allowance: freellm.Allowance{Status: freellm.AllowanceAvailable, RemainingPercent: &left}})

	rec := serveFreeModel(h.handleRefreshFreeModel, http.MethodPost, pathFreeModelRefresh, desktopOrigin)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.JSONEq(t, `{"status":"available","remainingPercent":10,"models":[]}`, rec.Body.String())
}

type refreshFailingFreeModel struct {
	platformFreeModel
	err error
}

func (f refreshFailingFreeModel) Refresh(context.Context) error { return f.err }

func TestRefreshingTheFreeModelReportsAFailedRefresh(t *testing.T) {
	left := 10.0
	cases := map[string]struct {
		err  error
		want string
	}{
		"sign-in refused":        {err: fmt.Errorf("platform OAuth grant token: %w", llm.ErrReauthorizationRequired), want: `{"status":"reauthorization_required","remainingPercent":null,"models":[{"modelName":"solace-free-general","displayName":"Sonnet 5.5"}]}`},
		"sign-in service failed": {err: errors.New("refresh OAuth grant: oauth2cc: token endpoint returned HTTP 500"), want: `{"status":"unavailable","remainingPercent":null,"models":[{"modelName":"solace-free-general","displayName":"Sonnet 5.5"}]}`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			allowances := fixedAllowance{allowance: freellm.Allowance{Status: freellm.AllowanceAvailable, RemainingPercent: &left}, known: knownSonnet}
			h := freeModelHandlerOn(t, activeAdaOnFreeModel, allowances, refreshFailingFreeModel{err: tc.err})

			rec := serveFreeModel(h.handleRefreshFreeModel, http.MethodPost, pathFreeModelRefresh, desktopOrigin)

			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			assert.JSONEq(t, tc.want, rec.Body.String())
		})
	}
}

func TestRefreshingTheFreeModelIsNotFoundForAnInstallOnItsOwnModel(t *testing.T) {
	h := freeModelHandler(t, activeAda, fixedAllowance{})

	assert.Equal(t, http.StatusNotFound, serveFreeModel(h.handleRefreshFreeModel, http.MethodPost, pathFreeModelRefresh, desktopOrigin).Code)
}

func TestRefreshingTheFreeModelRefusesAnotherSite(t *testing.T) {
	h := freeModelHandler(t, activeAdaOnFreeModel, fixedAllowance{})

	assert.Equal(t, http.StatusForbidden, serveFreeModel(h.handleRefreshFreeModel, http.MethodPost, pathFreeModelRefresh, "https://evil.example").Code)
}

func TestSigningInAgainForTheFreeModelFromTheApp(t *testing.T) {
	browser := newBrowserSignIn()
	adapter, svc := activationAdapterSigningIn(t, activeAdaOnFreeModel, browser)

	rec, resp := callActivation(t, adapter, http.MethodPost, pathFreeModelSignIn, desktopOrigin, `{}`)
	require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())
	assert.True(t, resp.SigningIn)

	browser.finishAs(ada)
	awaitSignIn(t, svc)
	rec, resp = callActivation(t, adapter, http.MethodGet, pathActivation, desktopOrigin, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Nil(t, resp.SignInFailure)
	assert.Equal(t, string(activation.StateActive), resp.State)
}

func TestSigningInAgainWithAnotherAccountIsReportedByTheStatus(t *testing.T) {
	browser := newBrowserSignIn()
	adapter, svc := activationAdapterSigningIn(t, activeAdaOnFreeModel, browser)
	rec, _ := callActivation(t, adapter, http.MethodPost, pathFreeModelSignIn, desktopOrigin, `{}`)
	require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())

	browser.finishAs(activation.SignInResult{Subject: "auth0|grace", Email: "grace@example.com", Tokens: activation.FreeModelTokens{RefreshToken: "refresh-grace"}})
	awaitSignIn(t, svc)
	_, resp := callActivation(t, adapter, http.MethodGet, pathActivation, desktopOrigin, "")

	require.NotNil(t, resp.SignInFailure)
	assert.Equal(t, "different_account", resp.SignInFailure.Code)
}

func TestSigningInAgainIsNotFoundForAnInstallOnItsOwnModel(t *testing.T) {
	adapter, _ := activationAdapterSigningIn(t, activeAda, newBrowserSignIn())

	rec, _ := callActivation(t, adapter, http.MethodPost, pathFreeModelSignIn, desktopOrigin, `{}`)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestSigningInAgainRefusesAnotherSite(t *testing.T) {
	adapter, svc := activationAdapterSigningIn(t, activeAdaOnFreeModel, newBrowserSignIn())
	t.Cleanup(svc.Close)

	rec, _ := callActivation(t, adapter, http.MethodPost, pathFreeModelSignIn, "https://evil.example", `{}`)

	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.False(t, svc.Status().SigningIn, "a refused request starts no sign-in")
}
