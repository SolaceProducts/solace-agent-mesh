// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package whatsapp

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/identity"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

type recordingScopeResolver struct {
	byPrincipal map[string][]string
	asked       []string
}

func (r *recordingScopeResolver) ResolveScopes(userID string, _ []string) []string {
	r.asked = append(r.asked, userID)
	return r.byPrincipal[userID]
}

type permissiveScopeResolver struct {
	scopes []string
	asked  []string
}

func (r *permissiveScopeResolver) ResolveScopes(userID string, _ []string) []string {
	r.asked = append(r.asked, userID)
	return r.scopes
}

func TestAuthorizingPrincipal(t *testing.T) {
	const agentName = "TestAgent"
	const senderPhone = "15551230000"
	senderPrincipal := "whatsapp:" + senderPhone

	for _, tt := range []struct {
		name          string
		runAs         string
		wantPrincipal string
	}{
		{
			name:          "no run_as authorizes as the sender",
			wantPrincipal: senderPrincipal,
		},
		{
			name:          "run_as authorizes as the named system user",
			runAs:         "ops",
			wantPrincipal: rbac.SystemSubPrefix + "ops",
		},
		{
			name:          "run_as default names the built-in system user",
			runAs:         "default",
			wantPrincipal: rbac.DefaultSystemUserSub,
		},
		{
			name:          "an already-prefixed run_as resolves to the same principal",
			runAs:         "system:ops",
			wantPrincipal: rbac.SystemSubPrefix + "ops",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			mock := newWAMock(t)
			a := newAdapterRig(t, mock, agentName)
			a.cfg.RunAs = tt.runAs
			a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: agentName})

			resolver := &recordingScopeResolver{byPrincipal: map[string][]string{
				tt.wantPrincipal: {rbac.AgentInvokeScope(agentName)},
			}}
			a.core.SetScopeResolver(resolver)

			published := publishedRequest(t, a, agentName, senderPhone)
			if published == nil {
				t.Fatalf("expected the task to reach the agent; principals asked about = %v", resolver.asked)
			}
			if len(resolver.asked) == 0 {
				t.Fatal("scope resolver was never consulted, so no principal was authorized")
			}
			for _, asked := range resolver.asked {
				if asked != tt.wantPrincipal {
					t.Errorf("authorized as %q, want %q", asked, tt.wantPrincipal)
				}
			}
		})
	}
}

func TestRunAsSkipsSenderResolution(t *testing.T) {
	const agentName = "TestAgent"
	const senderPhone = "15551230000"

	mock := newWAMock(t)
	a := newAdapterRig(t, mock, agentName)
	a.cfg.RunAs = "ops"
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: agentName})
	resolver := &permissiveScopeResolver{scopes: []string{rbac.AgentInvokeScope(agentName)}}
	a.core.SetScopeResolver(resolver)

	if publishedRequest(t, a, agentName, senderPhone) == nil {
		t.Fatal("expected the task to reach the agent")
	}
	if len(resolver.asked) == 0 {
		t.Fatal("scope resolver was never consulted, so this proves nothing about which principal was looked up")
	}
	for _, asked := range resolver.asked {
		if strings.HasPrefix(asked, "whatsapp:") {
			t.Errorf("the sender was presented for identity resolution as %q; run_as must skip that entirely (asked = %v)", asked, resolver.asked)
		}
	}
}

func TestRunAsBypassesTheWholeIdentityFlow(t *testing.T) {
	const agentName = "TestAgent"

	mock := newWAMock(t)
	stub := &stubAuthorizer{res: auth.SenderAuthorization{Resolution: identity.LookupNoMatch}}
	a := authorizingRig(t, mock, stub, func(c *WhatsAppConfig) {
		c.RunAs = "ops"
		c.ContactRequestTemplate = "request_contact_info_1"
		c.ContactRequestTemplateLanguage = "en_US"
	})
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: agentName})
	a.core.SetScopeResolver(&permissiveScopeResolver{scopes: []string{rbac.AgentInvokeScope(agentName)}})

	brk := a.core.Services().Broker()
	q, err := brk.CreateQueue("wa-runas-bypass-q", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create queue: %v", err)
	}
	if err = brk.Subscribe(q, a2a.AgentRequestTopic("test-ns", agentName)); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	value, msg := withheldNumberMessage("wamid.BYPASS", "what is the capital of France?")
	if got := a.deliver(value, msg); got != settleAck {
		t.Fatalf("settlement = %v, want an ack", got)
	}

	recvCtx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	received, _ := brk.Receive(recvCtx, q)
	if received == nil {
		t.Fatal("no task reached the agent: a run_as entrypoint must serve a sender it never identifies")
	}
	if stub.calls != 0 {
		t.Errorf("platform lookup ran %d times, want 0", stub.calls)
	}
	var spoken []string
	for _, sent := range mock.calls() {
		if sent.Text.Body != "" {
			spoken = append(spoken, sent.Text.Body)
		}
	}
	for _, raw := range mock.rawSends() {
		if bytes.Contains(raw, []byte(`"type":"template"`)) || bytes.Contains(raw, []byte(`"type":"interactive"`)) {
			spoken = append(spoken, string(raw))
		}
	}
	if len(spoken) != 0 {
		t.Errorf("said %d things to an unidentified sender (%q), want none — no refusal, no sign-in prompt, no contact-share template", len(spoken), spoken)
	}
	if a.pendingAuth != nil {
		row, liveErr := a.pendingAuth.Live(t.Context(), a.gatewayID(), msg.senderID(), a.nowMs())
		if liveErr != nil {
			t.Fatalf("Live: %v", liveErr)
		}
		if row != nil {
			t.Errorf("a suppression record was written for a run_as entrypoint (kind=%q)", row.Kind)
		}
	}
}

