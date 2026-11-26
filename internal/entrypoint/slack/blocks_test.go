// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package slack

import (
	"encoding/json"
	"strings"
	"testing"

	goslack "github.com/slack-go/slack"
)

func extractBlockText(t *testing.T, block goslack.Block) string {
	t.Helper()
	b, err := json.Marshal(block)
	if err != nil {
		t.Fatalf("marshal block: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("unmarshal block: %v", err)
	}
	if elems, ok := raw["elements"].([]any); ok && len(elems) > 0 {
		if elem, ok := elems[0].(map[string]any); ok {
			if text, ok := elem["text"].(string); ok {
				return text
			}
		}
	}
	if textObj, ok := raw["text"].(map[string]any); ok {
		if text, ok := textObj["text"].(string); ok {
			return text
		}
	}
	return ""
}

func TestStatusBlocks(t *testing.T) {
	t.Parallel()
	blocks := StatusBlocks("Processing...")

	if len(blocks) != 1 {
		t.Fatalf("got %d blocks, want 1", len(blocks))
	}

	if blocks[0].BlockType() != goslack.MBTContext {
		t.Errorf("block type = %v, want context", blocks[0].BlockType())
	}

	text := extractBlockText(t, blocks[0])
	if text != "Processing..." {
		t.Errorf("text = %q, want %q", text, "Processing...")
	}
}

func TestContentBlocks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		wantText string
	}{
		{"non-empty", "Hello world", "Hello world"},
		{"empty replaced with space", "", " "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			blocks := ContentBlocks(tt.input)
			if len(blocks) != 1 {
				t.Fatalf("got %d blocks, want 1", len(blocks))
			}
			if blocks[0].BlockType() != goslack.MBTSection {
				t.Errorf("block type = %v, want section", blocks[0].BlockType())
			}
			text := extractBlockText(t, blocks[0])
			if text != tt.wantText {
				t.Errorf("text = %q, want %q", text, tt.wantText)
			}
		})
	}
}

func TestBuildBlocks_Combinations(t *testing.T) {
	t.Parallel()

	dummyButton := goslack.NewButtonBlockElement("test_action", "val",
		goslack.NewTextBlockObject(goslack.PlainTextType, "Click", false, false))

	tests := []struct {
		name      string
		status    string
		content   string
		actions   []goslack.BlockElement
		wantCount int
		wantTypes []goslack.MessageBlockType
	}{
		{
			name:      "status only",
			status:    "Working...",
			wantCount: 1,
			wantTypes: []goslack.MessageBlockType{goslack.MBTContext},
		},
		{
			name:      "content only",
			content:   "Here is your answer",
			wantCount: 1,
			wantTypes: []goslack.MessageBlockType{goslack.MBTSection},
		},
		{
			name:      "status and content",
			status:    "Done",
			content:   "Result text",
			wantCount: 2,
			wantTypes: []goslack.MessageBlockType{goslack.MBTContext, goslack.MBTSection},
		},
		{
			name:      "all three",
			status:    "Done",
			content:   "Result",
			actions:   []goslack.BlockElement{dummyButton},
			wantCount: 3,
			wantTypes: []goslack.MessageBlockType{goslack.MBTContext, goslack.MBTSection, goslack.MBTAction},
		},
		{
			name:      "empty inputs",
			wantCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			blocks := BuildBlocks(tt.status, tt.content, tt.actions...)
			if len(blocks) != tt.wantCount {
				t.Fatalf("got %d blocks, want %d", len(blocks), tt.wantCount)
			}
			for i, wantType := range tt.wantTypes {
				if blocks[i].BlockType() != wantType {
					t.Errorf("blocks[%d] type = %v, want %v", i, blocks[i].BlockType(), wantType)
				}
			}
		})
	}
}

