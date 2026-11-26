// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/translate"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	evalplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/eval"
)

func TestExampleUpdateBodySetsTheExpectedFacts(t *testing.T) {
	refund := &evalplatform.ExpectedFacts{Required: []string{"refund"}}
	cases := []struct {
		name    string
		actual  string
		desired *evalplatform.ExpectedFacts
		want    string
	}{
		{"added", `{"id":"x1","sequenceNumber":1,"prompt":"p","expectedFacts":null}`, refund,
			`{"prompt":"p","expectedFacts":{"required":["refund"],"forbidden":[]}}`},
		{"changed", `{"id":"x1","sequenceNumber":1,"prompt":"p","expectedFacts":{"forbidden":["refund"]}}`, refund,
			`{"prompt":"p","expectedFacts":{"required":["refund"],"forbidden":[]}}`},
		{"changed to the same length", `{"id":"x1","sequenceNumber":1,"prompt":"p","expectedFacts":{"required":["rufund"]}}`, refund,
			`{"prompt":"p","expectedFacts":{"required":["refund"],"forbidden":[]}}`},
		{"reordered", `{"id":"x1","sequenceNumber":1,"prompt":"p","expectedFacts":{"required":["b","a"]}}`,
			&evalplatform.ExpectedFacts{Required: []string{"a", "b"}},
			`{"prompt":"p","expectedFacts":{"required":["a","b"],"forbidden":[]}}`},
		{"removed", `{"id":"x1","sequenceNumber":1,"prompt":"p","expectedFacts":{"required":["refund"]}}`, nil,
			`{"prompt":"p","expectedFacts":null}`},
		{"never had any", `{"id":"x1","sequenceNumber":1,"prompt":"old","expectedFacts":null}`, nil,
			`{"prompt":"p"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			desired := translate.ExampleSpec{SequenceNumber: 1, Prompt: "p", ExpectedFacts: tc.desired}
			assertExampleUpdateBody(t, tc.actual, desired, tc.want)
		})
	}
}

func TestApplyExampleDiff_CreatesCarryThePathAndFacts(t *testing.T) {
	hooks := &applyMockServer{}
	c := newApplyMockClient(t, "ds-1", nil, hooks)
	path := &evalplatform.ExpectedPath{Nodes: map[string]string{"approve": "ran"}}
	facts := &evalplatform.ExpectedFacts{Required: []string{"O-1"}}
	op := datasetOp{Name: "ds", ID: "ds-1", ExamplesPresent: true, Examples: []translate.ExampleSpec{
		{SequenceNumber: 1, Prompt: "refund O-1", ExpectedPath: path, ExpectedFacts: facts},
	}}

	if out := applyExampleDiff(t.Context(), c, op); out.Err != nil || out.Inserts != 1 {
		t.Fatalf("apply = %+v", out)
	}
	if len(hooks.creates) != 1 || !evalplatform.SameExpectedPath(wire.EvalExpectedPath(hooks.creates[0].ExpectedPath), path) ||
		!evalplatform.SameExpectedFacts(wire.EvalExpectedFacts(hooks.creates[0].ExpectedFacts), facts) {
		t.Errorf("creates = %+v, want the path and facts sent", hooks.creates)
	}
}

func TestPulledExpectedFactsReapplyWithNoChanges(t *testing.T) {
	sent := evalplatform.ExampleResponse{ID: "x1", SequenceNumber: 1, Prompt: "p",
		ExpectedFacts: &evalplatform.ExpectedFacts{Required: []string{"O-1"}, Forbidden: []string{"escalated"}}}
	pulled, err := translate.EmitExamplesCSV([]translate.ExampleSpec{{SequenceNumber: 1, Prompt: "p", ExpectedFacts: sent.ExpectedFacts}})
	if err != nil {
		t.Fatal(err)
	}
	specs, err := translate.ParseExamplesCSV(pulled)
	if err != nil {
		t.Fatalf("parse the pulled CSV %s: %v", pulled, err)
	}
	actual := wire.ExampleResponse{ID: sent.ID, SequenceNumber: sent.SequenceNumber, Prompt: sent.Prompt, ExpectedFacts: wire.ExpectedFactsFromEval(sent.ExpectedFacts)}
	diff := computeWireExampleDiff([]wire.ExampleResponse{actual}, translateExampleSpecs(specs))
	if len(diff.Inserts)+len(diff.Updates)+len(diff.Deletes) != 0 {
		t.Errorf("re-applying pulled facts planned %+v, want nothing", diff)
	}
}
