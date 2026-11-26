// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package authwrap

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth/toolauth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const (
	credKey      = "sf-cred"
	validToken   = `{"access_token":"tok-1","token_type":"Bearer","expires_at":4102444800}`
	expiredToken = `{"access_token":"old","refresh_token":"r-1","token_type":"Bearer","expires_at":1}`
	flowKey      = "u1/trace-1/" + credKey
)

type journal struct{ entries []string }

func (j *journal) add(format string, args ...any) {
	j.entries = append(j.entries, fmt.Sprintf(format, args...))
}

type memCreds map[string]string

func (m memCreds) Get(_ context.Context, agent, app, user, key string) (string, error) {
	return m[agent+"|"+app+"|"+user+"|"+key], nil
}

func (m memCreds) Set(_ context.Context, agent, app, user, key, value string) error {
	m[agent+"|"+app+"|"+user+"|"+key] = value
	return nil
}

func (m memCreds) Delete(_ context.Context, agent, app, user, key string) error {
	delete(m, agent+"|"+app+"|"+user+"|"+key)
	return nil
}

func (m memCreds) List(context.Context, string, string, string) ([]string, error) { return nil, nil }

type mockTool struct {
	log    *journal
	result *tool.Result
	err    error
}

func (m *mockTool) Name() string                 { return "sf" }
func (m *mockTool) Description() string          { return "mock tool" }
func (m *mockTool) Parameters() *tool.JSONSchema { return &tool.JSONSchema{Type: "object"} }
func (m *mockTool) Instructions() string         { return "use with care" }
func (m *mockTool) IsLongRunning() bool          { return true }
func (m *mockTool) RawStringArgs() []string      { return []string{"code"} }
func (m *mockTool) ContextParam() string         { return "tool_context" }
func (m *mockTool) ArtifactParams() map[string]tool.ArtifactParamInfo {
	return map[string]tool.ArtifactParamInfo{"file": {IsOptional: true}}
}

func (m *mockTool) Execute(_ context.Context, _ map[string]any, toolCtx *tool.Context) (*tool.Result, error) {
	token := ""
	if tok, ok := toolCtx.OAuthToken.(*toolauth.OAuthToken); ok {
		token = tok.AccessToken
	}
	header := ""
	if h, ok := toolCtx.AuthHeaders.(http.Header); ok {
		header = h.Get("Authorization")
	}
	m.log.add("inner token=%s auth=%s", token, header)
	return m.result, m.err
}

type capableTool struct{ mockTool }

func (c *capableTool) RequiredScopes() []string { return []string{"tool:sf:invoke"} }
func (c *capableTool) ResultSchema() *tool.JSONSchema {
	return &tool.JSONSchema{Type: "object", Required: []string{"filename"}}
}
func (c *capableTool) AuthoredSchemas() bool        { return true }
func (c *capableTool) ArtifactNotFoundHint() string { return "create the file first" }
func (c *capableTool) ArgumentErrorHint(args map[string]any) string {
	return fmt.Sprintf("takes path, got %v", args["path"])
}

type unauthoredTool struct{ mockTool }

func (u *unauthoredTool) AuthoredSchemas() bool { return false }

type descriptors struct {
	Name, Description, Instructions, ContextParam string
	Parameters, ResultSchema                      *tool.JSONSchema
	LongRunning, Authored                         bool
	RawStringArgs, Scopes                         []string
	ArtifactParams                                map[string]tool.ArtifactParamInfo
	NotFoundHint, ArgumentHint                    string
}

func describe(t tool.BuiltinTool) descriptors {
	return descriptors{
		Name: t.Name(), Description: t.Description(), Instructions: t.Instructions(), ContextParam: t.ContextParam(),
		Parameters: t.Parameters(), ResultSchema: tool.ResultSchemaOf(t),
		LongRunning: t.IsLongRunning(), Authored: tool.HasAuthoredSchemas(t),
		RawStringArgs: t.RawStringArgs(), Scopes: tool.RequiredScopesOf(t),
		ArtifactParams: t.ArtifactParams(),
		NotFoundHint:   tool.ArtifactNotFoundHintOf(t), ArgumentHint: tool.ArgumentErrorHintOf(t, map[string]any{"path": "x"}),
	}
}

