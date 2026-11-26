// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package whatsapp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/identity"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
)

const metricOutboundDuration = "outbound.request.duration"

const agentUnderTest = "Concierge"

func newMetricsRig(t *testing.T, defaultAgent string) (*Adapter, *countingMetrics, *waMock) {
	t.Helper()
	mock := newWAMock(t)
	a := newAdapterRig(t, mock, defaultAgent)
	metrics := newCountingMetrics()
	a.metrics = metrics
	if defaultAgent != "" {
		a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: defaultAgent})
	}
	return a, metrics, mock
}

func gatewayLabels(a *Adapter, extra ...string) map[string]string {
	want := map[string]string{"gatewayId": a.gatewayID()}
	for i := 0; i+1 < len(extra); i += 2 {
		want[extra[i]] = extra[i+1]
	}
	return want
}

func typedMessage(wamid, from, msgType string) (*webhookChangeValue, *inboundMessage) {
	msg := &inboundMessage{From: from, ID: wamid, Type: msgType}
	return &webhookChangeValue{Contacts: []webhookContact{{WaID: from}}}, msg
}

func TestMetricLabels_InboundTypeIsBounded(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"text":        "text",
		"interactive": "interactive",
		"reaction":    "reaction",
		"sticker":     "sticker",
		"unknown":     "unknown",
		"":            "none",
		"flow_reply":  "other",
		"../../etc":   "other",
	} {
		if got := inboundTypeLabel(in); got != want {
			t.Errorf("inboundTypeLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMetrics_NilRecorderIsSafe(t *testing.T) {
	t.Parallel()
	mock := newWAMock(t)
	a := newAdapterRig(t, mock, agentUnderTest)
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: agentUnderTest})
	a.metrics = nil

	if got := a.deliver(textMessage("wamid.NILMETRICS", "15551230000", "hello")); got != settleAck {
		t.Fatalf("settlement = %v, want settleAck", got)
	}
}

func TestMetrics_InboundMessagesCountedOncePerMessage(t *testing.T) {
	t.Parallel()
	a, metrics, _ := newMetricsRig(t, agentUnderTest)
	a.core.SetScopeResolver(waScopeResolver{scopes: nil})

	value, msg := textMessage("wamid.RETRIED", "15551230000", "hello")
	for range maxAuthzDenialAttempts + 1 {
		if a.deliver(value, msg) == settleAck {
			break
		}
	}

	if got := metrics.countWhere(metricWhatsAppInboundMessages, gatewayLabels(a, "type", "text")); got != 1 {
		t.Errorf("inbound messages counted = %d, want 1 — a message retried through the budget arrived once", got)
	}
}

func TestMetrics_InboundTypesAreAllCounted(t *testing.T) {
	t.Parallel()
	a, metrics, _ := newMetricsRig(t, agentUnderTest)

	a.deliver(textMessage("wamid.T", "15551230000", "hello"))
	a.deliver(typedMessage("wamid.S", "15551230001", "sticker"))
	a.deliver(typedMessage("wamid.R", "15551230002", "reaction"))
	a.deliver(typedMessage("wamid.X", "15551230003", "flow_reply"))

	for label, want := range map[string]int64{"text": 1, "sticker": 1, "reaction": 1, "other": 1} {
		if got := metrics.countWhere(metricWhatsAppInboundMessages, gatewayLabels(a, "type", label)); got != want {
			t.Errorf("inbound type %q counted = %d, want %d", label, got, want)
		}
	}
}

func TestMetrics_RedeliveryIsNeitherArrivalNorLoss(t *testing.T) {
	t.Parallel()
	a, metrics, _ := newMetricsRig(t, agentUnderTest)

	value, msg := textMessage("wamid.DUP", "15551230000", "hello")
	if got := a.deliver(value, msg); got != settleAck {
		t.Fatalf("first delivery settled %v, want settleAck", got)
	}
	if got := a.deliver(value, msg); got != settleAck {
		t.Fatalf("redelivery settled %v, want settleAck", got)
	}

	if got := metrics.countWhere(metricWhatsAppInboundMessages, gatewayLabels(a, "type", "text")); got != 1 {
		t.Errorf("inbound messages = %d, want 1 — a duplicate is not a second arrival", got)
	}
	if got := metrics.countWhere(metricWhatsAppAckedWithoutTask, gatewayLabels(a)); got != 0 {
		t.Errorf("acked-without-task = %d, want 0 — the duplicate's original produced the task", got)
	}
}

