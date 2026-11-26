// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package teams

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestStatusCard_WithCancelButton(t *testing.T) {
	card := StatusCard("Processing...", "task-123", "", true)

	if card.ContentType != "application/vnd.microsoft.card.adaptive" {
		t.Errorf("ContentType = %q", card.ContentType)
	}

	data, err := json.Marshal(card.Content)
	if err != nil {
		t.Fatalf("failed to marshal card: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if m["type"] != "AdaptiveCard" {
		t.Errorf("type = %v", m["type"])
	}
	if m["version"] != "1.5" {
		t.Errorf("version = %v, want 1.5", m["version"])
	}
	if m["$schema"] != "https://adaptivecards.io/schemas/adaptive-card.json" {
		t.Errorf("$schema = %v", m["$schema"])
	}

	body, ok := m["body"].([]any)
	if !ok || len(body) != 1 {
		t.Fatalf("body = %v", m["body"])
	}
	text := body[0].(map[string]any)
	if text["size"] != "small" {
		t.Errorf("TextBlock size = %v, want small", text["size"])
	}
	if text["color"] != "accent" {
		t.Errorf("TextBlock color = %v, want accent", text["color"])
	}

	actions, ok := m["actions"].([]any)
	if !ok || len(actions) != 1 {
		t.Fatalf("expected 1 action, got %v", m["actions"])
	}
	action := actions[0].(map[string]any)
	if action["title"] != "Cancel Request" {
		t.Errorf("action title = %v, want Cancel Request", action["title"])
	}
	if action["style"] != "destructive" {
		t.Errorf("action style = %v, want destructive", action["style"])
	}
	actionData := action["data"].(map[string]any)
	if actionData["action"] != CancelButtonActionID {
		t.Errorf("action.data.action = %v", actionData["action"])
	}
	if actionData["taskId"] != "task-123" {
		t.Errorf("action.data.taskId = %v", actionData["taskId"])
	}
	if _, present := actionData["targetAgentName"]; present {
		t.Errorf("targetAgentName should be omitted when empty")
	}
}

func TestStatusCard_WithCancelButton_IncludesTargetAgent(t *testing.T) {
	card := StatusCard("Processing...", "task-1", "MyAgent", true)
	data, _ := json.Marshal(card.Content)
	var m map[string]any
	_ = json.Unmarshal(data, &m)

	actions := m["actions"].([]any)
	actionData := actions[0].(map[string]any)["data"].(map[string]any)
	if actionData["targetAgentName"] != "MyAgent" {
		t.Errorf("targetAgentName = %v, want MyAgent", actionData["targetAgentName"])
	}
}

func TestStatusCard_WithoutCancelButton(t *testing.T) {
	card := StatusCard("Done.", "", "", false)

	data, err := json.Marshal(card.Content)
	if err != nil {
		t.Fatalf("failed to marshal card: %v", err)
	}

	var m map[string]any
	_ = json.Unmarshal(data, &m)

	if _, ok := m["actions"]; ok {
		t.Error("expected no actions when includeCancelButton is false")
	}
}

func TestStatusCard_NoButtonWhenTaskIDMissing(t *testing.T) {
	card := StatusCard("Done.", "", "", true)
	data, _ := json.Marshal(card.Content)
	var m map[string]any
	_ = json.Unmarshal(data, &m)

	if _, ok := m["actions"]; ok {
		t.Error("expected no actions when taskID is empty even with includeCancelButton=true")
	}
}

func TestFileConsentCardAttachmentWithDescription(t *testing.T) {
	ctx := map[string]any{"task_id": "t-1"}
	card := FileConsentCardAttachmentWithDescription("report.pdf", 1024, "📎 **report.pdf** (1.0 KB) is ready to download", ctx)

	if card.ContentType != "application/vnd.microsoft.teams.card.file.consent" {
		t.Errorf("ContentType = %q", card.ContentType)
	}
	if card.Name != "report.pdf" {
		t.Errorf("Name = %q", card.Name)
	}

	content, ok := card.Content.(map[string]any)
	if !ok {
		t.Fatal("Content is not a map")
	}
	if content["sizeInBytes"] != int64(1024) {
		t.Errorf("sizeInBytes = %v", content["sizeInBytes"])
	}
	if !strings.Contains(content["description"].(string), "report.pdf") {
		t.Errorf("description = %v", content["description"])
	}

	accept := content["acceptContext"].(map[string]any)
	if accept["filename"] != "report.pdf" {
		t.Errorf("acceptContext.filename = %v, want report.pdf (auto-added)", accept["filename"])
	}
	if accept["task_id"] != "t-1" {
		t.Errorf("acceptContext.task_id = %v", accept["task_id"])
	}

	if content["declineContext"] == nil {
		t.Error("declineContext missing")
	}
}

func TestFileConsentCardAttachment_DescriptionDefault(t *testing.T) {
	card := FileConsentCardAttachmentWithDescription("file.txt", 5, "", nil)
	content := card.Content.(map[string]any)
	if content["description"] != "File ready to download: file.txt" {
		t.Errorf("description = %v", content["description"])
	}
}

func TestFileInfoCardAttachment(t *testing.T) {
	card := FileInfoCardAttachment("doc.txt", "unique-1", ".txt", "https://example.com/doc.txt")

	if card.ContentType != "application/vnd.microsoft.teams.card.file.info" {
		t.Errorf("ContentType = %q", card.ContentType)
	}
	if card.ContentURL != "https://example.com/doc.txt" {
		t.Errorf("ContentURL = %q", card.ContentURL)
	}
	content := card.Content.(map[string]any)
	if content["uniqueId"] != "unique-1" {
		t.Errorf("uniqueId = %v", content["uniqueId"])
	}
	if content["fileType"] != ".txt" {
		t.Errorf("fileType = %v", content["fileType"])
	}
}

func TestFormatDataPartForTeams(t *testing.T) {
	out := FormatDataPartForTeams(map[string]any{"hello": "world"})
	if !strings.HasPrefix(out, "**Received Data:**\n\n```json\n") {
		t.Errorf("prefix wrong: %q", out)
	}
	if !strings.Contains(out, `"hello": "world"`) {
		t.Errorf("json content missing: %q", out)
	}
	if !strings.HasSuffix(out, "\n```") {
		t.Errorf("suffix wrong: %q", out)
	}

	nested := FormatDataPartForTeams(map[string]any{"outer": map[string]any{"inner": "value"}})
	if !strings.Contains(nested, `"outer"`) || !strings.Contains(nested, `"inner"`) {
		t.Errorf("nested dict missing inner/outer keys: %q", nested)
	}

	bad := FormatDataPartForTeams(map[string]any{"chan": make(chan int)})
	if !strings.Contains(bad, "Error formatting data") {
		t.Errorf("non-serializable payload missing error text: %q", bad)
	}

	empty := FormatDataPartForTeams(map[string]any{})
	if !strings.Contains(empty, "{}") {
		t.Errorf("empty dict missing {}: %q", empty)
	}
}

func TestFormatFileSize(t *testing.T) {
	cases := []struct {
		bytes int64
		want  string
	}{
		{0, "0.0 B"},
		{1, "1.0 B"},
		{1023, "1023.0 B"},
		{500, "500.0 B"},
		{1024, "1.0 KB"},
		{1024 * 1024, "1.0 MB"},
		{1024 * 1024 * 1024, "1.0 GB"},
		{1024 * 1024 * 1024 * 1024, "1.0 TB"},
	}
	for _, c := range cases {
		if got := FormatFileSize(c.bytes); got != c.want {
			t.Errorf("FormatFileSize(%d) = %q, want %q", c.bytes, got, c.want)
		}
	}
}
