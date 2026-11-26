// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"maps"
	"net/http"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/resolve"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
)

const (
	linkTestDigest       = "6c6c6c6c6c6c6c6c6c6c6c6c6c6c6c6c6c6c6c6c6c6c6c6c6c6c6c6c6c6c6c6c"
	linkTestEntrypointID = "0190f5ec-0000-7000-8000-00000000e001"
	linkTestAgentID      = "0190f5ec-0000-7000-8000-00000000a001"
)

func resolvedLinkAgent(spec map[string]any) resolve.ResolvedResource {
	full := map[string]any{"entrypoint": "partner-link", "tokenSha256": linkTestDigest}
	for key, value := range spec {
		if value == nil {
			delete(full, key)
			continue
		}
		full[key] = value
	}
	return resolve.ResolvedResource{
		Kind:        linkAgentKind,
		Name:        "Claims Desk",
		SourceFile:  "linkAgents/claims-desk.yaml",
		Spec:        full,
		Description: "Answers claims questions for the partner.",
	}
}

func storedLinkAgent(edit func(*wire.LinkAgentResponse)) wire.LinkAgentResponse {
	stored := wire.LinkAgentResponse{
		ID:               linkTestAgentID,
		Name:             "Claims Desk",
		InstanceName:     "agent_0190f5ec_0000_7000_8000_00000000a001",
		Description:      sp("Answers claims questions for the partner."),
		EntrypointID:     linkTestEntrypointID,
		Enabled:          true,
		Peers:            []string{},
		DeploymentStatus: "deployed",
		SyncStatus:       sp("in_sync"),
	}
	if edit != nil {
		edit(&stored)
	}
	return stored
}

func storedWake() *apiv2.LinkAgentWake {
	return &apiv2.LinkAgentWake{
		URL:            "https://runtime.example.com/invocations",
		TokenURL:       sp("https://idp.example.com/oauth2/token"),
		ClientID:       sp("claims-client"),
		ClientSecret:   sp(redactedSentinel),
		Headers:        map[string]string{},
		TimeoutSeconds: 900,
		OnStart:        true,
	}
}

func storedWakeHeaders(names ...string) func(*wire.LinkAgentResponse) {
	return func(a *wire.LinkAgentResponse) {
		a.Wake = storedWake()
		for _, name := range names {
			a.Wake.Headers[name] = redactedSentinel
		}
	}
}

func declaredWake(edit map[string]any) map[string]any {
	wake := map[string]any{
		"url":          "https://runtime.example.com/invocations",
		"tokenUrl":     "https://idp.example.com/oauth2/token",
		"clientId":     "claims-client",
		"clientSecret": "wake-client-secret",
	}
	for key, value := range edit {
		wake[key] = value
	}
	return wake
}

func partnerLinkOnPlatform() linkEntrypoints {
	return newLinkEntrypoints([]wire.EntrypointResponse{{ID: linkTestEntrypointID, Name: "partner-link", EntrypointType: "agent_link"}}, nil)
}