func TestFeedbackPayload_RatingOmitEmpty(t *testing.T) {
	t.Parallel()

	noRating := FeedbackPayload{TaskID: "t1", UserID: "u1", SessionID: "s1"}
	data, _ := json.Marshal(noRating)
	raw := make(map[string]any)
	_ = json.Unmarshal(data, &raw)
	if _, exists := raw["rating"]; exists {
		t.Error("rating should be omitted when empty")
	}

	withRating := FeedbackPayload{TaskID: "t1", UserID: "u1", SessionID: "s1", Rating: "up"}
	data, _ = json.Marshal(withRating)
	raw = make(map[string]any)
	_ = json.Unmarshal(data, &raw)
	if raw["rating"] != "up" {
		t.Errorf("rating = %v, want %q", raw["rating"], "up")
	}
}

func TestArtifactDownloadButton(t *testing.T) {
	t.Parallel()
	elem := ArtifactDownloadButton("report.pdf", 3)

	data, err := json.Marshal(elem)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if raw["action_id"] != DownloadArtifactActionID {
		t.Errorf("action_id = %v, want %q", raw["action_id"], DownloadArtifactActionID)
	}

	if textObj, ok := raw["text"].(map[string]any); ok {
		if textObj["text"] != "report.pdf" {
			t.Errorf("button text = %v, want %q", textObj["text"], "report.pdf")
		}
	} else {
		t.Error("missing text object on button")
	}

	val, _ := raw["value"].(string)
	var valObj map[string]any
	if err := json.Unmarshal([]byte(val), &valObj); err != nil {
		t.Fatalf("value is not valid JSON: %v", err)
	}
	if valObj["filename"] != "report.pdf" {
		t.Errorf("value.filename = %v", valObj["filename"])
	}
	if valObj["version"].(float64) != 3 {
		t.Errorf("value.version = %v", valObj["version"])
	}
}

func TestIconForMimeType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		mimeType string
		want     string
	}{
		{"image/png", ":art:"},
		{"image/jpeg", ":art:"},
		{"audio/mpeg", ":sound:"},
		{"video/mp4", ":film_frames:"},
		{"application/pdf", ":page_facing_up:"},
		{"application/zip", ":compression:"},
		{"application/x-tar-archive", ":compression:"},
		{"text/plain", ":page_with_curl:"},
		{"application/json", ":page_with_curl:"},
		{"text/csv", ":page_with_curl:"},
		{"", ":page_facing_up:"},
		{"application/octet-stream", ":page_facing_up:"},
	}

	for _, tt := range tests {
		t.Run(tt.mimeType, func(t *testing.T) {
			t.Parallel()
			got := IconForMimeType(tt.mimeType)
			if got != tt.want {
				t.Errorf("IconForMimeType(%q) = %q, want %q", tt.mimeType, got, tt.want)
			}
		})
	}
}

func TestNativeFeedbackModal_CommentInputIsMultilineAndBounded(t *testing.T) {
	modal := NativeFeedbackModal(FeedbackPayload{TaskID: "t", SessionID: "s", Rating: "down"}, "C1", "1700000000.000100")

	var input *goslack.PlainTextInputBlockElement
	for _, b := range modal.Blocks.BlockSet {
		ib, ok := b.(*goslack.InputBlock)
		if !ok || ib.BlockID != FeedbackCommentBlockID {
			continue
		}
		input, _ = ib.Element.(*goslack.PlainTextInputBlockElement)
	}
	if input == nil {
		t.Fatal("comment input block not found")
	}
	if !input.Multiline {
		t.Error("comment input is single-line; it must be multiline")
	}
	if input.MaxLength != feedbackCommentMaxLength {
		t.Errorf("MaxLength = %d, want %d", input.MaxLength, feedbackCommentMaxLength)
	}
}

func TestNativeFeedbackModal_CarriesTheDataUseNotice(t *testing.T) {
	const want = "This conversation may be viewed to help improve responses"

	modal := NativeFeedbackModal(FeedbackPayload{TaskID: "t", SessionID: "s", Rating: "down"}, "C1", "1700000000.000100")

	var found string
	for _, b := range modal.Blocks.BlockSet {
		cb, ok := b.(*goslack.ContextBlock)
		if !ok || cb.BlockID != FeedbackNoticeBlockID {
			continue
		}
		found = extractBlockText(t, cb)
	}
	if found != want {
		t.Errorf("notice = %q, want %q", found, want)
	}
}

