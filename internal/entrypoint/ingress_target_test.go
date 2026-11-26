// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/strutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
)

const wireName = "agent_019fe224_dd07_764b_ae4c_125a04fb3ab2"

const wireNameDisplay = "Orchestrator"

func newIngressCore(t *testing.T) *GatewayCore {
	t.Helper()
	core := newCanDelegateCore(t, "default_rbac")
	core.AgentRegistry().AddOrUpdate(cardWithDisplayName(wireName, wireNameDisplay))
	core.AgentRegistry().AddOrUpdate(cardWithDisplayName("agent_019fe999_aaaa_bbbb_cccc_ddddddddddd0", "Reporter"))
	return core
}

func cardWithDisplayName(name, display string) a2a.AgentCard {
	card := a2a.AgentCard{Name: name}
	card.Capabilities.Extensions = append(card.Capabilities.Extensions, a2a.AgentExtension{
		URI:    a2a.ExtensionURIDisplayName,
		Params: map[string]any{"display_name": display},
	})
	return card
}

func ctxWithScopes(t *testing.T, scopes ...string) context.Context {
	t.Helper()
	return auth.ContextWithSubject(t.Context(), auth.NewTestSubject(auth.SubjectFields{
		UserID: "person@example.com",
		Scopes: scopes,
	}))
}

func TestResolveIngressTarget_DeniedAndUnknownAreIndistinguishable(t *testing.T) {
	core := newIngressCore(t)
	ctx := ctxWithScopes(t, rbac.InvokeScopeForInstance("agent_019fe999_aaaa_bbbb_cccc_ddddddddddd0"))

	byDisplay := core.ResolveIngressTarget(ctx, IngressTargetRequest{Caller: stagedCaller(ctx), Text: "@Orchestrator do a thing"})
	byFiction := core.ResolveIngressTarget(ctx, IngressTargetRequest{Caller: stagedCaller(ctx), Text: "@Orchestrator2 do a thing"})

	if !byDisplay.Refused() {
		t.Fatal("an agent the caller lacks invoke on must be refused, not routed")
	}
	if !byFiction.Refused() {
		t.Fatal("a name matching no card must be refused, not silently routed to the default agent")
	}
	if byDisplay.Refusal == "" || byFiction.Refusal == "" {
		t.Fatal("both probes must carry refusal text")
	}

	deniedShape := strings.Replace(byDisplay.Refusal, "Orchestrator", "TOKEN", 1)
	fictionShape := strings.Replace(byFiction.Refusal, "Orchestrator2", "TOKEN", 1)
	if deniedShape != fictionShape {
		t.Errorf("denied and nonexistent targets must produce identical copy, else the entrypoint is an agent-enumeration oracle:\n denied: %q\n unknown: %q", byDisplay.Refusal, byFiction.Refusal)
	}
	if byDisplay.RefusalReason != taxonomy.RejectForbidden {
		t.Errorf("a denied target should meter as %q, got %q", taxonomy.RejectForbidden, byDisplay.RefusalReason)
	}
	if byFiction.RefusalReason != taxonomy.RejectAgentNotFound {
		t.Errorf("a nonexistent target should meter as %q, got %q", taxonomy.RejectAgentNotFound, byFiction.RefusalReason)
	}
}

func TestResolveIngressTarget_NeverLeaksWireName(t *testing.T) {
	core := newIngressCore(t)
	ctx := ctxWithScopes(t)

	for _, text := range []string{
		"@" + wireName + " hello",
		"@" + wireNameDisplay + " hello",
		"just a plain message with no mention",
	} {
		t.Run(text, func(t *testing.T) {
			got := core.ResolveIngressTarget(ctx, IngressTargetRequest{Caller: stagedCaller(ctx),
				Text:             text,
				DefaultAgentName: wireNameDisplay,
			})
			if !got.Refused() {
				t.Fatalf("expected a refusal for a caller holding no scopes, got name=%q", got.Name)
			}
			if strings.Contains(got.Refusal, wireName) && !strings.Contains(text, wireName) {
				t.Errorf("refusal leaks the internal wire name: %q", got.Refusal)
			}
			if strings.Contains(got.Refusal, "agent delegation denied") {
				t.Errorf("refusal exposes the raw Go error: %q", got.Refusal)
			}
		})
	}
}

