// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"cmp"
	"context"
	"log/slog"
	"net/http"

	"github.com/SolaceProducts/solace-agent-mesh/internal/apikey"
	samauth "github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/oidcauth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/trust"
)

const msgAuthenticationRequired = "Authentication required"

func NewAuthMiddleware(
	authenticator samauth.Authenticator,
	authzSvc *oidcauth.AuthorizationService,
	keyAuthority apikey.Authority,
	logger *slog.Logger,
) func(http.Handler) http.Handler {
	log := withLoggerName(logger, "platform-auth")

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if auth.ShouldSkipAuth(r) {
				next.ServeHTTP(w, r)
				return
			}

			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				auth.WriteError(w, http.StatusUnauthorized, auth.ErrorAuthenticationRequired, "Missing authorization header")
				return
			}

			token, isBearer := apikey.BearerCredential(authHeader)
			if !isBearer {
				auth.WriteError(w, http.StatusUnauthorized, auth.ErrorAuthenticationRequired, "Invalid authorization header format")
				return
			}

			if apikey.HasMarker(token) {
				identity, rejection := auth.ResolveAPIKey(r.Context(), keyAuthority, token)
				if rejection != nil {
					rejection.Respond(w, r)
					return
				}
				next.ServeHTTP(w, r.WithContext(auth.ContextWithSubject(r.Context(), identity)))
				return
			}

			if authenticator == nil {
				log.Error("authenticator not initialized", "path", r.URL.Path)
				auth.WriteError(w, http.StatusServiceUnavailable, auth.ErrorServiceUnavailable, "Auth service unavailable")
				return
			}

			samIdentity, err := authenticator.Authenticate(r.Context(), token, "")
			if err != nil {
				log.Warn("token validation failed", "error", err)
				auth.WriteError(w, http.StatusUnauthorized, auth.ErrorInvalidToken, "Invalid or expired session token, please log in again")
				return
			}
			if samIdentity == nil {
				auth.WriteError(w, http.StatusUnauthorized, auth.ErrorInvalidToken, "Authentication failed")
				return
			}

			caller := samauth.CallerFromIdentity(samIdentity)
			email := samIdentity.UserID
			if caller.Email != "" {
				email = caller.Email
			}
			name := samIdentity.UserID
			if caller.Name != "" {
				name = caller.Name
			}

			claimed, _ := samIdentity.Claims[trust.ClaimPlatformUserID].(string)
			platformUserID := cmp.Or(samIdentity.PlatformUserID, claimed)
			identity := auth.ForPlatformAdminToken(auth.TokenClaims{
				UserID:         samIdentity.UserID,
				Email:          email,
				Name:           name,
				Roles:          samIdentity.Roles,
				Scopes:         samIdentity.Scopes,
				PlatformUserID: platformUserID,
			}, oidcauth.NewScopeExpander(authzSvc))

			ctx := auth.ContextWithSubject(r.Context(), identity)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func requireScope(scope string, handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		identity, ok := auth.SubjectFromContext(r.Context())
		if !ok || identity == nil {
			auth.WriteError(w, http.StatusUnauthorized, auth.ErrorAuthenticationRequired, msgAuthenticationRequired)
			return
		}

		if !identity.HasScope(scope) {
			auth.WriteError(w, http.StatusForbidden, auth.ErrorAuthorizationFailure, msgInsufficientPermissions)
			return
		}

		handler(w, r)
	}
}

func requireAnyOfScope(scopes []string, handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		identity, ok := auth.SubjectFromContext(r.Context())
		if !ok || identity == nil {
			auth.WriteError(w, http.StatusUnauthorized, auth.ErrorAuthenticationRequired, msgAuthenticationRequired)
			return
		}
		for _, s := range scopes {
			if identity.HasScope(s) {
				handler(w, r)
				return
			}
		}
		auth.WriteError(w, http.StatusForbidden, auth.ErrorAuthorizationFailure, msgInsufficientPermissions)
	}
}

func requireIdentity(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		identity, ok := auth.SubjectFromContext(r.Context())
		if !ok || identity == nil {
			auth.WriteError(w, http.StatusUnauthorized, auth.ErrorAuthenticationRequired, msgAuthenticationRequired)
			return
		}
		handler(w, r)
	}
}

func requireSuperAdmin(logger *slog.Logger, resource string, handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		identity, ok := auth.SubjectFromContext(r.Context())
		if !ok || identity == nil {
			auth.WriteErrorReq(w, r, http.StatusUnauthorized, auth.ErrorAuthenticationRequired, msgAuthenticationRequired)
			return
		}
		if !rbac.IsSuperAdmin(identity.Scopes()) {
			logRBACAuditDenied(withAuditCredential(r.Context(), logger), resource, userIDFromRequest(r), "reason", "not a super administrator")
			auth.WriteErrorReq(w, r, http.StatusForbidden, auth.ErrorAuthorizationFailure,
				"RBAC management requires a super administrator")
			return
		}
		handler(w, r)
	}
}

type readRefusal struct {
	logMessage string
	message    string
}

func requireSuperAdminToRead(logger *slog.Logger, refusal readRefusal, next http.HandlerFunc) http.HandlerFunc {
	return requireReadGrant(logger, refusal, "not a super administrator", rbac.IsSuperAdmin, next)
}

func requireScopeToRead(logger *slog.Logger, scope string, refusal readRefusal, next http.HandlerFunc) http.HandlerFunc {
	holds := func(scopes []string) bool { return rbac.HasStrictScope(scopes, scope) }
	return requireReadGrant(logger, refusal, "missing scope", holds, next)
}

func requireReadGrant(logger *slog.Logger, refusal readRefusal, reason string, holds func([]string) bool, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		identity, ok := auth.SubjectFromContext(r.Context())
		if !ok || identity == nil {
			auth.WriteErrorReq(w, r, http.StatusUnauthorized, auth.ErrorAuthenticationRequired, msgAuthenticationRequired)
			return
		}
		if !holds(identity.Scopes()) {
			withAuditCredential(r.Context(), logger).Warn(refusal.logMessage, "reason", reason, "method", r.Method,
				"actor", identity.UserID(), "actorUserID", identity.PlatformUserID())
			auth.WriteErrorReq(w, r, http.StatusForbidden, auth.ErrorAuthorizationFailure, refusal.message)
			return
		}
		next(w, r)
	}
}

func callerMayReadPricing(ctx context.Context) bool {
	identity, ok := auth.SubjectFromContext(ctx)
	return ok && identity != nil && identity.HasScope(rbac.ScopeModelConfigRead)
}
