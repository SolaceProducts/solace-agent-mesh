// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package slacktool

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/slackapi"
	goslack "github.com/slack-go/slack"
)

func TestUpdatePlainText(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()
	api.SeedMessage("C01234", "1700000000.000001", "original")

	p := UpdateMessageParams{
		Channel: "C01234",
		Text:    "Updated text",
		TS:      "1700000000.000001",
	}

	result, err := updateSlackMessage(t.Context(), api, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("expected success, got %q: %s", result.Status, result.Message)
	}

	calls := callsByMethod(api.Calls(), "UpdateMessage")
	if len(calls) != 1 {
		t.Fatalf("expected 1 UpdateMessage call, got %d", len(calls))
	}
	if calls[0].Channel != "C01234" {
		t.Errorf("channel = %q, want C01234", calls[0].Channel)
	}
	if calls[0].ThreadTS != "1700000000.000001" {
		t.Errorf("ts = %q, want 1700000000.000001", calls[0].ThreadTS)
	}
	if calls[0].Text != "Updated text" {
		t.Errorf("text = %q, want Updated text", calls[0].Text)
	}
	if result.Data["ts"] != "1700000000.000001" {
		t.Errorf("response ts = %v, want 1700000000.000001", result.Data["ts"])
	}
}

func TestUpdateWithBlocks(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()
	api.SeedMessage("C01234", "1700000000.000001", "original")

	blocksJSON := `[{"type":"section","text":{"type":"mrkdwn","text":"*Bold*"}}]`
	p := UpdateMessageParams{
		Channel: "C01234",
		Text:    "Fallback",
		TS:      "1700000000.000001",
		Blocks:  strPtr(blocksJSON),
	}

	result, err := updateSlackMessage(t.Context(), api, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("expected success, got %q: %s", result.Status, result.Message)
	}

	calls := callsByMethod(api.Calls(), "UpdateMessage")
	if len(calls) != 1 {
		t.Fatalf("expected 1 UpdateMessage call, got %d", len(calls))
	}
	if calls[0].Blocks == "" {
		t.Error("expected blocks in UpdateMessage call, got empty string")
	}
}

func TestUpdateMarkdownConverted(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()
	api.SeedMessage("C01234", "1700000000.000001", "original")

	p := UpdateMessageParams{
		Channel: "C01234",
		Text:    "**bold** and [link](https://x.com)",
		TS:      "1700000000.000001",
	}

	result, err := updateSlackMessage(t.Context(), api, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("expected success, got %q: %s", result.Status, result.Message)
	}

	calls := callsByMethod(api.Calls(), "UpdateMessage")
	if len(calls) != 1 {
		t.Fatalf("expected 1 UpdateMessage call, got %d", len(calls))
	}
	want := "*bold* and <https://x.com|link>"
	if calls[0].Text != want {
		t.Errorf("text = %q, want %q", calls[0].Text, want)
	}
}

func TestUpdateEmptyTextWithBlocks(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()
	api.SeedMessage("C01234", "1700000000.000001", "original")

	blocksJSON := `[{"type":"section","text":{"type":"mrkdwn","text":"Rich content"}}]`
	p := UpdateMessageParams{
		Channel: "C01234",
		Text:    "",
		TS:      "1700000000.000001",
		Blocks:  strPtr(blocksJSON),
	}

	result, err := updateSlackMessage(t.Context(), api, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("expected success, got %q: %s", result.Status, result.Message)
	}
}