func TestMetrics_AckedWithoutTaskReasons(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name       string
		agent      string
		setup      func(t *testing.T, a *Adapter)
		message    func() (*webhookChangeValue, *inboundMessage)
		wantReason string
	}{
		{
			name:  "a type this entrypoint does not handle",
			agent: agentUnderTest,
			message: func() (*webhookChangeValue, *inboundMessage) {
				return typedMessage("wamid.STICKER", "15551230000", "sticker")
			},
			wantReason: ackNoTaskDeclined,
		},
		{
			name:  "a sender nobody has enrolled",
			agent: agentUnderTest,
			setup: func(_ *testing.T, a *Adapter) {
				a.cfg.IdentityLookupKey = IdentityLookupPhone
				a.cfg.IdentityClaimKey = defaultPhoneClaimKey
				a.core.SetIdentityAuthorizer(&stubAuthorizer{res: auth.SenderAuthorization{Resolution: identity.LookupNoMatch}})
			},
			message: func() (*webhookChangeValue, *inboundMessage) {
				return textMessage("wamid.STRANGER2", "15551230000", "hello")
			},
			wantReason: refusalSenderUnidentified,
		},
		{
			name:  "a sender the identity gate has no store to check against",
			agent: agentUnderTest,
			setup: func(_ *testing.T, a *Adapter) {
				a.cfg.IdentityLookupKey = IdentityLookupPhone
				a.cfg.IdentityClaimKey = defaultPhoneClaimKey
				a.core.SetIdentityAuthorizer(&stubAuthorizer{res: auth.SenderAuthorization{Resolution: identity.LookupNotConfigured}})
			},
			message: func() (*webhookChangeValue, *inboundMessage) {
				return textMessage("wamid.UNWIRED", "15551230000", "hello")
			},
			wantReason: refusalIdentityGateUnavailable,
		},
		{
			name:  "a sender past their message budget",
			agent: agentUnderTest,
			setup: func(t *testing.T, a *Adapter) {
				for range senderBurst {
					if !a.senders.admitMessage("15551230000") {
						t.Fatal("the burst budget was already spent before the test charged it")
					}
				}
			},
			message: func() (*webhookChangeValue, *inboundMessage) {
				return textMessage("wamid.FLOOD", "15551230000", "again")
			},
			wantReason: refusalRateLimited,
		},
		{
			name:       "a message Meta sent with no id",
			message:    func() (*webhookChangeValue, *inboundMessage) { return textMessage("", "15551230000", "hello") },
			wantReason: ackNoTaskUntracked,
		},
		{
			name: "a message that spent its retry budget",
			setup: func(_ *testing.T, a *Adapter) {
				a.claims = &stubClaims{
					outcomes: []store.ClaimOutcome{store.ClaimAcquired},
					attempts: maxTransientAttempts, total: maxTransientAttempts, kind: "transient",
				}
			},
			message: func() (*webhookChangeValue, *inboundMessage) {
				return textMessage("wamid.SPENT", "15551230000", "hello")
			},
			wantReason: ackNoTaskExhausted,
		},
		{
			name:  "a sender whose authorization budget ran out",
			agent: agentUnderTest,
			setup: func(_ *testing.T, a *Adapter) {
				a.core.SetScopeResolver(waScopeResolver{scopes: nil})
				a.claims = &stubClaims{
					outcomes: []store.ClaimOutcome{store.ClaimAcquired},
					attempts: maxAuthzDenialAttempts, total: maxAuthzDenialAttempts, kind: "unauthorized",
				}
			},
			message: func() (*webhookChangeValue, *inboundMessage) {
				return textMessage("wamid.DENIED", "15551230000", "hello")
			},
			wantReason: ackNoTaskUnauthorized,
		},
		{
			name:  "an authz refusal that hit the absolute ceiling",
			agent: agentUnderTest,
			setup: func(_ *testing.T, a *Adapter) {
				a.core.SetScopeResolver(waScopeResolver{scopes: nil})
				a.claims = &stubClaims{
					outcomes: []store.ClaimOutcome{store.ClaimAcquired},
					attempts: maxAuthzDenialAttempts, total: maxTransientAttempts, kind: "unauthorized",
				}
			},
			message: func() (*webhookChangeValue, *inboundMessage) {
				return textMessage("wamid.CEILING", "15551230000", "hello")
			},
			wantReason: ackNoTaskExhausted,
		},
		{
			name:  "a message that keeps killing its consumer",
			agent: agentUnderTest,
			setup: func(_ *testing.T, a *Adapter) {
				a.claims = &stubClaims{
					outcomes: []store.ClaimOutcome{store.ClaimAcquired},
					attempts: stalledRetryBudget, total: stalledRetryBudget, kind: store.WhatsAppStalledFailure,
				}
			},
			message: func() (*webhookChangeValue, *inboundMessage) {
				return textMessage("wamid.STALL", "15551230000", "hello")
			},
			wantReason: ackNoTaskStalled,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a, metrics, _ := newMetricsRig(t, tt.agent)
			if tt.setup != nil {
				tt.setup(t, a)
			}

			if got := a.deliver(tt.message()); got != settleAck {
				t.Fatalf("settlement = %v, want settleAck — a message that is nacked was not acked without a task", got)
			}
			if got := metrics.countWhere(metricWhatsAppAckedWithoutTask, gatewayLabels(a, "reason", tt.wantReason)); got != 1 {
				t.Errorf("acked-without-task{reason=%s} = %d, want 1; all emissions = %+v",
					tt.wantReason, got, metrics.samplesFor(metricWhatsAppAckedWithoutTask))
			}
			if got := metrics.countWhere(metricWhatsAppAckedWithoutTask, gatewayLabels(a)); got != 1 {
				t.Errorf("total acked-without-task = %d, want 1; emissions = %+v",
					got, metrics.samplesFor(metricWhatsAppAckedWithoutTask))
			}
		})
	}
}

