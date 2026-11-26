// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package analytics

import "testing"

func TestAuditTarget(t *testing.T) {
	tests := []struct {
		name          string
		owner, caller string
		wantTarget    string
		wantCross     bool
	}{
		{"own data", "alice", "alice", "alice", false},
		{"admin drills into another user", "bob", "admin", "bob", true},
		{"tenant-wide (no owner filter) is cross-user", "", "admin", AuditTargetAll, true},
		{"tenant-wide with no caller identity", "", "", AuditTargetAll, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target, cross := AuditTarget(tt.owner, tt.caller)
			if target != tt.wantTarget || cross != tt.wantCross {
				t.Errorf("AuditTarget(%q, %q) = (%q, %v), want (%q, %v)",
					tt.owner, tt.caller, target, cross, tt.wantTarget, tt.wantCross)
			}
		})
	}
}

func TestAuditTargetMatchesResolvedScope(t *testing.T) {
	owner, err := ResolveOwnerScope("admin", "", true)
	if err != nil {
		t.Fatalf("ResolveOwnerScope: %v", err)
	}
	target, cross := AuditTarget(owner, "admin")
	if !cross {
		t.Errorf("a tenant-wide resolved scope (owner=%q) must audit as crossUser=true", owner)
	}
	if target != AuditTargetAll {
		t.Errorf("tenant-wide target = %q, want %q", target, AuditTargetAll)
	}
}
