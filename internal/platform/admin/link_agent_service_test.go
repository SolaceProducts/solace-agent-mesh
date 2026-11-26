// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/agentlink"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const (
	linkFixtureEntrypointID      = "0190f5ec-0000-7000-8000-00000000e001"
	linkFixtureOtherEntrypointID = "0190f5ec-0000-7000-8000-00000000e002"
	linkFixtureWebhookID         = "0190f5ec-0000-7000-8000-00000000e003"
	linkFixtureMissingID         = "0190f5ec-0000-7000-8000-00000000e009"
	linkFixtureWakeSecret        = "wake-client-secret"
	linkFixtureTokenURL          = "https://idp.example.com/oauth2/token"
	linkFixtureWakeHeaderValue   = "Bearer alice-wake-key"
	linkFixtureOtherWakeURL      = "https://other.example.com/invocations"
	secretResentMessage          = "must be sent again when the URL it is sent to changes"
)

var linkAgentEditorScopes = []string{rbac.ScopeAgentBuilderUpdate, rbac.ScopeAgentBuilderDeploy}

type linkLogs struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *linkLogs) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *linkLogs) lines() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	text := strings.TrimSpace(l.buf.String())
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

func (l *linkLogs) entries(t *testing.T) []map[string]any {
	t.Helper()
	var entries []map[string]any
	for _, line := range l.lines() {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("log line %q is not JSON: %v", line, err)
		}
		entries = append(entries, entry)
	}
	return entries
}

func (l *linkLogs) messages(t *testing.T) []string {
	t.Helper()
	var messages []string
	for _, entry := range l.entries(t) {
		message, _ := entry["msg"].(string)
		messages = append(messages, message)
	}
	return messages
}

func (l *linkLogs) countMessage(t *testing.T, message string) int {
	t.Helper()
	count := 0
	for _, got := range l.messages(t) {
		if got == message {
			count++
		}
	}
	return count
}

func (l *linkLogs) omit(t *testing.T, step string, secrets ...string) {
	t.Helper()
	for _, line := range l.lines() {
		for _, secret := range secrets {
			if strings.Contains(line, secret) {
				t.Errorf("%s: a log record carries a secret or its digest: %s", step, line)
			}
		}
	}
}

func newLinkLogger() (*slog.Logger, *linkLogs) {
	logs := &linkLogs{}
	return slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})), logs
}

type linkAgentFixture struct {
	db     *store.DB
	svc    *LinkAgentService
	logger *slog.Logger
	logs   *linkLogs
}

func newLinkAgentFixture(t *testing.T) *linkAgentFixture {
	t.Helper()
	db := testOpenDB(t)
	gateways := store.NewGatewayRepo(db)
	for _, gw := range []*store.Gateway{
		{ID: linkFixtureEntrypointID, Slug: "partner-link", Name: "Partner link", Type: "agent_link", DNSID: new("abc12345")},
		{ID: linkFixtureOtherEntrypointID, Slug: "second-link", Name: "Second link", Type: "agent_link", DNSID: new("def67890")},
		{ID: linkFixtureWebhookID, Slug: "orders-hook", Name: "Orders hook", Type: "webhook", DNSID: new("ghi13579")},
	} {
		gw.CreatedBy, gw.UpdatedBy = "alice@example.com", "alice@example.com"
		if err := gateways.Create(t.Context(), gw); err != nil {
			t.Fatalf("seed entrypoint %q: %v", gw.Name, err)
		}
	}
	logger, logs := newLinkLogger()
	svc := NewLinkAgentService(store.NewLinkAgentRepo(db), store.NewAgentRepo(db), gateways, store.NewAgentDeploymentRepo(db),
		nil, NewGatewayNetworkingService(gateways, "", "", logger), logger)
	return &linkAgentFixture{db: db, svc: svc, logger: logger, logs: logs}
}

func (f *linkAgentFixture) create(t *testing.T, req CreateLinkAgentRequest) (*LinkAgentResponse, string) {
	t.Helper()
	resp, token, err := f.svc.Create(t.Context(), req, "alice@example.com")
	if err != nil {
		t.Fatalf("Create(%q) = %v", req.Name, describeValidationError(err))
	}
	return resp, token
}

func (f *linkAgentFixture) stored(t *testing.T, id string) *store.LinkAgent {
	t.Helper()
	agent, err := store.NewLinkAgentRepo(f.db).GetByID(t.Context(), id)
	if err != nil || agent == nil {
		t.Fatalf("stored link agent %s = %v, %v", id, agent, err)
	}
	return agent
}

func (f *linkAgentFixture) recordDeployment(t *testing.T, agentID, action string) {
	t.Helper()
	row := &store.AgentDeployment{AgentID: agentID, Action: action, Status: DeployStatusSuccess, AgentConfigSnapshot: new("{}"), CreatedBy: "alice@example.com"}
	if err := store.NewAgentDeploymentRepo(f.db).Create(t.Context(), row); err != nil {
		t.Fatalf("seed %s of %s: %v", action, agentID, err)
	}
}

func linkValidationDetails(t *testing.T, err error) map[string][]string {
	t.Helper()
	if err == nil {
		return nil
	}
	verr, ok := errors.AsType[*ValidationError](err)
	if !ok {
		t.Fatalf("error = %T %v, want a *ValidationError", err, err)
	}
	return verr.Details
}

func meshNameOf(agentID string) string {
	return "agent_" + strings.ReplaceAll(agentID, "-", "_")
}