func TestPlanLinkAgents_ComparesAFileWithWhatThePlatformStores(t *testing.T) {
	tests := []struct {
		name       string
		spec       map[string]any
		stored     func(*wire.LinkAgentResponse)
		wantUpdate bool
	}{
		{name: "a file that leaves every default out"},
		{name: "defaults written out", spec: map[string]any{"enabled": true, "shareUserProfile": false, "peers": []any{}}},
		{name: "enabled turned off", spec: map[string]any{"enabled": false}, wantUpdate: true},
		{name: "an owner the platform does not hold", spec: map[string]any{"owner": "alice@example.com"}, wantUpdate: true},
		{
			name:   "an owner the file no longer names",
			stored: func(a *wire.LinkAgentResponse) { a.Owner = sp("alice@example.com") },

			wantUpdate: true,
		},
		{name: "a peer added", spec: map[string]any{"peers": []any{"policy-desk"}}, wantUpdate: true},
		{
			name:   "peers in another order",
			spec:   map[string]any{"peers": []any{"billing-desk", "policy-desk"}},
			stored: func(a *wire.LinkAgentResponse) { a.Peers = []string{"policy-desk", "billing-desk"} },
		},
		{
			name:   "peers the file lists out of order",
			spec:   map[string]any{"peers": []any{"policy-desk", "billing-desk"}},
			stored: func(a *wire.LinkAgentResponse) { a.Peers = []string{"billing-desk", "policy-desk"} },
		},
		{name: "a token digest the file leaves out", spec: map[string]any{"tokenSha256": nil}},
		{
			name:   "wake settings whose defaults the file leaves out",
			spec:   map[string]any{"wake": declaredWake(nil)},
			stored: func(a *wire.LinkAgentResponse) { a.Wake = storedWake() },
		},
		{
			name:   "wake defaults written out",
			spec:   map[string]any{"wake": declaredWake(map[string]any{"timeoutSeconds": 900, "onStart": true, "headers": map[string]any{}})},
			stored: func(a *wire.LinkAgentResponse) { a.Wake = storedWake() },
		},
		{
			name:       "a wake timeout changed",
			spec:       map[string]any{"wake": declaredWake(map[string]any{"timeoutSeconds": 600})},
			stored:     func(a *wire.LinkAgentResponse) { a.Wake = storedWake() },
			wantUpdate: true,
		},
		{
			name:       "wake on start turned off",
			spec:       map[string]any{"wake": declaredWake(map[string]any{"onStart": false})},
			stored:     func(a *wire.LinkAgentResponse) { a.Wake = storedWake() },
			wantUpdate: true,
		},
		{
			name:       "wake settings the file no longer has",
			stored:     func(a *wire.LinkAgentResponse) { a.Wake = storedWake() },
			wantUpdate: true,
		},
		{
			name:   "a wake header value the platform returns redacted",
			spec:   map[string]any{"wake": declaredWake(map[string]any{"headers": map[string]any{"X-Api-Key": "alice-wake-key"}})},
			stored: storedWakeHeaders("X-Api-Key"),
		},
		{
			name:   "a wake header written as redacted",
			spec:   map[string]any{"wake": declaredWake(map[string]any{"headers": map[string]any{"X-Api-Key": redactedSentinel}})},
			stored: storedWakeHeaders("X-Api-Key"),
		},
		{
			name:       "a wake header the file no longer has",
			spec:       map[string]any{"wake": declaredWake(nil)},
			stored:     storedWakeHeaders("X-Api-Key"),
			wantUpdate: true,
		},
		{
			name:       "a wake header added",
			spec:       map[string]any{"wake": declaredWake(map[string]any{"headers": map[string]any{"X-Api-Key": "alice-wake-key", "X-Trace": "1"}})},
			stored:     storedWakeHeaders("X-Api-Key"),
			wantUpdate: true,
		},
		{
			name:       "a wake header renamed",
			spec:       map[string]any{"wake": declaredWake(map[string]any{"headers": map[string]any{"X-Key": "alice-wake-key"}})},
			stored:     storedWakeHeaders("X-Api-Key"),
			wantUpdate: true,
		},
		{
			name:       "a wake header renamed in letter case only",
			spec:       map[string]any{"wake": declaredWake(map[string]any{"headers": map[string]any{"x-api-key": "alice-wake-key"}})},
			stored:     storedWakeHeaders("X-Api-Key"),
			wantUpdate: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			desired := []resolve.ResolvedResource{resolvedLinkAgent(tt.spec)}
			actual := []wire.LinkAgentResponse{storedLinkAgent(tt.stored)}

			plan, err := planLinkAgents(t.Context(), &stubSecretDiffer{}, desired, actual, partnerLinkOnPlatform())

			if err != nil {
				t.Fatalf("planLinkAgents: %v", err)
			}
			if gotUpdate := len(plan.Updates) == 1; gotUpdate != tt.wantUpdate || len(plan.Updates)+len(plan.Unchanged) != 1 || len(plan.Creates)+len(plan.Deletes) != 0 {
				t.Errorf("plan = %d updates, %d unchanged, %d creates, %d deletes; want update=%t and nothing else",
					len(plan.Updates), len(plan.Unchanged), len(plan.Creates), len(plan.Deletes), tt.wantUpdate)
			}
		})
	}
}

