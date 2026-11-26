// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"reflect"
	"slices"
	"testing"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
)

func TestChangedFields_NamesOnlyWhatActuallyChanged(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()

	if _, err := svc.Apply(ctx, ApplyBuildRequest{
		Components: []BuildComponentInput{agentComponent("diffed_agent")},
	}, testActor); err != nil {
		t.Fatalf("seed apply: %v", err)
	}

	resp, err := svc.Apply(ctx, ApplyBuildRequest{
		Components: []BuildComponentInput{editedAgent("diffed_agent")},
	}, testActor)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	op := opFor(t, resp, kindAgent, "diffed_agent")
	if op.Action != actionUpdate {
		t.Fatalf("Action = %q, want update", op.Action)
	}
	if !slices.Equal(op.ChangedFields, []string{"systemPrompt"}) {
		t.Errorf("changedFields = %v, want exactly [systemPrompt].\n"+
			"Only the instruction was edited. Fields named here that did not change are phantoms — "+
			"they come from comparing a Create DTO against a Response DTO rather than the projection "+
			"the plan hashes.", op.ChangedFields)
	}
}

func TestChangedFields_NamesTheRotationThatPromotedTheUpdate(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()

	if _, err := svc.Apply(ctx, ApplyBuildRequest{
		Components: []BuildComponentInput{sqlConnector("db_conn", "postgres", fullSQLValues())},
	}, testActor); err != nil {
		t.Fatalf("seed apply: %v", err)
	}

	rotated := fullSQLValues()
	rotated["password"] = "a-freshly-rotated-password"
	resp, err := svc.Apply(ctx, ApplyBuildRequest{
		DryRun:     true,
		Components: []BuildComponentInput{sqlConnector("db_conn", "postgres", rotated)},
	}, testActor)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	op := opFor(t, resp, kindConnector, "db_conn")
	if op.Action != actionUpdate {
		t.Fatalf("Action = %q, want update", op.Action)
	}
	if !slices.Contains(op.ChangedFields, "values") {
		t.Errorf("changedFields = %v, want it to name values — otherwise the preview promises a "+
			"write and declines to say what it will change", op.ChangedFields)
	}
}

func TestA2AHashView_CoversEveryPlannedField(t *testing.T) {
	t.Parallel()
	notInView := map[string]string{
		"agentAuthConfig":     "returned redacted, so it can never match; rotations are caught outside the hash",
		"agentCardAuthConfig": "returned redacted, so it can never match; rotations are caught outside the hash",
	}

	view := map[string]struct{}{}
	viewType := reflect.TypeOf(a2aHashView{})
	for i := range viewType.NumField() {
		if tag := jsonFieldName(viewType.Field(i).Tag.Get("json")); tag != "" && tag != "-" {
			view[tag] = struct{}{}
		}
	}

	createType := reflect.TypeOf(entplatform.CreateRemoteAgentRequest{})
	for i := range createType.NumField() {
		f := createType.Field(i)
		tag := jsonFieldName(f.Tag.Get("json"))
		if tag == "" || tag == "-" {
			continue
		}
		if _, declared := notInView[tag]; declared {
			if _, inView := view[tag]; inView {
				t.Errorf("a2aHashView carries %q, which is listed as deliberately excluded", tag)
			}
			continue
		}
		if _, inView := view[tag]; !inView {
			t.Errorf("CreateRemoteAgentRequest.%s is not in a2aHashView.\n"+
				"Add it, or add it to notInView with a reason. A field missing from the view is "+
				"invisible to the plan, so an edit to it reports unchanged and is silently dropped.", tag)
		}
	}
}
