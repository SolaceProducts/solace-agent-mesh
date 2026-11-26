// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"go/ast"
	"go/types"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/httpsse/chatv2"
)

var chatRouteDeps = map[string]string{
	"GET /tasks/{id}/events":                                  "StreamTask",
	"GET /tasks/{id}/pendingInput":                            "ReadPendingInput",
	"PUT /tasks/{id}/userInput":                               "AnswerInput",
	"PUT /message/send":                                       "Send",
	"PUT /message/stream":                                     "Send",
	"GET /tasks/{id}":                                         "ReadTask",
	"GET /tasks/{id}/export":                                  "ExportTask",
	"PUT /tasks/{id}/cancel":                                  "CancelTask",
	"GET /sessions":                                           "ListSessions",
	"GET /sessions/search":                                    "SearchSessions",
	"POST /sessions":                                          "CreateSession",
	"GET /sessions/{id}":                                      "GetSession",
	"PATCH /sessions/{id}":                                    "UpdateSession",
	"DELETE /sessions/{id}":                                   "DeleteSession",
	"PUT /sessions/{id}/project":                              "SetSessionProject",
	"PUT /sessions/{id}/viewed":                               "MarkSessionViewed",
	"PUT /sessions/{id}/title":                                "GenerateSessionTitle",
	"GET /sessions/{id}/messages":                             "ListSessionMessages",
	"GET /sessions/{id}/events/unconsumed":                    "ReadUnconsumedEvents",
	"GET /sessions/{id}/contextUsage":                         "ReadContextUsage",
	"PUT /sessions/{id}/compaction":                           "CompactSession",
	"PUT /sessions/{sessionId}/chatTasks/{id}":                "SaveChatTask",
	"GET /sessions/{id}/chatTasks":                            "ListChatTasks",
	"GET /config":                                             "ReadConfig",
	"GET /config/login":                                       "ReadLoginConfig",
	"GET /user":                                               "ReadUser",
	"GET /user/capabilities":                                  "ReadUserCapabilities",
	"GET /agents":                                             "ListAgents",
	"GET /artifacts/scheduled/{sessionId}/{filename}":         "DownloadScheduledArtifact",
	"POST /artifacts":                                         "UploadArtifact",
	"GET /artifacts":                                          "ListAllArtifacts",
	"GET /artifacts/byUri":                                    "DownloadArtifactByURI",
	"GET /sessions/{id}/artifacts":                            "ListSessionArtifacts",
	"GET /sessions/{sessionId}/artifacts/{filename}":          "DownloadArtifact",
	"GET /sessions/{sessionId}/artifacts/{filename}/versions": "ListArtifactVersions",
	"GET /sessions/{sessionId}/artifacts/{filename}/versions/{version}": "DownloadArtifactVersion",
	"DELETE /sessions/{sessionId}/artifacts/{filename}":                 "DeleteArtifact",
	"PUT /sessions/{id}/artifacts/copy":                                 "CopyArtifacts",
}

func TestChatV2PerCallerMarkersMatchTheGatewayFunction(t *testing.T) {
	p := loadConformancePkg(t, nil)
	bound := p.chatDepsBindings()

	perCaller := 0
	for _, rt := range chatv2.Routes(chatv2.Deps{}) {
		reg := chatV2Registration(rt)
		if name := markerName(reg.IsPublic); !markersRequiringIdentity[name] && name != "publicReasonAgentInvokeChecked" && name != "publicReasonCapabilityFiltered" {
			continue
		}
		perCaller++
		key := rt.Method + " " + rt.Path
		field, ok := chatRouteDeps[key]
		if !ok {
			t.Errorf("%s declares a per-caller marker but chatRouteDeps does not say which Deps hook answers it, so the claim is unverified", key)
			continue
		}
		fn := bound[field]
		if fn == nil {
			t.Errorf("%s: no gateway function is bound to chatv2.Deps.%s", key, field)
			continue
		}
		if via := p.reachesResolver(fn); via == "" {
			t.Errorf("%s declares %q but %s, bound to Deps.%s, never reads the caller", key, reg.IsPublic, shortName(fn), field)
		}
		if markerName(reg.IsPublic) == "publicReasonCapabilityFiltered" && !p.reachesCanAccessAgent(fn) {
			t.Errorf("%s declares %q but %s, bound to Deps.%s, never reaches GatewayCore.CanAccessAgent, so nothing filters the catalog per caller", key, reg.IsPublic, shortName(fn), field)
		}
	}
	checkAgentInvokeIsEnforced(t, p, bound)
	if perCaller == 0 {
		t.Fatal("no chat route carries a per-caller marker, so this check verified nothing")
	}
	for key := range chatRouteDeps {
		if !chatRouteExists(key) {
			t.Errorf("chatRouteDeps names %q, which is not a chat route any more", key)
		}
	}
}

