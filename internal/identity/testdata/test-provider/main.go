// Test profile-provider tool used by the identity package's integration test.
// Returns canned profiles supplied inline via tool_config so the test has zero
// filesystem coordination — the real STR worker subprocesses this binary, the
// real STREnricher reads tool_config from the test DB, and the test asserts
// on the Profile that flows back through both.
package main

import (
	"context"
	"encoding/json"
	"strings"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

// enrichParams mirrors the real bamboohr_profile_provider / dev_profile_provider wire shape.
type enrichParams struct {
	LookupValue string `json:"lookup_value" desc:"Resolved JWT claim value to look up."`
}

func main() {
	sdk.Run(
		sdk.NewTool("enrich_authenticated_user",
			"Returns a canned profile for the lookup value from inline tool_config.users.",
			enrich,
			sdk.WithConfigSchema(
				sdk.ConfigSchemaField{Key: "users", Type: "string", Description: "JSON-encoded array of {id, displayName, jobTitle, department} objects."},
				sdk.ConfigSchemaField{Key: "force_error", Type: "string", Description: "When non-empty, the tool returns an error Result with this message — exercises the failure-propagation path."},
			),
		),
	)
}

// userRow is the inline fixture shape callers pass through tool_config.users
// (a JSON string). Kept separate from the platform's canonical Profile so the
// test fixture can evolve without dragging the wire shape with it — toProfile
// converts.
type userRow struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	JobTitle    string `json:"jobTitle"`
	Department  string `json:"department"`
}

func enrich(_ context.Context, p enrichParams, tc *sdk.ToolContext) (*sdk.Result, error) {
	if msg := strings.TrimSpace(tc.GetConfigString("force_error", "")); msg != "" {
		return sdk.Error(msg), nil
	}
	raw := tc.GetConfigString("users", "")
	if raw == "" {
		return sdk.Error("test-provider: users config missing"), nil
	}
	var rows []userRow
	if err := json.Unmarshal([]byte(raw), &rows); err != nil {
		return sdk.Error("test-provider: users decode: " + err.Error()), nil
	}
	want := strings.ToLower(strings.TrimSpace(p.LookupValue))
	for _, r := range rows {
		if strings.EqualFold(r.ID, want) {
			return sdk.OK("found", sdk.WithData(map[string]any{
				"profile": map[string]any{
					"id":          r.ID,
					"displayName": r.DisplayName,
					"workEmail":   r.ID,
					"jobTitle":    r.JobTitle,
					"department":  r.Department,
				},
			})), nil
		}
	}
	return sdk.OK("not found", sdk.WithData(map[string]any{"profile": nil})), nil
}
