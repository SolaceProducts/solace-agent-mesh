// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package oidcauth

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/identity"
)

const (
	scheduledOwnerEmail   = "alice@example.com"
	ownerNotActiveMessage = "owner does not resolve to an active user account"
	ownerDeactivatedText  = "owner's account is deactivated"
)

var scheduledOwnerScopes = []string{"agent:test-agent:invoke"}

func TestAScheduledRunHoldsTheOwnerToTheirOwnAccess(t *testing.T) {
	for _, tc := range []struct {
		name        string
		reply       auth.AuthorizationResolution
		wantMessage string
	}{
		{"matched user", auth.AuthorizationResolution{Resolution: identity.LookupMatched, ResolvedUserID: matchedUserID, RolesSource: identity.RolesSourceAssigned}, ""},
		{"matched user on the default roles", auth.AuthorizationResolution{Resolution: identity.LookupMatched, ResolvedUserID: matchedUserID, RolesSource: identity.RolesSourceDefaults}, ""},
		{"no user row but a YAML assignment", auth.AuthorizationResolution{Resolution: identity.LookupNoMatch, RolesSource: identity.RolesSourceAssigned}, ""},
		{"a responder that says nothing about the user", auth.AuthorizationResolution{}, ""},
		{"deactivated account", auth.AuthorizationResolution{Resolution: identity.LookupRefused, RefusalReason: "inactive_user"}, ownerDeactivatedText},
		{"refused for another reason", auth.AuthorizationResolution{Resolution: identity.LookupRefused, RefusalReason: "no_claim_key"}, ownerNotActiveMessage},
		{"refused with no reason", auth.AuthorizationResolution{Resolution: identity.LookupRefused}, ownerNotActiveMessage},
		{"ambiguous", auth.AuthorizationResolution{Resolution: identity.LookupAmbiguous}, ownerNotActiveMessage},
		{"not configured", auth.AuthorizationResolution{Resolution: identity.LookupNotConfigured}, ownerNotActiveMessage},
		{"no user row on the default roles", auth.AuthorizationResolution{Resolution: identity.LookupNoMatch, RolesSource: identity.RolesSourceDefaults}, ownerNotActiveMessage},
		{"no user row from a platform that does not say where the roles came from", auth.AuthorizationResolution{Resolution: identity.LookupNoMatch}, ownerNotActiveMessage},
		{"no user row with a roles source this build does not know", auth.AuthorizationResolution{Resolution: identity.LookupNoMatch, RolesSource: "inherited"}, ownerNotActiveMessage},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reply := tc.reply
			reply.Roles, reply.Scopes = []string{"analyst"}, scheduledOwnerScopes
			authz, err := NewAuthorizationService(AuthorizationServiceConfig{Type: "default_rbac"}, "gw-1", testLogger())
			if err != nil {
				t.Fatalf("NewAuthorizationService: %v", err)
			}
			authz.SetAuthorizationClient(&stubAuthzClient{resolution: reply})
			core, brk, requests := submittingCore(t)
			core.SetIdentityResolver(NewIdentityResolver(authz, nil))
			signer := &signedIdentities{}
			core.SetTaskSigner(signer)
			repo := store.NewScheduledTaskRepo(core.Store())
			task := &store.ScheduledTask{
				ID: "0192f0c4-6a4e-7b1e-9d3c-5f2a8b7e1c51", Name: "nightly", Namespace: "test-ns", CreatedBy: scheduledOwnerEmail,
				ScheduleType: "cron", ScheduleExpression: "0 * * * *", TargetAgentName: "test-agent",
				TaskMessage: `[{"type":"text","text":"run"}]`, Enabled: true,
			}
			if err = repo.Create(t.Context(), task); err != nil {
				t.Fatalf("create task: %v", err)
			}

			if _, err = core.ScheduledTasks().TriggerTaskNow(t.Context(), task.ID, scheduledOwnerEmail); err != nil {
				t.Fatalf("TriggerTaskNow: %v", err)
			}
			if err = core.ScheduledTasks().RunPasses(t.Context()); err != nil {
				t.Fatalf("RunPasses: %v", err)
			}

			waitForAgent := 200 * time.Millisecond
			if tc.wantMessage == "" {
				waitForAgent = 5 * time.Second
			}
			ctx, cancel := context.WithTimeout(t.Context(), waitForAgent)
			defer cancel()
			_, receiveErr := brk.Receive(ctx, requests)
			runs, err := repo.FindExecutionsByTask(t.Context(), task.ID, 10, 0, nil, nil)
			if err != nil || len(runs) != 1 {
				t.Fatalf("runs = %d, %v, want 1", len(runs), err)
			}
			got, err := repo.GetByID(t.Context(), task.ID)
			if err != nil || got == nil {
				t.Fatalf("reload task: %v, %v", got, err)
			}

			if tc.wantMessage == "" {
				if receiveErr != nil {
					t.Fatalf("no task reached the agent: %v", receiveErr)
				}
				if signed := signer.first(); signed == nil || !slices.Equal(signed.Scopes(), scheduledOwnerScopes) {
					t.Errorf("signed identity = %+v, want the owner's scopes %v", signed, scheduledOwnerScopes)
				}
				return
			}
			if receiveErr == nil {
				t.Error("a task reached the agent for an owner the platform does not resolve to an active user")
			}
			if signed := signer.first(); signed != nil {
				t.Errorf("signed %+v for an owner the platform does not resolve to an active user", signed)
			}
			message := ""
			if runs[0].ErrorMessage != nil {
				message = *runs[0].ErrorMessage
			}
			if runs[0].Status != "failed" || message != tc.wantMessage {
				t.Errorf("run = {status %q, message %q}, want failed with %q", runs[0].Status, message, tc.wantMessage)
			}
			if got.ConsecutiveFailureCount != 1 {
				t.Errorf("consecutive_failure_count = %d, want 1: a refused owner counts toward auto-pause", got.ConsecutiveFailureCount)
			}
		})
	}
}
