// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package webhook

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	gwauth "github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
)

const msgArtifactProcessingFailed = "artifact processing failed"

func (a *WebhookAdapter) dispatch(route *RouteConfig, w http.ResponseWriter, r *http.Request) {
	a.core.ReportSubmitRefusal(a.serve(route, w, r))
}

func (a *WebhookAdapter) serve(route *RouteConfig, w http.ResponseWriter, r *http.Request) string {
	logger := a.logger.With("route", route.Name)

	if route.Auth.Token != nil {
		if err := verifyToken(route.Auth.Token, r); err != nil {
			errorID := writeJSONError(w, http.StatusUnauthorized, "unauthorized")
			logger.Warn("webhook auth failed", "remote", r.RemoteAddr, "errorId", errorID)
			return taxonomy.RejectForbidden
		}
	}

	maxBody := a.cfg.MaxBodyBytes
	if route.MaxBodyBytes > 0 {
		maxBody = route.MaxBodyBytes
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			errorID := writeJSONError(w, http.StatusRequestEntityTooLarge, "request body too large")
			logger.Warn("webhook body exceeded size cap", "remote", r.RemoteAddr, "maxBytes", maxBody, "errorId", errorID)
			return taxonomy.RejectMalformed
		}
		errorID := writeJSONError(w, http.StatusBadRequest, "failed to read request body")
		logger.Warn("failed to read request body", "error", err, "errorId", errorID)
		return taxonomy.RejectMalformed
	}

	if route.Auth.HMAC != nil {
		if hmacErr := verifyHMAC(route.Auth.HMAC, r, body); hmacErr != nil {
			errorID := writeJSONError(w, http.StatusUnauthorized, "unauthorized")
			logger.Warn("webhook auth failed", "remote", r.RemoteAddr, "errorId", errorID)
			return taxonomy.RejectForbidden
		}
	}

	pathParams := a.router.extractPathParams(route.Name, r)
	exprCtx := buildRequestContext(r, route, body, pathParams, logger)

	caller := gwauth.ForWebhookCaller(route.RunAs)
	userID := caller.UserID()

	targetName, targetType, err := resolveTarget(route)
	if err != nil {
		errorID := writeJSONError(w, http.StatusInternalServerError, "no target resolved")
		logger.Error("failed to resolve target", "error", err, "errorId", errorID)
		return taxonomy.RejectInternal
	}

	ctx := r.Context()
	var fileParts []a2a.Part
	if route.ArtifactProcessing != nil {
		parts, uris, artErr := processArtifacts(ctx, exprCtx, route, userID, a.core.Services().Artifacts())
		if artErr != nil {
			var clientErr *artifactClientError
			refusal := taxonomy.RejectInternal
			if errors.As(artErr, &clientErr) {
				refusal = taxonomy.RejectMalformed
				errorID := writeJSONError(w, http.StatusBadRequest, "invalid artifact data")
				logger.Warn("invalid artifact data in request", "artifactIndex", clientErr.Index, "reason", clientErr.Reason, "remote", r.RemoteAddr, "errorId", errorID)
			} else {
				errorID := writeJSONError(w, http.StatusInternalServerError, msgArtifactProcessingFailed)
				logger.Error(msgArtifactProcessingFailed, "error", artErr, "errorId", errorID)
			}
			return refusal
		}
		fileParts = parts
		if len(uris) > 0 {
			if setErr := exprCtx.SetData("user_data.created_artifacts:uris", uris); setErr != nil {
				errorID := writeJSONError(w, http.StatusInternalServerError, msgArtifactProcessingFailed)
				logger.Error("failed to set created_artifacts URIs in expression context", "error", setErr, "errorId", errorID)
				return taxonomy.RejectInternal
			}
		}
	}

	inputText, err := evalInputExpression(exprCtx, route.InputExpression)
	if err != nil {
		errorID := writeJSONError(w, http.StatusInternalServerError, "input rendering failed")
		logger.Error("failed to evaluate input_expression", "error", err, "errorId", errorID)
		return taxonomy.RejectInternal
	}
	if inputText == "" {
		errorID := writeJSONError(w, http.StatusBadRequest, "input expression resolved to empty string")
		logger.Warn("input_expression resolved to empty string", "remote", r.RemoteAddr, "errorId", errorID)
		return taxonomy.RejectMalformed
	}

	parts := []a2a.Part{a2a.NewTextPart(inputText)}
	parts = append(parts, fileParts...)
	message := a2a.Message{Role: "user", Parts: parts}

	taskID := idgen.New()

	source := triggerSource(r, route, body)
	if err := a.core.SubmitTask(ctx, targetName, taskID, message, caller, entrypoint.FromEvent(), entrypoint.WithTriggerSource(source)); err != nil {
		if retryAt, ok := budget.RetryAt(err); ok {
			budget.SetRetryAfter(w, retryAt, time.Now())
			errorID := writeJSONError(w, http.StatusTooManyRequests, entrypoint.BudgetRefusalText(err))
			logger.Debug("webhook refused by the budget gate", "target", targetName, "reason", entrypoint.SubmitFailureReason(err),
				"budgetTier", entrypoint.BudgetRefusalTier(err), "errorId", errorID)
			return entrypoint.SubmitFailureReason(err)
		}
		if entrypoint.IsAgentNotAvailable(err) {
			errorID := writeJSONError(w, http.StatusNotFound, entrypoint.AgentNotAvailableText)
			logger.Debug("webhook refused: its target is not deployed", "target", targetName, "errorId", errorID)
			return entrypoint.SubmitFailureReason(err)
		}
		if entrypoint.IsAuthorizationDenial(err) {
			errorID := writeJSONError(w, http.StatusForbidden, "forbidden")
			logger.Debug("webhook refused: caller lacks the target's invoke scope", "target", targetName, "errorId", errorID)
			return entrypoint.SubmitFailureReason(err)
		}
		errorID := writeJSONError(w, http.StatusBadGateway, "failed to submit task")
		logger.Error("failed to submit task", "error", err, "target", targetName, "errorId", errorID)
		return entrypoint.SubmitFailureReason(err)
	}
	logger.Debug("task submitted", "taskID", taskID, "target", targetName, "targetType", targetType,
		"triggerEvent", source.TriggerEvent, "triggerDeliveryId", source.TriggerDeliveryID)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]string{"taskId": taskID})
	return ""
}

func evalInputExpression(exprCtx interface {
	GetData(string) (any, error)
}, expr string) (string, error) {
	resolved, err := exprCtx.GetData(expr)
	if err != nil {
		return "", err
	}
	switch v := resolved.(type) {
	case nil:
		return "", nil
	case string:
		return v, nil
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return "", err
		}
		return string(b), nil
	}
}
