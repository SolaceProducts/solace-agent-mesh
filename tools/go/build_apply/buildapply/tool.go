// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package buildapply

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

func Tools() []*sdk.ToolDef {
	return []*sdk.ToolDef{
		sdk.NewTool(
			"mark_changes_applied",
			"Record that an Apply Changes flow has finished. Writes apply_complete.json artifact with the summary + the list of edits made. Call exactly once at the end of an edit cycle, after validation + redeploy succeed.",
			markChangesApplied,
			sdk.WithInstructions(markChangesAppliedInstructions),
			sdk.WithEffect(sdk.EffectSideEffecting),
		),
	}
}

type MarkChangesAppliedParams struct {
	Summary string `json:"summary" desc:"One-paragraph natural-language summary of what was changed."`

	ChangesApplied []ChangeApplied `json:"changesApplied" desc:"Per-component list of edits made during the apply."`
}

type ChangeApplied struct {
	Component string `json:"component" desc:"Component (workflow / agent / connector) that was edited."`
	Area      string `json:"area" desc:"Configuration area changed: system_prompt | tools | workflow_logic | model_choice | other."`
	Change    string `json:"change" desc:"Concrete description of what was edited."`
}

type ApplyComplete struct {
	Summary        string          `json:"summary"`
	ChangesApplied []ChangeApplied `json:"changesApplied"`
}

const ApplyCompleteArtifactFilename = "apply_complete.json"

const markChangesAppliedInstructions = `## mark_changes_applied

Call this exactly once at the very end of an Apply Changes edit cycle —
after every component you modified has been redeployed and is back in a
working state.

Do NOT call it:
- before validation + redeploy of edited components succeeds (the FE
  reads it as "all done" and will show the post-edit completion UI
  prematurely),
- in response to anything other than an Apply Changes flow (it's
  scoped to that specific lifecycle event),
- more than once per apply cycle (the FE reads the latest artifact
  version anyway, but multiple calls clutter the test session's
  artifact history without adding signal).

Make the summary concrete. Reference the components by name and the
areas you changed. The user is reading this immediately after a test
run, so they need to see exactly what got moved.`

func markChangesApplied(_ context.Context, p MarkChangesAppliedParams, tc *sdk.ToolContext) (*sdk.Result, error) {
	record, bytes, err := buildApplyCompleteRecord(p)
	if err != nil {
		return sdk.Error(err.Error()), nil
	}
	if err := tc.SaveArtifact(ApplyCompleteArtifactFilename, bytes, "application/json"); err != nil {
		return sdk.Error(fmt.Sprintf("save apply_complete artifact: %v", err)), nil
	}

	return sdk.OK(
		fmt.Sprintf("Changes applied (%d edit(s) recorded).", len(record.ChangesApplied)),
		sdk.WithData(map[string]any{
			"summary":        record.Summary,
			"changesApplied": record.ChangesApplied,
		}),
	), nil
}

func buildApplyCompleteRecord(p MarkChangesAppliedParams) (ApplyComplete, []byte, error) {
	if strings.TrimSpace(p.Summary) == "" {
		return ApplyComplete{}, nil, fmt.Errorf("summary is required")
	}

	record := ApplyComplete{
		Summary:        strings.TrimSpace(p.Summary),
		ChangesApplied: p.ChangesApplied,
	}
	bytes, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return ApplyComplete{}, nil, fmt.Errorf("marshal apply record: %w", err)
	}
	return record, bytes, nil
}