func TestResolveIngressTarget_OnlyLeadingMentionIsAnExplicitTarget(t *testing.T) {
	const reporter = "agent_019fe999_aaaa_bbbb_cccc_ddddddddddd0"
	core := newIngressCore(t)
	ctx := ctxWithScopes(t, rbac.InvokeScopeForInstance(reporter))

	t.Run("mid-sentence mention of a usable agent still routes", func(t *testing.T) {
		got := core.ResolveIngressTarget(ctx, IngressTargetRequest{Caller: stagedCaller(ctx), Text: "hey @Reporter can you help"})
		if got.Name != reporter {
			t.Errorf("Name = %q, want %q", got.Name, reporter)
		}
	})

	t.Run("mid-sentence mention of a denied agent falls through to the default", func(t *testing.T) {
		got := core.ResolveIngressTarget(ctx, IngressTargetRequest{Caller: stagedCaller(ctx),
			Text:             "please ask @Orchestrator about it",
			DefaultAgentName: "Reporter",
		})
		if got.Refused() {
			t.Fatalf("must fall through, not refuse: %q", got.Refusal)
		}
		if got.Name != reporter {
			t.Errorf("Name = %q, want the default agent %q", got.Name, reporter)
		}
	})

	t.Run("mid-sentence mention of a nonexistent agent falls through identically", func(t *testing.T) {
		got := core.ResolveIngressTarget(ctx, IngressTargetRequest{Caller: stagedCaller(ctx),
			Text:             "please ask @NotAnAgent about it",
			DefaultAgentName: "Reporter",
		})
		if got.Refused() || got.Name != reporter {
			t.Errorf("denied and nonexistent must behave the same mid-sentence; got refusal=%q name=%q", got.Refusal, got.Name)
		}
	})

	t.Run("an email address is not read as a mention", func(t *testing.T) {
		got := core.ResolveIngressTarget(ctx, IngressTargetRequest{Caller: stagedCaller(ctx),
			Text:             "forward it to someone@example.com please",
			DefaultAgentName: "Reporter",
		})
		if got.Refused() {
			t.Errorf("a bare email must not be treated as naming an agent: %q", got.Refusal)
		}
	})
}

func TestResolveIngressTarget_DefaultAgentDenialNamesThePermissionBoundary(t *testing.T) {
	core := newIngressCore(t)
	got := core.ResolveIngressTarget(ctxWithScopes(t), IngressTargetRequest{Caller: stagedCaller(ctxWithScopes(t)),
		Text:             "hello",
		DefaultAgentName: wireNameDisplay,
	})
	if !got.Refused() {
		t.Fatal("expected a refusal for a caller with no scopes")
	}
	if !strings.Contains(got.Refusal, "don't have access") {
		t.Errorf("refusal should name the permission boundary plainly, got %q", got.Refusal)
	}
	if got.RefusalReason != taxonomy.RejectForbidden {
		t.Errorf("RefusalReason = %q, want %q", got.RefusalReason, taxonomy.RejectForbidden)
	}
	if got.Refusal == SubmitFailedText {
		t.Error("a permission refusal must not reuse the retryable failure copy")
	}
}

func TestResolveIngressTarget_AllowsAndReportsMisconfig(t *testing.T) {
	core := newIngressCore(t)
	ctx := ctxWithScopes(t, rbac.InvokeScopeForInstance(wireName))

	t.Run("a usable leading mention routes", func(t *testing.T) {
		got := core.ResolveIngressTarget(ctx, IngressTargetRequest{Caller: stagedCaller(ctx), Text: "@Orchestrator hello"})
		if got.Refused() || got.Name != wireName {
			t.Errorf("got refusal=%q name=%q, want name=%q", got.Refusal, got.Name, wireName)
		}
	})

	t.Run("a default naming no card reports misconfiguration, not a refusal", func(t *testing.T) {
		got := core.ResolveIngressTarget(ctx, IngressTargetRequest{Caller: stagedCaller(ctx), Text: "hello", DefaultAgentName: "NoSuchAgent"})
		if !got.Misconfigured {
			t.Errorf("want Misconfigured, got refusal=%q name=%q", got.Refusal, got.Name)
		}
		if got.Refused() {
			t.Error("an operator fault must not be worded as a permission refusal to the user")
		}
	})
}

