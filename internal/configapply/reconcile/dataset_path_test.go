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

func TestExampleUpdateBodySetsTheExpectedPath(t *testing.T) {
	approve := &evalplatform.ExpectedPath{Nodes: map[string]string{"approve": "ran"}}
	cases := []struct {
		name    string
		actual  string
		desired *evalplatform.ExpectedPath
		want    string
	}{
		{"added", `{"id":"x1","sequenceNumber":1,"prompt":"p","expectedPath":null}`, approve,
			`{"prompt":"p","expectedPath":{"nodes":[{"nodeId":"approve","expectation":"ran"}],"tools":[]}}`},
		{"changed", `{"id":"x1","sequenceNumber":1,"prompt":"p","expectedPath":{"nodes":{"approve":"failed"}}}`, approve,
			`{"prompt":"p","expectedPath":{"nodes":[{"nodeId":"approve","expectation":"ran"}],"tools":[]}}`},
		{"a tool changed", `{"id":"x1","sequenceNumber":1,"prompt":"p","expectedPath":{"tools":{"issue_refund":"called"}}}`,
			&evalplatform.ExpectedPath{Tools: map[string]string{"issue_refund": "not_called"}},
			`{"prompt":"p","expectedPath":{"nodes":[],"tools":[{"toolName":"issue_refund","expectation":"not_called"}]}}`},
		{"removed", `{"id":"x1","sequenceNumber":1,"prompt":"p","expectedPath":{"nodes":{"approve":"ran"}}}`, nil,
			`{"prompt":"p","expectedPath":null}`},
		{"never had one", `{"id":"x1","sequenceNumber":1,"prompt":"old","expectedPath":null}`, nil,
			`{"prompt":"p"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			desired := translate.ExampleSpec{SequenceNumber: 1, Prompt: "p", ExpectedPath: tc.desired}
			assertExampleUpdateBody(t, tc.actual, desired, tc.want)
		})
	}
}

func TestPulledExpectedPathReappliesWithNoChanges(t *testing.T) {
	sent := evalplatform.ExampleResponse{ID: "x1", SequenceNumber: 1, Prompt: "p",
		ExpectedPath: &evalplatform.ExpectedPath{Nodes: map[string]string{"approve": "ran"}, Tools: map[string]string{"issue_refund": "called"}}}
	pulled, err := translate.EmitExamplesCSV([]translate.ExampleSpec{{SequenceNumber: 1, Prompt: "p", ExpectedPath: sent.ExpectedPath}})
	if err != nil {
		t.Fatal(err)
	}
	specs, err := translate.ParseExamplesCSV(pulled)
	if err != nil {
		t.Fatalf("parse the pulled CSV %s: %v", pulled, err)
	}
	actual := wire.ExampleResponse{ID: sent.ID, SequenceNumber: sent.SequenceNumber, Prompt: sent.Prompt, ExpectedPath: wire.ExpectedPathFromEval(sent.ExpectedPath)}
	diff := computeWireExampleDiff([]wire.ExampleResponse{actual}, translateExampleSpecs(specs))
	if len(diff.Inserts)+len(diff.Updates)+len(diff.Deletes) != 0 {
		t.Errorf("re-applying a pulled path planned %+v, want nothing", diff)
	}
}