func TestPlanLinkAgents_AStoredDigestOrSecretThatDiffersBecomesAnUpdate(t *testing.T) {
	desired := []resolve.ResolvedResource{resolvedLinkAgent(map[string]any{"wake": declaredWake(nil)})}
	actual := []wire.LinkAgentResponse{storedLinkAgent(func(a *wire.LinkAgentResponse) { a.Wake = storedWake() })}
	differ := &stubSecretDiffer{matchByID: map[string]bool{linkTestAgentID: false}}

	plan, err := planLinkAgents(t.Context(), differ, desired, actual, partnerLinkOnPlatform())

	if err != nil {
		t.Fatalf("planLinkAgents: %v", err)
	}
	if len(plan.Updates) != 1 || len(plan.Unchanged) != 0 {
		t.Fatalf("plan = %d updates, %d unchanged; want the link agent promoted to an update", len(plan.Updates), len(plan.Unchanged))
	}
	wantCompared := []stubDiffCall{{kind: "linkAgents", id: linkTestAgentID, values: apiv2.LinkAgentSecretsDiffForm{
		TokenSHA256: linkTestDigest,
		Wake:        &apiv2.LinkAgentWakeSecretsDiffForm{ClientSecret: "wake-client-secret"},
	}}}
	if diff := cmp.Diff(wantCompared, differ.calls, cmp.AllowUnexported(stubDiffCall{})); diff != "" {
		t.Errorf("values compared with the stored ones (-want +got):\n%s", diff)
	}
	update := plan.Updates[0]
	if update.ID != linkTestAgentID || update.Update.TokenSHA256 != linkTestDigest || update.Update.EntrypointID != linkTestEntrypointID || update.Update.Wake == nil || update.Update.Wake.ClientSecret != "wake-client-secret" {
		t.Errorf("the update does not replace the link agent with the file's digest, entrypoint and wake secret")
	}
	if update.Actual == nil || update.Actual.DeploymentStatus != "deployed" {
		t.Errorf("the update lost the stored link agent, so its deployment could not be chosen")
	}
}

func TestPlanLinkAgents_AComparisonTheTokenMayNotMakeNamesTheScope(t *testing.T) {
	desired := []resolve.ResolvedResource{resolvedLinkAgent(nil)}
	actual := []wire.LinkAgentResponse{storedLinkAgent(nil)}
	differ := &stubSecretDiffer{errByID: map[string]error{linkTestAgentID: &HTTPError{StatusCode: http.StatusForbidden}}}

	_, err := planLinkAgents(t.Context(), differ, desired, actual, partnerLinkOnPlatform())

	if err == nil {
		t.Fatal("planLinkAgents succeeded without comparing the digest")
	}
	for _, want := range []string{`linkAgent "Claims Desk"`, "tokenSha256", "agent_builder:*:update", "plan with the token apply uses", "omit the field to keep the stored value"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
}

func TestPlanLinkAgents_AFileWithoutADigestKeepsTheStoredToken(t *testing.T) {
	tests := []struct {
		name         string
		spec         map[string]any
		stored       func(*wire.LinkAgentResponse)
		wantCompared []stubDiffCall
		wantUpdate   bool
	}{
		{name: "nothing else changed"},
		{
			name:         "a wake secret is still compared",
			spec:         map[string]any{"wake": declaredWake(nil)},
			stored:       func(a *wire.LinkAgentResponse) { a.Wake = storedWake() },
			wantCompared: []stubDiffCall{{kind: "linkAgents", id: linkTestAgentID, values: apiv2.LinkAgentSecretsDiffForm{Wake: &apiv2.LinkAgentWakeSecretsDiffForm{ClientSecret: "wake-client-secret"}}}},
		},
		{name: "another setting changed", spec: map[string]any{"owner": "bob@example.com"}, wantUpdate: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := map[string]any{"tokenSha256": nil}
			maps.Copy(spec, tt.spec)
			differ := &stubSecretDiffer{matchByID: map[string]bool{linkTestAgentID: false}}
			if tt.wantCompared != nil {
				differ.matchByID[linkTestAgentID] = true
			}

			plan, err := planLinkAgents(t.Context(), differ, []resolve.ResolvedResource{resolvedLinkAgent(spec)}, []wire.LinkAgentResponse{storedLinkAgent(tt.stored)}, partnerLinkOnPlatform())

			if err != nil {
				t.Fatalf("planLinkAgents: %v", err)
			}
			if diff := cmp.Diff(tt.wantCompared, differ.calls, cmp.AllowUnexported(stubDiffCall{})); diff != "" {
				t.Errorf("values compared with the stored ones (-want +got):\n%s", diff)
			}
			if gotUpdate := len(plan.Updates) == 1; gotUpdate != tt.wantUpdate || len(plan.Updates)+len(plan.Unchanged) != 1 {
				t.Fatalf("plan = %d updates, %d unchanged; want update=%t", len(plan.Updates), len(plan.Unchanged), tt.wantUpdate)
			}
			if tt.wantUpdate && plan.Updates[0].Update.TokenSHA256 != "" {
				t.Errorf("the replace sends a token digest the file left out, want it empty so the stored token is kept")
			}
		})
	}
}

