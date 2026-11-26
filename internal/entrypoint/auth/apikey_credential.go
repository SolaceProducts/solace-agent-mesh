// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package auth

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/SolaceProducts/solace-agent-mesh/internal/apikey"
)

const APIKeyAuthMethod = "api_key"

const (
	APIKeyReasonInvalid = "api_key_invalid"

	APIKeyReasonNotEnabled = "api_key_not_enabled"

	APIKeyReasonModeDisabled = "api_key_authz_mode_disabled"

	APIKeyReasonUnavailable = "api_key_authority_unavailable"

	APIKeyReasonSurfaceNotSupported = "api_key_surface_not_supported"

	APIKeyReasonWrongPosition = "api_key_wrong_position"
)

const msgAPIKeyRejected = "Invalid or expired API key"

const msgAPIKeyUnavailable = "API key authority unavailable"

type APIKeyRejection struct {
	Status int

	Reason string

	Code ErrorType

	Message string
}

func RejectAPIKeyPosition() *APIKeyRejection {
	return newAPIKeyRejection(APIKeyReasonWrongPosition)
}

func newAPIKeyRejection(reason string) *APIKeyRejection {
	if reason == APIKeyReasonUnavailable {
		return &APIKeyRejection{
			Status:  http.StatusServiceUnavailable,
			Reason:  reason,
			Code:    ErrorServiceUnavailable,
			Message: msgAPIKeyUnavailable,
		}
	}
	return &APIKeyRejection{
		Status:  http.StatusUnauthorized,
		Reason:  reason,
		Code:    ErrorInvalidToken,
		Message: msgAPIKeyRejected,
	}
}

func (r *APIKeyRejection) Respond(w http.ResponseWriter, req *http.Request) {
	if req.Context().Err() != nil {
		return
	}
	WriteErrorReq(w, req, r.Status, r.Code, r.Message)
}

func ResolveAPIKey(ctx context.Context, authority apikey.Authority, presented string) (*Subject, *APIKeyRejection) {
	if authority == nil {
		return nil, newAPIKeyRejection(APIKeyReasonNotEnabled)
	}
	if !apikey.IsWellFormed(presented) {
		return nil, newAPIKeyRejection(APIKeyReasonInvalid)
	}

	resolution, err := authority.Validate(ctx, apikey.Hash(presented))
	if err != nil {
		rejection := rejectionForAPIKeyError(err)
		logAuthorityUnavailable(ctx, rejection, err)
		return nil, rejection
	}

	return ForAPIKey(resolution), nil
}

func rejectionForAPIKeyError(err error) *APIKeyRejection {
	switch {
	case errors.Is(err, apikey.ErrNotEnabled):
		return newAPIKeyRejection(APIKeyReasonNotEnabled)
	case errors.Is(err, apikey.ErrInvalidKey):
		return newAPIKeyRejection(APIKeyReasonInvalid)
	case errors.Is(err, apikey.ErrAuthorizationModeDisabled):
		return newAPIKeyRejection(APIKeyReasonModeDisabled)
	default:
		return newAPIKeyRejection(APIKeyReasonUnavailable)
	}
}

func logAuthorityUnavailable(ctx context.Context, rejection *APIKeyRejection, cause error) {
	if rejection.Reason != APIKeyReasonUnavailable || ctx.Err() != nil {
		return
	}
	slog.Default().With("logger_name", "gateway-auth").ErrorContext(ctx, "api key authority unavailable", "error", cause)
}