func TestMetrics_SubmittedMessageIsNotAckedWithoutTask(t *testing.T) {
	t.Parallel()
	a, metrics, _ := newMetricsRig(t, agentUnderTest)

	if got := a.deliver(textMessage("wamid.OK", "15551230000", "hello")); got != settleAck {
		t.Fatalf("settlement = %v, want settleAck", got)
	}
	if got := metrics.countWhere(metricWhatsAppAckedWithoutTask, gatewayLabels(a)); got != 0 {
		t.Errorf("acked-without-task = %d, want 0; emissions = %+v", got, metrics.samplesFor(metricWhatsAppAckedWithoutTask))
	}
	if got := metrics.countWhere(metricWhatsAppSubmitFailures, gatewayLabels(a)); got != 0 {
		t.Errorf("submit failures = %d, want 0", got)
	}
}

func TestMetrics_SubmitFailuresCountedByReason(t *testing.T) {
	t.Parallel()

	t.Run("no agent to route to", func(t *testing.T) {
		t.Parallel()
		a, metrics, _ := newMetricsRig(t, "")

		a.deliver(textMessage("wamid.NOAGENT", "15551230000", "hello"))

		if got := metrics.countWhere(metricWhatsAppSubmitFailures, gatewayLabels(a, "reason", "agent_not_found")); got != 1 {
			t.Errorf("submit failures{reason=agent_not_found} = %d, want 1; emissions = %+v",
				got, metrics.samplesFor(metricWhatsAppSubmitFailures))
		}
	})

	t.Run("authorization refusal", func(t *testing.T) {
		t.Parallel()
		a, metrics, _ := newMetricsRig(t, agentUnderTest)
		a.core.SetScopeResolver(waScopeResolver{scopes: []string{rbac.AgentInvokeScope("OtherAgent")}})

		a.deliver(textMessage("wamid.DENIED", "15551230000", "hello"))

		if got := metrics.countWhere(metricWhatsAppSubmitFailures, gatewayLabels(a, "reason", "forbidden")); got != 1 {
			t.Errorf("submit failures{reason=forbidden} = %d, want 1; emissions = %+v",
				got, metrics.samplesFor(metricWhatsAppSubmitFailures))
		}
		if got := metrics.countWhere(metricWhatsAppSubmitFailures, gatewayLabels(a, "reason", "internal")); got != 0 {
			t.Errorf("submit failures{reason=internal} = %d, want 0 — a denial is not a fault of ours", got)
		}
	})

	t.Run("a store fault is internal", func(t *testing.T) {
		t.Parallel()
		a, metrics, _ := newMetricsRig(t, agentUnderTest)
		a.sessKeys = store.NewGatewaySessionKeyRepo(closedStore(t))

		a.deliver(textMessage("wamid.STOREFAULT", "15551230000", "hello"))

		if got := metrics.countWhere(metricWhatsAppSubmitFailures, gatewayLabels(a, "reason", "internal")); got != 1 {
			t.Errorf("submit failures{reason=internal} = %d, want 1; emissions = %+v",
				got, metrics.samplesFor(metricWhatsAppSubmitFailures))
		}
	})
}