func TestWrap_WithoutHandlerReturnsToolUnwrapped(t *testing.T) {
	t.Parallel()
	inner := &mockTool{}
	if got := Wrap(inner, nil, nil); got != tool.BuiltinTool(inner) {
		t.Errorf("Wrap(inner, nil) = %T, want the inner tool itself", got)
	}
}

func TestWrappedTool_ForwardsInnerDescriptors(t *testing.T) {
	t.Parallel()
	core := descriptors{
		Name: "sf", Description: "mock tool", Instructions: "use with care", ContextParam: "tool_context",
		Parameters: &tool.JSONSchema{Type: "object"}, LongRunning: true, RawStringArgs: []string{"code"},
		ArtifactParams: map[string]tool.ArtifactParamInfo{"file": {IsOptional: true}},
	}
	capable := core
	capable.ResultSchema = &tool.JSONSchema{Type: "object", Required: []string{"filename"}}
	capable.Authored = true
	capable.Scopes = []string{"tool:sf:invoke"}
	capable.NotFoundHint = "create the file first"
	capable.ArgumentHint = "takes path, got x"

	tests := []struct {
		name  string
		inner tool.BuiltinTool
		want  descriptors
	}{
		{name: "inner with every capability keeps them all", inner: &capableTool{}, want: capable},
		{name: "inner without optional capabilities gains none", inner: &mockTool{}, want: core},
		{name: "inner that declines authored schemas stays unauthored", inner: &unauthoredTool{}, want: core},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := describe(Wrap(tc.inner, &toolauth.AuthHandler{}, nil)); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("describe(wrapped) = %+v, want %+v", got, tc.want)
			}
		})
	}
}

type outcome struct {
	Status, Message, ErrorCode string
	PendingCallID, PendingKey  string
	PendingArgQ                any
	Follower                   bool
}

func summarize(r *tool.Result) outcome {
	msg, _, _ := strings.Cut(r.Message, ": ")
	return outcome{
		Status: r.Status, Message: msg, ErrorCode: r.ErrorCode,
		PendingCallID: toolauth.PendingAuthFunctionCallID(r), PendingKey: toolauth.PendingAuthCredentialKey(r),
		PendingArgQ: toolauth.PendingAuthOriginalArgs(r)["q"], Follower: toolauth.IsPendingAuthFollower(r),
	}
}

func oauthConfig(tokenURL string) *toolauth.AuthConfig {
	return &toolauth.AuthConfig{
		Type: "oauth2", CredentialKey: credKey,
		Scheme:     &toolauth.OAuthScheme{AuthorizationURL: "https://auth.example.com/authorize", TokenURL: tokenURL},
		Credential: &toolauth.AuthCredential{ClientID: "client-1"},
	}
}

