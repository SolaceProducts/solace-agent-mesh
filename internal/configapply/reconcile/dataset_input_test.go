// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"encoding/json"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/translate"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
	evalplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/eval"
)

func TestExampleUpdateBodyNamesInputDataOnlyWhenNeeded(t *testing.T) {
	cases := []struct {
		name    string
		actual  string
		desired translate.ExampleSpec
		want    string
	}{
		{
			name:    "agent example",
			actual:  `{"id":"x1","sequenceNumber":1,"prompt":"old","expectedResponse":null,"inputData":null}`,
			desired: translate.ExampleSpec{SequenceNumber: 1, Prompt: "new"},
			want:    `{"prompt":"new"}`,
		},
		{
			name:    "expected response removed",
			actual:  `{"id":"x1","sequenceNumber":1,"prompt":"old","expectedResponse":"4","inputData":null}`,
			desired: translate.ExampleSpec{SequenceNumber: 1, Prompt: "new"},
			want:    `{"prompt":"new","expectedResponse":null}`,
		},
		{
			name:    "input removed",
			actual:  `{"id":"x1","sequenceNumber":1,"prompt":"p","inputData":{"order_id":"O-1"}}`,
			desired: translate.ExampleSpec{SequenceNumber: 1, Prompt: "p"},
			want:    `{"prompt":"p","inputData":null}`,
		},
		{
			name:    "input changed",
			actual:  `{"id":"x1","sequenceNumber":1,"prompt":"","inputData":{"order_id":"O-1"}}`,
			desired: translate.ExampleSpec{SequenceNumber: 1, InputData: json.RawMessage(`{"order_id":"O-2"}`)},
			want:    `{"prompt":"","inputData":{"order_id":"O-2"}}`,
		},
		{
			name:    "empty input added",
			actual:  `{"id":"x1","sequenceNumber":1,"prompt":"p","inputData":null}`,
			desired: translate.ExampleSpec{SequenceNumber: 1, Prompt: "p", InputData: json.RawMessage(`{}`)},
			want:    `{"prompt":"p","inputData":{}}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertExampleUpdateBody(t, tc.actual, tc.desired, tc.want)
		})
	}
}

func assertExampleUpdateBody(t *testing.T, stored string, desired translate.ExampleSpec, want string) {
	t.Helper()
	diff := computeWireExampleDiff([]wire.ExampleResponse{storedExample(t, stored)}, translateExampleSpecs([]translate.ExampleSpec{desired}))
	if len(diff.Updates) != 1 {
		t.Fatalf("updates = %+v, want one", diff.Updates)
	}
	got, err := json.Marshal(diff.Updates[0].Req)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Errorf("body = %s, want %s", got, want)
	}
}

func storedExample(t *testing.T, stored string) wire.ExampleResponse {
	t.Helper()
	var row evalplatform.ExampleResponse
	if err := json.Unmarshal([]byte(stored), &row); err != nil {
		t.Fatal(err)
	}
	return wire.ExampleResponse{
		ID:               row.ID,
		SequenceNumber:   row.SequenceNumber,
		Prompt:           row.Prompt,
		ExpectedResponse: row.ExpectedResponse,
		InputData:        apiv2.JSONObject(row.InputData),
		ExpectedPath:     wire.ExpectedPathFromEval(row.ExpectedPath),
		ExpectedFacts:    wire.ExpectedFactsFromEval(row.ExpectedFacts),
	}
}

func TestExampleUpdateBodySetsTheExpectedResponse(t *testing.T) {
	newResponse := "new"
	cases := []struct {
		name    string
		actual  string
		desired *string
		want    string
	}{
		{"added", `{"id":"x1","sequenceNumber":1,"prompt":"p","expectedResponse":null}`, &newResponse, `{"prompt":"p","expectedResponse":"new"}`},
		{"changed", `{"id":"x1","sequenceNumber":1,"prompt":"p","expectedResponse":"old"}`, &newResponse, `{"prompt":"p","expectedResponse":"new"}`},
		{"removed", `{"id":"x1","sequenceNumber":1,"prompt":"p","expectedResponse":"old"}`, nil, `{"prompt":"p","expectedResponse":null}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			desired := translate.ExampleSpec{SequenceNumber: 1, Prompt: "p", ExpectedResponse: tc.desired}
			assertExampleUpdateBody(t, tc.actual, desired, tc.want)
		})
	}
}

func TestPulledInputDataReappliesWithNoChanges(t *testing.T) {
	sent := wire.ExampleResponse{ID: "x1", SequenceNumber: 1,
		InputData: apiv2.JSONObject(`{"note":"<a&b>","orderId":9007199254740993,"total":1.0}`)}
	canonical, err := evalplatform.CanonicalInputData(json.RawMessage(sent.InputData))
	if err != nil {
		t.Fatal(err)
	}
	pulled, err := translate.EmitExamplesCSV([]translate.ExampleSpec{{SequenceNumber: 1, InputData: canonical}})
	if err != nil {
		t.Fatal(err)
	}
	specs, err := translate.ParseExamplesCSV(pulled)
	if err != nil {
		t.Fatalf("parse the pulled CSV %s: %v", pulled, err)
	}
	diff := computeWireExampleDiff([]wire.ExampleResponse{sent}, translateExampleSpecs(specs))
	if len(diff.Inserts)+len(diff.Updates)+len(diff.Deletes) != 0 {
		t.Errorf("re-applying a pulled input planned %+v, want nothing", diff)
	}
}
