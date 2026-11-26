// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build product_analytics

package entrypoint

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/modelfamily"
	"github.com/SolaceProducts/solace-agent-mesh/internal/productanalytics"
	"github.com/SolaceProducts/solace-agent-mesh/internal/usagefacts"
)

func TestEveryApprovedRunFieldHasAProducer(t *testing.T) {
	interactive := TaskOrigin{Trigger: a2a.TriggerInteractive, AgentType: productanalytics.AgentTypeUserCreated}

	cases := map[string]func() map[string]any{
		productanalytics.PropOutcome: func() map[string]any {
			return runReportProperties(&TaskInfo{TaskOrigin: interactive}, completedTask("t"), nil, usageFacts{})
		},
		productanalytics.PropAgentType: func() map[string]any {
			return runReportProperties(&TaskInfo{TaskOrigin: interactive}, completedTask("t"), nil, usageFacts{})
		},
		productanalytics.PropErrorCategory: func() map[string]any {
			return runReportProperties(&TaskInfo{TaskOrigin: interactive}, nil, &a2a.Error{Code: -32603, Data: map[string]any{
				a2a.ErrorDataLLMErrorCategory: productanalytics.ErrorCategories[0],
			}}, usageFacts{})
		},
		productanalytics.PropProvider: func() map[string]any {
			return runReportProperties(&TaskInfo{TaskOrigin: interactive}, completedTask("t"), nil,
				usageFacts{model: usagefacts.Model{Provider: productanalytics.ProviderLabels[0]}})
		},
		productanalytics.PropModel: func() map[string]any {
			return runReportProperties(&TaskInfo{TaskOrigin: interactive}, completedTask("t"), nil,
				usageFacts{model: usagefacts.Model{Family: modelfamily.Families[0]}})
		},
		productanalytics.PropToolModels: func() map[string]any {
			return runReportProperties(&TaskInfo{TaskOrigin: interactive}, completedTask("t"), nil,
				usageFacts{toolModelFamilies: []string{modelfamily.Families[0]}})
		},
		productanalytics.PropModelType: func() map[string]any {
			return runReportProperties(&TaskInfo{TaskOrigin: interactive}, completedTask("t"), nil,
				usageFacts{model: usagefacts.Model{ServedFrom: productanalytics.ModelTypeLocal}})
		},
		productanalytics.PropSubmittedFrom: func() map[string]any {
			origin := interactive
			origin.Surface = httputil.SurfaceCLI
			return runReportProperties(&TaskInfo{TaskOrigin: origin}, completedTask("t"), nil, usageFacts{})
		},
		productanalytics.PropTraceID: func() map[string]any {
			return runReportProperties(&TaskInfo{TraceID: "019a1c2d-3e4f-7abc-8def-0123456789ab", TaskOrigin: interactive}, completedTask("t"), nil, usageFacts{})
		},
	}

	assertEveryFieldProduced(t, productanalytics.EventAgentRunCompleted, cases)
}

func TestEveryApprovedToolUseFieldHasAProducer(t *testing.T) {
	cases := map[string]func() map[string]any{
		productanalytics.PropToolCategory: func() map[string]any {
			return firstEventProperties(usageFactEvents(&TaskInfo{}, "2026-01-01T00:00:00.000Z",
				usageFacts{toolCategories: []string{productanalytics.ToolCategoryBuiltin}}))
		},
		productanalytics.PropBuiltinCategory: func() map[string]any {
			return firstEventProperties(usageFactEvents(&TaskInfo{}, "2026-01-01T00:00:00.000Z",
				usageFacts{
					toolCategories:    []string{productanalytics.ToolCategoryBuiltin},
					builtinCategories: []string{productanalytics.BuiltinToolCategories[0]},
				}))
		},
		productanalytics.PropConnectorCategory: func() map[string]any {
			return firstEventProperties(usageFactEvents(&TaskInfo{}, "2026-01-01T00:00:00.000Z",
				usageFacts{connectorCategories: []string{productanalytics.ConnectorCategories[0]}}))
		},
		productanalytics.PropTraceID: func() map[string]any {
			return firstEventProperties(usageFactEvents(&TaskInfo{TraceID: "019a1c2d-3e4f-7abc-8def-0123456789ab"}, "2026-01-01T00:00:00.000Z",
				usageFacts{toolCategories: []string{productanalytics.ToolCategoryBuiltin}}))
		},
		productanalytics.PropRunOrigin: func() map[string]any {
			return firstEventProperties(usageFactEvents(&TaskInfo{}, "2026-01-01T00:00:00.000Z",
				usageFacts{toolCategories: []string{productanalytics.ToolCategoryBuiltin}}))
		},
	}
	assertEveryFieldProduced(t, productanalytics.EventToolUsed, cases)
}