func digestOf(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func claimsDesk() CreateLinkAgentRequest {
	return CreateLinkAgentRequest{Name: "Claims Desk", EntrypointID: linkFixtureEntrypointID}
}

func wakeWithOAuth() *LinkAgentWakeRequest {
	return &LinkAgentWakeRequest{
		URL:          "https://runtime.example.com/invocations",
		TokenURL:     linkFixtureTokenURL,
		ClientID:     "claims-client",
		ClientSecret: linkFixtureWakeSecret,
	}
}

func TestLinkAgentService_CreateStoresWhatTheAgentLinkRuns(t *testing.T) {
	tests := []struct {
		name        string
		req         CreateLinkAgentRequest
		wantRuntime map[string]any
	}{
		{
			name: "only what is required",
			req:  claimsDesk(),
			wantRuntime: map[string]any{
				"enabled": true, "display_name": "Claims Desk", "share_user_profile": false,
			},
		},
		{
			name: "every setting at its limit",
			req: CreateLinkAgentRequest{
				Name: "Claims Desk", Description: "Answers claims questions.", EntrypointID: linkFixtureEntrypointID,
				Enabled: new(false), Owner: strings.Repeat("o", 256), Team: " Claims Operations ",
				Peers: []string{"policy-desk", "agent_0190f5ec_0000_7000_8000_00000000a002"}, ShareUserProfile: true,
				Wake: &LinkAgentWakeRequest{
					URL: "https://runtime.example.com/invocations", TokenURL: linkFixtureTokenURL,
					ClientID: "claims-client", ClientSecret: linkFixtureWakeSecret, Scope: "https://runtime.example.com/.default",
					Headers: map[string]string{"X-Session": "claims"}, TimeoutSeconds: 86400, OnStart: new(false),
				},
			},
			wantRuntime: map[string]any{
				"enabled": false, "display_name": "Claims Desk", "share_user_profile": true,
				"owner": strings.Repeat("o", 256), "team": "Claims Operations",
				"peers": []any{"policy-desk", "agent_0190f5ec_0000_7000_8000_00000000a002"},
				"wake": map[string]any{
					"url": "https://runtime.example.com/invocations", "token_url": linkFixtureTokenURL,
					"client_id": "claims-client", "client_secret": linkFixtureWakeSecret, "scope": "https://runtime.example.com/.default",
					"headers": map[string]any{"X-Session": "claims"}, "timeout_seconds": 86400, "on_start": false,
				},
			},
		},
		{
			name: "a wake call without a token on a loopback runtime",
			req: CreateLinkAgentRequest{
				Name: "Claims Desk", EntrypointID: linkFixtureEntrypointID,
				Wake: &LinkAgentWakeRequest{URL: "http://localhost:8088/wake", TimeoutSeconds: 1},
			},
			wantRuntime: map[string]any{
				"enabled": true, "display_name": "Claims Desk", "share_user_profile": false,
				"wake": map[string]any{"url": "http://localhost:8088/wake", "timeout_seconds": 1, "on_start": true},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newLinkAgentFixture(t)

			created, token := f.create(t, tt.req)

			runtime := linkAgentRuntimeConfig(f.stored(t, created.ID))
			want := tt.wantRuntime
			want["name"] = meshNameOf(created.ID)
			want["token_sha256"] = digestOf(token)
			if diff := cmp.Diff(want, runtime); diff != "" {
				t.Errorf("runtime config of the created link agent mismatch (-want +got):\n%s", diff)
			}
			if _, err := agentlink.ParseConfig(map[string]any{"platform_managed": true, "agents": []any{runtime}}); err != nil {
				t.Errorf("agentlink.ParseConfig(runtime config of the created link agent) = %v", err)
			}
		})
	}
}

func TestLinkAgentService_CreateRefuses(t *testing.T) {
	manyPeers := make([]string, 65)
	for i := range manyPeers {
		manyPeers[i] = "peer-" + string(rune('a'+i%26)) + string(rune('a'+i/26))
	}
	tests := []struct {
		name        string
		req         CreateLinkAgentRequest
		wantDetails map[string][]string
	}{
		{
			name:        "a blank name",
			req:         CreateLinkAgentRequest{Name: "  ", EntrypointID: linkFixtureEntrypointID},
			wantDetails: map[string][]string{"name": {"name is required"}},
		},
		{
			name:        "an entrypointId that is not a UUID",
			req:         CreateLinkAgentRequest{Name: "Claims Desk", EntrypointID: "partner-link"},
			wantDetails: map[string][]string{"entrypointId": {"entrypointId must be the id of an agent_link entrypoint"}},
		},
		{
			name:        "an entrypointId no entrypoint has",
			req:         CreateLinkAgentRequest{Name: "Claims Desk", EntrypointID: linkFixtureMissingID},
			wantDetails: map[string][]string{"entrypointId": {"entrypointId must be the id of an agent_link entrypoint"}},
		},
		{
			name:        "an entrypointId of a webhook entrypoint",
			req:         CreateLinkAgentRequest{Name: "Claims Desk", EntrypointID: linkFixtureWebhookID},
			wantDetails: map[string][]string{"entrypointId": {"entrypointId must be the id of an agent_link entrypoint"}},
		},
		{
			name: "an owner and a team one character too long",
			req: CreateLinkAgentRequest{Name: "Claims Desk", EntrypointID: linkFixtureEntrypointID,
				Owner: strings.Repeat("o", 257), Team: strings.Repeat("t", 257)},
			wantDetails: map[string][]string{
				"owner": {"owner is longer than 256 bytes"},
				"team":  {"team is longer than 256 bytes"},
			},
		},
		{
			name: "a peer that is not a mesh name and a peer listed twice",
			req: CreateLinkAgentRequest{Name: "Claims Desk", EntrypointID: linkFixtureEntrypointID,
				Peers: []string{"policy/desk", "policy-desk", "policy-desk"}},
			wantDetails: map[string][]string{"peers": {
				`peer "policy/desk" is not an agent's name on the mesh: letters, digits, '-' and '_', starting with a letter or digit`,
				`peer "policy-desk" is listed more than once`,
			}},
		},
		{
			name:        "one peer more than the limit",
			req:         CreateLinkAgentRequest{Name: "Claims Desk", EntrypointID: linkFixtureEntrypointID, Peers: manyPeers},
			wantDetails: map[string][]string{"peers": {"peers lists more than 64 agents"}},
		},
		{
			name: "a wake URL over plain http on another host",
			req: CreateLinkAgentRequest{Name: "Claims Desk", EntrypointID: linkFixtureEntrypointID,
				Wake: &LinkAgentWakeRequest{URL: "http://runtime.example.com/wake"}},
			wantDetails: map[string][]string{"wake.url": {"wake.url must use https"}},
		},
		{
			name: "a wake URL that is not absolute",
			req: CreateLinkAgentRequest{Name: "Claims Desk", EntrypointID: linkFixtureEntrypointID,
				Wake: &LinkAgentWakeRequest{URL: "/wake"}},
			wantDetails: map[string][]string{"wake.url": {"wake.url must be an absolute URL"}},
		},
		{
			name: "a wake URL with a query string",
			req: CreateLinkAgentRequest{Name: "Claims Desk", EntrypointID: linkFixtureEntrypointID,
				Wake: &LinkAgentWakeRequest{URL: "https://runtime.example.com/wake?code=abc"}},
			wantDetails: map[string][]string{"wake.url": {"wake.url must carry no query parameter other than api-version; send credentials in wake.headers"}},
		},
		{
			name: "a token URL with an api-version, which only the wake URL may carry",
			req: CreateLinkAgentRequest{Name: "Claims Desk", EntrypointID: linkFixtureEntrypointID,
				Wake: &LinkAgentWakeRequest{URL: "https://runtime.example.com/wake?api-version=v1", TokenURL: linkFixtureTokenURL + "?api-version=v1",
					ClientID: "claims-client", ClientSecret: linkFixtureWakeSecret}},
			wantDetails: map[string][]string{"wake.tokenUrl": {"wake.tokenUrl must not carry a query string; send credentials in wake.clientId and wake.clientSecret"}},
		},
		{
			name: "a token URL with a query string",
			req: CreateLinkAgentRequest{Name: "Claims Desk", EntrypointID: linkFixtureEntrypointID,
				Wake: &LinkAgentWakeRequest{URL: "https://runtime.example.com/wake", TokenURL: linkFixtureTokenURL + "?code=abc",
					ClientID: "claims-client", ClientSecret: linkFixtureWakeSecret}},
			wantDetails: map[string][]string{"wake.tokenUrl": {"wake.tokenUrl must not carry a query string; send credentials in wake.clientId and wake.clientSecret"}},
		},
		{
			name: "a token URL without its client",
			req: CreateLinkAgentRequest{Name: "Claims Desk", EntrypointID: linkFixtureEntrypointID,
				Wake: &LinkAgentWakeRequest{URL: "https://runtime.example.com/wake", TokenURL: linkFixtureTokenURL}},
			wantDetails: map[string][]string{
				"wake.clientId":     {"wake.clientId is required with wake.tokenUrl"},
				"wake.clientSecret": {"wake.clientSecret is required with wake.tokenUrl"},
			},
		},
		{
			name: "a client without its token URL",
			req: CreateLinkAgentRequest{Name: "Claims Desk", EntrypointID: linkFixtureEntrypointID,
				Wake: &LinkAgentWakeRequest{URL: "https://runtime.example.com/wake", ClientID: "claims-client", ClientSecret: linkFixtureWakeSecret}},
			wantDetails: map[string][]string{"wake.tokenUrl": {"wake.tokenUrl must be an absolute URL"}},
		},
		{
			name: "a token URL over plain http on another host",
			req: CreateLinkAgentRequest{Name: "Claims Desk", EntrypointID: linkFixtureEntrypointID,
				Wake: &LinkAgentWakeRequest{URL: "https://runtime.example.com/wake", TokenURL: "http://idp.example.com/token",
					ClientID: "claims-client", ClientSecret: linkFixtureWakeSecret}},
			wantDetails: map[string][]string{"wake.tokenUrl": {"wake.tokenUrl must use https"}},
		},
		{
			name: "a wake URL at the IPv4 cloud metadata address",
			req: CreateLinkAgentRequest{Name: "Claims Desk", EntrypointID: linkFixtureEntrypointID,
				Wake: &LinkAgentWakeRequest{URL: "https://169.254.169.254/x"}},
			wantDetails: map[string][]string{"wake.url": {"wake.url must not point at link-local (covers AWS instance metadata 169.254.169.254)"}},
		},
		{
			name: "a wake URL at the IPv6 cloud metadata address",
			req: CreateLinkAgentRequest{Name: "Claims Desk", EntrypointID: linkFixtureEntrypointID,
				Wake: &LinkAgentWakeRequest{URL: "https://[fd00:ec2::254]/"}},
			wantDetails: map[string][]string{"wake.url": {"wake.url must not point at cloud instance metadata endpoint"}},
		},
		{
			name: "a wake URL at an unspecified address",
			req: CreateLinkAgentRequest{Name: "Claims Desk", EntrypointID: linkFixtureEntrypointID,
				Wake: &LinkAgentWakeRequest{URL: "https://0.0.0.0/x"}},
			wantDetails: map[string][]string{"wake.url": {"wake.url must not point at unspecified address (0.0.0.0 / ::)"}},
		},
		{
			name: "a token URL at the IPv4 cloud metadata address",
			req: CreateLinkAgentRequest{Name: "Claims Desk", EntrypointID: linkFixtureEntrypointID,
				Wake: &LinkAgentWakeRequest{URL: "https://runtime.example.com/wake", TokenURL: "https://169.254.169.254/x",
					ClientID: "claims-client", ClientSecret: linkFixtureWakeSecret}},
			wantDetails: map[string][]string{"wake.tokenUrl": {"wake.tokenUrl must not point at link-local (covers AWS instance metadata 169.254.169.254)"}},
		},
		{
			name: "a token URL at the IPv6 cloud metadata address",
			req: CreateLinkAgentRequest{Name: "Claims Desk", EntrypointID: linkFixtureEntrypointID,
				Wake: &LinkAgentWakeRequest{URL: "https://runtime.example.com/wake", TokenURL: "https://[fd00:ec2::254]/",
					ClientID: "claims-client", ClientSecret: linkFixtureWakeSecret}},
			wantDetails: map[string][]string{"wake.tokenUrl": {"wake.tokenUrl must not point at cloud instance metadata endpoint"}},
		},
		{
			name: "a header sent as redacted when none is stored",
			req: CreateLinkAgentRequest{Name: "Claims Desk", EntrypointID: linkFixtureEntrypointID,
				Wake: &LinkAgentWakeRequest{URL: "https://runtime.example.com/wake", Headers: map[string]string{"Authorization": "<REDACTED>"}}},
			wantDetails: map[string][]string{"wake.headers": {`wake.headers: the value of "Authorization" is <REDACTED> but none is stored; send its value`}},
		},
		{
			name: "wake headers the HTTP client would refuse",
			req: CreateLinkAgentRequest{Name: "Claims Desk", EntrypointID: linkFixtureEntrypointID,
				Wake: &LinkAgentWakeRequest{URL: "https://runtime.example.com/wake", Headers: map[string]string{"Bad Header": "x", "X-Session": "a\nb"}}},
			wantDetails: map[string][]string{"wake.headers": {
				`wake.headers: "Bad Header" is not a valid header name`,
				`wake.headers: the value of "X-Session" is not a valid header value`,
			}},
		},
		{
			name: "wake header names that differ only in case",
			req: CreateLinkAgentRequest{Name: "Claims Desk", EntrypointID: linkFixtureEntrypointID,
				Wake: &LinkAgentWakeRequest{URL: "https://runtime.example.com/wake", Headers: map[string]string{"X-A": "1", "x-a": "2"}}},
			wantDetails: map[string][]string{"wake.headers": {`wake.headers: "x-a" repeats another header name; header names are case-insensitive`}},
		},
		{
			name: "wake headers the wake call sets itself",
			req: CreateLinkAgentRequest{Name: "Claims Desk", EntrypointID: linkFixtureEntrypointID,
				Wake: &LinkAgentWakeRequest{URL: "https://runtime.example.com/wake",
					Headers: map[string]string{"Host": "other.example.com", "Content-Length": "1", "transfer-encoding": "chunked", "Content-Type": "text/plain"}}},
			wantDetails: map[string][]string{"wake.headers": {
				`wake.headers: "Content-Length" is reserved for the wake call and cannot be sent`,
				`wake.headers: "Content-Type" is reserved for the wake call and cannot be sent`,
				`wake.headers: "Host" is reserved for the wake call and cannot be sent`,
				`wake.headers: "transfer-encoding" is reserved for the wake call and cannot be sent`,
			}},
		},
		{
			name: "an Authorization header with a token URL",
			req: CreateLinkAgentRequest{Name: "Claims Desk", EntrypointID: linkFixtureEntrypointID,
				Wake: &LinkAgentWakeRequest{URL: "https://runtime.example.com/wake", TokenURL: "https://idp.example.com/oauth2/token",
					ClientID: "claims-client", ClientSecret: linkFixtureWakeSecret, Headers: map[string]string{"authorization": linkFixtureWakeHeaderValue}}},
			wantDetails: map[string][]string{"wake.headers": {`wake.headers: "Authorization" cannot be sent with wake.tokenUrl, which sends its own bearer token`}},
		},
		{
			name: "a wake timeout one second over the limit",
			req: CreateLinkAgentRequest{Name: "Claims Desk", EntrypointID: linkFixtureEntrypointID,
				Wake: &LinkAgentWakeRequest{URL: "https://runtime.example.com/wake", TimeoutSeconds: 86401}},
			wantDetails: map[string][]string{"wake.timeoutSeconds": {"wake.timeoutSeconds must be between 1 and 86400, or left out for the default"}},
		},
		{
			name: "a negative wake timeout",
			req: CreateLinkAgentRequest{Name: "Claims Desk", EntrypointID: linkFixtureEntrypointID,
				Wake: &LinkAgentWakeRequest{URL: "https://runtime.example.com/wake", TimeoutSeconds: -1}},
			wantDetails: map[string][]string{"wake.timeoutSeconds": {"wake.timeoutSeconds must be between 1 and 86400, or left out for the default"}},
		},
		{
			name: "several problems are reported together",
			req: CreateLinkAgentRequest{Name: "", EntrypointID: linkFixtureWebhookID,
				Wake: &LinkAgentWakeRequest{URL: "ftp://runtime.example.com"}},
			wantDetails: map[string][]string{
				"name":         {"name is required"},
				"entrypointId": {"entrypointId must be the id of an agent_link entrypoint"},
				"wake.url":     {"wake.url must use https"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newLinkAgentFixture(t)

			resp, token, err := f.svc.Create(t.Context(), tt.req, "alice@example.com")

			if resp != nil || token != "" {
				t.Errorf("Create() returned a link agent or a token with its refusal")
			}
			if diff := cmp.Diff(tt.wantDetails, linkValidationDetails(t, err)); diff != "" {
				t.Errorf("Create() refusal details mismatch (-want +got):\n%s", diff)
			}
			if n, _ := countLinkAgents(t, f.db); n != 0 {
				t.Errorf("link agents stored after a refused create = %d, want 0", n)
			}
		})
	}
}

func countLinkAgents(t *testing.T, db *store.DB) (linkAgents, mirrors int) {
	t.Helper()
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM link_agents`).Scan(&linkAgents); err != nil {
		t.Fatalf("count link agents: %v", err)
	}
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM agents WHERE type = 'link'`).Scan(&mirrors); err != nil {
		t.Fatalf("count link agent mirrors: %v", err)
	}
	return linkAgents, mirrors
}

func TestLinkAgentService_TheTokenIsIssuedOnCreateAndOnRotateOnly(t *testing.T) {
	f := newLinkAgentFixture(t)

	created, firstToken := f.create(t, claimsDesk())

	if !strings.HasPrefix(firstToken, "samk_") || len(firstToken) != 48 {
		t.Fatalf("Create() token has length %d and marker %t, want a 48-character samk_ token", len(firstToken), strings.HasPrefix(firstToken, "samk_"))
	}
	if got := f.stored(t, created.ID).TokenSHA256; got != digestOf(firstToken) {
		t.Errorf("stored digest after Create is not the SHA-256 of the token Create returned")
	}
	if created.TokenPrefix != firstToken[5:13] {
		t.Errorf("Create().TokenPrefix is not the 8 characters after the token's marker")
	}

	rotation, err := f.svc.RotateToken(t.Context(), created.ID, "bob@example.com", nil)

	if err != nil {
		t.Fatalf("RotateToken() = %v", err)
	}
	rotated, secondToken := rotation.Agent, rotation.Token
	if secondToken == firstToken || !strings.HasPrefix(secondToken, "samk_") {
		t.Fatalf("RotateToken() returned a new token %t, want a different samk_ token", secondToken != firstToken)
	}
	if rotation.Deployment != nil || rotation.DeploymentRefusal != "" {
		t.Errorf("RotateToken() of an undeployed agent = deployment %v refusal %q, want neither", rotation.Deployment, rotation.DeploymentRefusal)
	}
	if got := f.stored(t, created.ID).TokenSHA256; got != digestOf(secondToken) {
		t.Errorf("stored digest after RotateToken is not the SHA-256 of the token it returned")
	}
	if rotated.TokenPrefix != secondToken[5:13] || rotated.UpdatedBy != "bob@example.com" {
		t.Errorf("RotateToken() = prefix match %t, updatedBy %q, want the new token's prefix and bob@example.com", rotated.TokenPrefix == secondToken[5:13], rotated.UpdatedBy)
	}

	read, err := f.svc.GetByID(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("GetByID() = %v", err)
	}
	listed, err := f.svc.ListPaginated(t.Context(), 1, 20)
	if err != nil {
		t.Fatalf("ListPaginated() = %v", err)
	}
	for name, body := range map[string]any{"GetByID": read, "ListPaginated": listed, "Create": created, "RotateToken": rotated} {
		encoded, _ := json.Marshal(body)
		for _, secret := range []string{firstToken, secondToken, digestOf(firstToken), digestOf(secondToken)} {
			if strings.Contains(string(encoded), secret) {
				t.Errorf("%s response carries a token or its digest", name)
			}
		}
	}
}

func TestLinkAgentService_ReplaceKeepsTheStoredWakeSecretUnlessToldOtherwise(t *testing.T) {
	tests := []struct {
		name         string
		wake         *LinkAgentWakeRequest
		wantStored   *store.LinkAgentWake
		wantReturned string
		wantDetails  map[string][]string
	}{
		{
			name: "left out is kept",
			wake: &LinkAgentWakeRequest{URL: "https://runtime.example.com/invocations", TokenURL: linkFixtureTokenURL, ClientID: "claims-client"},
			wantStored: &store.LinkAgentWake{URL: "https://runtime.example.com/invocations", TokenURL: linkFixtureTokenURL,
				ClientID: "claims-client", ClientSecret: linkFixtureWakeSecret, OnStart: true},
			wantReturned: "<REDACTED>",
		},
		{
			name: "the redacted sentinel is kept",
			wake: &LinkAgentWakeRequest{URL: "https://runtime.example.com/invocations", TokenURL: linkFixtureTokenURL, ClientID: "claims-client", ClientSecret: "<REDACTED>"},
			wantStored: &store.LinkAgentWake{URL: "https://runtime.example.com/invocations", TokenURL: linkFixtureTokenURL,
				ClientID: "claims-client", ClientSecret: linkFixtureWakeSecret, OnStart: true},
			wantReturned: "<REDACTED>",
		},
		{
			name: "a new secret replaces it",
			wake: &LinkAgentWakeRequest{URL: "https://runtime.example.com/invocations", TokenURL: linkFixtureTokenURL, ClientID: "claims-client", ClientSecret: "second-secret"},
			wantStored: &store.LinkAgentWake{URL: "https://runtime.example.com/invocations", TokenURL: linkFixtureTokenURL,
				ClientID: "claims-client", ClientSecret: "second-secret", OnStart: true},
			wantReturned: "<REDACTED>",
		},
		{
			name: "a new token URL with the secret sent again",
			wake: &LinkAgentWakeRequest{URL: "https://runtime.example.com/invocations", TokenURL: "https://login.example.com/token", ClientID: "claims-client", ClientSecret: linkFixtureWakeSecret},
			wantStored: &store.LinkAgentWake{URL: "https://runtime.example.com/invocations", TokenURL: "https://login.example.com/token",
				ClientID: "claims-client", ClientSecret: linkFixtureWakeSecret, OnStart: true},
			wantReturned: "<REDACTED>",
		},
		{
			name: "a new token URL without the secret is refused",
			wake: &LinkAgentWakeRequest{URL: "https://runtime.example.com/invocations", TokenURL: "https://login.example.com/token", ClientID: "claims-client"},
			wantStored: &store.LinkAgentWake{URL: "https://runtime.example.com/invocations", TokenURL: linkFixtureTokenURL,
				ClientID: "claims-client", ClientSecret: linkFixtureWakeSecret, OnStart: true},
			wantDetails: map[string][]string{"wake.clientSecret": {"must be sent again when the URL it is sent to changes"}},
		},
		{
			name: "a new wake URL with the secret left out is refused",
			wake: &LinkAgentWakeRequest{URL: linkFixtureOtherWakeURL, TokenURL: linkFixtureTokenURL, ClientID: "claims-client"},
			wantStored: &store.LinkAgentWake{URL: "https://runtime.example.com/invocations", TokenURL: linkFixtureTokenURL,
				ClientID: "claims-client", ClientSecret: linkFixtureWakeSecret, OnStart: true},
			wantDetails: map[string][]string{"wake.clientSecret": {secretResentMessage}},
		},
		{
			name: "a new wake URL with the redacted sentinel is refused",
			wake: &LinkAgentWakeRequest{URL: linkFixtureOtherWakeURL, TokenURL: linkFixtureTokenURL, ClientID: "claims-client", ClientSecret: "<REDACTED>"},
			wantStored: &store.LinkAgentWake{URL: "https://runtime.example.com/invocations", TokenURL: linkFixtureTokenURL,
				ClientID: "claims-client", ClientSecret: linkFixtureWakeSecret, OnStart: true},
			wantDetails: map[string][]string{"wake.clientSecret": {secretResentMessage}},
		},
		{
			name: "a new wake URL with the secret sent again",
			wake: &LinkAgentWakeRequest{URL: linkFixtureOtherWakeURL, TokenURL: linkFixtureTokenURL, ClientID: "claims-client", ClientSecret: linkFixtureWakeSecret},
			wantStored: &store.LinkAgentWake{URL: linkFixtureOtherWakeURL, TokenURL: linkFixtureTokenURL,
				ClientID: "claims-client", ClientSecret: linkFixtureWakeSecret, OnStart: true},
			wantReturned: "<REDACTED>",
		},
		{
			name:         "a wake call without a token drops it",
			wake:         &LinkAgentWakeRequest{URL: "https://runtime.example.com/invocations"},
			wantStored:   &store.LinkAgentWake{URL: "https://runtime.example.com/invocations", OnStart: true},
			wantReturned: "",
		},
		{
			name:       "no wake settings drops it with them",
			wake:       nil,
			wantStored: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newLinkAgentFixture(t)
			req := claimsDesk()
			req.Wake = wakeWithOAuth()
			created, _ := f.create(t, req)
			update := UpdateLinkAgentRequest(claimsDesk())
			update.Wake = tt.wake

			resp, err := f.svc.Replace(t.Context(), created.ID, update, "bob@example.com", linkAgentEditorScopes)

			if diff := cmp.Diff(tt.wantDetails, linkValidationDetails(t, err)); diff != "" {
				t.Errorf("Replace() refusal details mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantStored, f.stored(t, created.ID).Wake); diff != "" {
				t.Errorf("stored wake settings after Replace mismatch (-want +got):\n%s", diff)
			}
			var returned string
			if resp != nil && resp.Wake != nil {
				returned = resp.Wake.ClientSecret
			}
			if returned != tt.wantReturned {
				t.Errorf("Replace().Wake.ClientSecret = %q, want %q", returned, tt.wantReturned)
			}
		})
	}
}

func TestLinkAgentService_ReplaceKeepsAWakeHeaderValueTheRequestSendsRedacted(t *testing.T) {
	const (
		originalURL = "https://runtime.example.com/invocations"
		none        = "<REDACTED> but none is stored; send its value"
	)
	original := map[string]string{"Authorization": linkFixtureWakeHeaderValue, "X-Session": "claims"}
	tests := []struct {
		name        string
		wake        *LinkAgentWakeRequest
		wantStored  map[string]string
		wantDetails map[string][]string
	}{
		{
			name:       "redacted is kept and another header changes",
			wake:       &LinkAgentWakeRequest{URL: originalURL, Headers: map[string]string{"Authorization": "<REDACTED>", "X-Session": "claims-2"}},
			wantStored: map[string]string{"Authorization": linkFixtureWakeHeaderValue, "X-Session": "claims-2"},
		},
		{
			name:       "the header name is matched ignoring case",
			wake:       &LinkAgentWakeRequest{URL: originalURL, Headers: map[string]string{"authorization": "<REDACTED>"}},
			wantStored: map[string]string{"authorization": linkFixtureWakeHeaderValue},
		},
		{
			name:       "a new value replaces it",
			wake:       &LinkAgentWakeRequest{URL: originalURL, Headers: map[string]string{"Authorization": "Bearer bob-wake-key"}},
			wantStored: map[string]string{"Authorization": "Bearer bob-wake-key"},
		},
		{
			name:        "redacted for a header none is stored under is refused",
			wake:        &LinkAgentWakeRequest{URL: originalURL, Headers: map[string]string{"X-Other": "<REDACTED>"}},
			wantStored:  original,
			wantDetails: map[string][]string{"wake.headers": {`wake.headers: the value of "X-Other" is ` + none}},
		},
		{
			name:        "redacted with a new wake URL is refused",
			wake:        &LinkAgentWakeRequest{URL: linkFixtureOtherWakeURL, Headers: map[string]string{"Authorization": "<REDACTED>"}},
			wantStored:  original,
			wantDetails: map[string][]string{"wake.headers": {`wake.headers: the value of "Authorization" ` + secretResentMessage}},
		},
		{
			name:       "a new value with a new wake URL",
			wake:       &LinkAgentWakeRequest{URL: linkFixtureOtherWakeURL, Headers: map[string]string{"Authorization": linkFixtureWakeHeaderValue}},
			wantStored: map[string]string{"Authorization": linkFixtureWakeHeaderValue},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newLinkAgentFixture(t)
			req := claimsDesk()
			req.Wake = &LinkAgentWakeRequest{URL: originalURL, Headers: original}
			created, _ := f.create(t, req)
			update := UpdateLinkAgentRequest(claimsDesk())
			update.Wake = tt.wake

			resp, err := f.svc.Replace(t.Context(), created.ID, update, "bob@example.com", linkAgentEditorScopes)

			if diff := cmp.Diff(tt.wantDetails, linkValidationDetails(t, err)); diff != "" {
				t.Errorf("Replace() refusal details mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantStored, f.stored(t, created.ID).Wake.Headers); diff != "" {
				t.Errorf("stored wake headers after Replace mismatch (-want +got):\n%s", diff)
			}
			if resp != nil {
				for name, value := range resp.Wake.Headers {
					if value != "<REDACTED>" {
						t.Errorf("Replace().Wake.Headers[%q] = %q, want <REDACTED>", name, value)
					}
				}
			}
		})
	}
}

func TestLinkAgentService_ResponsesNeverCarryAWakeHeaderValue(t *testing.T) {
	f := newLinkAgentFixture(t)
	req := claimsDesk()
	req.Wake = &LinkAgentWakeRequest{URL: "https://runtime.example.com/invocations", Headers: map[string]string{"Authorization": linkFixtureWakeHeaderValue}}
	created, _ := f.create(t, req)
	update := UpdateLinkAgentRequest(claimsDesk())
	update.Wake = &LinkAgentWakeRequest{URL: "https://runtime.example.com/invocations", Headers: map[string]string{"Authorization": "<REDACTED>"}}

	replaced, err := f.svc.Replace(t.Context(), created.ID, update, "bob@example.com", linkAgentEditorScopes)
	if err != nil {
		t.Fatalf("Replace() = %v", describeValidationError(err))
	}
	read, err := f.svc.GetByID(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("GetByID() = %v", err)
	}
	listed, err := f.svc.ListPaginated(t.Context(), 1, 20)
	if err != nil {
		t.Fatalf("ListPaginated() = %v", err)
	}
	rotation, err := f.svc.RotateToken(t.Context(), created.ID, "bob@example.com", nil)
	if err != nil {
		t.Fatalf("RotateToken() = %v", err)
	}

	wake := map[string]*LinkAgentWakeResponse{"Create": created.Wake, "Replace": replaced.Wake, "GetByID": read.Wake, "ListPaginated": listed.Data[0].Wake, "RotateToken": rotation.Agent.Wake}
	for name, got := range wake {
		if diff := cmp.Diff(map[string]string{"Authorization": "<REDACTED>"}, got.Headers); diff != "" {
			t.Errorf("%s wake headers mismatch (-want +got):\n%s", name, diff)
		}
	}
	for name, body := range map[string]any{"Create": created, "Replace": replaced, "GetByID": read, "ListPaginated": listed, "RotateToken": rotation} {
		encoded, _ := json.Marshal(body)
		if strings.Contains(string(encoded), linkFixtureWakeHeaderValue) {
			t.Errorf("%s response carries the wake header value", name)
		}
	}
	if got := f.stored(t, created.ID).Wake.Headers["Authorization"]; got != linkFixtureWakeHeaderValue {
		t.Errorf("stored wake header value = %q, want it kept", got)
	}
}

func TestLinkAgentService_RotateTokenReturnsTheUpdateTimeItStored(t *testing.T) {
	f := newLinkAgentFixture(t)
	created, _ := f.create(t, claimsDesk())
	const staleMillis = 1000
	if _, err := f.db.ExecContext(t.Context(), `UPDATE link_agents SET updated_time = $1 WHERE id = $2`, staleMillis, created.ID); err != nil {
		t.Fatalf("age the link agent: %v", err)
	}

	rotation, err := f.svc.RotateToken(t.Context(), created.ID, "bob@example.com", nil)

	if err != nil {
		t.Fatalf("RotateToken() = %v", err)
	}
	stored := f.stored(t, created.ID)
	if rotation.Agent.UpdatedTime == Timestamp(staleMillis) || rotation.Agent.UpdatedTime != Timestamp(stored.UpdatedTime) {
		t.Errorf("RotateToken().UpdatedTime = %d, want the stored %d", rotation.Agent.UpdatedTime, stored.UpdatedTime)
	}
}

func TestInsecureURLReason(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{url: "https://runtime.example.com/wake", want: ""},
		{url: "https://10.0.0.7/wake", want: ""},
		{url: "https://127.0.0.1/wake", want: ""},
		{url: "http://localhost:8088/wake", want: ""},
		{url: "http://127.0.0.1:8088/wake", want: ""},
		{url: "http://[::1]:8088/wake", want: ""},
		{url: "http://runtime.example.com/wake", want: "must use https"},
		{url: "http://10.0.0.7/wake", want: "must use https"},
		{url: "/wake", want: "must be an absolute URL"},
		{url: "https://alice:s3cret@runtime.example.com/wake", want: "must not carry a user name or password; send credentials in wake.headers"},
		{url: "https://alice@runtime.example.com/wake", want: "must not carry a user name or password; send credentials in wake.headers"},
		{url: "https://runtime.example.com/wake?code=abc", want: "must carry no query parameter other than api-version; send credentials in wake.headers"},
		{url: "https://runtime.example.com/wake?", want: "must not end with an empty query string"},
		{url: "https://runtime.example.com/wake?api-version=v1", want: ""},
		{url: "https://runtime.example.com/wake?API-Version=2025-05-01-preview", want: ""},
		{url: "http://127.0.0.1:8088/wake?api-version=v1", want: ""},
		{url: "https://runtime.example.com/wake?api-version=v1&code=abc", want: "must carry no query parameter other than api-version; send credentials in wake.headers"},
		{url: "https://runtime.example.com/wake?api-versions=1", want: "must carry no query parameter other than api-version; send credentials in wake.headers"},
		{url: "https://runtime.example.com/wake?api-version-key=abc", want: "must carry no query parameter other than api-version; send credentials in wake.headers"},
		{url: "https://runtime.example.com/wake?xapi-version=1", want: "must carry no query parameter other than api-version; send credentials in wake.headers"},
		{url: "https://runtime.example.com/wake?api_version=1", want: "must carry no query parameter other than api-version; send credentials in wake.headers"},
		{url: "https://runtime.example.com/wake?api-version=v1&api-version=v2", want: "may carry api-version only once"},
		{url: "https://runtime.example.com/wake?api-version=", want: "must carry api-version as a version such as 2025-05-01 or v1"},
		{url: "https://runtime.example.com/wake?api-version=%20", want: "must carry api-version as a version such as 2025-05-01 or v1"},
		{url: "https://runtime.example.com/wake?api-version=+", want: "must carry api-version as a version such as 2025-05-01 or v1"},
		{url: "https://runtime.example.com/wake?api-version=a b", want: "must carry api-version as a version such as 2025-05-01 or v1"},
		{url: "https://runtime.example.com/wake?api-version=sk%2Blive%2Fabc", want: "must carry api-version as a version such as 2025-05-01 or v1"},
		{url: "https://runtime.example.com/wake?api-version=v1%26code%3Dabc", want: "must carry api-version as a version such as 2025-05-01 or v1"},
		{url: "https://runtime.example.com/wake?api-version=" + strings.Repeat("1", 65), want: "must carry api-version as a version such as 2025-05-01 or v1"},
		{url: "https://runtime.example.com/wake?api-version=%", want: "has a query string that cannot be read"},
		{url: "https://runtime.example.com/wake?api-version=" + strings.Repeat("1", 64), want: ""},
		{url: "https://runtime.example.com/wake?api-version=2025-05-01.preview", want: ""},
		{url: "https://runtime.example.com/wake?api-version=v1_beta", want: ""},
		{url: "https://runtime.example.com/wake?api-version=V1-PREVIEW", want: ""},
		{url: "https://runtime.example.com/wake?api-version=v1%2Bx", want: "must carry api-version as a version such as 2025-05-01 or v1"},
		{url: "https://runtime.example.com/wake?api-version=v1%25", want: "must carry api-version as a version such as 2025-05-01 or v1"},
		{url: "https://runtime.example.com/wake?api-version=v1%2Fx", want: "must carry api-version as a version such as 2025-05-01 or v1"},
		{url: "https://runtime.example.com/wake?api-version=-v1", want: "must carry api-version as a version such as 2025-05-01 or v1"},
		{url: "https://runtime.example.com/wake?api-version=.v1", want: "must carry api-version as a version such as 2025-05-01 or v1"},
		{url: "https://runtime.example.com/wake?code=abc&api-version=", want: "must carry no query parameter other than api-version; send credentials in wake.headers"},
		{url: "https://runtime.example.com/wake?api-version=&code=abc", want: "must carry no query parameter other than api-version; send credentials in wake.headers"},
		{url: "https://runtime.example.com/wake?api-version=v1&API-Version=v2", want: "may carry api-version only once"},
		{url: "https://runtime.example.com/wake?&", want: "must not carry an empty query string"},
		{url: "https://runtime.example.com/wake?api-version=v1;code=abc", want: "has a query string that cannot be read"},
		{url: "http://runtime.example.com/wake?api-version=v1", want: "must use https"},
		{url: "https://169.254.169.254/latest?api-version=v1", want: "must not point at link-local (covers AWS instance metadata 169.254.169.254)"},
		{url: "https://[fd00:ec2::254]/?api-version=v1", want: "must not point at cloud instance metadata endpoint"},
		{url: "https://runtime.example.com/wake#section", want: ""},
		{url: "https://169.254.169.254/latest/meta-data", want: "must not point at link-local (covers AWS instance metadata 169.254.169.254)"},
		{url: "https://[fd00:ec2::254]/", want: "must not point at cloud instance metadata endpoint"},
		{url: "https://100.100.100.200/", want: "must not point at cloud instance metadata endpoint"},
		{url: "https://[fe80::1]/", want: "must not point at link-local (covers AWS instance metadata 169.254.169.254)"},
		{url: "https://0.0.0.0/", want: "must not point at unspecified address (0.0.0.0 / ::)"},
		{url: "https://224.0.0.1/", want: "must not point at link-local (covers AWS instance metadata 169.254.169.254)"},
		{url: "https://[ff02::1]/", want: "must not point at link-local (covers AWS instance metadata 169.254.169.254)"},
	}
	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			if got := insecureURLReason(tt.url, "wake.headers", wakeURLQueryParameter); got != tt.want {
				t.Errorf("insecureURLReason(%q) = %q, want %q", tt.url, got, tt.want)
			}
		})
	}
}

