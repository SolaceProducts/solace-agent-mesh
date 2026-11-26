// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func renderPlanString(t *testing.T, plan *PlanResult) string {
	t.Helper()
	var buf bytes.Buffer
	RenderPlan(&buf, plan, FormatOptions{NoColor: true})
	return buf.String()
}

func renderApplyString(t *testing.T, res *ApplyResult) string {
	t.Helper()
	var buf bytes.Buffer
	RenderApply(&buf, res, FormatOptions{NoColor: true})
	return buf.String()
}

func TestRenderPlan_DefaultRolesPutIsPreviewed(t *testing.T) {
	out := renderPlanString(t, &PlanResult{
		DefaultRoles: &defaultRolesPlan{
			Verb:         defaultRolesVerbPut,
			RoleNames:    []string{"analyst", "viewer"},
			CurrentNames: []string{"analyst"},
		},
	})

	for _, want := range []string{"platform/", "defaultRoles", "put", "analyst -> analyst, viewer"} {
		if !strings.Contains(out, want) {
			t.Errorf("plan output missing %q\n--- output ---\n%s", want, out)
		}
	}
	if !strings.Contains(out, "1 platform: 0 create, 1 update, 0 delete, 0 unchanged") {
		t.Errorf("plan totals wrong\n--- output ---\n%s", out)
	}
}

func TestRenderPlan_DefaultRolesClearShowsEmptySet(t *testing.T) {
	out := renderPlanString(t, &PlanResult{
		DefaultRoles: &defaultRolesPlan{
			Verb:         defaultRolesVerbPut,
			RoleNames:    nil,
			CurrentNames: []string{"analyst"},
		},
	})
	if !strings.Contains(out, "analyst -> (none)") {
		t.Errorf("clearing the default-role set must render as an explicit empty set\n--- output ---\n%s", out)
	}
}

func TestRenderPlan_UnmanagedPlatformBlockIsSilent(t *testing.T) {
	out := renderPlanString(t, &PlanResult{})
	if strings.Contains(out, "platform/") {
		t.Errorf("an unmanaged platform block must not render a section\n--- output ---\n%s", out)
	}
}

func TestRenderPlan_PlatformUnchangedIsReported(t *testing.T) {
	out := renderPlanString(t, &PlanResult{
		DefaultRoles: &defaultRolesPlan{Verb: defaultRolesVerbUnchanged, RoleNames: []string{"analyst"}},
	})
	if !strings.Contains(out, "unchanged") || !strings.Contains(out, "defaultRoles") {
		t.Errorf("a managed-but-matching set must still be reported\n--- output ---\n%s", out)
	}
	if !strings.Contains(out, "1 platform: 0 create, 0 update, 0 delete, 1 unchanged") {
		t.Errorf("plan totals wrong\n--- output ---\n%s", out)
	}
}

func TestRenderPlan_ProfileProviderIsPreviewed(t *testing.T) {
	out := renderPlanString(t, &PlanResult{
		ProfileProvider: &profileProviderPlan{
			Verb:        profileProviderVerbPut,
			ToolsetName: "hr-lookup",
			LookupKey:   "email",
		},
	})
	for _, want := range []string{"platform/", "profileProvider", "toolset: hr-lookup", "claim: email"} {
		if !strings.Contains(out, want) {
			t.Errorf("plan output missing %q\n--- output ---\n%s", want, out)
		}
	}
}

func TestRenderPlan_BothPlatformSingletonsShareOneSection(t *testing.T) {
	out := renderPlanString(t, &PlanResult{
		DefaultRoles:    &defaultRolesPlan{Verb: defaultRolesVerbPut, RoleNames: []string{"analyst"}},
		ProfileProvider: &profileProviderPlan{Verb: profileProviderVerbDelete},
	})
	if got := strings.Count(out, "platform/"); got != 1 {
		t.Errorf("expected one platform section, got %d\n--- output ---\n%s", got, out)
	}
	if !strings.Contains(out, "2 platform: 0 create, 1 update, 1 delete, 0 unchanged") {
		t.Errorf("plan totals wrong\n--- output ---\n%s", out)
	}
}

func TestRenderApply_DefaultRolesWriteIsConfirmed(t *testing.T) {
	out := renderApplyString(t, &ApplyResult{
		DefaultRoles: &defaultRolesOutcome{Applied: &defaultRolesOpResult{Verb: defaultRolesVerbPut}},
	})
	for _, want := range []string{"Applied platform:", "defaultRoles", "put applied"} {
		if !strings.Contains(out, want) {
			t.Errorf("apply output missing %q\n--- output ---\n%s", want, out)
		}
	}
}

func TestRenderApply_DefaultRolesFailureNamesTheReason(t *testing.T) {
	out := renderApplyString(t, &ApplyResult{
		DefaultRoles: &defaultRolesOutcome{
			Failures: []defaultRolesOpResult{{
				Verb: defaultRolesVerbPut,
				Err:  errors.New(`role "sam_user" is not a known DB role`),
			}},
		},
	})
	for _, want := range []string{"defaultRoles", "put failed", "not a known DB role"} {
		if !strings.Contains(out, want) {
			t.Errorf("apply output missing %q\n--- output ---\n%s", want, out)
		}
	}
}

func TestRenderApply_DefaultRolesSkipIsDistinguishable(t *testing.T) {
	out := renderApplyString(t, &ApplyResult{
		DefaultRoles: &defaultRolesOutcome{
			Skipped: &defaultRolesOpResult{Verb: defaultRolesVerbPut, Reason: skipReasonMiniIDP},
		},
	})
	if !strings.Contains(out, skipMiniIDPLabel) {
		t.Errorf("a mini-IDP skip must say so\n--- output ---\n%s", out)
	}

	quiet := renderApplyString(t, &ApplyResult{})
	if strings.Contains(quiet, "Applied platform:") {
		t.Errorf("no platform work must render no platform section\n--- output ---\n%s", quiet)
	}
}