func TestPlanLinkAgents_CreatingALinkAgentNeedsADigest(t *testing.T) {
	desired := []resolve.ResolvedResource{resolvedLinkAgent(map[string]any{"tokenSha256": nil})}

	_, err := planLinkAgents(t.Context(), &stubSecretDiffer{}, desired, nil, partnerLinkOnPlatform())

	if err == nil {
		t.Fatal("planLinkAgents planned a create without a token digest")
	}
	for _, want := range []string{`linkAgent "Claims Desk"`, "spec.tokenSha256 is required to create a link agent", "sam-link-sdk link-token"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not say %q", err, want)
		}
	}
}

func TestPlanLinkAgents_ComparesWakeHeaderValuesThroughTheSecretsCheck(t *testing.T) {
	desired := []resolve.ResolvedResource{resolvedLinkAgent(map[string]any{"wake": declaredWake(map[string]any{"headers": map[string]any{"X-Api-Key": "alice-wake-key"}})})}
	actual := []wire.LinkAgentResponse{storedLinkAgent(storedWakeHeaders("X-Api-Key"))}
	differ := &stubSecretDiffer{errByID: map[string]error{linkTestAgentID: &HTTPError{StatusCode: http.StatusForbidden}}}

	_, err := planLinkAgents(t.Context(), differ, desired, actual, partnerLinkOnPlatform())

	if err == nil || !strings.Contains(err.Error(), "wake.headers") {
		t.Errorf("planLinkAgents error = %v, want one that names wake.headers", err)
	}
	wantCompared := []stubDiffCall{{kind: "linkAgents", id: linkTestAgentID, values: apiv2.LinkAgentSecretsDiffForm{
		TokenSHA256: linkTestDigest,
		Wake:        &apiv2.LinkAgentWakeSecretsDiffForm{ClientSecret: "wake-client-secret", Headers: map[string]string{"X-Api-Key": "alice-wake-key"}},
	}}}
	if diff := cmp.Diff(wantCompared, differ.calls, cmp.AllowUnexported(stubDiffCall{})); diff != "" {
		t.Errorf("values compared with the stored ones (-want +got):\n%s", diff)
	}
}