func TestLinkAgentService_ReplaceKeepsTheTokenAndReplacesTheRest(t *testing.T) {
	f := newLinkAgentFixture(t)
	req := claimsDesk()
	req.Description, req.Owner, req.Peers, req.ShareUserProfile, req.Enabled = "Answers claims questions.", "alice@example.com", []string{"policy-desk"}, true, new(false)
	created, token := f.create(t, req)

	resp, err := f.svc.Replace(t.Context(), created.ID, UpdateLinkAgentRequest{Name: "Policy Desk", EntrypointID: linkFixtureOtherEntrypointID}, "bob@example.com", linkAgentEditorScopes)

	if err != nil {
		t.Fatalf("Replace() = %v", describeValidationError(err))
	}
	stored := f.stored(t, created.ID)
	want := &store.LinkAgent{
		ID: created.ID, Name: "Policy Desk", EntrypointID: linkFixtureOtherEntrypointID,
		TokenSHA256: digestOf(token), TokenPrefix: token[5:13], Enabled: true, Peers: []string{},
		CreatedBy: "alice@example.com", UpdatedBy: "bob@example.com", CreatedTime: stored.CreatedTime, UpdatedTime: stored.UpdatedTime,
	}
	if diff := cmp.Diff(want, stored); diff != "" {
		t.Errorf("stored link agent after Replace mismatch (-want +got):\n%s", diff)
	}
	if resp.Name != "Policy Desk" || resp.EntrypointID != linkFixtureOtherEntrypointID || resp.ConnectPath != "/gw/second-link/v1/connect" {
		t.Errorf("Replace() = name %q entrypoint %q connectPath %q, want the replaced settings", resp.Name, resp.EntrypointID, resp.ConnectPath)
	}
}