func TestRunAsDeniesWhenTheSystemUserHoldsNoScope(t *testing.T) {
	const agentName = "TestAgent"
	const senderPhone = "15551230000"

	mock := newWAMock(t)
	a := newAdapterRig(t, mock, agentName)
	a.cfg.RunAs = "ops"
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: agentName})
	a.core.SetScopeResolver(&recordingScopeResolver{byPrincipal: map[string][]string{
		"whatsapp:" + senderPhone: {rbac.AgentInvokeScope(agentName)},
	}})

	if published := publishedRequest(t, a, agentName, senderPhone); published != nil {
		t.Fatalf("expected denial: the run-as system user holds no invoke scope, got payload=%s", published)
	}
}

func TestSwappedSubmitCarriesRequestingUser(t *testing.T) {
	const agentName = "TestAgent"
	const senderPhone = "15551230000"

	for _, tt := range []struct {
		name  string
		runAs string
		want  bool
	}{
		{name: "unswapped submit carries no run-as metadata", want: false},
		{name: "swapped submit names the requesting user", runAs: "ops", want: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			mock := newWAMock(t)
			a := newAdapterRig(t, mock, agentName)
			a.cfg.RunAs = tt.runAs
			a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: agentName})

			var payload []byte
			recs := captureLogs(t, a, func() {
				payload = publishedRequest(t, a, agentName, senderPhone)
			})
			if payload == nil {
				t.Fatal("expected the task to reach the agent")
			}
			meta := submittedMessageMetadata(t, payload)
			wantSource := principalSourceSender
			if tt.want {
				wantSource = principalSourceRunAs
			}
			if rec := findLog(recs, "task submitted"); rec == nil || rec["principalSource"] != wantSource {
				t.Errorf("task submitted principalSource = %v, want %q", rec["principalSource"], wantSource)
			}

			if !tt.want {
				for _, key := range []string{"requesting_user", "run_as"} {
					if _, present := meta[key]; present {
						t.Errorf("metadata carries %q on an unswapped submit: %v", key, meta)
					}
				}
				return
			}
			if got := meta["requesting_user"]; got != "whatsapp:"+senderPhone {
				t.Errorf("requesting_user = %v, want the real sender", got)
			}
			if got := meta["run_as"]; got != rbac.SystemSubPrefix+"ops" {
				t.Errorf("run_as = %v, want the system principal", got)
			}
		})
	}
}

func publishedRequest(t *testing.T, a *Adapter, agentName, senderPhone string) []byte {
	t.Helper()
	if received := publishedMessage(t, a, agentName, senderPhone); received != nil {
		return received.Payload
	}
	return nil
}

func publishedMessage(t *testing.T, a *Adapter, agentName, senderPhone string) *broker.Message {
	t.Helper()
	brk := a.core.Services().Broker()
	q, err := brk.CreateQueue("wa-runas-q", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create queue: %v", err)
	}
	if err = brk.Subscribe(q, a2a.AgentRequestTopic("test-ns", agentName)); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	a.deliver(textMessage("wamid.runas", senderPhone, "invoke the agent"))

	recvCtx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	received, _ := brk.Receive(recvCtx, q)
	return received
}

func submittedMessageMetadata(t *testing.T, payload []byte) map[string]any {
	t.Helper()
	var req struct {
		Params struct {
			Message struct {
				Metadata map[string]any `json:"metadata"`
			} `json:"message"`
		} `json:"params"`
	}
	if err := json.Unmarshal(payload, &req); err != nil {
		t.Fatalf("unmarshal published request: %v (payload=%s)", err, payload)
	}
	return req.Params.Message.Metadata
}

