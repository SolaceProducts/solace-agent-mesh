// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package whatsapp

import (
	"encoding/json"
	"testing"
)

func TestInboundMessage_DecodesReaction(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"from":"16135550100","id":"wamid.R1","timestamp":"1789668164","type":"reaction",
		"reaction":{"message_id":"wamid.OUT-7","emoji":"👍"}}`)
	var msg inboundMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if msg.Reaction == nil {
		t.Fatal("Reaction not decoded")
	}
	if msg.Reaction.MessageID != "wamid.OUT-7" || msg.Reaction.Emoji != "👍" {
		t.Fatalf("reaction = %+v", *msg.Reaction)
	}
}

func TestInboundMessage_ReactionRemovalHasEmptyEmoji(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"from":"16135550100","id":"wamid.R2","type":"reaction","reaction":{"message_id":"wamid.OUT-7","emoji":""}}`)
	var msg inboundMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if msg.Reaction == nil || msg.Reaction.Emoji != "" {
		t.Fatalf("removal must decode with empty emoji, got %+v", msg.Reaction)
	}
}

func TestInteractivePayload_AbsentFooterIsOmittedNotEmpty(t *testing.T) {
	t.Parallel()
	p := interactivePayload{
		Type:   "list",
		Body:   interactiveBody{Text: "body"},
		Footer: &interactiveFooter{Text: "footer text"},
		Action: interactiveAction{Button: "Choose", Sections: []interactiveSection{{Title: "Options", Rows: []listRowDTO{{ID: "x", Title: "X"}}}}},
	}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	_ = json.Unmarshal(b, &got)
	footer, _ := got["footer"].(map[string]any)
	if footer["text"] != "footer text" {
		t.Fatalf("footer.text missing or wrong: %s", b)
	}

	p.Footer = nil
	b, _ = json.Marshal(p)
	got = map[string]any{}
	_ = json.Unmarshal(b, &got)
	if _, present := got["footer"]; present {
		t.Fatalf("nil footer must be omitted, got %s", b)
	}
}