const ownMeshNamePlaceholder = "<the agent's own mesh name>"

func TestLinkAgentService_ReplaceRefuses(t *testing.T) {
	tests := []struct {
		name        string
		deployments []string
		update      UpdateLinkAgentRequest
		wantDetails map[string][]string
	}{
		{
			name:        "the agent itself as a peer",
			update:      UpdateLinkAgentRequest{Name: "Claims Desk", EntrypointID: linkFixtureEntrypointID, Peers: []string{ownMeshNamePlaceholder}},
			wantDetails: map[string][]string{"peers": {"a link agent cannot list itself as a peer"}},
		},
		{
			name:        "another entrypoint while the agent is deployed",
			deployments: []string{DeployActionDeploy},
			update:      UpdateLinkAgentRequest{Name: "Claims Desk", EntrypointID: linkFixtureOtherEntrypointID},
			wantDetails: map[string][]string{"entrypointId": {"undeploy the link agent before moving it to another entrypoint"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newLinkAgentFixture(t)
			created, _ := f.create(t, claimsDesk())
			for _, action := range tt.deployments {
				f.recordDeployment(t, created.ID, action)
			}
			update := tt.update
			update.Peers = make([]string, len(tt.update.Peers))
			for i, peer := range tt.update.Peers {
				update.Peers[i] = strings.ReplaceAll(peer, ownMeshNamePlaceholder, meshNameOf(created.ID))
			}

			_, err := f.svc.Replace(t.Context(), created.ID, update, "bob@example.com", linkAgentEditorScopes)

			if diff := cmp.Diff(tt.wantDetails, linkValidationDetails(t, err)); diff != "" {
				t.Errorf("Replace() refusal details mismatch (-want +got):\n%s", diff)
			}
			if got := f.stored(t, created.ID); got.EntrypointID != linkFixtureEntrypointID || len(got.Peers) != 0 {
				t.Errorf("stored link agent after a refused Replace = entrypoint %q peers %v, want it unchanged", got.EntrypointID, got.Peers)
			}
		})
	}
}

func TestLinkAgentService_NamesAreUniqueAmongAllAgents(t *testing.T) {
	tests := []struct {
		name          string
		takenName     string
		sameNameAgain string
	}{
		{name: "an agent built on the platform", takenName: "orders agent", sameNameAgain: "OrdersAgent"},
		{name: "a remote agent", takenName: "shipping tracker", sameNameAgain: "ShippingTracker"},
		{name: "another link agent", takenName: "policy desk", sameNameAgain: "PolicyDesk"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newLinkAgentFixture(t)
			standard := &store.Agent{Name: "Orders Agent", CreatedBy: "alice@example.com", UpdatedBy: "alice@example.com"}
			if err := store.NewAgentRepo(f.db).Create(t.Context(), standard, nil, nil, nil, nil, nil); err != nil {
				t.Fatalf("seed the standard agent: %v", err)
			}
			remote := &store.A2AAgent{Name: "Shipping Tracker", AgentURL: "https://agents.example.com/a2a", AgentCardLocation: "well_known",
				AgentAuthType: new("none"), AgentAuthConfig: new("{}"), RequestTimeoutSeconds: 120, CreatedBy: "alice@example.com", UpdatedBy: "alice@example.com"}
			if err := store.NewA2AAgentRepo(f.db).CreateWithMirror(t.Context(), remote); err != nil {
				t.Fatalf("seed the remote agent: %v", err)
			}
			f.create(t, CreateLinkAgentRequest{Name: "Policy Desk", EntrypointID: linkFixtureOtherEntrypointID})
			own, _ := f.create(t, claimsDesk())

			_, _, createErr := f.svc.Create(t.Context(), CreateLinkAgentRequest{Name: tt.takenName, EntrypointID: linkFixtureEntrypointID}, "alice@example.com")
			_, renameErr := f.svc.Replace(t.Context(), own.ID, UpdateLinkAgentRequest{Name: tt.sameNameAgain, EntrypointID: linkFixtureEntrypointID}, "alice@example.com", linkAgentEditorScopes)

			if _, isConflict := errors.AsType[*ConflictError](createErr); !isConflict {
				t.Errorf("Create(%q) = %v, want a *ConflictError", tt.takenName, createErr)
			}
			if _, isConflict := errors.AsType[*ConflictError](renameErr); !isConflict {
				t.Errorf("Replace(name %q) = %v, want a *ConflictError", tt.sameNameAgain, renameErr)
			}
			if got := f.stored(t, own.ID).Name; got != "Claims Desk" {
				t.Errorf("stored name after a refused rename = %q, want Claims Desk", got)
			}
		})
	}
}

