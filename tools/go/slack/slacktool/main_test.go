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

func strPtr(s string) *string { return &s }

func TestSendPlainText(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()

	params := SendMessageParams{
		Channel: "C01234",
		Text:    "Hello, world!",
	}

	result, err := postSlackMessage(t.Context(), api, params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("expected success, got %q: %s", result.Status, result.Message)
	}

	calls := api.Calls()
	if len(calls) == 0 {
		t.Fatal("expected at least one Slack API call")
	}
	if calls[0].Channel != "C01234" {
		t.Errorf("channel = %q, want C01234", calls[0].Channel)
	}
	if calls[0].Text != "Hello, world!" {
		t.Errorf("text = %q, want Hello, world!", calls[0].Text)
	}
	if result.Data["ts"] == nil || result.Data["ts"] == "" {
		t.Error("expected ts in response data")
	}
}

func TestSendWithBlockKit(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()

	blocksJSON := `[{"type":"section","text":{"type":"mrkdwn","text":"*Bold* text"}}]`
	params := SendMessageParams{
		Channel: "C01234",
		Text:    "Fallback text",
		Blocks:  strPtr(blocksJSON),
	}

	result, err := postSlackMessage(t.Context(), api, params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("expected success, got %q: %s", result.Status, result.Message)
	}

	calls := api.Calls()
	if len(calls) == 0 {
		t.Fatal("expected at least one Slack API call")
	}
	if calls[0].Blocks == "" {
		t.Error("expected blocks in Slack API call, got empty string")
	}
}

func TestSendThreadedReply(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()

	params := SendMessageParams{
		Channel:  "C01234",
		Text:     "Thread reply",
		ThreadTS: strPtr("1700000000.000001"),
	}

	result, err := postSlackMessage(t.Context(), api, params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("expected success, got %q: %s", result.Status, result.Message)
	}

	calls := api.Calls()
	if len(calls) == 0 {
		t.Fatal("expected at least one Slack API call")
	}
	if calls[0].ThreadTS != "1700000000.000001" {
		t.Errorf("thread_ts = %q, want 1700000000.000001", calls[0].ThreadTS)
	}
	if result.Data["thread_ts"] != "1700000000.000001" {
		t.Errorf("response thread_ts = %v, want 1700000000.000001", result.Data["thread_ts"])
	}
}