func TestResolveIngressTarget_RefusalIsLoggedWithDistinguishingReason(t *testing.T) {
	const email = "person@example.com"

	otherAgent := rbac.InvokeScopeForInstance("agent_019fe999_aaaa_bbbb_cccc_ddddddddddd0")

	capture := func(text string) string {
		t.Helper()
		core := newIngressCore(t)
		var buf bytes.Buffer
		core.logger = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
		got := core.ResolveIngressTarget(ctxWithScopes(t, otherAgent), IngressTargetRequest{Caller: stagedCaller(ctxWithScopes(t, otherAgent)), Text: text})
		if !got.Refused() {
			t.Fatalf("expected a refusal for %q", text)
		}
		out := buf.String()
		if !strings.Contains(out, "entrypoint refused agent target") {
			t.Fatalf("refusal was not logged for %q; operators would see nothing: %s", text, out)
		}
		if strings.Contains(out, email) {
			t.Errorf("refusal log leaks the raw identity: %s", out)
		}
		if !strings.Contains(out, "userFP="+strutil.PIIFingerprint(email)) {
			t.Errorf("refusal log must fingerprint the identity, got: %s", out)
		}
		return out
	}

	denied := capture("@" + wireNameDisplay + " do a thing")
	unknown := capture("@NoSuchAgentAnywhere do a thing")

	if !strings.Contains(denied, "reason="+denyReasonScope) {
		t.Errorf("a real-but-denied target should log reason=%s, got: %s", denyReasonScope, denied)
	}
	if !strings.Contains(unknown, "reason="+reasonUnknownAgent) {
		t.Errorf("a nonexistent target should log reason=%s, got: %s", reasonUnknownAgent, unknown)
	}

	if !strings.Contains(denied, "level=WARN") {
		t.Errorf("an authorization denial must stay WARN, got: %s", denied)
	}
	if !strings.Contains(unknown, "level=DEBUG") {
		t.Errorf("a mistyped mention must log below WARN, got: %s", unknown)
	}
}

func TestResolveIngressTarget_LeadingMentionTolleratesSentencePunctuation(t *testing.T) {
	const reporter = "agent_019fe999_aaaa_bbbb_cccc_ddddddddddd0"
	core := newIngressCore(t)
	ctx := ctxWithScopes(t, rbac.InvokeScopeForInstance(reporter))

	for _, text := range []string{
		"@Reporter what's the status",
		"@Reporter. what's the status",
		"@Reporter, what's the status",
		"@Reporter: what's the status",
		"@Reporter- what's the status",
		"@Reporter_ what's the status",
		"@Reporter.",
		"@Reporter",
	} {
		t.Run(text, func(t *testing.T) {
			got := core.ResolveIngressTarget(ctx, IngressTargetRequest{Caller: stagedCaller(ctx), Text: text})
			if got.Refused() {
				t.Fatalf("naming an agent the caller may use must route, not refuse: %q", got.Refusal)
			}
			if got.Name != reporter {
				t.Errorf("Name = %q, want %q", got.Name, reporter)
			}
		})
	}
}

func TestResolveIngressTarget_PunctuatedMentionKeepsTheOracleShut(t *testing.T) {
	core := newIngressCore(t)
	ctx := ctxWithScopes(t, rbac.InvokeScopeForInstance("agent_019fe999_aaaa_bbbb_cccc_ddddddddddd0"))

	denied := core.ResolveIngressTarget(ctx, IngressTargetRequest{Caller: stagedCaller(ctx), Text: "@Orchestrator. do a thing"})
	unknown := core.ResolveIngressTarget(ctx, IngressTargetRequest{Caller: stagedCaller(ctx), Text: "@Orchestrator2. do a thing"})

	if !denied.Refused() || !unknown.Refused() {
		t.Fatalf("both must refuse; denied=%v unknown=%v", denied.Refused(), unknown.Refused())
	}
	if !strings.Contains(denied.Refusal, `"Orchestrator"`) {
		t.Errorf("refusal should echo the trimmed token, got %q", denied.Refusal)
	}
	deniedShape := strings.Replace(denied.Refusal, "Orchestrator", "TOKEN", 1)
	unknownShape := strings.Replace(unknown.Refusal, "Orchestrator2", "TOKEN", 1)
	if deniedShape != unknownShape {
		t.Errorf("punctuated denied and unknown must stay indistinguishable:\n denied: %q\n unknown: %q", denied.Refusal, unknown.Refusal)
	}
}

func TestLeadingMention(t *testing.T) {
	tests := []struct {
		text  string
		want  string
		found bool
	}{
		{"@agent hello", "agent", true},
		{"  @agent-name.v2 hello", "agent-name.v2", true},
		{"@agent", "agent", true},
		{"hey @agent", "", false},
		{"@ agent", "", false},
		{"no mention here", "", false},
		{"", "", false},
		{"someone@example.com", "", false},
		{"<@U012345> hello", "", false},
		{"@agent. hello", "agent", true},
		{"@agent.", "agent", true},
		{"@agent- hello", "agent", true},
		{"@agent_ hello", "agent", true},
		{"@agent... hello", "agent", true},
		{"@agent-name.v2. hello", "agent-name.v2", true},
		{"@... hello", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.text, func(t *testing.T) {
			got, ok := leadingMention(tc.text)
			if ok != tc.found || got != tc.want {
				t.Errorf("leadingMention(%q) = (%q, %v), want (%q, %v)", tc.text, got, ok, tc.want, tc.found)
			}
		})
	}
}