func TestLinkAgentService_ReplaceAcceptsTheAgentsOwnNameInAnotherCase(t *testing.T) {
	f := newLinkAgentFixture(t)
	own, _ := f.create(t, claimsDesk())

	resp, err := f.svc.Replace(t.Context(), own.ID, UpdateLinkAgentRequest{Name: "claims desk", EntrypointID: linkFixtureEntrypointID}, "alice@example.com", linkAgentEditorScopes)

	if err != nil || resp.Name != "claims desk" {
		t.Errorf("Replace(the agent's own name in another case) = %v, want it saved as claims desk", err)
	}
}

func TestLinkAgentService_DeleteNeedsTheAgentUndeployed(t *testing.T) {
	tests := []struct {
		name        string
		deployments []string
		wantErr     string
		wantStored  int
	}{
		{name: "never deployed is deleted", deployments: nil, wantErr: "", wantStored: 0},
		{name: "undeployed is deleted", deployments: []string{DeployActionDeploy, DeployActionUndeploy}, wantErr: "", wantStored: 0},
		{name: "deployed is kept", deployments: []string{DeployActionDeploy}, wantErr: "cannot delete a deployed link agent; undeploy it first", wantStored: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newLinkAgentFixture(t)
			created, _ := f.create(t, claimsDesk())
			for _, action := range tt.deployments {
				f.recordDeployment(t, created.ID, action)
			}

			err := f.svc.Delete(t.Context(), created.ID)

			var gotErr string
			if verr, ok := errors.AsType[*ValidationError](err); ok {
				gotErr = verr.Message
			} else if err != nil {
				t.Fatalf("Delete() = %T %v, want a *ValidationError or nil", err, err)
			}
			if gotErr != tt.wantErr {
				t.Errorf("Delete() refusal = %q, want %q", gotErr, tt.wantErr)
			}
			linkAgents, mirrors := countLinkAgents(t, f.db)
			if linkAgents != tt.wantStored || mirrors != tt.wantStored {
				t.Errorf("after Delete: %d link agents and %d mirrors stored, want %d of each", linkAgents, mirrors, tt.wantStored)
			}
		})
	}
}

