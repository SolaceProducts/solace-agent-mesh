// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
	evalplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/eval"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func TestExampleDiffParity(t *testing.T) {
	cases := []struct {
		name    string
		actual  []*store.EvalExample
		desired []wire.CreateExampleRequest
	}{
		{
			name: "inserts only",
			desired: []wire.CreateExampleRequest{
				{Prompt: "a", SequenceNumber: ptrInt(1)},
				{Prompt: "b", ExpectedResponse: ptrStr("B"), SequenceNumber: ptrInt(2)},
			},
		},
		{
			name: "deletes only",
			actual: []*store.EvalExample{
				{ID: "x1", SequenceNumber: 1, Prompt: "a"},
				{ID: "x2", SequenceNumber: 2, Prompt: "b"},
			},
		},
		{
			name: "updates only — prompt change",
			actual: []*store.EvalExample{
				{ID: "x1", SequenceNumber: 1, Prompt: "old"},
			},
			desired: []wire.CreateExampleRequest{
				{Prompt: "new", SequenceNumber: ptrInt(1)},
			},
		},
		{
			name: "updates only — expected null-out",
			actual: []*store.EvalExample{
				{ID: "x1", SequenceNumber: 1, Prompt: "a", ExpectedResponse: ptrStr("hi")},
			},
			desired: []wire.CreateExampleRequest{
				{Prompt: "a", SequenceNumber: ptrInt(1)},
			},
		},
		{
			name: "mixed insert / update / delete",
			actual: []*store.EvalExample{
				{ID: "x1", SequenceNumber: 1, Prompt: "a"},
				{ID: "x3", SequenceNumber: 3, Prompt: "old"},
				{ID: "x5", SequenceNumber: 5, Prompt: "drop"},
				{ID: "x7", SequenceNumber: 7, Prompt: "drop2"},
			},
			desired: []wire.CreateExampleRequest{
				{Prompt: "a", SequenceNumber: ptrInt(1)},
				{Prompt: "new", SequenceNumber: ptrInt(3)},
				{Prompt: "fresh", SequenceNumber: ptrInt(4)},
				{Prompt: "fresher", SequenceNumber: ptrInt(8)},
			},
		},
		{
			name:    "empty",
			actual:  nil,
			desired: nil,
		},
		{
			name: "inserts only — input-only example",
			desired: []wire.CreateExampleRequest{
				{InputData: apiv2.JSONObject(`{"order_id":"O-1"}`), SequenceNumber: ptrInt(1)},
			},
		},
		{
			name: "unchanged — equal input in another spelling, escaped on the wire",
			actual: []*store.EvalExample{
				{ID: "x1", SequenceNumber: 1, InputData: ptrStr(`{"note":"<a&b>","order_id":"O-1"}`)},
			},
			desired: []wire.CreateExampleRequest{
				{InputData: apiv2.JSONObject(`{ "order_id": "O-1", "note": "<a&b>" }`), SequenceNumber: ptrInt(1)},
			},
		},
		{
			name: "updates only — input change",
			actual: []*store.EvalExample{
				{ID: "x1", SequenceNumber: 1, InputData: ptrStr(`{"order_id":"O-1"}`)},
			},
			desired: []wire.CreateExampleRequest{
				{InputData: apiv2.JSONObject(`{"order_id":"O-2"}`), SequenceNumber: ptrInt(1)},
			},
		},
		{
			name: "updates only — ids one apart past 2^53",
			actual: []*store.EvalExample{
				{ID: "x1", SequenceNumber: 1, InputData: ptrStr(`{"id":9007199254740993}`)},
			},
			desired: []wire.CreateExampleRequest{
				{InputData: apiv2.JSONObject(`{"id":9007199254740992}`), SequenceNumber: ptrInt(1)},
			},
		},
		{
			name: "updates only — input added and input cleared",
			actual: []*store.EvalExample{
				{ID: "x1", SequenceNumber: 1, Prompt: "a"},
				{ID: "x2", SequenceNumber: 2, Prompt: "b", InputData: ptrStr(`{}`)},
			},
			desired: []wire.CreateExampleRequest{
				{Prompt: "a", InputData: apiv2.JSONObject(`{}`), SequenceNumber: ptrInt(1)},
				{Prompt: "b", SequenceNumber: ptrInt(2)},
			},
		},
		{
			name: "updates only — facts and path changed, facts cleared, facts unchanged",
			actual: []*store.EvalExample{
				{ID: "x1", SequenceNumber: 1, Prompt: "a", ExpectedFacts: ptrStr(`{"required":["a"]}`)},
				{ID: "x2", SequenceNumber: 2, Prompt: "b", ExpectedPath: ptrStr(`{"tools":{"t":"called"}}`)},
				{ID: "x3", SequenceNumber: 3, Prompt: "c", ExpectedFacts: ptrStr(`{"forbidden":["c"]}`)},
				{ID: "x4", SequenceNumber: 4, Prompt: "d", ExpectedFacts: ptrStr(`{"required":["d"]}`)},
			},
			desired: []wire.CreateExampleRequest{
				{Prompt: "a", ExpectedFacts: &apiv2.ExpectedFacts{Forbidden: []string{"a"}}, SequenceNumber: ptrInt(1)},
				{Prompt: "b", ExpectedPath: &apiv2.ExpectedPath{Tools: []apiv2.ExpectedTool{{ToolName: "t", Expectation: "not_called"}}}, SequenceNumber: ptrInt(2)},
				{Prompt: "c", SequenceNumber: ptrInt(3)},
				{Prompt: "d", ExpectedFacts: &apiv2.ExpectedFacts{Required: []string{"d"}, Forbidden: []string{}}, SequenceNumber: ptrInt(4)},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			actualResp := exampleResponseFromRows(t, tc.actual)
			wireDiff := computeWireExampleDiff(actualResp, tc.desired)

			desiredEval := desiredFromCreates(tc.desired)
			evalDiff := evalplatform.DiffExamples(tc.actual, desiredEval)

			gotInserts := wireInsertSeqs(wireDiff.Inserts)
			wantInserts := evalInsertSeqs(evalDiff.InsertOps)
			if !reflect.DeepEqual(gotInserts, wantInserts) {
				t.Errorf("insert sequence drift\n  wire: %v\n  eval: %v", gotInserts, wantInserts)
			}

			if strings.HasPrefix(tc.name, "updates only — facts") && len(evalDiff.UpdateOps) != 3 {
				t.Errorf("updates = %d, want the three changed rows", len(evalDiff.UpdateOps))
			}

			gotUpdateIDs := wireUpdateIDs(wireDiff.Updates)
			wantUpdateIDs := evalUpdateIDs(evalDiff.UpdateOps)
			if !reflect.DeepEqual(gotUpdateIDs, wantUpdateIDs) {
				t.Errorf("update ID drift\n  wire: %v\n  eval: %v", gotUpdateIDs, wantUpdateIDs)
			}

			if !reflect.DeepEqual(wireDiff.Deletes, evalDiff.DeleteIDs) {
				t.Errorf("delete ID drift\n  wire: %v\n  eval: %v", wireDiff.Deletes, evalDiff.DeleteIDs)
			}
		})
	}
}

