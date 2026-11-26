// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package auth

import (
	"cmp"
	"context"
	"net/http"
	"os"
	"regexp"
	"strings"

	coreauth "github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/identity"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/openapispec"
	"github.com/SolaceProducts/solace-agent-mesh/internal/trust"
)

type Subject struct {
	userID        string
	email         string
	name          string
	authenticated bool
	authMethod    string
	roles         []string
	scopes        []string
	metadata      map[string]any

	credentialID string

	platformUserID string

	senderVerified bool

	profile          *identity.Profile
	enrichmentStatus identity.EnrichmentStatus
	enrichmentReason string

	systemInitiated bool

	scopesResolved bool

	fullIdentity bool
}

func (id *Subject) FullIdentity() bool {
	return id != nil && id.fullIdentity
}

func ForUserIDOnly(userID string) *Subject {
	return &Subject{userID: userID}
}

const allowAllScope = "*"

func DevIdentity() *Subject {
	return &Subject{
		fullIdentity:  true,
		userID:        cmp.Or(os.Getenv("SAM_DEV_USER_ID"), "sam_dev_user"),
		email:         cmp.Or(os.Getenv("SAM_DEV_USER_EMAIL"), "sam@dev.local"),
		name:          cmp.Or(os.Getenv("SAM_DEV_USER_NAME"), "Sam Dev User"),
		authenticated: true,
		authMethod:    "development",
		scopes:        []string{allowAllScope},
	}
}

func DevMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := ContextWithSubject(r.Context(), ForDevMiddlewareCaller(scopesFromHeader(r)))
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func scopesFromHeader(r *http.Request) []string {
	raw := r.Header.Get("X-SAM-Scopes")
	if raw == "" {
		return nil
	}
	var scopes []string
	for s := range strings.SplitSeq(raw, ",") {
		if s = strings.TrimSpace(s); s != "" {
			scopes = append(scopes, s)
		}
	}
	return scopes
}

type AuthProvider interface {
	Middleware() func(http.Handler) http.Handler

	RegisterRoutes(mux *http.ServeMux)

	ProviderType() string
}

const (
	ProviderTypeNoop       = "noop"
	ProviderTypeEnterprise = "enterprise"
)

var SkipPaths = map[string]bool{
	"/api/v1/config":                                 true,
	"/api/v1/config/capabilities":                    true,
	"/api/v2/chat/config/login":                      true,
	"/api/v1/auth/callback":                          true,
	"/api/v1/auth/cli-config":                        true,
	"/api/v1/auth/tool/callback":                     true,
	"/api/v1/auth/login":                             true,
	"/api/v1/auth/refresh":                           true,
	"/api/v1/csrf-token":                             true,
	"/api/v2/platform/connectors/mcp/oauth/callback": true,
	"/api/v1/platform/health":                        true,
	"/api/v1/version":                                true,
	"/api/v2/platform/health":                        true,
	"/api/v2/platform/version":                       true,
	openapispec.Path:                                 true,
	"/health":                                        true,
}

func ShouldSkipAuth(r *http.Request) bool {
	if r.Method == http.MethodOptions {
		return true
	}
	if SkipPaths[r.URL.Path] {
		return true
	}
	if !strings.HasPrefix(r.URL.Path, "/api/") {
		return true
	}
	return false
}

var shareSoftAuthRE = regexp.MustCompile(`^` + regexp.QuoteMeta(sharePathPrefix) + `[A-Za-z0-9_-]{21}(?:/artifacts/.+)?$`)

func IsSoftAuthPath(r *http.Request) bool {
	return r.Method == http.MethodGet && shareSoftAuthRE.MatchString(r.URL.Path)
}

func UserIDFromRequest(r *http.Request, defaultUserID string) string {
	return UserIDFromRequestContext(r.Context(), defaultUserID)
}

func UserIDFromRequestContext(ctx context.Context, defaultUserID string) string {
	id, ok := SubjectFromContext(ctx)
	if ok && id.userID != "" {
		return id.userID
	}
	return defaultUserID
}

func (id *Subject) Caller() coreauth.CallerIdentity {
	if id == nil {
		return coreauth.CallerIdentity{}
	}
	return coreauth.CallerIdentity{
		Sub:          id.userID,
		Email:        id.email,
		Name:         id.name,
		Scopes:       id.Scopes(),
		CredentialID: id.credentialID,
	}
}

func (id *Subject) TaskSignClaims(taskID string) trust.SignClaims {
	return trust.SignClaims{
		Subject:        id.userID,
		TaskID:         taskID,
		Roles:          id.Roles(),
		Scopes:         id.Scopes(),
		Email:          id.email,
		Name:           id.name,
		AuthMethod:     id.authMethod,
		CredentialID:   id.credentialID,
		PlatformUserID: id.platformUserID,
	}
}

func CallerFromRequest(r *http.Request) (coreauth.CallerIdentity, bool) {
	id, ok := SubjectFromContext(r.Context())
	if !ok {
		return coreauth.CallerIdentity{}, false
	}
	return id.Caller(), true
}

func (id *Subject) HasScope(required string) bool {
	return coreauth.HasScope(id.scopes, required)
}