func TestLinkAgentService_AMissingLinkAgentIsNotFound(t *testing.T) {
	f := newLinkAgentFixture(t)
	_, getErr := f.svc.GetByID(t.Context(), linkFixtureMissingID)
	_, replaceErr := f.svc.Replace(t.Context(), linkFixtureMissingID, UpdateLinkAgentRequest(claimsDesk()), "alice@example.com", linkAgentEditorScopes)
	_, rotateErr := f.svc.RotateToken(t.Context(), linkFixtureMissingID, "alice@example.com", nil)
	for name, err := range map[string]error{
		"GetByID": getErr, "Replace": replaceErr, "RotateToken": rotateErr,
		"Delete":             f.svc.Delete(t.Context(), linkFixtureMissingID),
		"DeleteWithUndeploy": f.svc.DeleteWithUndeploy(t.Context(), linkFixtureMissingID, "alice@example.com", nil),
	} {
		if _, notFound := errors.AsType[*NotFoundError](err); !notFound {
			t.Errorf("%s(missing link agent) = %v, want a *NotFoundError", name, err)
		}
	}
}

func TestLinkAgentService_ConnectDetailsFollowWhatThePlatformKnowsOfTheEntrypointsAddress(t *testing.T) {
	tests := []struct {
		name        string
		domain      string
		webBase     string
		wantURL     string
		wantConnect string
	}{
		{name: "no public address known", wantURL: "", wantConnect: "/gw/partner-link/v1/connect"},
		{name: "the web UI's public address", webBase: "https://mesh.example.com/", wantURL: "wss://mesh.example.com/gw/partner-link/v1/connect", wantConnect: "/gw/partner-link/v1/connect"},
		{name: "a plain http web UI address", webBase: "http://localhost:8000", wantURL: "ws://localhost:8000/gw/partner-link/v1/connect", wantConnect: "/gw/partner-link/v1/connect"},
		{name: "the entrypoint's own host", domain: "sam.example.com", webBase: "https://mesh.example.com", wantURL: "wss://gw-abc12345-mesh1.sam.example.com/v1/connect", wantConnect: "/gw/partner-link/v1/connect"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("WEBUI_FRONTEND_SERVER_URL", tt.webBase)
			f := newLinkAgentFixture(t)
			f.svc.networking = NewGatewayNetworkingService(store.NewGatewayRepo(f.db), tt.domain, "mesh1", slog.New(slog.DiscardHandler))

			created, _ := f.create(t, claimsDesk())

			if created.ConnectURL != tt.wantURL || created.ConnectPath != tt.wantConnect {
				t.Errorf("Create() connect details = %q, %q, want %q, %q", created.ConnectURL, created.ConnectPath, tt.wantURL, tt.wantConnect)
			}
			if created.InstanceName != meshNameOf(created.ID) {
				t.Errorf("Create().InstanceName = %q, want agent_ and the id with underscores", created.InstanceName)
			}
		})
	}
}

