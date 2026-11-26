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
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"syscall"

	"github.com/SolaceProducts/solace-agent-mesh/internal/activation"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/freellm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/tokenbroker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/solaceid"
)

var activationAppOrigins = map[string]bool{
	"wails://wails":           true,
	"http://wails.localhost":  true,
	"https://wails.localhost": true,
}

type ActivationResponse struct {
	State              string                   `json:"state"`
	SigningIn          bool                     `json:"signingIn"`
	SignInFailure      *ActivationSignInFailure `json:"signInFailure,omitempty"`
	Account            *ActivationAccount       `json:"account,omitempty"`
	Agreements         ActivationAgreements     `json:"agreements"`
	FreeModelAvailable bool                     `json:"freeModelAvailable"`
	FreeModel          *ActivationFreeModel     `json:"freeModel,omitempty"`
	FreeModelChosen    bool                     `json:"freeModelChosen"`
}

type ActivationFreeModel struct {
	GrantID           string `json:"grantId"`
	Provider          string `json:"provider"`
	APIBase           string `json:"apiBase"`
	GeneralModelName  string `json:"generalModelName"`
	PlanningModelName string `json:"planningModelName"`
}

type ActivationSignInFailure struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type ActivationAccount struct {
	Subject string `json:"subject"`
	Email   string `json:"email"`
	Name    string `json:"name"`
}

type ActivationAgreements struct {
	EulaClickwrapID string `json:"eulaClickwrapId"`
}

type AcceptActivationRequest struct {
	FreeModel         bool     `json:"freeModel"`
	OpenedDocumentIDs []string `json:"openedDocumentIds,omitempty"`
}

type ActivationTermsResponse struct {
	Agreed       bool                      `json:"agreed"`
	CheckboxText string                    `json:"checkboxText"`
	Documents    []ActivationTermsDocument `json:"documents"`
}

type ActivationTermsDocument struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version string `json:"version"`
	URL     string `json:"url"`
}

const (
	maxOpenedDocuments     = 16
	maxOpenedDocumentIDLen = 32
)

type activationHandler struct {
	svc        *activation.Service
	core       *entrypoint.GatewayCore
	logger     *slog.Logger
	allowances freeModelAllowances
}

type freeModelAllowances interface {
	Get(ctx context.Context) (freellm.Allowance, error)
	Models(ctx context.Context) ([]freellm.ModelDisplay, error)
	KnownModels() []freellm.ModelDisplay
}

type FreeModelResponse struct {
	Status           string             `json:"status"`
	RemainingPercent *float64           `json:"remainingPercent"`
	Models           []FreeModelDisplay `json:"models"`
}

type FreeModelDisplay struct {
	ModelName   string `json:"modelName"`
	DisplayName string `json:"displayName"`
}

func (h *activationHandler) handleGet(w http.ResponseWriter, r *http.Request) {
	if !requireActivationAppOrigin(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, activationResponse(h.svc.Status()))
}

func (h *activationHandler) handleSignIn(w http.ResponseWriter, r *http.Request) {
	if !requireActivationAppOrigin(w, r) {
		return
	}
	status, err := h.svc.StartSignIn(r.Context(), resolveUserID(r, h.core))
	if err != nil {
		writeActivationError(w, h.logger, err)
		return
	}
	writeJSON(w, http.StatusAccepted, activationResponse(status))
}

func (h *activationHandler) handleCancelSignIn(w http.ResponseWriter, r *http.Request) {
	if !requireActivationAppOrigin(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, activationResponse(h.svc.CancelSignIn()))
}

func (h *activationHandler) handleAccept(w http.ResponseWriter, r *http.Request) {
	if !requireActivationAppOrigin(w, r) {
		return
	}
	var req AcceptActivationRequest
	if err := decodeJSON(r, &req); err != nil {
		badRequestFromDecode(w, err)
		return
	}
	if problems := openedDocumentProblems(req.OpenedDocumentIDs); len(problems) > 0 {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{
			Message:           "validation error",
			ErrorID:           idgen.New(),
			ValidationDetails: map[string][]string{"openedDocumentIds": problems},
		})
		return
	}
	status, err := h.svc.Accept(r.Context(), resolveUserID(r, h.core), req.FreeModel, req.OpenedDocumentIDs)
	if err != nil {
		writeActivationError(w, h.logger, err)
		return
	}
	writeJSON(w, http.StatusOK, activationResponse(status))
}