func TestExecute(t *testing.T) {
	t.Parallel()
	outage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"server_error"}`))
	}))
	t.Cleanup(outage.Close)
	errInner := errors.New("inner failed")
	pending := outcome{
		Status: "pending", Message: "Pending user authorization.",
		PendingCallID: "call-1", PendingKey: credKey, PendingArgQ: "x",
	}
	follower := pending
	follower.Follower = true

	tests := []struct {
		name        string
		cfg         *toolauth.AuthConfig
		user        string
		stored      string
		innerResult *tool.Result
		innerErr    error
		gaveUp      bool
		follower    bool
		sendErr     error
		want        outcome
		wantErr     error
		wantJournal []string
		wantStored  string
	}{
		{
			name: "stored token is handed to the inner tool", cfg: oauthConfig(outage.URL), user: "u1",
			stored: validToken, innerResult: tool.OK("done"),
			want:        outcome{Status: "success", Message: "done"},
			wantJournal: []string{"inner token=tok-1 auth="}, wantStored: validToken,
		},
		{
			name: "missing token prompts once and pauses as the flow leader", cfg: oauthConfig(outage.URL), user: "u1",
			want:        pending,
			wantJournal: []string{"gaveUp " + flowKey, "begin " + flowKey, "signal " + credKey},
		},
		{
			name: "inner AUTH_REQUIRED clears the stored token and prompts", cfg: oauthConfig(outage.URL), user: "u1",
			stored: validToken, innerResult: tool.AuthRequired("expired upstream"),
			want:        pending,
			wantJournal: []string{"inner token=tok-1 auth=", "gaveUp " + flowKey, "begin " + flowKey, "signal " + credKey},
		},
		{
			name: "inner execution error is returned unchanged", cfg: oauthConfig(outage.URL), user: "u1",
			stored: validToken, innerResult: tool.Error("partial"), innerErr: errInner,
			want: outcome{Status: "error", Message: "partial"}, wantErr: errInner,
			wantJournal: []string{"inner token=tok-1 auth="}, wantStored: validToken,
		},
		{
			name: "inner error result other than AUTH_REQUIRED is returned without prompting", cfg: oauthConfig(outage.URL), user: "u1",
			stored: validToken, innerResult: tool.Error("not found"),
			want:        outcome{Status: "error", Message: "not found"},
			wantJournal: []string{"inner token=tok-1 auth="}, wantStored: validToken,
		},
		{
			name: "sign-in provider outage keeps the credential and does not prompt", cfg: oauthConfig(outage.URL), user: "u1",
			stored: expiredToken,
			want: outcome{Status: "error", ErrorCode: "DISPATCH_BACKEND_5XX",
				Message: `The sign-in provider for "sf" is unavailable right now, so its stored sign-in could not be renewed. Do not retry it in this turn; tell the user it is temporarily unavailable and to try again in a few minutes. There is no need to sign in again.`},
			wantStored: expiredToken,
		},
		{
			name: "non-delegatable principal is refused before any flow bookkeeping", cfg: oauthConfig(outage.URL),
			user: rbac.DefaultSystemUserSub,
			want: outcome{Status: "error", ErrorCode: "DISPATCH_USER_INPUT_UNAVAILABLE",
				Message: `"sf" needs your personal access, which isn't available in a shared channel. DM me directly and I'll do it under your account.`},
		},
		{
			name: "abandoned prompt fails terminally without prompting again", cfg: oauthConfig(outage.URL), user: "u1",
			gaveUp: true,
			want: outcome{Status: "error", ErrorCode: "DISPATCH_USER_INPUT_UNANSWERED",
				Message: `Authorization for "sf" was not completed after repeated prompts in this conversation, so it will not be requested again here. Please authorize the integration, then start a new request.`},
			wantJournal: []string{"gaveUp " + flowKey},
		},
		{
			name: "flow already in flight makes the call a silent follower", cfg: oauthConfig(outage.URL), user: "u1",
			follower:    true,
			want:        follower,
			wantJournal: []string{"gaveUp " + flowKey, "begin " + flowKey},
		},
		{
			name: "prompt that cannot be built releases the in-flight marker", cfg: &toolauth.AuthConfig{Type: "oauth2", CredentialKey: credKey},
			user:        "u1",
			want:        outcome{Status: "error", Message: "failed to build auth signal"},
			wantJournal: []string{"gaveUp " + flowKey, "begin " + flowKey, "end " + flowKey},
		},
		{
			name: "refused prompt releases the marker and keeps the refusal class", cfg: oauthConfig(outage.URL), user: "u1",
			sendErr:     tool.UnavailableInput(errors.New("workflow node")),
			want:        outcome{Status: "error", Message: "could not deliver the authorization prompt", ErrorCode: "DISPATCH_USER_INPUT_UNAVAILABLE"},
			wantJournal: []string{"gaveUp " + flowKey, "begin " + flowKey, "signal " + credKey, "end " + flowKey},
		},
		{
			name: "bearer credential is injected as a header", user: "u1", innerResult: tool.OK("done"),
			cfg:         &toolauth.AuthConfig{Type: "bearer", CredentialKey: credKey, Credential: &toolauth.AuthCredential{Token: "static-key"}},
			want:        outcome{Status: "success", Message: "done"},
			wantJournal: []string{"inner token= auth=Bearer static-key"},
		},
		{
			name: "basic credential is injected as a header", user: "u1", innerResult: tool.OK("done"),
			cfg:         &toolauth.AuthConfig{Type: "basic", CredentialKey: credKey, Credential: &toolauth.AuthCredential{Username: "admin", Password: "secret"}},
			want:        outcome{Status: "success", Message: "done"},
			wantJournal: []string{"inner token= auth=Basic YWRtaW46c2VjcmV0"},
		},
		{
			name: "static auth without a credential fails without running the tool", user: "u1",
			cfg:  &toolauth.AuthConfig{Type: "bearer", CredentialKey: credKey},
			want: outcome{Status: "error", Message: "authentication failed"},
		},
		{
			name: "unrecognised auth type runs the tool without credentials", user: "u1", innerResult: tool.OK("done"),
			cfg:         &toolauth.AuthConfig{Type: "apikey", CredentialKey: credKey},
			want:        outcome{Status: "success", Message: "done"},
			wantJournal: []string{"inner token= auth="},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			log := &journal{}
			creds := memCreds{"A|A|" + tc.user + "|" + credKey: tc.stored}
			toolCtx := &tool.Context{
				AgentName: "A", UserID: tc.user, TraceID: "trace-1", ToolCallID: "call-1", CredentialService: creds,
				AuthGaveUp: func(u, tr, c string) bool { log.add("gaveUp %s/%s/%s", u, tr, c); return tc.gaveUp },
				BeginAuthFlow: func(u, tr, c string) bool {
					log.add("begin %s/%s/%s", u, tr, c)
					return !tc.follower
				},
				EndAuthFlow: func(u, tr, c string) { log.add("end %s/%s/%s", u, tr, c) },
				SendSignal: func(s any) error {
					if d, ok := s.(*a2a.AuthenticationRequiredData); ok {
						log.add("signal %s", d.CredentialKey)
					} else {
						log.add("untyped signal %T", s)
					}
					return tc.sendErr
				},
			}
			w := Wrap(&mockTool{log: log, result: tc.innerResult, err: tc.innerErr}, toolauth.NewAuthHandler(tc.cfg, nil), nil)

			got, err := w.Execute(t.Context(), map[string]any{"q": "x"}, toolCtx)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Execute() error = %v, want %v", err, tc.wantErr)
			}
			if o := summarize(got); !reflect.DeepEqual(o, tc.want) {
				t.Errorf("Execute() = %+v, want %+v", o, tc.want)
			}
			if !reflect.DeepEqual(log.entries, tc.wantJournal) {
				t.Errorf("journal = %q, want %q", log.entries, tc.wantJournal)
			}
			if s := creds["A|A|"+tc.user+"|"+credKey]; s != tc.wantStored {
				t.Errorf("stored credential = %q, want %q", s, tc.wantStored)
			}
		})
	}
}

func TestExecute_OneWrapperKeepsUsersTokensApart(t *testing.T) {
	t.Parallel()
	const otherUserToken = `{"access_token":"tok-2","token_type":"Bearer","expires_at":4102444800}`
	log := &journal{}
	creds := memCreds{"A|A|u1|" + credKey: validToken, "A|A|u2|" + credKey: otherUserToken}
	w := Wrap(&mockTool{log: log, result: tool.OK("done")}, toolauth.NewAuthHandler(oauthConfig("https://auth.example.invalid/token"), nil), nil)
	for _, user := range []string{"u1", "u2"} {
		if _, err := w.Execute(t.Context(), nil, &tool.Context{AgentName: "A", UserID: user, CredentialService: creds}); err != nil {
			t.Fatalf("Execute(%s) error = %v", user, err)
		}
	}
	if want := []string{"inner token=tok-1 auth=", "inner token=tok-2 auth="}; !reflect.DeepEqual(log.entries, want) {
		t.Errorf("journal = %q, want %q", log.entries, want)
	}
}

func TestExecute_WithoutAuthConfigFailsWithoutRunningTool(t *testing.T) {
	t.Parallel()
	log := &journal{}
	w := Wrap(&mockTool{log: log, result: tool.OK("done")}, &toolauth.AuthHandler{}, nil)

	got, err := w.Execute(t.Context(), nil, &tool.Context{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if o, want := summarize(got), (outcome{Status: "error", Message: "auth configuration is nil"}); !reflect.DeepEqual(o, want) {
		t.Errorf("Execute() = %+v, want %+v", o, want)
	}
	if log.entries != nil {
		t.Errorf("inner tool ran: %q", log.entries)
	}
}

func TestExecute_InterruptedRefreshFailsWithoutPrompt(t *testing.T) {
	t.Parallel()
	log := &journal{}
	creds := memCreds{"A|A|u1|" + credKey: expiredToken}
	w := Wrap(&mockTool{log: log, result: tool.OK("done")}, toolauth.NewAuthHandler(oauthConfig("https://auth.example.com/token"), nil), nil)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	got, err := w.Execute(ctx, nil, &tool.Context{
		AgentName: "A", UserID: "u1", CredentialService: creds,
		SendSignal: func(any) error { log.add("signal"); return nil },
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if o, want := summarize(got), (outcome{Status: "error", Message: "authentication failed"}); !reflect.DeepEqual(o, want) {
		t.Errorf("Execute() = %+v, want %+v", o, want)
	}
	if log.entries != nil {
		t.Errorf("journal = %q, want nothing", log.entries)
	}
}

func TestExecute_WithoutFlowCallbacksReportsMissingPromptChannel(t *testing.T) {
	t.Parallel()
	w := Wrap(&mockTool{log: &journal{}}, toolauth.NewAuthHandler(oauthConfig("https://auth.example.com/token"), nil), nil)

	got, err := w.Execute(t.Context(), nil, &tool.Context{AgentName: "A", UserID: "u1", CredentialService: memCreds{}})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	want := outcome{Status: "error", Message: "authorization required but no signal channel available", ErrorCode: "DISPATCH_USER_INPUT_UNDELIVERABLE"}
	if o := summarize(got); !reflect.DeepEqual(o, want) {
		t.Errorf("Execute() = %+v, want %+v", o, want)
	}
}

type replacingTool struct {
	mockTool
	creds   memCreds
	replace bool
}

func (r *replacingTool) Execute(ctx context.Context, args map[string]any, toolCtx *tool.Context) (*tool.Result, error) {
	_, _ = r.mockTool.Execute(ctx, args, toolCtx)
	if tok, ok := toolCtx.OAuthToken.(*toolauth.OAuthToken); ok && tok.AccessToken == "tok-2" {
		return tool.OK("done"), nil
	}
	if r.replace {
		r.creds["A|A|u1|"+credKey] = `{"access_token":"tok-2","token_type":"Bearer","expires_at":4102444800}`
	}
	return tool.AuthRequired("rejected upstream"), nil
}

func TestExecute_RejectedTokenAnotherWriterReplaced(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		replace     bool
		want        outcome
		wantJournal []string
	}{
		{
			name: "the call is retried once with the stored token and no sign-in is asked", replace: true,
			want:        outcome{Status: "success", Message: "done"},
			wantJournal: []string{"inner token=tok-1 auth=", "inner token=tok-2 auth="},
		},
		{
			name:        "an AUTH_REQUIRED for the stored token itself still asks the user to sign in",
			want:        outcome{Status: "pending", Message: "Pending user authorization.", PendingCallID: "call-1", PendingKey: credKey, PendingArgQ: "x"},
			wantJournal: []string{"inner token=tok-1 auth=", "gaveUp " + flowKey, "begin " + flowKey, "signal " + credKey},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			log := &journal{}
			creds := memCreds{"A|A|u1|" + credKey: validToken}
			toolCtx := &tool.Context{
				AgentName: "A", UserID: "u1", TraceID: "trace-1", ToolCallID: "call-1", CredentialService: creds,
				AuthGaveUp:    func(u, tr, c string) bool { log.add("gaveUp %s/%s/%s", u, tr, c); return false },
				BeginAuthFlow: func(u, tr, c string) bool { log.add("begin %s/%s/%s", u, tr, c); return true },
				EndAuthFlow:   func(u, tr, c string) { log.add("end %s/%s/%s", u, tr, c) },
				SendSignal: func(s any) error {
					if d, ok := s.(*a2a.AuthenticationRequiredData); ok {
						log.add("signal %s", d.CredentialKey)
					}
					return nil
				},
			}
			inner := &replacingTool{mockTool: mockTool{log: log}, creds: creds, replace: tc.replace}
			w := Wrap(inner, toolauth.NewAuthHandler(oauthConfig("https://auth.example.invalid/token"), nil), nil)

			got, err := w.Execute(t.Context(), map[string]any{"q": "x"}, toolCtx)

			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if o := summarize(got); !reflect.DeepEqual(o, tc.want) {
				t.Errorf("Execute() = %+v, want %+v", o, tc.want)
			}
			if !reflect.DeepEqual(log.entries, tc.wantJournal) {
				t.Errorf("journal = %q, want %q", log.entries, tc.wantJournal)
			}
		})
	}
}
