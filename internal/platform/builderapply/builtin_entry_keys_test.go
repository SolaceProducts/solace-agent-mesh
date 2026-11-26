// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"slices"
	"strings"
	"testing"
)

const builtinWebRequestToolset = "builtin_web_request_tools"

func builtinEntryComponent(t *testing.T, name, entry string) BuildComponentInput {
	t.Helper()
	c := agentComponent(name)
	edited := strings.Replace(c.Content,
		"        - tool_type: builtin-group\n          group_name: artifact_management\n", entry, 1)
	if edited == c.Content {
		t.Fatal("fixture has no group entry to replace")
	}
	c.Content = edited
	return c
}

func TestApplyBuiltinEntryKeys(t *testing.T) {
	t.Parallel()
	const webRequest = "        - tool_type: builtin\n          tool_name: web_request\n"
	const unstoredSuffix = " has no storage for a built-in tool and was not saved"
	for _, tc := range []struct {
		name, entry string
		wantErr     string
		wantWarns   []string
	}{
		{name: "required_scopes is refused", entry: webRequest + "          required_scopes: [tool:web:invoke]\n",
			wantErr: "tools[0] (web_request): required_scopes is not saved for a built-in tool, and dropping it would change who may run the tool"},
		{name: "auth is refused", entry: webRequest + "          auth: {type: bearer}\n",
			wantErr: "tools[0] (web_request): auth is not saved for a built-in tool"},
		{name: "required_scopes on a group is refused",
			entry:   "        - tool_type: builtin-group\n          group_name: artifact_management\n          required_scopes: [tool:artifact:invoke]\n",
			wantErr: "tools[0] (artifact_management): required_scopes is not saved for a built-in tool"},
		{name: "timeout_seconds is warned", entry: webRequest + "          timeout_seconds: 60\n",
			wantWarns: []string{"tools[0] (web_request): timeout_seconds" + unstoredSuffix}},
		{name: "effect is refused", entry: webRequest + "          effect: side_effecting\n",
			wantErr: "tools[0] (web_request): effect is not saved for a built-in tool, and dropping it would drop the operator's effect override and leave the tool on its own classification"},
		{name: "effect on a group is refused",
			entry:   "        - tool_type: builtin-group\n          group_name: artifact_management\n          effect: side_effecting\n",
			wantErr: "tools[0] (artifact_management): effect is not saved for a built-in tool"},
		{name: "sandbox_profile is warned", entry: webRequest + "          sandbox_profile: strict\n",
			wantWarns: []string{"tools[0] (web_request): sandbox_profile" + unstoredSuffix}},
		{name: "parameters is warned", entry: webRequest + "          parameters: {a: 1}\n",
			wantWarns: []string{"tools[0] (web_request): parameters" + unstoredSuffix}},
		{name: "tool_description is warned", entry: webRequest + "          tool_description: fetch pages\n",
			wantWarns: []string{"tools[0] (web_request): tool_description" + unstoredSuffix}},
		{name: "an unknown key is warned", entry: webRequest + "          colour: blue\n",
			wantWarns: []string{"tools[0] (web_request): colour was not recognised and was not saved"}},
		{name: "tool_config is read", entry: webRequest + "          tool_config: {timeout: 30}\n"},
		{name: "hil is read", entry: webRequest + "          hil: {require_approval: true}\n"},
		{name: "no extra key is silent", entry: webRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc := newTestService(t)
			build := []BuildComponentInput{builtinEntryComponent(t, "gated", tc.entry)}
			dry, dryErr := svc.Apply(t.Context(), ApplyBuildRequest{DryRun: true, Components: build}, testActor)
			resp, err := svc.Apply(t.Context(), ApplyBuildRequest{Components: build}, testActor)
			if tc.wantErr != "" {
				for which, e := range map[string]error{"dry run": dryErr, "apply": err} {
					if e == nil || !strings.Contains(e.Error(), tc.wantErr) {
						t.Errorf("%s error = %v, want %q", which, e, tc.wantErr)
					}
				}
				return
			}
			if dryErr != nil || err != nil {
				t.Fatalf("dry=%v apply=%v", dryErr, err)
			}
			op := opFor(t, resp, kindAgent, "gated")
			if !slices.Equal(opFor(t, dry, kindAgent, "gated").Warnings, op.Warnings) {
				t.Errorf("dry-run and apply warnings differ")
			}
			if !slices.Equal(op.Warnings, tc.wantWarns) {
				t.Errorf("warnings = %q, want %q", op.Warnings, tc.wantWarns)
			}
			if got := agentNamed(t.Context(), t, svc, "gated").Toolsets; !slices.Contains(got, builtinWebRequestToolset) {
				t.Errorf("stored toolsets = %v, want %s", got, builtinWebRequestToolset)
			}
		})
	}
}