func openedDocumentProblems(ids []string) []string {
	var problems []string
	if len(ids) > maxOpenedDocuments {
		problems = append(problems, fmt.Sprintf("must name at most %d documents", maxOpenedDocuments))
	}
	for _, id := range ids {
		if id == "" || len(id) > maxOpenedDocumentIDLen {
			problems = append(problems, fmt.Sprintf("each id must be 1 to %d characters", maxOpenedDocumentIDLen))
			break
		}
	}
	return problems
}

func (h *activationHandler) handleGetTerms(w http.ResponseWriter, r *http.Request) {
	if !requireActivationAppOrigin(w, r) {
		return
	}
	terms, err := h.svc.Terms(r.Context())
	if err != nil {
		writeActivationError(w, h.logger, err)
		return
	}
	writeJSON(w, http.StatusOK, activationTerms(terms))
}

func activationTerms(terms activation.Terms) ActivationTermsResponse {
	documents := make([]ActivationTermsDocument, 0, len(terms.Documents))
	for _, d := range terms.Documents {
		documents = append(documents, ActivationTermsDocument{ID: d.ID, Name: d.Name, Version: d.Version, URL: d.URL})
	}
	return ActivationTermsResponse{Agreed: terms.Agreed, CheckboxText: terms.CheckboxText, Documents: documents}
}

const msgFreeModelNotSetUp = "The free model is not set up."

func (h *activationHandler) handleGetFreeModel(w http.ResponseWriter, r *http.Request) {
	if !requireActivationAppOrigin(w, r) {
		return
	}
	if h.allowances == nil || !h.svc.Status().FreeModelChosen {
		writeError(w, http.StatusNotFound, msgFreeModelNotSetUp)
		return
	}
	h.writeFreeModel(w, r)
}

func (h *activationHandler) handleFreeModelSignIn(w http.ResponseWriter, r *http.Request) {
	if !requireActivationAppOrigin(w, r) {
		return
	}
	status, err := h.svc.StartFreeModelSignIn(r.Context(), resolveUserID(r, h.core))
	if err != nil {
		writeActivationError(w, h.logger, err)
		return
	}
	writeJSON(w, http.StatusAccepted, activationResponse(status))
}

func (h *activationHandler) handleRefreshFreeModel(w http.ResponseWriter, r *http.Request) {
	if !requireActivationAppOrigin(w, r) {
		return
	}
	if h.allowances == nil {
		writeError(w, http.StatusNotFound, msgFreeModelNotSetUp)
		return
	}
	if err := h.svc.RefreshFreeModel(r.Context()); err != nil {
		if errors.Is(err, activation.ErrFreeModelNotChosen) {
			writeError(w, http.StatusNotFound, msgFreeModelNotSetUp)
			return
		}
		h.logger.Warn("free model refresh failed", "error", err)
		writeAllowance(w, freellm.RefreshFailure(err), h.allowances.KnownModels())
		return
	}
	h.writeFreeModel(w, r)
}

func (h *activationHandler) writeFreeModel(w http.ResponseWriter, r *http.Request) {
	allowance, err := h.allowances.Get(r.Context())
	if err != nil {
		h.logger.Warn("free model allowance unavailable", "upstreamStatus", allowance.UpstreamStatus, "error", err)
	}
	writeAllowance(w, allowance, h.freeModelNames(r.Context(), allowance.Status))
}

func (h *activationHandler) freeModelNames(ctx context.Context, status freellm.AllowanceStatus) []freellm.ModelDisplay {
	if status != freellm.AllowanceAvailable {
		return h.allowances.KnownModels()
	}
	models, err := h.allowances.Models(ctx)
	if err != nil {
		h.logger.Warn("free model display names unavailable", "error", err)
	}
	return models
}