func TestChatV2NoIdentityMarkersMatchTheGatewayFunction(t *testing.T) {
	p := loadConformancePkg(t, nil)
	bound := p.chatDepsBindings()

	checked := 0
	for _, rt := range chatv2.Routes(chatv2.Deps{}) {
		reg := chatV2Registration(rt)
		if !markersForbiddingIdentity[markerName(reg.IsPublic)] {
			continue
		}
		checked++
		key := rt.Method + " " + rt.Path
		field, ok := chatRouteDeps[key]
		if !ok {
			t.Errorf("%s declares a marker that forbids reading the caller but chatRouteDeps does not say which Deps hook answers it, so the claim is unverified", key)
			continue
		}
		fn := bound[field]
		if fn == nil {
			t.Errorf("%s: no gateway function is bound to chatv2.Deps.%s", key, field)
			continue
		}
		if via := p.reachesResolver(fn); via != "" {
			t.Errorf("%s declares %q, so it must not read the caller, but %s, bound to Deps.%s, reaches %s", key, reg.IsPublic, shortName(fn), field, via)
		}
	}
	if checked == 0 {
		t.Fatal("no chat route carries a marker that forbids reading the caller, so this check verified nothing")
	}
}

func chatRouteExists(key string) bool {
	for _, rt := range chatv2.Routes(chatv2.Deps{}) {
		if rt.Method+" "+rt.Path == key {
			return true
		}
	}
	return false
}

func markerName(text string) string {
	for name, value := range map[string]string{
		"publicReasonOwnerScoped":        publicReasonOwnerScoped,
		"publicReasonShareScoped":        publicReasonShareScoped,
		"publicReasonActivityScoped":     publicReasonActivityScoped,
		"publicReasonTaskTraceCovered":   publicReasonTaskTraceCovered,
		"publicReasonAgentInvokeChecked": publicReasonAgentInvokeChecked,
		"publicReasonCapabilityFiltered": publicReasonCapabilityFiltered,
		"publicReasonNoUserData":         publicReasonNoUserData,
		"publicReasonAuthSkip":           publicReasonAuthSkip,
	} {
		if value == text {
			return name
		}
	}
	return ""
}

func (p *conformancePkg) chatDepsBindings() map[string]*types.Func {
	out := map[string]*types.Func{}
	for _, file := range p.pkg.Syntax {
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok || !p.isChatDeps(lit) {
				return true
			}
			for _, elt := range lit.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				key, ok := kv.Key.(*ast.Ident)
				if !ok {
					continue
				}
				if chain := p.resolveChain(kv.Value); len(chain) == 1 {
					out[key.Name] = chain[0]
				}
			}
			return true
		})
	}
	return out
}

func (p *conformancePkg) isChatDeps(lit *ast.CompositeLit) bool {
	named, ok := p.pkg.TypesInfo.TypeOf(lit).(*types.Named)
	return ok && named.Obj().Name() == "Deps" && strings.HasSuffix(named.Obj().Pkg().Path(), "/httpsse/chatv2")
}

func checkAgentInvokeIsEnforced(t *testing.T, p *conformancePkg, bound map[string]*types.Func) {
	t.Helper()
	send := bound[chatRouteDeps["PUT /message/send"]]
	if send == nil {
		t.Fatal("no gateway function is bound to chatv2.Deps.Send")
	}
	reachesSubmit := p.reaches(send, func(callee *types.Func) bool {
		return callee.Name() == "SubmitTaskAttributedTo" && receiverTypeName(callee) == "GatewayCore"
	})
	if reachesSubmit == "" {
		t.Errorf("%s, bound to Deps.Send, never reaches GatewayCore.SubmitTaskAttributedTo, which is where the agent invoke check runs", shortName(send))
	}
}

func (p *conformancePkg) reachesCanAccessAgent(fn *types.Func) bool {
	return p.reaches(fn, func(callee *types.Func) bool {
		return callee.Name() == "CanAccessAgent" && receiverTypeName(callee) == "GatewayCore"
	}) != ""
}