func TestMetrics_HILResumeOutcomes(t *testing.T) {
	t.Parallel()
	const agentName = agentUnderTest
	const phone = "15551230000"

	for _, tt := range []struct {
		name       string
		scopes     []string
		wantResult string
	}{
		{name: "answer reaches the agent", scopes: []string{rbac.AgentInvokeScope(agentName)}, wantResult: outcomeSubmitted.String()},
		{name: "answer refused by authorization", scopes: nil, wantResult: outcomeHILUnauthorized.String()},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a, metrics, _ := newMetricsRig(t, agentName)
			a.core.SetScopeResolver(waScopeResolver{scopes: tt.scopes})
			_ = a.core.Tasks().Register("t-hil", agentName, "whatsapp:"+phone, entrypoint.TaskIdentity{})
			a.core.Tasks().SetMetadata("t-hil", &taskMeta{FromPhone: phone, SessionID: "sess"})

			a.OnStatusUpdate(t.Context(), "t-hil", inputNeededEvent(t, "req-1", json.RawMessage(askSurfaceSingleChoiceWA)), nil)
			waitPromptDelivered(t, a, phone)

			value, msg := interactiveReply(phone, "wamid.hil-reply", optionRefID(1))
			a.deliver(value, msg)

			if got := metrics.countWhere(metricWhatsAppHILResume, gatewayLabels(a, "outcome", tt.wantResult)); got != 1 {
				t.Errorf("hil resume{outcome=%s} = %d, want 1; emissions = %+v",
					tt.wantResult, got, metrics.samplesFor(metricWhatsAppHILResume))
			}
		})
	}
}

func TestMetrics_HILResumeCountsUnusableReply(t *testing.T) {
	t.Parallel()
	const agentName = agentUnderTest
	const phone = "15551230000"
	a, metrics, _ := newMetricsRig(t, agentName)
	a.core.SetScopeResolver(waScopeResolver{scopes: []string{rbac.AgentInvokeScope(agentName)}})
	_ = a.core.Tasks().Register("t-hil", agentName, "whatsapp:"+phone, entrypoint.TaskIdentity{})
	a.core.Tasks().SetMetadata("t-hil", &taskMeta{FromPhone: phone, SessionID: "sess"})

	a.OnStatusUpdate(t.Context(), "t-hil", inputNeededEvent(t, "req-1", json.RawMessage(askSurfaceSingleChoiceWA)), nil)
	waitPromptDelivered(t, a, phone)

	value, msg := typedMessage("wamid.hil-image", phone, "image")
	a.deliver(value, msg)

	if got := metrics.countWhere(metricWhatsAppHILResume, gatewayLabels(a, "outcome", outcomeDeclined.String())); got != 1 {
		t.Errorf("hil resume{outcome=declined} = %d, want 1; emissions = %+v",
			got, metrics.samplesFor(metricWhatsAppHILResume))
	}
}