func exampleResponseFromRows(t *testing.T, rows []*store.EvalExample) []wire.ExampleResponse {
	t.Helper()
	out := make([]wire.ExampleResponse, 0, len(rows))
	for _, r := range rows {
		received := wire.ExampleResponse{
			ID:               r.ID,
			SequenceNumber:   r.SequenceNumber,
			Prompt:           r.Prompt,
			ExpectedResponse: r.ExpectedResponse,
		}
		if r.InputData != nil {
			input, err := json.Marshal(json.RawMessage(*r.InputData))
			if err != nil {
				t.Fatal(err)
			}
			received.InputData = apiv2.JSONObject(input)
		}
		var path *evalplatform.ExpectedPath
		storedObject(t, r.ExpectedPath, &path)
		received.ExpectedPath = wire.ExpectedPathFromEval(path)
		var facts *evalplatform.ExpectedFacts
		storedObject(t, r.ExpectedFacts, &facts)
		received.ExpectedFacts = wire.ExpectedFactsFromEval(facts)
		out = append(out, received)
	}
	return out
}

func desiredFromCreates(reqs []wire.CreateExampleRequest) []evalplatform.DesiredExample {
	out := make([]evalplatform.DesiredExample, 0, len(reqs))
	for _, r := range reqs {
		seq := 0
		if r.SequenceNumber != nil {
			seq = *r.SequenceNumber
		}
		out = append(out, evalplatform.DesiredExample{
			SequenceNumber:   seq,
			Prompt:           r.Prompt,
			ExpectedResponse: r.ExpectedResponse,
			InputData:        json.RawMessage(r.InputData),
			ExpectedPath:     wire.EvalExpectedPath(r.ExpectedPath),
			ExpectedFacts:    wire.EvalExpectedFacts(r.ExpectedFacts),
		})
	}
	return out
}

func storedObject[T any](t *testing.T, stored *string, into **T) {
	t.Helper()
	if stored == nil {
		return
	}
	*into = new(T)
	if err := json.Unmarshal([]byte(*stored), *into); err != nil {
		t.Fatal(err)
	}
}

func wireInsertSeqs(ins []wire.CreateExampleRequest) []int {
	out := make([]int, 0, len(ins))
	for _, i := range ins {
		out = append(out, derefInt(i.SequenceNumber))
	}
	return out
}

func evalInsertSeqs(ins []evalplatform.DesiredExample) []int {
	out := make([]int, 0, len(ins))
	for _, i := range ins {
		out = append(out, i.SequenceNumber)
	}
	return out
}

func wireUpdateIDs(ups []wireExampleUpdate) []string {
	out := make([]string, 0, len(ups))
	for _, u := range ups {
		out = append(out, u.ID)
	}
	return out
}

func evalUpdateIDs(ups []evalplatform.ExampleUpdateOp) []string {
	out := make([]string, 0, len(ups))
	for _, u := range ups {
		out = append(out, u.ID)
	}
	return out
}

func ptrInt(v int) *int       { return &v }
func ptrStr(v string) *string { return &v }
