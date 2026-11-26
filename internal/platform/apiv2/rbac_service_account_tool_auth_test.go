// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const (
	suToolAgent        = "support_agent"
	suToolInteractive  = "google_drive"
	suToolStatic       = "jira"
	suToolStateMarker  = "CANARYstate0190f5f1"
	suToolVerifier     = "CANARYverifier0190f5f1"
	suToolPathMarker   = "canary-authorize-path"
	suToolAuthURI      = "https://idp.example.com/" + suToolPathMarker + "?client_id=sam&state=" + suToolStateMarker
	suToolFirstExpiry  = "2030-01-02T03:04:05Z"
	suToolSecondExpiry = "2030-02-03T04:05:06Z"

	suToolSecondStateMarker = "CANARYstate2nd0190f5f2"
	suToolSecondAuthURI     = "https://idp.example.com/" + suToolPathMarker + "?client_id=sam&state=" + suToolSecondStateMarker

	suToolStateGrant       = "7b981ede8114805c3ee5e473880c731f95c23e7e456c5dc636db3bd26a05522d"
	suToolSecondStateGrant = "2d8be1459a9c867945df600a11fd6aad192f5fddad8ebe24992361fd33a12f7c"
	suToolEvaluationGrant  = "57fa7f8a19146bd86bf8dbb958be8d8242c63f00c931d4ba111bbc82bc5244e4"
	suToolEarlierGrant     = "0000000000000000000000000000000000000000000000000000000000000000"

	suToolAlice = "alice@example.com"
	suToolBob   = "bob@example.com"
)

var errSuToolAgentRefused = errors.New("the agent refused the request")

type heldToolCredential struct {
	expiry   string
	grantRef string
}

type fakeCredentialAWE struct {
	mu                sync.Mutex
	held              map[string]heldToolCredential
	userIDs           []string
	beginURI          string
	refusesDisconnect bool
}

func newFakeCredentialAWE() *fakeCredentialAWE {
	return &fakeCredentialAWE{held: map[string]heldToolCredential{}, beginURI: suToolAuthURI}
}

func (a *fakeCredentialAWE) refuseDisconnects(refuse bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.refusesDisconnect = refuse
}

func (a *fakeCredentialAWE) answerBeginWith(authURI string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.beginURI = authURI
}

