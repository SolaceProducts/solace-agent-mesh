// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"net/http"

	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
)

type RouteRegistration struct {
	Method       string
	Path         string
	Scope        string
	IsPublic     string
	AllowNonJSON string
	AllowAPIKey  string
}

const publicReasonAuthenticatedOnly = "authenticated caller; no route-level scope declared"

const publicReasonAuthSkip = "in auth.SkipPaths — handler enforces its own state-binding"

const publicReasonOwnerScoped = "handler enforces per-user ownership — operates only on the caller's own resources"

const publicReasonActivityScoped = "activity read: own data by default, cross-user requires activity:*:read; enforced in handler"

const publicReasonTraceReachScoped = "activity read plus trace reach: own data by default, cross-user requires activity:*:read, or agent:<id>:read_trace on the tree's root agent (list) and on every agent in the tree (status read); enforced in handler"

const publicReasonTaskTraceCovered = "task trace: owner or activity:*:read; otherwise agent:<id>:read_trace on the tree's root agent and on every agent participant, else 404; feedback grants open nothing; enforced in handler"

const publicReasonShareScoped = "own resources plus those explicitly shared with the caller; share verified in handler"

const publicReasonNoUserData = "deployment-level response — no identity resolved, no caller-specific data, nothing to authorize"

const publicReasonOwnerOrNamespaceScoped = "caller's own resources plus unowned namespace-level ones (NULL user_id); no cross-user access"

const publicReasonCapabilityFiltered = "deployment catalog filtered per item by what the caller may invoke; enforced in handler"

const publicReasonAgentInvokeChecked = "invoke check on the agent the request names, enforced in submit; 403 when the caller may not use it"

const publicReasonFeedbackReadScoped = "feedback list (cross-user): rows restricted per kind to the caller's agent/workflow read_feedback reach; no self-scope fallback; enforced in handler"

const publicReasonFeedbackItemScoped = "feedback item: caller's own row, or a row whose subject is inside the caller's read_feedback reach; 404 otherwise; enforced in handler"

const publicReasonFeedbackRollupScoped = "feedback rollups: every count restricted per kind to the caller's agent/workflow read_feedback reach; no self-scope fallback; enforced in handler"

const publicReasonGetNoScopeDeclared = "read route; no route-level scope declared"

const allowNonJSONMultipart = "accepts multipart/form-data or form-encoded body"

const allowAPIKeyVersionProbe = "sam CLI version probe before config calls"

const (
	pathSession         = "/api/v1/sessions/{id}"
	pathProject         = "/api/v1/projects/{id}"
	pathProjectShares   = "/api/v1/projects/{id}/shares"
	pathPromptGroup     = "/api/v1/prompts/groups/{id}"
	pathPromptShares    = "/api/v1/prompts/groups/{id}/shares"
	pathPromptShareItem = "/api/v1/prompts/groups/{id}/shares/{recipientEmail}"
	pathScheduledTask   = "/api/v1/scheduledTasks/{id}"
	pathShareLink       = "/api/v1/share/{id}"
	pathSpeechConfig    = "/api/v1/speech/config"
)

func (a *HTTPSSEAdapter) wireRoute(mux *http.ServeMux, r RouteRegistration, handler http.HandlerFunc) {
	a.routes = append(a.routes, r)
	var h http.Handler = handler
	if r.Scope != "" {
		h = scoped(r.Scope, handler)
	}
	if httputil.IsStateChangingMethod(r.Method) && r.AllowNonJSON == "" {
		h = httputil.RequireJSONMiddleware(h)
	}
	mux.Handle(r.Method+" "+r.Path, h)
}

func (a *HTTPSSEAdapter) apiKeyRoutes() map[string]bool {
	allowed := make(map[string]bool)
	for _, r := range a.routes {
		if r.AllowAPIKey != "" {
			allowed[r.Method+" "+r.Path] = true
		}
	}
	return allowed
}

func (a *HTTPSSEAdapter) Routes() []RouteRegistration {
	out := make([]RouteRegistration, len(a.routes))
	copy(out, a.routes)
	return out
}
