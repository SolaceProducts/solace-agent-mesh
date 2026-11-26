// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func TestExecutionServiceTaskCarriesRunPrincipal(t *testing.T) {
	const qaBotID = "0192f0c4-6a4e-7b1e-9d3c-5f2a8b7e1c45"
	cases := []struct {
		name          string
		runAs         *string
		wantPrincipal string
		wantUserUUID  string
	}{
		{name: "explicit run_as", runAs: strPtr("system:qa-bot"), wantPrincipal: "system:qa-bot", wantUserUUID: qaBotID},
		{name: "explicit run_as no system user holds", runAs: strPtr("system:ghost"), wantPrincipal: "system:ghost"},
		{name: "default eval system user", runAs: nil, wantPrincipal: rbac.EvalSystemUserSub, wantUserUUID: rbac.EvalSystemUserID},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			signer := &stubTaskSigner{token: "signed.jwt"}
			propsCh := make(chan map[string]string, 16)
			f := startEvalRun(t, evalRunOpts{
				prompts:     []string{"who are you running as?"},
				runAs:       tc.runAs,
				signer:      signer,
				systemUsers: map[string]string{"system:qa-bot": qaBotID},
				startAgent: func(br broker.Broker, namespace string) {
					agent := newObservedFakeAgent(t, br, namespace, "Orchestrator",
						map[string]string{"who are you running as?": `{"ok":true}`},
						func(msg *broker.Message) {
							select {
							case propsCh <- msg.UserProperties:
							default:
							}
						})
					t.Cleanup(agent.Stop)
				},
			})

			var props map[string]string
			select {
			case props = <-propsCh:
			case <-time.After(8 * time.Second):
				t.Fatal("no task reached the agent within 8s")
			}
			if got := props[a2a.PropUserID]; got != tc.wantPrincipal {
				t.Errorf("userId = %q, want %q", got, tc.wantPrincipal)
			}
			if got := props[a2a.PropTrigger]; got != a2a.TriggerEval {
				t.Errorf("trigger = %q, want %q", got, a2a.TriggerEval)
			}
			if got := props[a2a.PropChannel]; got != a2a.ChannelEval {
				t.Errorf("channel = %q, want %q", got, a2a.ChannelEval)
			}
			if got := props[a2a.PropUserUUID]; got != tc.wantUserUUID {
				t.Errorf("userUuid = %q, want %q", got, tc.wantUserUUID)
			}
			if got, ok := props[a2a.PropEntrypointUUID]; ok {
				t.Errorf("entrypointUuid = %q stamped on an eval, which no entrypoint admitted", got)
			}
			var cfg map[string]any
			if err := json.Unmarshal([]byte(props[a2a.PropA2AUserConfig]), &cfg); err != nil {
				t.Fatalf("decode a2aUserConfig: %v (raw=%q)", err, props[a2a.PropA2AUserConfig])
			}
			identity, _ := cfg["_user_identity"].(map[string]any)
			if identity["id"] != tc.wantPrincipal {
				t.Errorf("_user_identity.id = %v, want %q", identity["id"], tc.wantPrincipal)
			}
			seen := signer.principals()
			if len(seen) == 0 || seen[0] != tc.wantPrincipal {
				t.Errorf("JWT signed for %v, want first=%q", seen, tc.wantPrincipal)
			}
			if signed := signer.signedUserUUIDs(); len(signed) == 0 || signed[0] != tc.wantUserUUID {
				t.Errorf("JWT signed platform users %v, want first=%q: the agent books spend to the signed row", signed, tc.wantUserUUID)
			}

			if run := waitForTerminalRun(t, f.runRepo, f.runID); run.Status != StatusCompleted {
				t.Fatalf("final status = %q, want completed", run.Status)
			}
		})
	}
}

func strPtr(s string) *string { return &s }