func (a *fakeCredentialAWE) handle(_ string, path []string, body json.RawMessage) (any, error) {
	if len(path) != 4 || path[0] != "instances" || path[2] != "toolAuth" {
		return nil, errors.New("unexpected path")
	}
	if path[1] != suToolAgent {
		return nil, fmt.Errorf("instance %q: %w", path[1], control.ErrResourceNotFound)
	}
	var req struct {
		UserID        string `json:"userId"`
		CredentialKey string `json:"credentialKey"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.userIDs = append(a.userIDs, req.UserID)
	switch path[3] {
	case "query":
		return a.credentials(req.UserID), nil
	case "begin":
		if req.CredentialKey != suToolInteractive {
			return nil, fmt.Errorf("credential key %q: %w", req.CredentialKey, control.ErrResourceNotFound)
		}
		return map[string]any{
			"type": "authentication_required", "target_agent": suToolAgent, "target_agent_task_id": "preauth-1",
			"user_id": req.UserID, "client_id": "platform-toolauth", "auth_uri": a.beginURI,
			"credential_key": req.CredentialKey, "code_verifier": suToolVerifier, "gateway_task_id": nil,
		}, nil
	case "disconnect":
		if a.refusesDisconnect {
			return nil, errSuToolAgentRefused
		}
		if req.CredentialKey != suToolInteractive && req.CredentialKey != suToolStatic {
			return nil, fmt.Errorf("credential key %q: %w", req.CredentialKey, control.ErrResourceNotFound)
		}
		delete(a.held, req.UserID+"|"+req.CredentialKey)
		return nil, nil
	}
	return nil, errors.New("unexpected action")
}

func (a *fakeCredentialAWE) credentials(userID string) []map[string]any {
	drive := map[string]any{
		"credentialKey": suToolInteractive, "displayName": "Google Drive", "authType": "oauth2",
		"interactive": true, "status": "not_connected",
	}
	if held, ok := a.held[userID+"|"+suToolInteractive]; ok {
		drive["status"] = "connected"
		if held.expiry != "" {
			drive["expiresAt"] = held.expiry
		}
		if held.grantRef != "" {
			drive["grantRef"] = held.grantRef
		}
	}
	return []map[string]any{drive, {"credentialKey": suToolStatic, "interactive": false, "status": "not_connected"}}
}

func (a *fakeCredentialAWE) hold(userID string, credential heldToolCredential) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.held[userID+"|"+suToolInteractive] = credential
}

func (a *fakeCredentialAWE) askedFor() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.userIDs...)
}

type serviceAccountToolAuthFixture struct {
	api  *entrypointAPI
	awe  *fakeCredentialAWE
	user ServiceAccount
	base string
}

func newServiceAccountToolAuthFixture(t *testing.T) serviceAccountToolAuthFixture {
	t.Helper()
	api := newRBACAPI(t)
	awe := newFakeCredentialAWE()
	startControlApp(t, api.core, "awe", awe.handle)
	api.core.AgentRegistry().AddOrUpdate(a2a.AgentCard{Name: suToolAgent})
	user := api.createSystemUser("nightly-sync", api.createRole("nightly-sync-role").ID)
	return serviceAccountToolAuthFixture{api: api, awe: awe, user: user, base: "/rbac/serviceAccounts/" + user.ID + "/toolAuth"}
}

func (f serviceAccountToolAuthFixture) credential(key string) ServiceAccountToolCredential {
	f.api.t.Helper()
	rec := f.api.call(http.MethodGet, f.base+"/agents/"+suToolAgent, nil)
	expectStatus(f.api.t, rec, http.StatusOK)
	got := decodeData[ServiceAccountAgentToolAuth](f.api.t, rec)
	if got.Sub != f.user.Sub || got.ServiceAccountID != f.user.ID || got.AgentName != suToolAgent {
		f.api.t.Fatalf("agent tool auth = %+v, want %s on %s", got, f.user.Sub, suToolAgent)
	}
	for _, c := range got.Credentials {
		if c.CredentialKey == key {
			return c
		}
	}
	f.api.t.Fatalf("credential %q missing from %+v", key, got.Credentials)
	return ServiceAccountToolCredential{}
}

func actAs(t *testing.T, email string) {
	t.Helper()
	t.Setenv("SAM_DEV_USER_EMAIL", email)
}

func (f serviceAccountToolAuthFixture) begin(t *testing.T, actor, authURI string) {
	t.Helper()
	actAs(t, actor)
	f.awe.answerBeginWith(authURI)
	expectStatus(t, f.api.call(http.MethodPut, f.base+"/begin", f.credentialBody(suToolInteractive)), http.StatusOK)
}

func (f serviceAccountToolAuthFixture) credentialBody(key string) map[string]any {
	return map[string]any{"agentName": suToolAgent, "credentialKey": key}
}

func registrations(t *testing.T, f serviceAccountToolAuthFixture) func() a2a.ToolAuthStateRegistration {
	t.Helper()
	brk := f.api.core.Services().Broker()
	queue, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatal(err)
	}
	if err := brk.Subscribe(queue, a2a.ToolAuthRegisterSubscription(f.api.core.Config().Namespace)); err != nil {
		t.Fatal(err)
	}
	return func() a2a.ToolAuthStateRegistration {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		msg, err := brk.Receive(ctx, queue)
		if err != nil || msg == nil {
			t.Fatalf("no tool auth state registration published: %v", err)
		}
		var reg a2a.ToolAuthStateRegistration
		if err := json.Unmarshal(msg.Payload, &reg); err != nil {
			t.Fatal(err)
		}
		return reg
	}
}

func TestServiceAccountToolAuth_ConnectAndDisconnect(t *testing.T) {
	logs := captureLogs(t)
	f := newServiceAccountToolAuthFixture(t)
	nextRegistration := registrations(t, f)

	before := f.credential(suToolInteractive)
	if before.Status != "not_connected" || before.LastAuthorizedBy != nil || before.AuthorizationBegunBy != nil || !before.Interactive {
		t.Fatalf("before any connect = %+v", before)
	}

	actAs(t, suToolAlice)
	rec := f.api.call(http.MethodPut, f.base+"/begin", f.credentialBody(suToolInteractive))
	expectStatus(t, rec, http.StatusOK)
	begun := decodeData[ServiceAccountToolAuthorization](t, rec)
	if begun.AuthURI != suToolAuthURI || begun.CredentialKey != suToolInteractive || begun.ExpiresTime == 0 {
		t.Errorf("begin = %+v", begun)
	}

	reg := nextRegistration()
	if reg.UserID != f.user.Sub || reg.TargetAgent != suToolAgent || reg.CredentialKey != suToolInteractive ||
		reg.State != suToolStateMarker || reg.CodeVerifier != suToolVerifier || reg.TTLMs != 1800000 {
		t.Errorf("registration = user %q agent %q key %q ttl %dms, want the service account state for %s living 1800000ms", reg.UserID, reg.TargetAgent, reg.CredentialKey, reg.TTLMs, suToolAgent)
	}

	actAs(t, suToolBob)
	pending := f.credential(suToolInteractive)
	if pending.AuthorizationBegunBy == nil || *pending.AuthorizationBegunBy != suToolAlice || pending.AuthorizationBegunTime == 0 {
		t.Errorf("pending = %+v, want authorizationBegunBy %q", pending, suToolAlice)
	}
	if pending.LastAuthorizedBy != nil {
		t.Errorf("lastAuthorizedBy %q before the callback completed", *pending.LastAuthorizedBy)
	}

	f.awe.hold(f.user.Sub, heldToolCredential{expiry: suToolFirstExpiry, grantRef: suToolStateGrant})

	connected := f.credential(suToolInteractive)
	if connected.Status != "connected" || connected.ExpiresTime == 0 {
		t.Errorf("after the callback = %+v, want connected", connected)
	}
	if connected.LastAuthorizedBy == nil || *connected.LastAuthorizedBy != suToolAlice || connected.LastAuthorizedTime != pending.AuthorizationBegunTime {
		t.Errorf("attribution = %+v, want %q at the begin, not the reader %q", connected, suToolAlice, suToolBob)
	}
	if connected.AuthorizationBegunBy != nil {
		t.Errorf("authorizationBegunBy %q after completion, want null", *connected.AuthorizationBegunBy)
	}

	expectStatus(t, f.api.call(http.MethodPut, f.base+"/disconnect", f.credentialBody(suToolInteractive)), http.StatusNoContent)
	after := f.credential(suToolInteractive)
	if after.Status != "not_connected" {
		t.Errorf("after disconnect status %q", after.Status)
	}
	if after.LastDisconnectedBy == nil || *after.LastDisconnectedBy != suToolBob || after.LastDisconnectedTime == 0 {
		t.Errorf("disconnect attribution = %+v, want %q", after, suToolBob)
	}
	if after.LastAuthorizedBy == nil || *after.LastAuthorizedBy != suToolAlice {
		t.Errorf("lastAuthorizedBy was dropped or changed by the disconnect: %+v", after)
	}

	for _, userID := range f.awe.askedFor() {
		if userID != f.user.Sub {
			t.Errorf("the agent was asked about %q, want only %q", userID, f.user.Sub)
		}
	}
	f.expectAudit(t, logs, "connect_begun", suToolAlice)
	f.expectAudit(t, logs, "connect_completed", suToolAlice)
	f.expectAudit(t, logs, "disconnected", suToolBob)
	for _, marker := range []string{suToolStateMarker, suToolVerifier, suToolPathMarker} {
		if strings.Contains(logs.String(), marker) {
			t.Errorf("marker %q reached the logs", marker)
		}
	}
}

type auditEntry struct {
	Msg           string `json:"msg"`
	Action        string `json:"action"`
	Resource      string `json:"resource"`
	ResourceID    string `json:"resourceId"`
	Actor         string `json:"actor"`
	Principal     string `json:"principal"`
	AgentName     string `json:"agentName"`
	CredentialKey string `json:"credentialKey"`
}

func auditEntries(logs, action string) []auditEntry {
	var entries []auditEntry
	for line := range strings.Lines(logs) {
		var entry auditEntry
		if json.Unmarshal([]byte(line), &entry) == nil && entry.Msg == "rbac audit" &&
			entry.Resource == entplatform.AuditResourceSystemUserToolAuth && entry.Action == action {
			entries = append(entries, entry)
		}
	}
	return entries
}

func (f serviceAccountToolAuthFixture) expectAudit(t *testing.T, logs *lockedBuffer, action string, actors ...string) {
	t.Helper()
	var want []auditEntry
	for _, actor := range actors {
		want = append(want, auditEntry{
			Msg: "rbac audit", Action: action, Resource: "system_user_tool_auth", ResourceID: f.user.ID,
			Actor: actor, Principal: f.user.Sub, AgentName: suToolAgent, CredentialKey: suToolInteractive,
		})
	}
	if diff := cmp.Diff(want, auditEntries(logs.String(), action)); diff != "" {
		t.Errorf("%s audit lines mismatch (-want +got):\n%s", action, diff)
	}
}

func TestServiceAccountToolAuth_ASignInOfTheBeginIsCreditedOnceToItsActor(t *testing.T) {
	tests := []struct {
		name   string
		before *heldToolCredential
		after  heldToolCredential
	}{
		{"credential that was not connected", nil, heldToolCredential{expiry: suToolFirstExpiry, grantRef: suToolStateGrant}},
		{"credential replaced with a new expiry", &heldToolCredential{expiry: suToolFirstExpiry, grantRef: suToolEarlierGrant}, heldToolCredential{expiry: suToolSecondExpiry, grantRef: suToolStateGrant}},
		{"credential replaced with the same expiry", &heldToolCredential{expiry: suToolFirstExpiry, grantRef: suToolEarlierGrant}, heldToolCredential{expiry: suToolFirstExpiry, grantRef: suToolStateGrant}},
		{"credential that never expires before and after", &heldToolCredential{grantRef: suToolEarlierGrant}, heldToolCredential{grantRef: suToolStateGrant}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logs := captureLogs(t)
			f := newServiceAccountToolAuthFixture(t)
			if tt.before != nil {
				f.awe.hold(f.user.Sub, *tt.before)
			}
			f.begin(t, suToolAlice, suToolAuthURI)
			f.awe.hold(f.user.Sub, tt.after)

			actAs(t, suToolBob)
			var got ServiceAccountToolCredential
			for range 3 {
				got = f.credential(suToolInteractive)
			}

			if got.Status != "connected" || got.LastAuthorizedBy == nil || *got.LastAuthorizedBy != suToolAlice || got.AuthorizationBegunBy != nil {
				t.Errorf("after the sign-in of the begin = %+v, want connected and authorized by %q", got, suToolAlice)
			}
			f.expectAudit(t, logs, "connect_completed", suToolAlice)
		})
	}
}

func TestServiceAccountToolAuth_ChangesThatAreNotTheSignInOfTheBeginAreNotCredited(t *testing.T) {
	tests := []struct {
		name   string
		before *heldToolCredential
		after  heldToolCredential
	}{
		{"token refresh after an abandoned begin", &heldToolCredential{expiry: suToolFirstExpiry, grantRef: suToolEarlierGrant}, heldToolCredential{expiry: suToolSecondExpiry, grantRef: suToolEarlierGrant}},
		{"evaluation Connect Tools sign-in on a credential that was not connected", nil, heldToolCredential{expiry: suToolFirstExpiry, grantRef: suToolEvaluationGrant}},
		{"evaluation Connect Tools sign-in replacing the held credential", &heldToolCredential{expiry: suToolFirstExpiry, grantRef: suToolEarlierGrant}, heldToolCredential{expiry: suToolSecondExpiry, grantRef: suToolEvaluationGrant}},
		{"credential stored without a grant reference", &heldToolCredential{expiry: suToolFirstExpiry}, heldToolCredential{expiry: suToolSecondExpiry}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logs := captureLogs(t)
			f := newServiceAccountToolAuthFixture(t)
			if tt.before != nil {
				f.awe.hold(f.user.Sub, *tt.before)
			}
			f.begin(t, suToolAlice, suToolAuthURI)
			f.awe.hold(f.user.Sub, tt.after)

			actAs(t, suToolBob)
			got := f.credential(suToolInteractive)

			if got.Status != "connected" || got.LastAuthorizedBy != nil || got.AuthorizationBegunBy == nil || *got.AuthorizationBegunBy != suToolAlice {
				t.Errorf("after a change that is not the begin's sign-in = %+v, want the begin by %q still pending and nobody authorized", got, suToolAlice)
			}
			f.expectAudit(t, logs, "connect_completed")
		})
	}
}

func TestServiceAccountToolAuth_ASecondBeginTakesOverTheCompletionOfTheFirst(t *testing.T) {
	logs := captureLogs(t)
	f := newServiceAccountToolAuthFixture(t)
	f.begin(t, suToolAlice, suToolAuthURI)
	f.begin(t, suToolBob, suToolSecondAuthURI)

	f.awe.hold(f.user.Sub, heldToolCredential{expiry: suToolFirstExpiry, grantRef: suToolStateGrant})
	first := f.credential(suToolInteractive)
	if first.LastAuthorizedBy != nil || first.AuthorizationBegunBy == nil || *first.AuthorizationBegunBy != suToolBob {
		t.Errorf("after the first link was used = %+v, want the second begin by %q still pending", first, suToolBob)
	}
	f.expectAudit(t, logs, "connect_completed")

	f.awe.hold(f.user.Sub, heldToolCredential{expiry: suToolSecondExpiry, grantRef: suToolSecondStateGrant})
	second := f.credential(suToolInteractive)
	if second.LastAuthorizedBy == nil || *second.LastAuthorizedBy != suToolBob || second.AuthorizationBegunBy != nil {
		t.Errorf("after the second link was used = %+v, want authorized by %q", second, suToolBob)
	}
	f.expectAudit(t, logs, "connect_completed", suToolBob)
}

func TestServiceAccountToolAuth_ASignInThatFinishesAfterADisconnectIsStillCreditedToItsBegin(t *testing.T) {
	logs := captureLogs(t)
	f := newServiceAccountToolAuthFixture(t)
	f.begin(t, suToolAlice, suToolAuthURI)
	actAs(t, suToolBob)
	expectStatus(t, f.api.call(http.MethodPut, f.base+"/disconnect", f.credentialBody(suToolInteractive)), http.StatusNoContent)

	f.awe.hold(f.user.Sub, heldToolCredential{expiry: suToolFirstExpiry, grantRef: suToolStateGrant})
	got := f.credential(suToolInteractive)

	if got.Status != "connected" || got.LastAuthorizedBy == nil || *got.LastAuthorizedBy != suToolAlice ||
		got.LastDisconnectedBy == nil || *got.LastDisconnectedBy != suToolBob {
		t.Errorf("after a sign-in finished after a disconnect = %+v, want authorized by %q and disconnected by %q", got, suToolAlice, suToolBob)
	}
	f.expectAudit(t, logs, "connect_completed", suToolAlice)
}

func TestServiceAccountToolAuth_DisconnectIsRecordedOnlyOnceTheAgentWipedTheCredential(t *testing.T) {
	logs := captureLogs(t)
	f := newServiceAccountToolAuthFixture(t)
	f.begin(t, suToolAlice, suToolAuthURI)
	f.awe.hold(f.user.Sub, heldToolCredential{expiry: suToolFirstExpiry, grantRef: suToolStateGrant})
	f.credential(suToolInteractive)
	actAs(t, suToolBob)

	expectStatus(t, f.api.call(http.MethodPut, f.base+"/disconnect", f.credentialBody("no_such_credential")), http.StatusNotFound)

	f.awe.refuseDisconnects(true)
	expectStatus(t, f.api.call(http.MethodPut, f.base+"/disconnect", f.credentialBody(suToolInteractive)), http.StatusBadGateway)
	refused := f.credential(suToolInteractive)
	if refused.Status != "connected" || refused.LastDisconnectedBy != nil || refused.LastDisconnectedTime != 0 ||
		refused.LastAuthorizedBy == nil || *refused.LastAuthorizedBy != suToolAlice {
		t.Errorf("after the agent refused the disconnect = %+v, want the credential still connected and no disconnect recorded", refused)
	}
	f.expectAudit(t, logs, "disconnected")

	f.awe.refuseDisconnects(false)
	expectStatus(t, f.api.call(http.MethodPut, f.base+"/disconnect", f.credentialBody(suToolInteractive)), http.StatusNoContent)
	done := f.credential(suToolInteractive)
	if done.Status != "not_connected" || done.LastDisconnectedBy == nil || *done.LastDisconnectedBy != suToolBob {
		t.Errorf("after the agent wiped the credential = %+v, want a disconnect by %q", done, suToolBob)
	}
	f.expectAudit(t, logs, "disconnected", suToolBob)
}

func TestServiceAccountToolAuth_BeginThatCannotRegisterItsStateLeavesTheRecordAlone(t *testing.T) {
	logs := captureLogs(t)
	f := newServiceAccountToolAuthFixture(t)
	f.awe.hold(f.user.Sub, heldToolCredential{expiry: suToolFirstExpiry, grantRef: suToolEarlierGrant})
	f.awe.answerBeginWith("ftp://idp.example.com/authorize?state=" + suToolStateMarker)

	expectStatus(t, f.api.call(http.MethodPut, f.base+"/begin", f.credentialBody(suToolInteractive)), http.StatusInternalServerError)

	got := f.credential(suToolInteractive)
	if got.Status != "connected" || got.AuthorizationBegunBy != nil || got.AuthorizationBegunTime != 0 {
		t.Errorf("after a begin whose state could not be registered = %+v, want the connected credential with no pending authorization", got)
	}
	f.expectAudit(t, logs, "connect_begun")
}

func TestServiceAccountToolAuth_Refusals(t *testing.T) {
	f := newServiceAccountToolAuthFixture(t)

	expectStatus(t, f.api.call(http.MethodPut, f.base+"/begin", f.credentialBody("no_such_credential")), http.StatusNotFound)
	expectDetail(t, f.api.call(http.MethodPut, f.base+"/begin", f.credentialBody(suToolStatic)), http.StatusUnprocessableEntity, "credentialKey")
	expectStatus(t, f.api.call(http.MethodGet, f.base+"/agents/no_such_agent", nil), http.StatusNotFound)
	expectDetail(t, f.api.call(http.MethodGet, f.base+"/agents/bad%20name", nil), http.StatusUnprocessableEntity, "agentName")
	expectStatus(t, f.api.call(http.MethodPut, f.base+"/begin", map[string]any{"agentName": suToolAgent}), http.StatusUnprocessableEntity)
	expectStatus(t, f.api.call(http.MethodPut, f.base+"/begin", `{"agentName":"support_agent","credentialKey":"google_drive","userId":"alice"}`), http.StatusBadRequest)

	missing := "/rbac/serviceAccounts/" + missingID + "/toolAuth"
	expectStatus(t, f.api.call(http.MethodGet, missing+"/agents/"+suToolAgent, nil), http.StatusNotFound)
	expectStatus(t, f.api.call(http.MethodPut, missing+"/begin", f.credentialBody(suToolInteractive)), http.StatusNotFound)
	expectStatus(t, f.api.call(http.MethodPut, missing+"/disconnect", f.credentialBody(suToolInteractive)), http.StatusNotFound)
	expectStatus(t, f.api.call(http.MethodGet, "/rbac/serviceAccounts/not-a-uuid/toolAuth/agents/"+suToolAgent, nil), http.StatusBadRequest)
}

func TestServiceAccountToolAuth_BuiltinServiceAccountsAre409(t *testing.T) {
	f := newServiceAccountToolAuthFixture(t)
	for _, builtin := range rbac.ReservedSystemUsers() {
		base := "/rbac/serviceAccounts/" + builtin.ID + "/toolAuth"
		for _, rec := range []struct {
			method, path string
			body         any
		}{
			{http.MethodGet, base + "/agents/" + suToolAgent, nil},
			{http.MethodPut, base + "/begin", f.credentialBody(suToolInteractive)},
			{http.MethodPut, base + "/disconnect", f.credentialBody(suToolInteractive)},
		} {
			got := f.api.call(rec.method, rec.path, rec.body)
			expectStatus(t, got, http.StatusConflict)
			if msg := decodeError(t, got).Message; !strings.Contains(msg, builtin.Sub) {
				t.Errorf("%s %s message %q does not name %s", rec.method, rec.path, msg, builtin.Sub)
			}
		}
	}
	if asked := f.awe.askedFor(); len(asked) != 0 {
		t.Errorf("a built-in service account reached the agent for %v", asked)
	}
}

func TestServiceAccountToolAuth_SuperAdministratorsOnly(t *testing.T) {
	f := newServiceAccountToolAuthFixture(t)
	for _, scopes := range [][]string{
		{rbac.ScopeRBACRead},
		{rbac.ScopeEvaluationRead, rbac.ScopeEvaluationInvoke, rbac.ScopeEvaluationCreate},
		{"agent:*:invoke", "agent_builder:*:read"},
		{"rbac:_:create", "rbac:_:update", "rbac:_:delete", rbac.ScopeRBACRead},
		{"rbac:_:*"},
		{"rbac:*:*"},
		{"agent:*:*", "connector:*:*", "rbac:*:*"},
	} {
		limited := f.api.as(scopes...)
		expectStatus(t, limited.call(http.MethodGet, f.base+"/agents/"+suToolAgent, nil), http.StatusForbidden)
		expectStatus(t, limited.call(http.MethodPut, f.base+"/begin", f.credentialBody(suToolInteractive)), http.StatusForbidden)
		expectStatus(t, limited.call(http.MethodPut, f.base+"/disconnect", f.credentialBody(suToolInteractive)), http.StatusForbidden)
	}
	if asked := f.awe.askedFor(); len(asked) != 0 {
		t.Errorf("a refused caller reached the agent for %v", asked)
	}
	expectStatus(t, f.api.as("*:*:*").call(http.MethodGet, f.base+"/agents/"+suToolAgent, nil), http.StatusOK)
}

func TestServiceAccountToolAuth_MiniIDPRefusesWrites(t *testing.T) {
	api := newRBACAPI(t, withMiniIDP)
	base := "/rbac/serviceAccounts/" + missingID + "/toolAuth"
	body := map[string]any{"agentName": suToolAgent, "credentialKey": suToolInteractive}

	expectMessage(t, api.call(http.MethodPut, base+"/begin", body), http.StatusForbidden, entplatform.MsgMiniIDPWriteDisabled)
	expectMessage(t, api.call(http.MethodPut, base+"/disconnect", body), http.StatusForbidden, entplatform.MsgMiniIDPWriteDisabled)
	expectStatus(t, api.call(http.MethodGet, base+"/agents/"+suToolAgent, nil), http.StatusNotFound)
}

func TestServiceAccountToolAuth_APersonIsNotAServiceAccount(t *testing.T) {
	f := newServiceAccountToolAuthFixture(t)
	person := f.api.seedUser(userSeed{sub: "alice-sub", displayName: "Alice", email: "alice@example.com"})
	base := "/rbac/serviceAccounts/" + person + "/toolAuth"

	expectStatus(t, f.api.call(http.MethodGet, base+"/agents/"+suToolAgent, nil), http.StatusNotFound)
	expectStatus(t, f.api.call(http.MethodPut, base+"/begin", f.credentialBody(suToolInteractive)), http.StatusNotFound)
	expectStatus(t, f.api.call(http.MethodPut, base+"/disconnect", f.credentialBody(suToolInteractive)), http.StatusNotFound)
	if asked := f.awe.askedFor(); len(asked) != 0 {
		t.Errorf("a person's id reached the agent for %v", asked)
	}
}

func TestServiceAccountToolAuth_FlagOffIs404(t *testing.T) {
	t.Setenv(features.EnvVarName(features.KeySystemUserToolAuth), "false")
	api := newRBACAPI(t)
	user := api.createSystemUser("nightly-sync", api.createRole("nightly-sync-role").ID)
	base := "/rbac/serviceAccounts/" + user.ID + "/toolAuth"
	body := map[string]any{"agentName": suToolAgent, "credentialKey": suToolInteractive}
	expectStatus(t, api.call(http.MethodGet, base+"/agents/"+suToolAgent, nil), http.StatusNotFound)
	expectStatus(t, api.call(http.MethodPut, base+"/begin", body), http.StatusNotFound)
	expectStatus(t, api.call(http.MethodPut, base+"/disconnect", body), http.StatusNotFound)
}