func writeAllowance(w http.ResponseWriter, allowance freellm.Allowance, models []freellm.ModelDisplay) {
	displays := make([]FreeModelDisplay, 0, len(models))
	for _, m := range models {
		displays = append(displays, FreeModelDisplay{ModelName: m.ModelName, DisplayName: m.DisplayName})
	}
	writeJSON(w, http.StatusOK, FreeModelResponse{Status: string(allowance.Status), RemainingPercent: allowance.RemainingPercent, Models: displays})
}

func activationResponse(status activation.Status) ActivationResponse {
	return ActivationResponse{
		State:              string(status.State),
		SigningIn:          status.SigningIn,
		SignInFailure:      signInFailure(status.SignInError),
		Account:            activationAccount(status.Account),
		Agreements:         activationAgreements(status),
		FreeModelAvailable: status.FreeModelAvailable,
		FreeModel:          activationFreeModel(status.FreeModel),
		FreeModelChosen:    status.FreeModelChosen,
	}
}

func activationFreeModel(settings *activation.FreeModelSettings) *ActivationFreeModel {
	if settings == nil {
		return nil
	}
	return &ActivationFreeModel{GrantID: settings.GrantID, Provider: settings.Provider, APIBase: settings.APIBase, GeneralModelName: settings.GeneralModelName, PlanningModelName: settings.PlanningModelName}
}

func activationAccount(account *activation.Account) *ActivationAccount {
	if account == nil {
		return nil
	}
	return &ActivationAccount{Subject: account.Subject, Email: account.Email, Name: account.Name}
}

func activationAgreements(status activation.Status) ActivationAgreements {
	return ActivationAgreements{EulaClickwrapID: status.Agreements.EULAClickwrapID}
}

func requireActivationAppOrigin(w http.ResponseWriter, r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" && r.Header.Get("Sec-Fetch-Site") != "cross-site" && isLoopbackHost(r.Host) {
		return true
	}
	if activationAppOrigins[origin] || isGatewaysOwnPageOnLoopback(origin, r.Host) {
		return true
	}
	writeError(w, http.StatusForbidden, "Activation can only be changed from Solace Agent Mesh.")
	return false
}

func isGatewaysOwnPageOnLoopback(origin, host string) bool {
	u, err := url.Parse(origin)
	return err == nil && u.Host == host && isLoopbackHost(host)
}

func isLoopbackHost(hostport string) bool {
	host, _, err := net.SplitHostPort(hostport)
	if err != nil {
		host = hostport
	}
	return host == "localhost" || net.ParseIP(host).IsLoopback()
}

const msgSignInNotSaved = "Solace Agent Mesh could not save your sign-in. Try again."

type activationProblem struct {
	status  int
	code    string
	message string
	passing bool
}