func TestEveryApprovedPeerDelegationFieldHasAProducer(t *testing.T) {
	delegation := func() map[string]any {
		return firstEventProperties(usageFactEvents(&TaskInfo{}, "2026-01-01T00:00:00.000Z",
			usageFacts{peerDelegated: true}))
	}
	cases := map[string]func() map[string]any{
		productanalytics.PropRunOrigin: delegation,
		productanalytics.PropTraceID: func() map[string]any {
			return firstEventProperties(usageFactEvents(&TaskInfo{TraceID: "019a1c2d-3e4f-7abc-8def-0123456789ab"}, "2026-01-01T00:00:00.000Z",
				usageFacts{peerDelegated: true}))
		},
	}
	assertEveryFieldProduced(t, productanalytics.EventAgentPeerDelegated, cases)
}

func TestEveryApprovedModelUseFieldHasAProducer(t *testing.T) {
	used := func() map[string]any {
		return firstEventProperties(usageFactEvents(&TaskInfo{}, "2026-01-01T00:00:00.000Z",
			usageFacts{models: []usagefacts.Model{{
				Family:     modelfamily.Families[0],
				Provider:   productanalytics.ProviderLabels[0],
				ServedFrom: productanalytics.ModelTypeLocal,
			}}}))
	}
	assertEveryFieldProduced(t, productanalytics.EventModelUsed, map[string]func() map[string]any{
		productanalytics.PropModel:     used,
		productanalytics.PropProvider:  used,
		productanalytics.PropModelType: used,
	})
}

func TestEveryApprovedRejectionFieldHasAProducer(t *testing.T) {
	cases := map[string]func() map[string]any{
		productanalytics.PropRejectReason: func() map[string]any {
			return submitRejectionProperties(productanalytics.RejectAgentNotFound, "", "")
		},
		productanalytics.PropSubmittedFrom: func() map[string]any {
			return submitRejectionProperties(productanalytics.RejectAgentNotFound, httputil.SurfaceCLI, "")
		},
		productanalytics.PropTraceID: func() map[string]any {
			return submitRejectionProperties(productanalytics.RejectAgentNotFound, "", "019a1c2d-3e4f-7abc-8def-0123456789ab")
		},
		productanalytics.PropEntrypointType: func() map[string]any {
			core := NewGatewayCore(&machineIngressAdapter{}, nil)
			return core.rejectionProperties(SubmitRejection{Reason: productanalytics.RejectForbidden})
		},
	}
	assertEveryFieldProduced(t, productanalytics.EventTaskSubmitRejected, cases)
}

func TestEveryApprovedEntrypointUseFieldHasAProducer(t *testing.T) {
	core := NewGatewayCore(&machineIngressAdapter{}, nil)
	cases := map[string]func() map[string]any{
		productanalytics.PropOutcome: func() map[string]any {
			return core.entrypointUsedEvent(completedTask("t"), nil, "2026-01-01T00:00:00.000Z").Properties
		},
		productanalytics.PropEntrypointType: func() map[string]any {
			return core.entrypointUsedEvent(completedTask("t"), nil, "2026-01-01T00:00:00.000Z").Properties
		},
	}
	assertEveryFieldProduced(t, productanalytics.EventEntrypointUsed, cases)
}

func assertEveryFieldProduced(t *testing.T, event productanalytics.EventName, cases map[string]func() map[string]any) {
	t.Helper()
	for _, field := range productanalytics.ApprovedFields(productanalytics.SurfaceServer, event) {
		produce, covered := cases[field]
		if !covered {
			t.Errorf("%s approves %q and nothing here produces it: add a case, or the field is dead weight the catalog claims is collected", event, field)
			continue
		}
		if _, ok := produce()[field]; !ok {
			t.Errorf("%s: the input for %q produced no such key, so the producer and the catalog disagree on the name or the producer stopped writing it", event, field)
		}
	}
}

func firstEventProperties(events []productanalytics.RawEvent) map[string]any {
	if len(events) == 0 {
		return nil
	}
	return events[0].Properties
}