func TestMetrics_HILResumeCountsOneTapNotOneDelivery(t *testing.T) {
	t.Parallel()
	const phone = "15551230000"
	a, metrics, _ := newMetricsRig(t, agentUnderTest)
	a.core.SetScopeResolver(waScopeResolver{scopes: nil})
	_ = a.core.Tasks().Register("t-hil", agentUnderTest, "whatsapp:"+phone, entrypoint.TaskIdentity{})
	a.core.Tasks().SetMetadata("t-hil", &taskMeta{FromPhone: phone, SessionID: "sess"})

	a.OnStatusUpdate(t.Context(), "t-hil", inputNeededEvent(t, "req-1", json.RawMessage(askSurfaceSingleChoiceWA)), nil)
	waitPromptDelivered(t, a, phone)

	value, msg := interactiveReply(phone, "wamid.hil-reply", optionRefID(1))
	if deliveries := driveToSettlement(t, a, value, msg); deliveries != maxAuthzDenialAttempts+1 {
		t.Fatalf("deliveries before the decline = %d, want %d — a denied tap must be retried",
			deliveries, maxAuthzDenialAttempts+1)
	}

	if got := metrics.countWhere(metricWhatsAppHILResume, gatewayLabels(a)); got != 1 {
		t.Errorf("hil resume emissions = %d, want 1 for one tap; emissions = %+v",
			got, metrics.samplesFor(metricWhatsAppHILResume))
	}
	if got := metrics.countWhere(metricWhatsAppHILResume, gatewayLabels(a, "outcome", outcomeHILUnauthorized.String())); got != 1 {
		t.Errorf("hil resume{outcome=unauthorized_hil} = %d, want 1", got)
	}
}

func TestMetrics_SendLatencyUsesTheSharedOutboundFamily(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(mockSendResponse()))
	}))
	defer srv.Close()

	metrics := newCountingMetrics()
	runtime.Init(metrics)
	t.Cleanup(func() { runtime.Init(runtime.NoopMetrics{}) })

	c, _ := newRetryTestClient(t, srv.URL)
	if _, err := c.SendText(t.Context(), "15551234567", "hello"); err != nil {
		t.Fatalf("SendText: %v", err)
	}

	sent := findSample(metrics.samplesFor(metricOutboundDuration), map[string]string{
		"service.peer.name": whatsappGatewayName,
		"operation.name":    opSendMessage,
	})
	if sent == nil {
		t.Fatalf("no send-latency histogram; emissions = %+v", metrics.samplesFor(metricOutboundDuration))
	}
	if sent.duration <= 0 {
		t.Errorf("send latency = %v, want a positive duration", sent.duration)
	}
	if got := sent.labels["error.type"]; got != "" {
		t.Errorf("error.type = %q, want empty for a successful send", got)
	}
}

func TestMetrics_SendLatencyLabelsTheFailure(t *testing.T) {
	for _, tt := range []struct {
		name    string
		status  int
		body    string
		wantErr string
	}{
		{"rejected credential", http.StatusUnauthorized, `{"error":{"code":190,"message":"invalid token"}}`, errTypeAuth},
		{"auth-shaped with no envelope", http.StatusForbidden, `<html>403</html>`, errTypeAuth},
		{"throttled", http.StatusTooManyRequests, `{"error":{"code":130429}}`, errTypeRateLimit},
		{"throttled under a 400", http.StatusBadRequest, `{"error":{"code":80007}}`, errTypeRateLimit},
		{"meta unavailable", http.StatusServiceUnavailable, `{"error":{"message":"unavailable"}}`, errTypeServer},
		{"message rejected outright", http.StatusBadRequest, `{"error":{"code":131026}}`, errTypeClient},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()

			metrics := newCountingMetrics()
			runtime.Init(metrics)
			t.Cleanup(func() { runtime.Init(runtime.NoopMetrics{}) })

			c, _ := newRetryTestClient(t, srv.URL)
			if _, err := c.SendText(t.Context(), "15551234567", "hello"); err == nil {
				t.Fatal("expected the send to fail")
			}

			if findSample(metrics.samplesFor(metricOutboundDuration), map[string]string{
				"service.peer.name": whatsappGatewayName,
				"operation.name":    opSendMessage,
				"error.type":        tt.wantErr,
			}) == nil {
				t.Errorf("no send-latency histogram with error.type=%s; emissions = %+v",
					tt.wantErr, metrics.samplesFor(metricOutboundDuration))
			}
		})
	}
}