func TestNativeFeedbackModal_HasNoConsentCheckbox(t *testing.T) {
	modal := NativeFeedbackModal(FeedbackPayload{TaskID: "t", SessionID: "s", Rating: "down"}, "C1", "1700000000.000100")

	var inputs int
	for _, b := range modal.Blocks.BlockSet {
		ib, ok := b.(*goslack.InputBlock)
		if !ok {
			continue
		}
		inputs++
		if _, ok := ib.Element.(*goslack.CheckboxGroupsBlockElement); ok {
			t.Errorf("block %q still renders a checkbox", ib.BlockID)
		}
	}
	if inputs == 0 {
		t.Fatal("modal has no input blocks; the checkbox scan proved nothing")
	}
}

func TestNativeFeedbackModal_CommentInputIsOptional(t *testing.T) {
	modal := NativeFeedbackModal(FeedbackPayload{TaskID: "t", SessionID: "s", Rating: "down"}, "C1", "1700000000.000100")

	var seen bool
	for _, b := range modal.Blocks.BlockSet {
		ib, ok := b.(*goslack.InputBlock)
		if !ok || ib.BlockID != FeedbackCommentBlockID {
			continue
		}
		seen = true
		if !ib.Optional {
			t.Error("comment input is required; it must be optional")
		}
	}
	if !seen {
		t.Fatal("comment input block not found")
	}
}

func TestNativeFeedbackModal_DoesNotAskForCloseNotifications(t *testing.T) {
	modal := NativeFeedbackModal(FeedbackPayload{TaskID: "t", SessionID: "s", Rating: "down"}, "C1", "1700000000.000100")

	if modal.NotifyOnClose {
		t.Error("NotifyOnClose is true; a dismissal has nothing to report")
	}
}

func TestNativeFeedbackButtons_OversizedPayloadYieldsNoBlocks(t *testing.T) {
	oversized := strings.Repeat("s", 2100)

	if got := NativeFeedbackButtons("task-1", "", oversized); got != nil {
		t.Errorf("expected no blocks for an oversized payload, got %d", len(got))
	}
}

func TestNativeFeedbackButtons_CarriesRatingPerButton(t *testing.T) {
	blocks := NativeFeedbackButtons("task-1", "", "session-1")
	if len(blocks) != 1 {
		t.Fatalf("expected one block, got %d", len(blocks))
	}
	ca, ok := blocks[0].(*goslack.ContextActionsBlock)
	if !ok {
		t.Fatalf("block is %T, want a context actions block", blocks[0])
	}
	if ca.BlockID != NativeFeedbackBlockID {
		t.Errorf("block id = %q, want %q", ca.BlockID, NativeFeedbackBlockID)
	}
	el, ok := ca.Elements.ElementSet[0].(*goslack.FeedbackButtonsBlockElement)
	if !ok {
		t.Fatalf("element is %T, want feedback buttons", ca.Elements.ElementSet[0])
	}

	for _, tc := range []struct {
		name  string
		value string
		want  string
	}{
		{"positive", el.PositiveButton.Value, feedbackRatingUp},
		{"negative", el.NegativeButton.Value, feedbackRatingDown},
	} {
		var got FeedbackPayload
		if err := json.Unmarshal([]byte(tc.value), &got); err != nil {
			t.Fatalf("%s button value is not a payload: %v", tc.name, err)
		}
		if got.Rating != tc.want {
			t.Errorf("%s button rating = %q, want %q", tc.name, got.Rating, tc.want)
		}
		if got.TaskID != "task-1" {
			t.Errorf("%s button task = %q", tc.name, got.TaskID)
		}
	}
}