func TestPlanLinkAgents_AnEntrypointThisApplyCreatesIsNamedUntilItHasAnID(t *testing.T) {
	toCreate := newLinkEntrypoints(nil, &gatewayKindPlan{Creates: []gatewayOp{{Name: "partner-link", Create: wire.CreateEntrypointRequest{Name: "partner-link", EntrypointType: "agent_link"}}}})

	plan, err := planLinkAgents(t.Context(), &stubSecretDiffer{}, []resolve.ResolvedResource{resolvedLinkAgent(nil)}, nil, toCreate)

	if err != nil {
		t.Fatalf("planLinkAgents: %v", err)
	}
	if len(plan.Creates) != 1 || plan.Creates[0].Create.EntrypointID != "partner-link" {
		t.Fatalf("planned creates = %+v, want one that names the entrypoint to create", plan.Creates)
	}

	tests := []struct {
		name      string
		created   createdEntrypointIDs
		wantID    string
		wantError string
	}{
		{name: "the entrypoint was created", created: createdEntrypointIDs{byName: map[string]string{"partner-link": linkTestEntrypointID}}, wantID: linkTestEntrypointID},
		{name: "the entrypoint was not created", created: createdEntrypointIDs{byName: map[string]string{}}, wantError: `linkAgent "Claims Desk" references entrypoint "partner-link"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			op := plan.Creates[0]

			err := tt.created.resolve(&op)

			if tt.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantError) {
					t.Fatalf("resolve error = %v, want one saying %q", err, tt.wantError)
				}
				return
			}
			if err != nil || op.Create.EntrypointID != tt.wantID {
				t.Errorf("resolved entrypoint = %q, %v; want %q", op.Create.EntrypointID, err, tt.wantID)
			}
		})
	}
}

func TestLinkAgentOp_IsDeployedAgainWhenItIsNotRunningWhatIsStored(t *testing.T) {
	tests := []struct {
		name             string
		deploymentStatus string
		syncStatus       *string
		want             bool
	}{
		{name: "deployed and in sync", deploymentStatus: "deployed", syncStatus: sp("in_sync"), want: false},
		{name: "deployed and out of sync", deploymentStatus: "deployed", syncStatus: sp("out_of_sync"), want: true},
		{name: "not deployed", deploymentStatus: "not_deployed", want: true},
		{name: "a first deployment that failed", deploymentStatus: "deploy_failed", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stored := storedLinkAgent(func(a *wire.LinkAgentResponse) { a.DeploymentStatus, a.SyncStatus = tt.deploymentStatus, tt.syncStatus })

			if got := (linkAgentOp{Name: "Claims Desk", Actual: &stored}).outOfSync(); got != tt.want {
				t.Errorf("outOfSync() = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestLinkAgentPeerErrors_FailOnlyOnAnAgentsDisplayName(t *testing.T) {
	const orderBotInstance = "agent_0190f5ec_0000_7000_8000_00000000a002"
	tests := []struct {
		name      string
		peers     []string
		wantError []string
	}{
		{name: "an instanceName, a runtime agent's name and an unknown name", peers: []string{orderBotInstance, "policy-desk"}},
		{name: "a Platform agent's display name", peers: []string{"OrderBot"}, wantError: []string{`linkAgent "Claims Desk"`, `peer "OrderBot"`, `write "` + orderBotInstance + `" in peers`}},
		{name: "the display name of an agent this apply creates", peers: []string{"Intake Bot"}, wantError: []string{`linkAgent "Claims Desk"`, `peer "Intake Bot"`, "apply without this peer", "instanceName"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan := &linkAgentKindPlan{Unchanged: []linkAgentOp{{Name: "Claims Desk", Source: "linkAgents/claims-desk.yaml", Create: wire.CreateLinkAgentRequest{Peers: tt.peers}}}}
			meshNames := map[string]struct{}{orderBotInstance: {}}
			instanceNameOf := map[string]string{"OrderBot": orderBotInstance, "Intake Bot": ""}

			err := linkAgentPeerErrors(plan, meshNames, instanceNameOf)

			if len(tt.wantError) == 0 {
				if err != nil {
					t.Errorf("linkAgentPeerErrors = %v, want none", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("linkAgentPeerErrors passed peers %q", tt.peers)
			}
			for _, want := range tt.wantError {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not say %q", err, want)
				}
			}
		})
	}
}

func TestAnyFailures_DetectsLinkAgentFailures(t *testing.T) {
	for _, tc := range []struct {
		name string
		r    *ApplyResult
		want bool
	}{
		{name: "a link agent failure", r: &ApplyResult{LinkAgents: &linkAgentKindOutcome{Failures: []linkAgentOpResult{{}}}}, want: true},
		{name: "link agents without failures", r: &ApplyResult{LinkAgents: &linkAgentKindOutcome{Created: []linkAgentOpResult{{}}}}, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := anyFailures(tc.r); got != tc.want {
				t.Errorf("anyFailures = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCheckLinkTokenDigest_RefusesTheDigestOfAnEmptyToken(t *testing.T) {
	err := checkLinkTokenDigest(entplatform.DigestOfAnEmptyToken)

	if err == nil || !strings.Contains(err.Error(), "digest of an empty token") {
		t.Errorf("checkLinkTokenDigest(digest of an empty token) = %v, want a refusal that names the empty token", err)
	}
	if err := checkLinkTokenDigest(linkTestDigest); err != nil {
		t.Errorf("checkLinkTokenDigest(a real digest) = %v, want nil", err)
	}
}