func classifyActivationError(err error) activationProblem {
	switch {
	case errors.Is(err, activation.ErrAgreementNotRecorded):
		return activationProblem{http.StatusBadGateway, "agreement_not_recorded", "Solace Agent Mesh couldn't record your agreement with Solace. Check the network connection and try again.", true}
	case errors.Is(err, activation.ErrTermsUnavailable):
		return activationProblem{http.StatusBadGateway, "terms_unavailable", "Solace Agent Mesh couldn't load the license agreement. Check the network connection and try again.", true}
	case errors.Is(err, activation.ErrAgreementUnlinked):
		return activationProblem{http.StatusServiceUnavailable, "agreement_unlinked", "This build of Solace Agent Mesh has no license agreement linked, so it can't be set up.", false}
	case errors.Is(err, activation.ErrAlreadySignedIn):
		return activationProblem{http.StatusConflict, "already_signed_in", "Already signed in with Solace ID.", false}
	case errors.Is(err, activation.ErrNotSignedIn):
		return activationProblem{http.StatusConflict, "not_signed_in", "Sign in with Solace ID first.", false}
	case errors.Is(err, activation.ErrFreeModelUnavailable):
		return activationProblem{http.StatusConflict, "free_model_unavailable", "The free model is not available in this build. Add your own model.", false}
	case errors.Is(err, activation.ErrFreeModelNotOffered):
		return activationProblem{http.StatusConflict, "free_model_not_offered", "The free model can only be chosen when you first set up Solace Agent Mesh.", false}
	case errors.Is(err, activation.ErrFreeModelNotChosen):
		return activationProblem{http.StatusNotFound, "free_model_not_set_up", msgFreeModelNotSetUp, false}
	case errors.Is(err, activation.ErrDifferentAccount):
		return activationProblem{http.StatusConflict, "different_account", "Sign in with the Solace ID account you first set up Solace Agent Mesh with.", false}
	case errors.Is(err, tokenbroker.ErrGrantInUse):
		return activationProblem{http.StatusConflict, "grant_in_use", "This version of Solace Agent Mesh reaches the free model differently from the version you set it up with, so the sign-in can't be saved. Contact Solace support.", false}
	case errors.Is(err, activation.ErrNoRefreshToken):
		return activationProblem{http.StatusBadGateway, "no_refresh_token", "Solace ID did not let Solace Agent Mesh stay signed in, so the sign-in can't be completed. Contact Solace support.", false}
	case errors.Is(err, activation.ErrFreeModelGrantNotSaved):
		return activationProblem{http.StatusInternalServerError, "not_saved", msgSignInNotSaved, false}
	case errors.Is(err, context.Canceled):
		return activationProblem{http.StatusConflict, "canceled", "The sign-in was canceled.", false}
	case errors.Is(err, context.DeadlineExceeded):
		return activationProblem{http.StatusConflict, "timed_out", "The sign-in took too long. Try again.", false}
	case errors.Is(err, solaceid.ErrDenied):
		return activationProblem{http.StatusConflict, "denied", "Solace ID did not complete the sign-in. Try again.", false}
	case errors.Is(err, activation.ErrRecordNotSaved):
		return activationProblem{http.StatusInternalServerError, "not_saved", "Solace Agent Mesh could not save your acceptance. Try again.", false}
	case errors.Is(err, activation.ErrSignInUnavailable):
		return activationProblem{http.StatusServiceUnavailable, "unavailable", "Solace ID sign-in is not available in this build.", false}
	case errors.Is(err, solaceid.ErrNoCallbackPort):
		return activationProblem{http.StatusServiceUnavailable, "no_callback_port", "Another program may be using the sign-in ports (53682–53684). Close it, or restart your computer, and try again.", false}
	default:
		return activationProblem{http.StatusBadGateway, "failed", "The Solace ID sign-in failed. Try again.", false}
	}
}

func writeActivationError(w http.ResponseWriter, logger *slog.Logger, err error) {
	problem := classifyActivationError(err)
	if problem.status < http.StatusInternalServerError {
		writeError(w, problem.status, problem.message)
		return
	}
	errID := idgen.New()
	if problem.passing {
		logger.Warn("activation request failed", "errorId", errID, "code", problem.code)
	} else {
		logger.Error("activation request failed", "errorId", errID, "code", problem.code, "cause", recordFailureCause(err))
	}
	writeErrorWithID(w, problem.status, problem.message, errID)
}

func recordFailureCause(err error) string {
	switch {
	case errors.Is(err, activation.ErrNoStore):
		return "no_directory"
	case errors.Is(err, fs.ErrPermission):
		return "permission_denied"
	case errors.Is(err, syscall.ENOSPC):
		return "disk_full"
	default:
		return "other"
	}
}

func signInFailure(err error) *ActivationSignInFailure {
	if err == nil {
		return nil
	}
	problem := classifyActivationError(err)
	if errors.Is(err, activation.ErrRecordNotSaved) {
		problem.message = msgSignInNotSaved
	}
	return &ActivationSignInFailure{Code: problem.code, Message: problem.message}
}
