// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package structured

import (
	"encoding/base64"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
)

var orderSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"order_id": map[string]any{"type": "string"},
	},
	"required": []any{"order_id"},
}

func workflowNodeMessage(fileParts ...a2a.Part) a2a.Message {
	parts := []a2a.Part{a2a.NewDataPart(map[string]any{"type": "structured_invocation_request", "node_id": "n1"})}
	parts = append(parts, fileParts...)
	parts = append(parts, a2a.NewTextPart("Summarise the order."))
	return a2a.Message{Parts: parts}
}

func inputFilePart(json string) a2a.Part {
	return a2a.NewFilePartWithBytes(base64.StdEncoding.EncodeToString([]byte(json)), "n1_input.json", "application/json")
}

func TestInputFilePartIndex(t *testing.T) {
	emptyFile := a2a.Part{Kind: "file", File: &a2a.FileContent{Name: "empty.json"}}
	uriFile := a2a.Part{Kind: "file", File: &a2a.FileContent{Name: "n1_input.json", URI: "artifact://app/alice/sess/n1_input.json?version=0"}}
	cases := []struct {
		name   string
		msg    a2a.Message
		schema map[string]any
		want   int
	}{
		{"structured schema reads the input file", workflowNodeMessage(inputFilePart(`{"order_id":"o1"}`)), orderSchema, 1},
		{"a file part with no content is not the input", workflowNodeMessage(emptyFile, inputFilePart(`{"order_id":"o1"}`)), orderSchema, 2},
		{"a URI-only file part is the input", workflowNodeMessage(uriFile), orderSchema, 1},
		{"the first of two input files wins", workflowNodeMessage(inputFilePart(`{"order_id":"o1"}`), inputFilePart(`{"order_id":"o2"}`)), orderSchema, 1},
		{"structured schema without an input file", workflowNodeMessage(), orderSchema, -1},
		{"single-text schema reads the text parts", workflowNodeMessage(inputFilePart(`{"order_id":"o1"}`)), defaultInputSchema, -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := InputFilePartIndex(tc.msg, tc.schema); got != tc.want {
				t.Errorf("InputFilePartIndex = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestExtractInputData_ReadsThePartInputFilePartIndexNames(t *testing.T) {
	emptyFile := a2a.Part{Kind: "file", File: &a2a.FileContent{Name: "empty.json"}}
	msg := workflowNodeMessage(emptyFile, inputFilePart(`{"order_id":"o1"}`), inputFilePart(`{"order_id":"o2"}`))

	data, err := ExtractInputData(msg, orderSchema)
	if err != nil {
		t.Fatalf("ExtractInputData: %v", err)
	}
	if data["order_id"] != "o1" {
		t.Errorf("order_id = %v, want o1 from part %d", data["order_id"], InputFilePartIndex(msg, orderSchema))
	}
}

func TestDecodeFileBytes(t *testing.T) {
	raw := `{"order_id":"o1"}`
	if got := string(DecodeFileBytes(base64.StdEncoding.EncodeToString([]byte(raw)))); got != raw {
		t.Errorf("base64 bytes decoded to %q, want %q", got, raw)
	}
	if got := string(DecodeFileBytes(raw)); got != raw {
		t.Errorf("unencoded bytes decoded to %q, want them unchanged", got)
	}
}