func TestLinkAgentService_ReadsWarnWhenTheDeploymentsCannotBeRead(t *testing.T) {
	f := newLinkAgentFixture(t)
	agent, _ := f.create(t, claimsDesk())
	f.recordDeployment(t, agent.ID, DeployActionDeploy)
	if read, err := f.svc.GetByID(t.Context(), agent.ID); err != nil || read.DeploymentStatus != DisplayStatusDeployed {
		t.Fatalf("GetByID() before the deployments are hidden = %+v, %v, want a deployed agent", read, err)
	}
	if got := f.logs.countMessage(t, logLinkAgentDeploysNotRead) + f.logs.countMessage(t, logLinkAgentStatusesNotRead); got != 0 {
		t.Fatalf("warnings logged by reads that could read the deployments = %d, want 0", got)
	}
	if _, err := f.db.ExecContext(t.Context(), `ALTER TABLE agent_deployments RENAME TO agent_deployments_unreadable`); err != nil {
		t.Fatalf("hide the agent_deployments table: %v", err)
	}
	reads := map[string]func() (string, error){
		"GetByID": func() (string, error) {
			read, err := f.svc.GetByID(t.Context(), agent.ID)
			if err != nil {
				return "", err
			}
			return read.DeploymentStatus, nil
		},
		"ListPaginated": func() (string, error) {
			page, err := f.svc.ListPaginated(t.Context(), 1, 10)
			if err != nil {
				return "", err
			}
			return page.Data[0].DeploymentStatus, nil
		},
	}

	for _, name := range []string{"GetByID", "ListPaginated"} {
		deploysBefore := f.logs.countMessage(t, logLinkAgentDeploysNotRead)
		statusesBefore := f.logs.countMessage(t, logLinkAgentStatusesNotRead)

		status, err := reads[name]()

		if err != nil {
			t.Fatalf("%s() = %v, want the read to answer with what it could read", name, err)
		}
		if status != DisplayStatusNotDeployed {
			t.Errorf("%s() deploymentStatus = %q, want %q: the agent's deployments could not be read", name, status, DisplayStatusNotDeployed)
		}
		if got := f.logs.countMessage(t, logLinkAgentDeploysNotRead) - deploysBefore; got != 1 {
			t.Errorf("%s() warnings that the latest deployments were not read = %d, want 1", name, got)
		}
		if got := f.logs.countMessage(t, logLinkAgentStatusesNotRead) - statusesBefore; got != 1 {
			t.Errorf("%s() warnings that the deployment statuses were not read = %d, want 1", name, got)
		}
	}
	for _, entry := range f.logs.entries(t) {
		if msg, _ := entry["msg"].(string); (msg == logLinkAgentDeploysNotRead || msg == logLinkAgentStatusesNotRead) && entry["level"] != "WARN" {
			t.Errorf("%q logged at %v, want WARN", msg, entry["level"])
		}
	}
}