func TestMissingBotToken(t *testing.T) {
	tc := &sdk.ToolContext{}

	params := SendMessageParams{
		Channel: "C01234",
		Text:    "Hello",
	}

	result, err := sendMessage(t.Context(), params, tc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "error" {
		t.Fatalf("expected error status, got %q", result.Status)
	}
	if !strings.Contains(result.Message, "slack_bot_token") {
		t.Errorf("error message should mention slack_bot_token, got: %s", result.Message)
	}
}

func TestMissingChannel(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()

	params := SendMessageParams{
		Text: "Hello",
	}

	result, err := postSlackMessage(t.Context(), api, params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "error" {
		t.Fatalf("expected error status, got %q", result.Status)
	}
	if !strings.Contains(result.Message, "channel is required") {
		t.Errorf("error message should mention channel is required, got: %s", result.Message)
	}
}

func TestEmptyTextNoBlocks(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()

	params := SendMessageParams{
		Channel: "C01234",
		Text:    "",
	}

	result, err := postSlackMessage(t.Context(), api, params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "error" {
		t.Fatalf("expected error status, got %q", result.Status)
	}
	if !strings.Contains(result.Message, "text or blocks") {
		t.Errorf("error message should mention text or blocks, got: %s", result.Message)
	}
}

func TestEmptyTextWithBlocks(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()

	blocksJSON := `[{"type":"section","text":{"type":"mrkdwn","text":"Rich content"}}]`
	params := SendMessageParams{
		Channel: "C01234",
		Text:    "",
		Blocks:  strPtr(blocksJSON),
	}

	result, err := postSlackMessage(t.Context(), api, params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("expected success, got %q: %s", result.Status, result.Message)
	}
}

func TestSlackAPIError_ChannelNotFound(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()
	api.SetNextError("PostMessage", fmt.Errorf("channel_not_found"))

	params := SendMessageParams{
		Channel: "C-nonexistent",
		Text:    "Hello",
	}

	result, err := postSlackMessage(t.Context(), api, params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "error" {
		t.Fatalf("expected error status, got %q", result.Status)
	}
	if !strings.Contains(result.Message, "channel_not_found") {
		t.Errorf("error message should contain Slack error, got: %s", result.Message)
	}
}

func TestSlackAPIError_InvalidAuth(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()
	api.SetNextError("PostMessage", fmt.Errorf("invalid_auth"))

	params := SendMessageParams{
		Channel: "C01234",
		Text:    "Hello",
	}

	result, err := postSlackMessage(t.Context(), api, params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "error" {
		t.Fatalf("expected error status, got %q", result.Status)
	}
	if !strings.Contains(result.Message, "invalid_auth") {
		t.Errorf("error message should contain invalid_auth, got: %s", result.Message)
	}
}

func TestSlackAPIError_NotInChannel(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()
	api.SetNextError("PostMessage", fmt.Errorf("not_in_channel"))

	params := SendMessageParams{
		Channel: "C-private",
		Text:    "Hello",
	}

	result, err := postSlackMessage(t.Context(), api, params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "error" {
		t.Fatalf("expected error status, got %q", result.Status)
	}
	if !strings.Contains(result.Message, "not_in_channel") {
		t.Errorf("error message should contain not_in_channel, got: %s", result.Message)
	}
}

func TestSendMessageParamsSchema(t *testing.T) {
	typ := reflect.TypeOf(SendMessageParams{})
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

	want := []string{"channel", "text", "blocks", "thread_ts", "attachments"}
	for _, w := range want {
		if !got[w] {
			t.Errorf("SendMessageParams missing json field %q (have %v)", w, got)
		}
	}

	if len(got) != len(want) {
		t.Errorf("SendMessageParams has %d json fields, want %d: %v", len(got), len(want), got)
	}
}

func TestSendMessageParamsUnmarshal(t *testing.T) {
	raw := []byte(`{
		"channel": "#general",
		"text": "Hello",
		"blocks": "[{\"type\":\"section\"}]",
		"thread_ts": "1700000000.000001"
	}`)
	var p SendMessageParams
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if p.Channel != "#general" {
		t.Errorf("Channel = %q, want #general", p.Channel)
	}
	if p.Text != "Hello" {
		t.Errorf("Text = %q, want Hello", p.Text)
	}
	if p.Blocks == nil || *p.Blocks != `[{"type":"section"}]` {
		t.Errorf("Blocks = %v, want [{\"type\":\"section\"}]", p.Blocks)
	}
	if p.ThreadTS == nil || *p.ThreadTS != "1700000000.000001" {
		t.Errorf("ThreadTS = %v, want 1700000000.000001", p.ThreadTS)
	}
}

func TestSendPlainTextConvertsMarkdown(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()

	params := SendMessageParams{
		Channel: "C01234",
		Text:    "**bold** and [link](https://x.com)",
	}

	result, err := postSlackMessage(t.Context(), api, params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("expected success, got %q: %s", result.Status, result.Message)
	}

	calls := api.Calls()
	if len(calls) == 0 {
		t.Fatal("expected at least one Slack API call")
	}
	want := "*bold* and <https://x.com|link>"
	if calls[0].Text != want {
		t.Errorf("text = %q, want %q", calls[0].Text, want)
	}
}

func TestInvalidBlocksJSON(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()

	params := SendMessageParams{
		Channel: "C01234",
		Text:    "Fallback",
		Blocks:  strPtr("not valid json"),
	}

	result, err := postSlackMessage(t.Context(), api, params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "error" {
		t.Fatalf("expected error status, got %q", result.Status)
	}
	if !strings.Contains(result.Message, "Invalid blocks JSON") {
		t.Errorf("error message should mention invalid blocks JSON, got: %s", result.Message)
	}
}

func TestMalformedInputBlockElementRejectedLocally(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()

	params := SendMessageParams{
		Channel: "C01234",
		Text:    "Fallback",
		Blocks:  strPtr(`[{"type":"input","label":{"type":"plain_text","text":"Name"},"element":[]}]`),
	}

	result, err := postSlackMessage(t.Context(), api, params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "error" {
		t.Fatalf("expected error status, got %q", result.Status)
	}
	if !strings.Contains(result.Message, "Invalid blocks JSON") {
		t.Errorf("error message should mention invalid blocks JSON, got: %s", result.Message)
	}
	if len(api.Calls()) != 0 {
		t.Errorf("malformed blocks must not reach Slack, got %d calls", len(api.Calls()))
	}
}

func TestWellFormedInputBlockAccepted(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()

	params := SendMessageParams{
		Channel: "C01234",
		Text:    "Fallback",
		Blocks:  strPtr(`[{"type":"input","label":{"type":"plain_text","text":"Name"},"element":{"type":"plain_text_input","action_id":"name"}}]`),
	}

	result, err := postSlackMessage(t.Context(), api, params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("expected success, got %q: %s", result.Status, result.Message)
	}
}

func TestPostSlackMessage_ContextCancelled(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()
	api.SetNextError("PostMessage", context.Canceled)

	params := SendMessageParams{
		Channel: "C01234",
		Text:    "Hello",
	}

	result, err := postSlackMessage(t.Context(), api, params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "error" {
		t.Fatalf("expected error status, got %q", result.Status)
	}
	if !strings.Contains(result.Message, "context canceled") {
		t.Errorf("error message should contain context canceled, got: %s", result.Message)
	}
}

func TestResolveAndSend_ChannelNameStripsHash(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()

	result, err := resolveAndSend(t.Context(), SendMessageParams{
		Channel: "#general",
		Text:    "Hello",
	}, api)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("expected success, got %q: %s", result.Status, result.Message)
	}

	calls := api.Calls()
	if len(calls) == 0 {
		t.Fatal("expected at least one Slack API call")
	}
	if calls[0].Channel != "general" {
		t.Errorf("channel = %q, want general (# should be stripped)", calls[0].Channel)
	}
}

func TestResolveAndSend_ChannelIDPassthrough(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()

	result, err := resolveAndSend(t.Context(), SendMessageParams{
		Channel: "C01234ABCDE",
		Text:    "Hello",
	}, api)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("expected success, got %q: %s", result.Status, result.Message)
	}

	calls := api.Calls()
	if len(calls) == 0 {
		t.Fatal("expected at least one Slack API call")
	}
	if calls[0].Channel != "C01234ABCDE" {
		t.Errorf("channel = %q, want C01234ABCDE (passthrough)", calls[0].Channel)
	}
}

func TestResolveAndSend_BareNamePassthrough(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()

	result, err := resolveAndSend(t.Context(), SendMessageParams{
		Channel: "general",
		Text:    "Hello",
	}, api)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("expected success, got %q: %s", result.Status, result.Message)
	}

	calls := api.Calls()
	if len(calls) == 0 {
		t.Fatal("expected at least one Slack API call")
	}
	if calls[0].Channel != "general" {
		t.Errorf("channel = %q, want general", calls[0].Channel)
	}
}

func TestResolveAndSend_EmailResolvesToUserID(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()
	api.SeedUserByEmail("alice@co.com", &goslack.User{ID: "U999"})

	result, err := resolveAndSend(t.Context(), SendMessageParams{
		Channel: "alice@co.com",
		Text:    "DM via email",
	}, api)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("expected success, got %q: %s", result.Status, result.Message)
	}

	postCalls := callsByMethod(api.Calls(), "PostMessage")
	if len(postCalls) == 0 {
		t.Fatal("expected a PostMessage call")
	}
	if postCalls[0].Channel != "U999" {
		t.Errorf("channel = %q, want U999 (resolved from email)", postCalls[0].Channel)
	}
}

func TestResolveAndSend_EmailNotFound(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()

	result, err := resolveAndSend(t.Context(), SendMessageParams{
		Channel: "nobody@co.com",
		Text:    "Hello",
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

func TestPostSlackMessage_MarkdownInBlocks(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()

	blocksJSON := `[{"type":"section","text":{"type":"mrkdwn","text":"*Bold* and <https://x.com|link>"}}]`
	params := SendMessageParams{
		Channel: "C01234",
		Text:    "**bold** fallback",
		Blocks:  strPtr(blocksJSON),
	}

	result, err := postSlackMessage(t.Context(), api, params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("expected success, got %q: %s", result.Status, result.Message)
	}

	calls := api.Calls()
	if calls[0].Blocks == "" {
		t.Error("expected non-empty blocks")
	}
	if !strings.Contains(calls[0].Text, "*bold*") {
		t.Errorf("fallback text should be markdown-converted, got: %q", calls[0].Text)
	}
}

func TestSlackDescriptionAdvertisesReturnedSlackID(t *testing.T) {
	mustContain := []string{
		"Slack ID",
		"C09",
		"U09",
		"slack_update",
	}
	for _, s := range mustContain {
		if !strings.Contains(slackDescription, s) {
			t.Errorf("slackDescription should mention %q, got: %s", s, slackDescription)
		}
	}
}

func TestSendMessageChannelDescTagMentionsReturnedID(t *testing.T) {
	typ := reflect.TypeOf(SendMessageParams{})
	field, ok := typ.FieldByName("Channel")
	if !ok {
		t.Fatal("Channel field not found on SendMessageParams")
	}
	desc := field.Tag.Get("desc")
	mustContain := []string{"Slack ID", "C09", "U09", "slack_update"}
	for _, s := range mustContain {
		if !strings.Contains(desc, s) {
			t.Errorf("Channel desc tag should mention %q, got: %s", s, desc)
		}
	}
}
