// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"net/http"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
)

func requestCaller(r *http.Request, userID string) *auth.Subject {
	return requestCallerCtx(r.Context(), userID)
}

func requestCallerCtx(ctx context.Context, userID string) *auth.Subject {
	if caller := contextSubjectCtx(ctx); caller != nil {
		return caller
	}
	return auth.ForUserIDOnly(userID)
}

func contextSubject(r *http.Request) *auth.Subject {
	return contextSubjectCtx(r.Context())
}

func contextSubjectCtx(ctx context.Context) *auth.Subject {
	caller, _ := auth.SubjectFromContext(ctx)
	return caller
}