func TestLinkAgentService_ReadsReportAnUndecodableSnapshotOnce(t *testing.T) {
	f := newLinkAgentFixture(t)
	agent, _ := f.create(t, claimsDesk())
	row := &store.AgentDeployment{AgentID: agent.ID, Action: DeployActionDeploy, Status: DeployStatusSuccess,
		AgentConfigSnapshot: new("{}"), AgentDataSnapshot: new("not json"), CreatedBy: "alice@example.com"}
	if err := store.NewAgentDeploymentRepo(f.db).Create(t.Context(), row); err != nil {
		t.Fatalf("seed a deployment with an unreadable data snapshot: %v", err)
	}

	for range 2 {
		read, err := f.svc.GetByID(t.Context(), agent.ID)
		if err != nil || read.DeploymentStatus != DisplayStatusDeployed || read.Deployed != nil {
			t.Fatalf("GetByID() = %+v, %v, want a deployed agent without deployed details", read, err)
		}
	}
	if page, err := f.svc.ListPaginated(t.Context(), 1, 10); err != nil || len(page.Data) != 1 || page.Data[0].Deployed != nil {
		t.Fatalf("ListPaginated() = %+v, %v, want the agent without deployed details", page, err)
	}

	if got := f.logs.countMessage(t, logLinkAgentSnapshotUndecodable); got != 1 {
		t.Errorf("errors logged for the same unreadable snapshot over three reads = %d, want 1", got)
	}
}

func TestLinkAgentService_SecretsDiffComparesEveryWakeHeaderValue(t *testing.T) {
	f := newLinkAgentFixture(t)
	req := claimsDesk()
	req.Wake = &LinkAgentWakeRequest{URL: "https://runtime.example.com/invocations", Headers: map[string]string{"X-Session": "abc", "X-Tenant": "acme"}}
	agent, _ := f.create(t, req)
	tests := []struct {
		name        string
		headers     map[string]string
		wantEntry   bool
		wantMatches bool
	}{
		{name: "the same names and values", headers: map[string]string{"X-Session": "abc", "X-Tenant": "acme"}, wantEntry: true, wantMatches: true},
		{name: "names in another letter case", headers: map[string]string{"x-session": "abc", "X-TENANT": "acme"}, wantEntry: true, wantMatches: true},
		{name: "redacted values", headers: map[string]string{"X-Session": redactedSentinel, "X-Tenant": redactedSentinel}, wantEntry: true, wantMatches: true},
		{name: "a changed value", headers: map[string]string{"X-Session": "xyz", "X-Tenant": "acme"}, wantEntry: true},
		{name: "an empty value where one is stored", headers: map[string]string{"X-Session": "", "X-Tenant": "acme"}, wantEntry: true},
		{name: "a header left out", headers: map[string]string{"X-Session": "abc"}, wantEntry: true},
		{name: "an extra header", headers: map[string]string{"X-Session": "abc", "X-Tenant": "acme", "X-Trace": "1"}, wantEntry: true},
		{name: "a renamed header", headers: map[string]string{"X-Session": "abc", "X-Region": "acme"}, wantEntry: true},
		{name: "a redacted value for a header that is not stored", headers: map[string]string{"X-Session": "abc", "X-Region": redactedSentinel}, wantEntry: true},
		{name: "no headers", headers: map[string]string{}, wantEntry: true},
		{name: "headers not sent", headers: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			diff, err := f.svc.GetSecretsDiff(t.Context(), agent.ID, LinkAgentSecretsCandidate{WakeHeaders: tt.headers})
			if err != nil {
				t.Fatalf("GetSecretsDiff() = %v", err)
			}
			entry, present := diff[fieldLinkWakeHeaders]
			if present != tt.wantEntry || entry.Matches != tt.wantMatches {
				t.Errorf("wake.headers entry = %+v (present %t), want present %t and matches %t", entry, present, tt.wantEntry, tt.wantMatches)
			}
		})
	}
}

func TestLinkAgentService_StoresAWakeURLThatCarriesAnAPIVersion(t *testing.T) {
	f := newLinkAgentFixture(t)
	req := claimsDesk()
	req.Wake = wakeWithOAuth()
	req.Wake.URL = "https://runtime.example.com/invocations?api-version=2025-05-01-preview"

	agent, _ := f.create(t, req)

	if got := f.stored(t, agent.ID).Wake.URL; got != req.Wake.URL {
		t.Errorf("stored wake URL = %q, want %q", got, req.Wake.URL)
	}
	if agent.Wake == nil || agent.Wake.URL != req.Wake.URL {
		t.Errorf("returned wake = %+v, want the URL as sent", agent.Wake)
	}
}

func TestLinkAgentService_RefusesTheDigestOfAnEmptyToken(t *testing.T) {
	f := newLinkAgentFixture(t)
	req := claimsDesk()
	req.TokenSHA256 = DigestOfAnEmptyToken

	_, _, err := f.svc.Create(t.Context(), req, "alice@example.com")

	var refused *ValidationError
	if !errors.As(err, &refused) || !slices.Contains(refused.Details[fieldLinkAgentTokenSHA256], msgLinkTokenDigestOfNothing) {
		t.Fatalf("Create() with the digest of an empty token = %v, want the empty-token refusal on tokenSha256", describeValidationError(err))
	}
}
