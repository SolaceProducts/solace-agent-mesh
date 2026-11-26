// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/identity"
	"github.com/SolaceProducts/solace-agent-mesh/internal/strutil"
)

const (
	ownerDeactivatedText = "owner's account is deactivated"
	ownerUnresolvedText  = "owner does not resolve to an active user account"
)

func TestScheduledTask_ARefusedOwnerIsLoggedWithWhyAndWithoutTheirId(t *testing.T) {
	cases := []struct {
		name       string
		ent        auth.Entitlement
		wantText   string
		wantFields map[string]string
	}{
		{
			name:       "a deactivated account",
			ent:        auth.Entitlement{Scopes: []string{"agent:test-agent:invoke"}, Resolution: identity.LookupRefused, RefusalReason: "inactive_user", RolesSource: identity.RolesSourceDefaults},
			wantText:   ownerDeactivatedText,
			wantFields: map[string]string{"resolution": "refused", "refusalReason": "inactive_user", "rolesSource": "defaults"},
		},
		{
			name:       "no account on the default roles",
			ent:        auth.Entitlement{Scopes: []string{"agent:test-agent:invoke"}, Resolution: identity.LookupNoMatch, RolesSource: identity.RolesSourceDefaults},
			wantText:   ownerUnresolvedText,
			wantFields: map[string]string{"resolution": "no_match", "refusalReason": "", "rolesSource": "defaults"},
		},
		{
			name:       "an ambiguous email",
			ent:        auth.Entitlement{Scopes: []string{"agent:test-agent:invoke"}, Resolution: identity.LookupAmbiguous, RolesSource: identity.RolesSourceDefaults},
			wantText:   ownerUnresolvedText,
			wantFields: map[string]string{"resolution": "ambiguous", "refusalReason": "", "rolesSource": "defaults"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newRunAsFixture(t, false, &directoryResolver{dir: entitlementDirectory{runAsCreator: c.ent}})
			logs := newLogCapture()
			f.svc.logger = logs.logger
			task := f.dueRunAsTask(t, "", nil)

			mustRunPasses(t, f.svc, time.Now())

			if run := f.onlyRun(t, task.ID); run.Status != "failed" || *run.ErrorMessage != c.wantText {
				t.Fatalf("run = {status %q, error %v}, want failed with %q", run.Status, run.ErrorMessage, c.wantText)
			}
			var refusal map[string]any
			for _, line := range logs.lines() {
				if msg, _ := line["msg"].(string); strings.Contains(msg, "owner no longer authorized") {
					refusal = line
				}
			}
			if refusal == nil {
				t.Fatalf("no refusal line was logged: %s", logs.text())
			}
			if level, _ := refusal["level"].(string); level != "WARN" {
				t.Errorf("refusal logged at %q, want WARN", level)
			}
			for field, want := range c.wantFields {
				if got, _ := refusal[field].(string); got != want {
					t.Errorf("%s = %q, want %q", field, got, want)
				}
			}
			if got, _ := refusal["createdByFP"].(string); got != strutil.PIIFingerprint(runAsCreator) {
				t.Errorf("createdByFP = %q, want %q", got, strutil.PIIFingerprint(runAsCreator))
			}
			if strings.Contains(logs.text(), runAsCreator) {
				t.Errorf("a log line carries the owner's raw id %q: %s", runAsCreator, logs.text())
			}
		})
	}
}

func TestScheduledRunAs_ACreatorWhoIsNotAnActiveUserIsToldSoAndNotThatTheyLostTheGrant(t *testing.T) {
	cases := []struct {
		name     string
		ent      auth.Entitlement
		wantText string
	}{
		{
			name:     "a deactivated creator",
			ent:      auth.Entitlement{Scopes: []string{"*:*:*"}, Resolution: identity.LookupRefused, RefusalReason: "inactive_user"},
			wantText: ownerDeactivatedText,
		},
		{
			name:     "a creator with no account on the default roles",
			ent:      auth.Entitlement{Scopes: []string{"*:*:*"}, Resolution: identity.LookupNoMatch, RolesSource: identity.RolesSourceDefaults},
			wantText: ownerUnresolvedText,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := superAdminDirectory()
			dir[runAsCreator] = c.ent
			resolver := &directoryResolver{dir: dir}
			f := newRunAsFixture(t, true, resolver)
			task := f.dueRunAsTask(t, runAsPrincipal, nil)

			mustRunPasses(t, f.svc, time.Now())

			run := f.onlyRun(t, task.ID)
			if run.Status != "failed" || *run.ErrorMessage != c.wantText {
				t.Fatalf("run = {status %q, error %v}, want failed with %q", run.Status, run.ErrorMessage, c.wantText)
			}
			if asked := resolver.askedRunAs(); len(asked) != 0 {
				t.Errorf("resolved %v after the creator was refused", asked)
			}
			if stored := reloadTask(t, f.svc, task.ID); stored.ConsecutiveFailureCount != 1 {
				t.Errorf("consecutive failures = %d, want the refusal counted", stored.ConsecutiveFailureCount)
			}
		})
	}
}
