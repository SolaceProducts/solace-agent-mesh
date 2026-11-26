// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"encoding/json"
	"testing"
)

func TestUpdateExampleRequestWritesExplicitNulls(t *testing.T) {
	cases := []struct {
		name string
		req  UpdateExampleRequest
		want string
	}{
		{"nothing set", UpdateExampleRequest{}, `{}`},
		{"a prompt", UpdateExampleRequest{Prompt: new("p")}, `{"prompt":"p"}`},
		{"an expected response", UpdateExampleRequest{ExpectedResponse: new("x")}, `{"expectedResponse":"x"}`},
		{"every field, in order", UpdateExampleRequest{Prompt: new("p"), ExpectedResponse: new("x"), ExpectedResponseSet: true, InputData: json.RawMessage(`{"a":1}`), InputDataSet: true}, `{"prompt":"p","expectedResponse":"x","inputData":{"a":1}}`},
		{"clearing both", UpdateExampleRequest{ExpectedResponseSet: true, InputDataSet: true}, `{"expectedResponse":null,"inputData":null}`},
		{"setting the input", UpdateExampleRequest{InputData: json.RawMessage(`{"order_id":"O-1"}`), InputDataSet: true}, `{"inputData":{"order_id":"O-1"}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := json.Marshal(tc.req)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.want {
				t.Errorf("wire = %s, want %s", got, tc.want)
			}
		})
	}
}