func TestMetrics_SendLatencyTreatsALostResponseAsSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "512")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"messages":`))
	}))
	defer srv.Close()

	metrics := newCountingMetrics()
	runtime.Init(metrics)
	t.Cleanup(func() { runtime.Init(runtime.NoopMetrics{}) })

	c, _ := newRetryTestClient(t, srv.URL)
	_, err := c.SendText(t.Context(), "15551234567", "hello")
	if !errors.Is(err, errRequestAccepted) {
		t.Fatalf("SendText err = %v, want an errRequestAccepted — the rig must produce a torn 2xx", err)
	}

	sent := findSample(metrics.samplesFor(metricOutboundDuration), map[string]string{
		"service.peer.name": whatsappGatewayName,
		"operation.name":    opSendMessage,
	})
	if sent == nil {
		t.Fatalf("no send-latency histogram; emissions = %+v", metrics.samplesFor(metricOutboundDuration))
	}
	if got := sent.labels["error.type"]; got != "" {
		t.Errorf("error.type = %q, want empty: Meta accepted the message and the user has it", got)
	}
}

func TestMetrics_OutboundErrorTypeAgreesWithTheLogClass(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name      string
		err       error
		wantClass graphFailureClass
		wantType  string
	}{
		{"throttle", &graphAPIError{HTTPStatus: 429, GraphErr: graphError{Code: codeThroughputReached}}, classRateLimited, errTypeRateLimit},
		{"dead token", &graphAPIError{HTTPStatus: 401, GraphErr: graphError{Code: codeOAuthException}}, classCredentials, errTypeAuth},
		{"unattributable 403", &graphAPIError{HTTPStatus: 403}, classUnverified, errTypeAuth},
		{"meta 5xx", &graphAPIError{HTTPStatus: 503}, classTransient, errTypeServer},
		{"rejected message", &graphAPIError{HTTPStatus: 400, GraphErr: graphError{Code: 131026}}, classPermanent, errTypeClient},
		{"never reached meta", &net.OpError{Op: "dial", Err: errors.New("connection refused")}, classTransport, errTypeConnection},
		{"never left this process", fmt.Errorf("marshal whatsapp message: %w: %w", errNeverSent, errors.New("unsupported type")), classLocal, errTypeLocal},
		{"an unattributable transport failure", errors.New("read: unexpected EOF"), classTransport, errTypeConnection},
		{"our own shutdown", context.Canceled, classCanceled, errTypeCanceled},
		{"meta too slow", context.DeadlineExceeded, classTransport, errTypeTimeout},
		{"a dns deadline", &net.DNSError{Err: "i/o timeout", IsTimeout: true}, classTransport, errTypeTimeout},
	} {
		if got := graphFailureClassOf(tt.err); got != tt.wantClass {
			t.Errorf("%s: errorClass = %q, want %q", tt.name, got, tt.wantClass)
		}
		if got := outboundErrorType(tt.err); got != tt.wantType {
			t.Errorf("%s: error.type = %q, want %q", tt.name, got, tt.wantType)
		}
	}
}

func TestMetrics_AuthorizationDenialIsNeverReportedAsInternal(t *testing.T) {
	t.Parallel()
	for _, err := range []error{
		entrypoint.ErrAgentDelegationDenied,
		entrypoint.ErrIdentityUnauthorized,
		fmt.Errorf("submit task: %w", entrypoint.ErrIdentityUnauthorized),
	} {
		if !entrypoint.IsAuthorizationDenial(err) {
			t.Errorf("IsAuthorizationDenial(%v) = false, want true", err)
		}
		if got := entrypoint.SubmitFailureReason(err); got != taxonomy.RejectForbidden {
			t.Errorf("SubmitFailureReason(%v) = %q, want %q", err, got, taxonomy.RejectForbidden)
		}
	}
	if got := entrypoint.SubmitFailureReason(errors.New("database is gone")); got != taxonomy.RejectInternal {
		t.Errorf("a genuine fault reported %q, want %q", got, taxonomy.RejectInternal)
	}
}

func TestMetrics_UndecodableMessageIsCountedAsLoss(t *testing.T) {
	t.Parallel()
	a, metrics, _ := newMetricsRig(t, agentUnderTest)

	a.handleIngressMessage(t.Context(), &broker.Message{Payload: []byte("{not json")})

	if got := metrics.countWhere(metricWhatsAppAckedWithoutTask, gatewayLabels(a, "reason", ackNoTaskUndecodable)); got != 1 {
		t.Errorf("acked-without-task{reason=undecodable} = %d, want 1; emissions = %+v",
			got, metrics.samplesFor(metricWhatsAppAckedWithoutTask))
	}
}

func TestMetrics_RefusedSenderStillCountsAsAnArrival(t *testing.T) {
	t.Parallel()
	a, metrics, _ := newMetricsRig(t, agentUnderTest)
	for range senderBurst {
		if !a.senders.admitMessage("15551230000") {
			t.Fatal("the burst budget was already spent before the test charged it")
		}
	}

	a.deliver(textMessage("wamid.FLOOD", "15551230000", "again"))

	if got := metrics.countWhere(metricWhatsAppInboundMessages, gatewayLabels(a, "type", "text")); got != 1 {
		t.Errorf("inbound messages = %d, want 1 — every no-task reason needs a matching arrival", got)
	}
	if got := metrics.countWhere(metricWhatsAppAckedWithoutTask, gatewayLabels(a, "reason", refusalRateLimited)); got != 1 {
		t.Errorf("acked-without-task{reason=sender_rate_limited} = %d, want 1", got)
	}
}

type publishFailingBroker struct {
	broker.Broker
	mu    sync.Mutex
	armed bool
}

func (b *publishFailingBroker) arm() {
	b.mu.Lock()
	b.armed = true
	b.mu.Unlock()
}

func (b *publishFailingBroker) PublishGuaranteed(ctx context.Context, topic string, msg *broker.Message) error {
	b.mu.Lock()
	armed := b.armed
	b.mu.Unlock()
	if armed && strings.Contains(topic, "/a2a/") {
		return errors.New("broker unavailable")
	}
	return b.Broker.PublishGuaranteed(ctx, topic, msg)
}

func TestMetrics_HILResumeCountsATransientFailure(t *testing.T) {
	const phone = "15551230000"
	dev := broker.NewDevBroker()
	if err := dev.Connect(t.Context()); err != nil {
		t.Fatalf("connect dev broker: %v", err)
	}
	t.Cleanup(func() { _ = dev.Disconnect(context.Background()) })
	brk := &publishFailingBroker{Broker: dev}

	mock := newWAMock(t)
	a := newAdapterRigOn(t, brk, mock, agentUnderTest)
	metrics := newCountingMetrics()
	a.metrics = metrics
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: agentUnderTest})
	a.core.SetScopeResolver(waScopeResolver{scopes: []string{rbac.AgentInvokeScope(agentUnderTest)}})
	_ = a.core.Tasks().Register("t-hil", agentUnderTest, "whatsapp:"+phone, entrypoint.TaskIdentity{})
	a.core.Tasks().SetMetadata("t-hil", &taskMeta{FromPhone: phone, SessionID: "sess"})

	a.OnStatusUpdate(t.Context(), "t-hil", inputNeededEvent(t, "req-1", json.RawMessage(askSurfaceSingleChoiceWA)), nil)
	waitPromptDelivered(t, a, phone)

	brk.arm()
	value, msg := interactiveReply(phone, "wamid.hil-reply", optionRefID(1))
	if got := a.deliver(value, msg); got != settleNack {
		t.Fatalf("settlement = %v, want settleNack — an infrastructure failure is retryable", got)
	}

	if got := metrics.countWhere(metricWhatsAppHILResume, gatewayLabels(a, "outcome", outcomeTransient.String())); got != 1 {
		t.Errorf("hil resume{outcome=transient} = %d, want 1; emissions = %+v",
			got, metrics.samplesFor(metricWhatsAppHILResume))
	}
}

func TestMetrics_OutcomeReadsTheSameInLogsAsInLabels(t *testing.T) {
	t.Parallel()
	for _, outcome := range []inboundOutcome{
		outcomeSubmitted, outcomeDeclined, outcomeUnauthorized,
		outcomeHILUnauthorized, outcomeStalled, outcomeTransient,
	} {
		var buf bytes.Buffer
		slog.New(slog.NewJSONHandler(&buf, nil)).Info("settled", "outcome", outcome)

		var line map[string]any
		if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
			t.Fatalf("unmarshal log line %q: %v", buf.String(), err)
		}
		if got := line["outcome"]; got != outcome.String() {
			t.Errorf("JSON log outcome = %#v, want %q — a number here cannot be correlated with the metric label",
				got, outcome.String())
		}
	}
}

func findSample(samples []metricSample, want map[string]string) *metricSample {
	for i := range samples {
		match := true
		for k, v := range want {
			if samples[i].labels[k] != v {
				match = false
				break
			}
		}
		if match {
			return &samples[i]
		}
	}
	return nil
}

func TestMetrics_ProbeRealPathLabels(t *testing.T) {
	t.Run("successful upload is not a failure", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"id":"media-123"}`))
		}))
		defer srv.Close()

		metrics := newCountingMetrics()
		runtime.Init(metrics)
		t.Cleanup(func() { runtime.Init(runtime.NoopMetrics{}) })

		c, _ := newRetryTestClient(t, srv.URL)
		if _, err := c.UploadMedia(t.Context(), []byte("bytes"), "text/plain", "a.txt"); err != nil {
			t.Fatalf("UploadMedia: %v", err)
		}

		up := findSample(metrics.samplesFor(metricOutboundDuration), map[string]string{
			"operation.name": opUploadMedia,
		})
		if up == nil {
			t.Fatalf("no upload histogram; emissions = %+v", metrics.samplesFor(metricOutboundDuration))
		}
		if got := up.labels["error.type"]; got != "" {
			t.Errorf("error.type = %q, want empty — the upload succeeded", got)
		}
	})

	t.Run("a fault of ours is not reported as an outage", func(t *testing.T) {
		metrics := newCountingMetrics()
		runtime.Init(metrics)
		t.Cleanup(func() { runtime.Init(runtime.NoopMetrics{}) })

		c, _ := newRetryTestClient(t, "http://meta.invalid\x7f")
		_, err := c.SendText(t.Context(), "15551234567", "hello")
		if !errors.Is(err, errNeverSent) {
			t.Fatalf("SendText err = %v, want an errNeverSent — the rig must fail before the wire", err)
		}

		sent := findSample(metrics.samplesFor(metricOutboundDuration), map[string]string{
			"operation.name": opSendMessage,
		})
		if sent == nil {
			t.Fatalf("no send histogram; emissions = %+v", metrics.samplesFor(metricOutboundDuration))
		}
		if got := sent.labels["error.type"]; got != errTypeLocal {
			t.Errorf("error.type = %q, want %q — this never reached the network", got, errTypeLocal)
		}
	})

	t.Run("an unclassifiable transport failure defaults to the network", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			if hj, ok := w.(http.Hijacker); ok {
				conn, _, hijackErr := hj.Hijack()
				if hijackErr == nil {
					_ = conn.Close()
					return
				}
			}
			t.Error("test server cannot hijack; the EOF shape cannot be produced")
		}))
		defer srv.Close()

		metrics := newCountingMetrics()
		runtime.Init(metrics)
		t.Cleanup(func() { runtime.Init(runtime.NoopMetrics{}) })

		c, _ := newRetryTestClient(t, srv.URL)
		if _, err := c.SendText(t.Context(), "15551234567", "hello"); err == nil {
			t.Fatal("expected the send to fail")
		}

		sent := findSample(metrics.samplesFor(metricOutboundDuration), map[string]string{
			"operation.name": opSendMessage,
		})
		if sent == nil {
			t.Fatalf("no send histogram; emissions = %+v", metrics.samplesFor(metricOutboundDuration))
		}
		if got := sent.labels["error.type"]; got != errTypeConnection {
			t.Errorf("error.type = %q, want %q — an unattributable transport failure is an outage, not our bug",
				got, errTypeConnection)
		}
	})

	t.Run("a real timeout is labelled as one", func(t *testing.T) {
		release := make(chan struct{})
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			<-release
			w.WriteHeader(http.StatusOK)
		}))
		t.Cleanup(func() { close(release); srv.Close() })

		metrics := newCountingMetrics()
		runtime.Init(metrics)
		t.Cleanup(func() { runtime.Init(runtime.NoopMetrics{}) })

		c, _ := newRetryTestClient(t, srv.URL)
		ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
		defer cancel()
		if _, err := c.SendText(ctx, "15551234567", "hello"); err == nil {
			t.Fatal("expected the send to time out")
		}

		sent := findSample(metrics.samplesFor(metricOutboundDuration), map[string]string{
			"operation.name": opSendMessage,
		})
		if sent == nil {
			t.Fatalf("no send histogram; emissions = %+v", metrics.samplesFor(metricOutboundDuration))
		}
		if got := sent.labels["error.type"]; got != errTypeTimeout {
			t.Errorf("error.type = %q, want %q — a timeout the client wrapped twice is still a timeout",
				got, errTypeTimeout)
		}
	})
}
