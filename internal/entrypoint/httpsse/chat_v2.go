// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"fmt"
	"net/http"

	"github.com/SolaceProducts/solace-agent-mesh/internal/apicontract"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/httpsse/chatv2"
	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
)

func (a *HTTPSSEAdapter) mountChatV2(mux *http.ServeMux, routes []apicontract.Route) {
	for _, rt := range routes {
		a.routes = append(a.routes, chatV2Registration(rt))
	}
	apicontract.Mount(mux, chatv2.Prefix, routes, chatV2Guard, nil)
}

var chatV2GateMarkers = map[apicontract.Gate]string{
	chatv2.GateSignedIn:        publicReasonNoUserData,
	chatv2.GatePreLogin:        publicReasonAuthSkip,
	chatv2.GateCaller:          publicReasonOwnerScoped,
	chatv2.GateTaskReader:      publicReasonActivityScoped,
	chatv2.GateTaskOwner:       publicReasonOwnerScoped,
	chatv2.GateTaskTrace:       publicReasonTaskTraceCovered,
	chatv2.GateAgentInvoke:     publicReasonAgentInvokeChecked,
	chatv2.GateTaskStream:      publicReasonActivityScoped,
	chatv2.GateTaskInputReader: publicReasonActivityScoped,
	chatv2.GateSessionOwner:    publicReasonOwnerScoped,
	chatv2.GateAgentVisible:    publicReasonCapabilityFiltered,

	chatv2.GateArtifactReader:    publicReasonShareScoped,
	chatv2.GateArtifactOwner:     publicReasonOwnerScoped,
	chatv2.GateScheduledArtifact: publicReasonOwnerScoped,
}

func chatV2Registration(rt apicontract.Route) RouteRegistration {
	reg := RouteRegistration{Method: rt.Method, Path: chatv2.Prefix + rt.Path}
	marker, gated := chatV2GateMarkers[rt.Gate]
	switch {
	case gated:
		reg.IsPublic = marker
	case rt.Gate == "" && rt.Scope != "":
		reg.Scope = rt.Scope
	default:
		panic(unauthorizedChatRouteMessage(rt))
	}
	return reg
}

func chatV2Guard(rt apicontract.Route, next http.HandlerFunc) http.HandlerFunc {
	_, gated := chatV2GateMarkers[rt.Gate]
	switch {
	case gated:
		return requireJSONUnlessForm(rt, next)
	case rt.Gate == "" && rt.Scope != "":
		return requireJSONUnlessForm(rt, scoped(rt.Scope, next))
	default:
		panic(unauthorizedChatRouteMessage(rt))
	}
}

func requireJSONUnlessForm(rt apicontract.Route, next http.HandlerFunc) http.HandlerFunc {
	if len(rt.Handler.FormParts) > 0 {
		return next
	}
	return requireJSONOnWrites(next)
}

func requireJSONOnWrites(next http.HandlerFunc) http.HandlerFunc {
	return httputil.RequireJSONMiddleware(next).ServeHTTP
}

func unauthorizedChatRouteMessage(rt apicontract.Route) string {
	return fmt.Sprintf("%s %s%s: a chat route needs a Scope or a known chatv2 gate, got gate %q, scope %q, anyOfScopes %v",
		rt.Method, chatv2.Prefix, rt.Path, rt.Gate, rt.Scope, rt.AnyOfScopes)
}