func TestUpdateMissingChannel(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()

	p := UpdateMessageParams{
		Text: "Hello",
		TS:   "1700000000.000001",
	}

	result, err := updateSlackMessage(t.Context(), api, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "error" {
		t.Fatalf("expected error, got %q", result.Status)
	}
	if !strings.Contains(result.Message, "channel is required") {
		t.Errorf("error should mention channel is required, got: %s", result.Message)
	}
}

func TestUpdateMissingTS(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()

	p := UpdateMessageParams{
		Channel: "C01234",
		Text:    "Hello",
	}

	result, err := updateSlackMessage(t.Context(), api, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "error" {
		t.Fatalf("expected error, got %q", result.Status)
	}
	if !strings.Contains(result.Message, "ts is required") {
		t.Errorf("error should mention ts is required, got: %s", result.Message)
	}
}

func TestUpdateEmptyTextNoBlocks(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()

	p := UpdateMessageParams{
		Channel: "C01234",
		TS:      "1700000000.000001",
	}

	result, err := updateSlackMessage(t.Context(), api, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "error" {
		t.Fatalf("expected error, got %q", result.Status)
	}
	if !strings.Contains(result.Message, "text or blocks") {
		t.Errorf("error should mention text or blocks, got: %s", result.Message)
	}
}

func TestUpdateInvalidBlocksJSON(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()

	p := UpdateMessageParams{
		Channel: "C01234",
		Text:    "Fallback",
		TS:      "1700000000.000001",
		Blocks:  strPtr("not valid json"),
	}

	result, err := updateSlackMessage(t.Context(), api, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "error" {
		t.Fatalf("expected error, got %q", result.Status)
	}
	if !strings.Contains(result.Message, "Invalid blocks JSON") {
		t.Errorf("error should mention Invalid blocks JSON, got: %s", result.Message)
	}
}

func TestUpdateMalformedInputBlockElementRejectedLocally(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()

	p := UpdateMessageParams{
		Channel: "C01234",
		Text:    "Fallback",
		TS:      "1700000000.000001",
		Blocks:  strPtr(`[{"type":"input","label":{"type":"plain_text","text":"Name"},"element":[]}]`),
	}

	result, err := updateSlackMessage(t.Context(), api, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "error" {
		t.Fatalf("expected error, got %q", result.Status)
	}
	if !strings.Contains(result.Message, "Invalid blocks JSON") {
		t.Errorf("error should mention Invalid blocks JSON, got: %s", result.Message)
	}
	if len(api.Calls()) != 0 {
		t.Errorf("malformed blocks must not reach Slack, got %d calls", len(api.Calls()))
	}
}

func TestUpdateSlackAPIError(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()
	api.SetNextError("UpdateMessage", fmt.Errorf("message_not_found"))

	p := UpdateMessageParams{
		Channel: "C01234",
		Text:    "Hello",
		TS:      "1700000000.000001",
	}

	result, err := updateSlackMessage(t.Context(), api, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "error" {
		t.Fatalf("expected error, got %q", result.Status)
	}
	if !strings.Contains(result.Message, "message_not_found") {
		t.Errorf("error should contain message_not_found, got: %s", result.Message)
	}
}

func TestUpdateContextCancelled(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()
	api.SetNextError("UpdateMessage", context.Canceled)

	p := UpdateMessageParams{
		Channel: "C01234",
		Text:    "Hello",
		TS:      "1700000000.000001",
	}

	result, err := updateSlackMessage(t.Context(), api, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "error" {
		t.Fatalf("expected error, got %q", result.Status)
	}
	if !strings.Contains(result.Message, "context canceled") {
		t.Errorf("error should contain context canceled, got: %s", result.Message)
	}
}

func TestUpdateMissingBotToken(t *testing.T) {
	tc := &sdk.ToolContext{}

	p := UpdateMessageParams{
		Channel: "C01234",
		Text:    "Hello",
		TS:      "1700000000.000001",
	}

	result, err := updateMessage(t.Context(), p, tc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "error" {
		t.Fatalf("expected error, got %q", result.Status)
	}
	if !strings.Contains(result.Message, "slack_bot_token") {
		t.Errorf("error should mention slack_bot_token, got: %s", result.Message)
	}
}

func TestSendThenUpdate(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()

	sendResult, err := postSlackMessage(t.Context(), api, SendMessageParams{
		Channel: "C99999",
		Text:    "Original message",
	})
	if err != nil {
		t.Fatalf("send error: %v", err)
	}
	if sendResult.Status != "success" {
		t.Fatalf("send failed: %s", sendResult.Message)
	}
	sentTS, ok := sendResult.Data["ts"].(string)
	if !ok || sentTS == "" {
		t.Fatalf("send result missing ts, data = %v", sendResult.Data)
	}

	updateResult, err := updateSlackMessage(t.Context(), api, UpdateMessageParams{
		Channel: "C99999",
		Text:    "Edited message",
		TS:      sentTS,
	})
	if err != nil {
		t.Fatalf("update error: %v", err)
	}
	if updateResult.Status != "success" {
		t.Fatalf("update failed: %s", updateResult.Message)
	}

	updateCalls := callsByMethod(api.Calls(), "UpdateMessage")
	if len(updateCalls) != 1 {
		t.Fatalf("expected 1 UpdateMessage call, got %d", len(updateCalls))
	}
	if updateCalls[0].Channel != "C99999" {
		t.Errorf("update channel = %q, want C99999", updateCalls[0].Channel)
	}
	if updateCalls[0].ThreadTS != sentTS {
		t.Errorf("update ts = %q, want %q", updateCalls[0].ThreadTS, sentTS)
	}
	if updateCalls[0].Text != "Edited message" {
		t.Errorf("update text = %q, want Edited message", updateCalls[0].Text)
	}
	if updateResult.Data["ts"] != sentTS {
		t.Errorf("update response ts = %v, want %q", updateResult.Data["ts"], sentTS)
	}
}

func TestResolveAndUpdate_ChannelNameStripsHash(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()
	api.SeedMessage("general", "1700000000.000001", "Original")

	result, err := resolveAndUpdate(t.Context(), UpdateMessageParams{
		Channel: "#general",
		Text:    "Updated",
		TS:      "1700000000.000001",
	}, api)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("expected success, got %q: %s", result.Status, result.Message)
	}

	calls := callsByMethod(api.Calls(), "UpdateMessage")
	if len(calls) != 1 {
		t.Fatalf("expected 1 UpdateMessage call, got %d", len(calls))
	}
	if calls[0].Channel != "general" {
		t.Errorf("channel = %q, want general (# should be stripped)", calls[0].Channel)
	}
}

func TestResolveAndUpdate_ChannelIDPassthrough(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()
	api.SeedMessage("C01234ABCDE", "1700000000.000001", "Original")

	result, err := resolveAndUpdate(t.Context(), UpdateMessageParams{
		Channel: "C01234ABCDE",
		Text:    "Updated",
		TS:      "1700000000.000001",
	}, api)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("expected success, got %q: %s", result.Status, result.Message)
	}

	calls := callsByMethod(api.Calls(), "UpdateMessage")
	if len(calls) != 1 {
		t.Fatalf("expected 1 UpdateMessage call, got %d", len(calls))
	}
	if calls[0].Channel != "C01234ABCDE" {
		t.Errorf("channel = %q, want C01234ABCDE (passthrough)", calls[0].Channel)
	}
}

func TestResolveAndUpdate_EmailResolvesToUserID(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()
	api.SeedUserByEmail("bob@co.com", &goslack.User{ID: "U555"})
	api.SeedMessage("U555", "1700000000.000001", "Original")

	result, err := resolveAndUpdate(t.Context(), UpdateMessageParams{
		Channel: "bob@co.com",
		Text:    "Updated via email",
		TS:      "1700000000.000001",
	}, api)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("expected success, got %q: %s", result.Status, result.Message)
	}

	calls := callsByMethod(api.Calls(), "UpdateMessage")
	if len(calls) != 1 {
		t.Fatalf("expected 1 UpdateMessage call, got %d", len(calls))
	}
	if calls[0].Channel != "U555" {
		t.Errorf("channel = %q, want U555 (resolved from email)", calls[0].Channel)
	}
}

func TestResolveAndUpdate_EmailNotFound(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()

	result, err := resolveAndUpdate(t.Context(), UpdateMessageParams{
		Channel: "nobody@co.com",
		Text:    "Updated",
		TS:      "1700000000.000001",
	}, api)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "error" {
		t.Fatalf("expected error, got %q", result.Status)
	}
	if !strings.Contains(result.Message, "resolve channel") || !strings.Contains(result.Message, "nobody@co.com") {
		t.Errorf("error should mention failed resolution, got: %s", result.Message)
	}
}

func TestUpdateMessageParamsSchema(t *testing.T) {
	typ := reflect.TypeOf(UpdateMessageParams{})
	got := make(map[string]bool, typ.NumField())
	for i := range typ.NumField() {
		tag := typ.Field(i).Tag.Get("json")
		if tag == "" {
			continue
		}
		name := tag
		if comma := strings.IndexByte(tag, ','); comma >= 0 {
			name = tag[:comma]
		}
		got[name] = true
	}

	want := []string{"channel", "text", "ts", "blocks"}
	for _, w := range want {
		if !got[w] {
			t.Errorf("UpdateMessageParams missing json field %q (have %v)", w, got)
		}
	}

	if len(got) != len(want) {
		t.Errorf("UpdateMessageParams has %d json fields, want %d: %v", len(got), len(want), got)
	}
}

func TestUpdateMessageParamsUnmarshal(t *testing.T) {
	raw := []byte(`{
		"channel": "#general",
		"text": "Updated",
		"ts": "1700000000.000001",
		"blocks": "[{\"type\":\"section\"}]"
	}`)
	var p UpdateMessageParams
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if p.Channel != "#general" {
		t.Errorf("Channel = %q, want #general", p.Channel)
	}
	if p.Text != "Updated" {
		t.Errorf("Text = %q, want Updated", p.Text)
	}
	if p.TS != "1700000000.000001" {
		t.Errorf("TS = %q, want 1700000000.000001", p.TS)
	}
	if p.Blocks == nil || *p.Blocks != `[{"type":"section"}]` {
		t.Errorf("Blocks = %v, want [{\"type\":\"section\"}]", p.Blocks)
	}
}

func TestSlackUpdateDescriptionRequiresSlackID(t *testing.T) {
	mustContain := []string{
		"Slack ID",
		"C09",
		"U09",
		"slack_update",
		"slack",
	}
	for _, s := range mustContain {
		if !strings.Contains(slackUpdateDescription, s) {
			t.Errorf("slackUpdateDescription should mention %q, got: %s", s, slackUpdateDescription)
		}
	}

	mustNotContain := []string{
		"#channel-name",
	}
	for _, s := range mustNotContain {
		if strings.Contains(slackUpdateDescription, s) {
			t.Errorf("slackUpdateDescription should NOT suggest %q is accepted, got: %s", s, slackUpdateDescription)
		}
	}
}

func TestUpdateInstructionsRequireSlackID(t *testing.T) {
	mustContain := []string{
		"Slack ID",
		"C09",
		"U09",
		"NOT accepted",
		"slack",
	}
	for _, s := range mustContain {
		if !strings.Contains(updateInstructions, s) {
			t.Errorf("updateInstructions should mention %q, got: %s", s, updateInstructions)
		}
	}
}

func TestUpdateMessageChannelDescTagRequiresSlackID(t *testing.T) {
	typ := reflect.TypeOf(UpdateMessageParams{})
	field, ok := typ.FieldByName("Channel")
	if !ok {
		t.Fatal("Channel field not found on UpdateMessageParams")
	}
	desc := field.Tag.Get("desc")
	mustContain := []string{"Slack ID", "C09", "U09", "NOT accepted"}
	for _, s := range mustContain {
		if !strings.Contains(desc, s) {
			t.Errorf("Channel desc tag should mention %q, got: %s", s, desc)
		}
	}
	if strings.Contains(desc, "channel name") {
		t.Errorf("Channel desc tag should not narrowly say %q (use 'name'/'names' to cover users too), got: %s", "channel name", desc)
	}
}