func TestIdentityPostureAdvisories(t *testing.T) {
	const (
		builtInSystemUser = "built-in default service account"
		defaultRoles      = "deployment's default roles"
		authzOffAnySender = "accepts messages from any sender"
	)
	allPhrases := []string{builtInSystemUser, defaultRoles, authzOffAnySender}

	for _, tt := range []struct {
		name         string
		runAs        string
		enforceAuthz bool
		want         []string
	}{
		{
			name:         "no run_as, authz enforced",
			enforceAuthz: true,
			want:         []string{defaultRoles},
		},
		{
			name: "no run_as, authz off",
			want: []string{authzOffAnySender},
		},
		{
			name:         "a scoped run_as warns about nothing",
			runAs:        "ops",
			enforceAuthz: true,
			want:         nil,
		},
		{
			name:  "a scoped run_as warns about nothing with authz off too",
			runAs: "ops",
			want:  nil,
		},
		{
			name:         "the built-in default system user earns its own warning",
			runAs:        "default",
			enforceAuthz: true,
			want:         []string{builtInSystemUser},
		},
		{
			name:         "the channel system user does not",
			runAs:        rbac.ChannelSystemUserRunAs,
			enforceAuthz: true,
			want:         nil,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			a, buf := advisoryRig(t, 25, tt.enforceAuthz)
			a.cfg.RunAs = tt.runAs

			a.logIdentityPosture()

			out := buf.String()
			for _, phrase := range allPhrases {
				want := containsPhrase(tt.want, phrase)
				if got := strings.Contains(out, phrase); got != want {
					t.Errorf("phrase %q present = %v, want %v; output = %q", phrase, got, want, out)
				}
			}
		})
	}
}

func containsPhrase(phrases []string, want string) bool {
	for _, p := range phrases {
		if p == want {
			return true
		}
	}
	return false
}

func TestIdentityPostureAdvisories_NamesThePrincipal(t *testing.T) {
	a, buf := advisoryRig(t, 25, true)
	a.cfg.RunAs = "default"

	a.logIdentityPosture()

	if got := buf.String(); !strings.Contains(got, "runAs="+rbac.DefaultSystemUserSub) {
		t.Errorf("advisory does not name principal %q; output = %q", rbac.DefaultSystemUserSub, got)
	}
}

func TestSwappedAuditRecordsTheSender(t *testing.T) {
	const agentName = "TestAgent"
	const senderPhone = "15551230000"

	for _, tt := range []struct {
		name  string
		runAs string
		want  bool
	}{
		{name: "unswapped audit needs no separate sender field", want: false},
		{name: "swapped audit records the sender", runAs: "ops", want: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
			mock := newWAMock(t)
			a := newAdapterRig(t, mock, agentName, logger)
			a.cfg.RunAs = tt.runAs
			a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: agentName})

			if payload := publishedRequest(t, a, agentName, senderPhone); payload == nil {
				t.Fatal("expected the task to reach the agent")
			}

			out := buf.String()
			if got := strings.Contains(out, "senderFP="); got != tt.want {
				t.Errorf("senderFP present = %v, want %v; output = %q", got, tt.want, out)
			}
			if strings.Contains(out, senderPhone) {
				t.Errorf("audit leaked the sender's phone number; output = %q", out)
			}
		})
	}
}

func TestHILResumeUsesTheSameAuthorizingPrincipal(t *testing.T) {
	const agentName = "TestAgent"
	const phone = "15551230000"

	mock := newWAMock(t)
	a := newAdapterRig(t, mock, agentName)
	a.cfg.RunAs = "ops"
	a.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: agentName})
	a.core.SetScopeResolver(&recordingScopeResolver{byPrincipal: map[string][]string{
		rbac.SystemSubPrefix + "ops": {rbac.AgentInvokeScope(agentName)},
	}})
	_ = a.core.Tasks().Register("t-hil", agentName, rbac.SystemSubPrefix+"ops", entrypoint.TaskIdentity{})
	a.core.Tasks().SetMetadata("t-hil", &taskMeta{FromPhone: phone, SessionID: "sess"})

	brk := a.core.Services().Broker()
	q, err := brk.CreateQueue("wa-hil-runas-q", broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("create queue: %v", err)
	}
	if err = brk.Subscribe(q, a2a.AgentRequestTopic("test-ns", agentName)); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	a.OnStatusUpdate(t.Context(), "t-hil", inputNeededEvent(t, "req-1", json.RawMessage(askSurfaceSingleChoiceWA)), nil)
	waitPromptDelivered(t, a, phone)
	a.deliver(interactiveReply(phone, "wamid.hil-reply", optionRefID(1)))

	recvCtx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	received, _ := brk.Receive(recvCtx, q)
	if received == nil {
		t.Fatal("expected the user's answer to reach the agent under the run-as principal, got none")
	}
	if pendingPromptRow(t, a, phone) != nil {
		t.Error("a submitted answer must clear the pending prompt")
	}
}
