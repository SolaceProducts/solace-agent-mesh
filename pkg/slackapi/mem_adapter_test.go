// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package slackapi

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	goslack "github.com/slack-go/slack"
)

func TestPostMessage_RecordsCall(t *testing.T) {
	api := NewInMemorySlackAPI()
	_, ts, err := api.PostMessage(t.Context(), "C-test",
		goslack.MsgOptionText("hello", false),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ts == "" {
		t.Fatal("expected non-empty ts")
	}

	calls := api.Calls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].Method != "PostMessage" {
		t.Errorf("method = %q, want PostMessage", calls[0].Method)
	}
	if calls[0].Channel != "C-test" {
		t.Errorf("channel = %q, want C-test", calls[0].Channel)
	}
	if calls[0].Text != "hello" {
		t.Errorf("text = %q, want hello", calls[0].Text)
	}
}

func TestPostMessage_WithBlocks(t *testing.T) {
	api := NewInMemorySlackAPI()

	var blocks goslack.Blocks
	blocks.BlockSet = append(blocks.BlockSet, goslack.NewSectionBlock(
		goslack.NewTextBlockObject(goslack.MarkdownType, "*bold*", false, false),
		nil, nil,
	))

	_, _, err := api.PostMessage(t.Context(), "C-test",
		goslack.MsgOptionText("fallback", false),
		goslack.MsgOptionBlocks(blocks.BlockSet...),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	calls := api.Calls()
	if calls[0].Blocks == "" {
		t.Error("expected non-empty blocks")
	}
}

func TestPostMessage_WithThreadTS(t *testing.T) {
	api := NewInMemorySlackAPI()
	_, _, err := api.PostMessage(t.Context(), "C-test",
		goslack.MsgOptionText("reply", false),
		goslack.MsgOptionTS("1700000000.000001"),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	calls := api.Calls()
	if calls[0].ThreadTS != "1700000000.000001" {
		t.Errorf("thread_ts = %q, want 1700000000.000001", calls[0].ThreadTS)
	}
}

func TestUpdateMessage_RecordsCall(t *testing.T) {
	api := NewInMemorySlackAPI()
	api.SeedMessage("C-test", "1700000000.000001", "original")

	_, respTS, respText, err := api.UpdateMessage(t.Context(), "C-test", "1700000000.000001",
		goslack.MsgOptionText("updated", false),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if respTS != "1700000000.000001" {
		t.Errorf("respTS = %q, want 1700000000.000001", respTS)
	}
	if respText != "updated" {
		t.Errorf("respText = %q, want updated", respText)
	}

	calls := api.Calls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].Method != "UpdateMessage" {
		t.Errorf("method = %q, want UpdateMessage", calls[0].Method)
	}
}

func TestDeleteMessage_RecordsCall(t *testing.T) {
	api := NewInMemorySlackAPI()
	api.SeedMessage("C-test", "1700000000.000001", "to-delete")

	_, _, err := api.DeleteMessage(t.Context(), "C-test", "1700000000.000001")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	calls := api.Calls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].Method != "DeleteMessage" {
		t.Errorf("method = %q, want DeleteMessage", calls[0].Method)
	}
	if calls[0].Channel != "C-test" {
		t.Errorf("channel = %q, want C-test", calls[0].Channel)
	}
}

func TestGetUserProfile_Seeded(t *testing.T) {
	api := NewInMemorySlackAPI()
	api.SeedUserProfile("U-test", &goslack.UserProfile{
		Email:    "test@example.com",
		RealName: "Test User",
	})

	profile, err := api.GetUserProfile(t.Context(), &goslack.GetUserProfileParameters{UserID: "U-test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if profile.Email != "test@example.com" {
		t.Errorf("email = %q, want test@example.com", profile.Email)
	}
}

func TestGetUserProfile_Unknown(t *testing.T) {
	api := NewInMemorySlackAPI()
	_, err := api.GetUserProfile(t.Context(), &goslack.GetUserProfileParameters{UserID: "U-unknown"})
	if err == nil {
		t.Fatal("expected error for unknown user")
	}
}

func TestUploadFlow(t *testing.T) {
	api := NewInMemorySlackAPI()

	resp, err := api.GetUploadURLExternal(t.Context(), goslack.GetUploadURLExternalParameters{
		FileName: "test.txt",
		FileSize: 5,
	})
	if err != nil {
		t.Fatalf("GetUploadURLExternal: %v", err)
	}
	if resp.UploadURL == "" || resp.FileID == "" {
		t.Fatal("expected non-empty upload URL and file ID")
	}

	if uploadErr := api.Upload(t.Context(), resp.UploadURL, "test.txt", []byte("hello")); uploadErr != nil {
		t.Fatalf("Upload: %v", uploadErr)
	}

	_, err = api.CompleteUploadExternal(t.Context(), goslack.CompleteUploadExternalParameters{
		Files:   []goslack.FileSummary{{ID: resp.FileID, Title: "test.txt"}},
		Channel: "C-test",
	})
	if err != nil {
		t.Fatalf("CompleteUploadExternal: %v", err)
	}

	calls := api.Calls()
	if len(calls) != 3 {
		t.Fatalf("expected 3 calls, got %d", len(calls))
	}
	if calls[0].Method != "GetUploadURLExternal" {
		t.Errorf("calls[0].Method = %q, want GetUploadURLExternal", calls[0].Method)
	}
	if calls[1].Method != "Upload" {
		t.Errorf("calls[1].Method = %q, want Upload", calls[1].Method)
	}
	if calls[2].Method != "CompleteUploadExternal" {
		t.Errorf("calls[2].Method = %q, want CompleteUploadExternal", calls[2].Method)
	}
}

func TestGetFile_Seeded(t *testing.T) {
	api := NewInMemorySlackAPI()
	api.SeedFile("F-test", []byte("file content"))

	var buf bytes.Buffer
	err := api.GetFile(t.Context(), "F-test", &buf)
	if err != nil {
		t.Fatalf("GetFile: %v", err)
	}
	if buf.String() != "file content" {
		t.Errorf("content = %q, want %q", buf.String(), "file content")
	}
}

func TestGetFile_Unknown(t *testing.T) {
	api := NewInMemorySlackAPI()
	var buf bytes.Buffer
	err := api.GetFile(t.Context(), "F-missing", &buf)
	if err == nil {
		t.Fatal("expected error for unknown file")
	}
}

func TestGetFileInfo_Seeded(t *testing.T) {
	api := NewInMemorySlackAPI()
	api.SeedFile("F-test", []byte("content"))

	info, err := api.GetFileInfo(t.Context(), "F-test")
	if err != nil {
		t.Fatalf("GetFileInfo: %v", err)
	}
	if info.ID != "F-test" {
		t.Errorf("ID = %q, want F-test", info.ID)
	}
}

func TestGetFileInfo_Unknown(t *testing.T) {
	api := NewInMemorySlackAPI()
	_, err := api.GetFileInfo(t.Context(), "F-missing")
	if err == nil {
		t.Fatal("expected error for unknown file")
	}
}

func TestSetNextError_OneShot(t *testing.T) {
	api := NewInMemorySlackAPI()
	api.SetNextError("PostMessage", fmt.Errorf("channel_not_found"))

	_, _, err := api.PostMessage(t.Context(), "C-test", goslack.MsgOptionText("x", false))
	if err == nil || err.Error() != "channel_not_found" {
		t.Fatalf("expected channel_not_found error, got: %v", err)
	}

	_, _, err = api.PostMessage(t.Context(), "C-test", goslack.MsgOptionText("x", false))
	if err != nil {
		t.Fatalf("expected no error on second call, got: %v", err)
	}
}

func TestSetNextError_CrossMethodIsolation(t *testing.T) {
	api := NewInMemorySlackAPI()
	api.SetNextError("DeleteMessage", fmt.Errorf("not_allowed"))

	_, _, err := api.PostMessage(t.Context(), "C-test", goslack.MsgOptionText("x", false))
	if err != nil {
		t.Fatalf("PostMessage should not be affected: %v", err)
	}

	_, _, err = api.DeleteMessage(t.Context(), "C-test", "ts")
	if err == nil || err.Error() != "not_allowed" {
		t.Fatalf("expected not_allowed error, got: %v", err)
	}
}

func TestReset_ClearsCallsAndCounters(t *testing.T) {
	api := NewInMemorySlackAPI()
	_, _, _ = api.PostMessage(t.Context(), "C-test", goslack.MsgOptionText("x", false))

	if len(api.Calls()) != 1 {
		t.Fatal("expected 1 call before reset")
	}

	api.Reset()
	if len(api.Calls()) != 0 {
		t.Fatal("expected 0 calls after reset")
	}

	_, ts, _ := api.PostMessage(t.Context(), "C-test", goslack.MsgOptionText("x", false))
	if ts != "1700000000.000001" {
		t.Errorf("ts = %q after reset, want 1700000000.000001", ts)
	}
}

func TestUpdateMessage_NotFound(t *testing.T) {
	api := NewInMemorySlackAPI()
	_, _, _, err := api.UpdateMessage(t.Context(), "C-test", "1700000000.999999",
		goslack.MsgOptionText("nope", false),
	)
	if err == nil {
		t.Fatal("expected error for nonexistent message")
	}
	if err.Error() != "message_not_found" {
		t.Errorf("error = %q, want message_not_found", err.Error())
	}
}

func TestDeleteMessage_NotFound(t *testing.T) {
	api := NewInMemorySlackAPI()
	_, _, err := api.DeleteMessage(t.Context(), "C-test", "1700000000.999999")
	if err == nil {
		t.Fatal("expected error for nonexistent message")
	}
	if err.Error() != "message_not_found" {
		t.Errorf("error = %q, want message_not_found", err.Error())
	}
}

func TestPostThenUpdateThenRead(t *testing.T) {
	api := NewInMemorySlackAPI()

	_, ts, err := api.PostMessage(t.Context(), "C-test",
		goslack.MsgOptionText("original", false),
	)
	if err != nil {
		t.Fatalf("PostMessage: %v", err)
	}

	_, _, _, err = api.UpdateMessage(t.Context(), "C-test", ts,
		goslack.MsgOptionText("edited", false),
	)
	if err != nil {
		t.Fatalf("UpdateMessage: %v", err)
	}

	msgs := api.Messages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 stored message, got %d", len(msgs))
	}
	if msgs[0].Text != "edited" {
		t.Errorf("stored text = %q, want edited", msgs[0].Text)
	}
}

func TestPostThenDelete(t *testing.T) {
	api := NewInMemorySlackAPI()

	_, ts, err := api.PostMessage(t.Context(), "C-test",
		goslack.MsgOptionText("ephemeral", false),
	)
	if err != nil {
		t.Fatalf("PostMessage: %v", err)
	}

	_, _, err = api.DeleteMessage(t.Context(), "C-test", ts)
	if err != nil {
		t.Fatalf("DeleteMessage: %v", err)
	}

	if len(api.Messages()) != 0 {
		t.Error("expected 0 stored messages after delete")
	}

	_, _, err = api.DeleteMessage(t.Context(), "C-test", ts)
	if err == nil {
		t.Fatal("expected error on double delete")
	}
}

func TestUploadFlow_ContentRetrieval(t *testing.T) {
	api := NewInMemorySlackAPI()
	content := []byte("file-content-here")

	resp, err := api.GetUploadURLExternal(t.Context(), goslack.GetUploadURLExternalParameters{
		FileName: "report.csv",
		FileSize: len(content),
	})
	if err != nil {
		t.Fatalf("GetUploadURLExternal: %v", err)
	}

	if err = api.Upload(t.Context(), resp.UploadURL, "report.csv", content); err != nil {
		t.Fatalf("Upload: %v", err)
	}

	_, err = api.CompleteUploadExternal(t.Context(), goslack.CompleteUploadExternalParameters{
		Files:          []goslack.FileSummary{{ID: resp.FileID, Title: "report.csv"}},
		Channel:        "C-test",
		InitialComment: "Here is the report",
	})
	if err != nil {
		t.Fatalf("CompleteUploadExternal: %v", err)
	}

	var buf bytes.Buffer
	if err = api.GetFile(t.Context(), resp.FileID, &buf); err != nil {
		t.Fatalf("GetFile: %v", err)
	}
	if buf.String() != "file-content-here" {
		t.Errorf("file content = %q, want file-content-here", buf.String())
	}
}

func TestUpload_UnknownURL(t *testing.T) {
	api := NewInMemorySlackAPI()
	err := api.Upload(t.Context(), "https://bogus.slack.com/nope", "x.txt", []byte("data"))
	if err == nil {
		t.Fatal("expected error for unknown upload URL")
	}
	if !bytes.Contains([]byte(err.Error()), []byte("unknown upload URL")) {
		t.Errorf("error = %q, want 'unknown upload URL'", err.Error())
	}
}

func TestUpload_EmptyFilename_OnFake(t *testing.T) {
	api := NewInMemorySlackAPI()
	err := api.Upload(t.Context(), "https://fake/x", "", []byte("data"))
	if err == nil {
		t.Fatal("expected error for empty filename, got nil")
	}
	if !bytes.Contains([]byte(err.Error()), []byte("filename is required")) {
		t.Errorf("error = %q, want it to mention 'filename is required'", err.Error())
	}
}

func TestCompleteUpload_UnknownFile(t *testing.T) {
	api := NewInMemorySlackAPI()
	_, err := api.CompleteUploadExternal(t.Context(), goslack.CompleteUploadExternalParameters{
		Files:   []goslack.FileSummary{{ID: "F-nonexistent", Title: "ghost.txt"}},
		Channel: "C-test",
	})
	if err == nil {
		t.Fatal("expected error for un-uploaded file")
	}
	if !bytes.Contains([]byte(err.Error()), []byte("file_not_found")) {
		t.Errorf("error = %q, want 'file_not_found'", err.Error())
	}
}

func TestGetFileInfo_ReturnsUploadedFilename(t *testing.T) {
	api := NewInMemorySlackAPI()

	resp, err := api.GetUploadURLExternal(t.Context(), goslack.GetUploadURLExternalParameters{
		FileName: "quarterly-report.xlsx",
		FileSize: 100,
	})
	if err != nil {
		t.Fatalf("GetUploadURLExternal: %v", err)
	}

	if err = api.Upload(t.Context(), resp.UploadURL, "quarterly-report.xlsx", make([]byte, 100)); err != nil {
		t.Fatalf("Upload: %v", err)
	}

	_, err = api.CompleteUploadExternal(t.Context(), goslack.CompleteUploadExternalParameters{
		Files:   []goslack.FileSummary{{ID: resp.FileID, Title: "quarterly-report.xlsx"}},
		Channel: "C-test",
	})
	if err != nil {
		t.Fatalf("CompleteUploadExternal: %v", err)
	}

	info, err := api.GetFileInfo(t.Context(), resp.FileID)
	if err != nil {
		t.Fatalf("GetFileInfo: %v", err)
	}
	if info.Name != "quarterly-report.xlsx" {
		t.Errorf("filename = %q, want quarterly-report.xlsx", info.Name)
	}
	if info.Size != 100 {
		t.Errorf("size = %d, want 100", info.Size)
	}
}

func TestCompleteUpload_CreatesMessage(t *testing.T) {
	api := NewInMemorySlackAPI()

	resp, err := api.GetUploadURLExternal(t.Context(), goslack.GetUploadURLExternalParameters{
		FileName: "doc.txt",
		FileSize: 4,
	})
	if err != nil {
		t.Fatalf("GetUploadURLExternal: %v", err)
	}

	if err = api.Upload(t.Context(), resp.UploadURL, "doc.txt", []byte("data")); err != nil {
		t.Fatalf("Upload: %v", err)
	}

	_, err = api.CompleteUploadExternal(t.Context(), goslack.CompleteUploadExternalParameters{
		Files:          []goslack.FileSummary{{ID: resp.FileID, Title: "doc.txt"}},
		Channel:        "C-files",
		InitialComment: "Here you go",
	})
	if err != nil {
		t.Fatalf("CompleteUploadExternal: %v", err)
	}

	msgs := api.Messages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 stored message from upload, got %d", len(msgs))
	}
	if msgs[0].Channel != "C-files" {
		t.Errorf("channel = %q, want C-files", msgs[0].Channel)
	}
	if msgs[0].Text != "Here you go" {
		t.Errorf("text = %q, want 'Here you go'", msgs[0].Text)
	}
}

func TestSeedMessage_AllowsUpdate(t *testing.T) {
	api := NewInMemorySlackAPI()
	api.SeedMessage("C-test", "1700000000.000099", "seeded")

	_, _, respText, err := api.UpdateMessage(t.Context(), "C-test", "1700000000.000099",
		goslack.MsgOptionText("updated-seeded", false),
	)
	if err != nil {
		t.Fatalf("UpdateMessage on seeded message: %v", err)
	}
	if respText != "updated-seeded" {
		t.Errorf("respText = %q, want updated-seeded", respText)
	}

	msgs := api.Messages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].Text != "updated-seeded" {
		t.Errorf("stored text = %q, want updated-seeded", msgs[0].Text)
	}
}

func TestConcurrentTSUniqueness(t *testing.T) {
	api := NewInMemorySlackAPI()
	var wg sync.WaitGroup
	timestamps := make(chan string, 50)

	for range 50 {
		wg.Go(func() {
			_, ts, _ := api.PostMessage(t.Context(), "C-test", goslack.MsgOptionText("x", false))
			timestamps <- ts
		})
	}
	wg.Wait()
	close(timestamps)

	seen := make(map[string]bool)
	for ts := range timestamps {
		if seen[ts] {
			t.Fatalf("duplicate timestamp: %s", ts)
		}
		seen[ts] = true
	}
	if len(seen) != 50 {
		t.Errorf("expected 50 unique timestamps, got %d", len(seen))
	}
}

func TestInMemoryOpenView_RecordsTheModal(t *testing.T) {
	api := NewInMemorySlackAPI()

	view := goslack.ModalViewRequest{
		Type:            goslack.VTModal,
		CallbackID:      "feedback_modal",
		PrivateMetadata: `{"task_id":"t-1"}`,
		Title:           goslack.NewTextBlockObject(goslack.PlainTextType, "Feedback", false, false),
	}
	resp, err := api.OpenView(context.Background(), "trigger-1", view)
	if err != nil {
		t.Fatalf("OpenView: %v", err)
	}
	if resp == nil || resp.CallbackID != "feedback_modal" {
		t.Fatalf("response did not echo the callback id: %+v", resp)
	}

	calls := api.Calls()
	if len(calls) != 1 || calls[0].Method != "OpenView" {
		t.Fatalf("calls = %+v, want one OpenView", calls)
	}
	if got := calls[0].Params["triggerID"]; got != "trigger-1" {
		t.Errorf("triggerID = %q, want trigger-1", got)
	}
	if got := calls[0].Params["callbackID"]; got != "feedback_modal" {
		t.Errorf("callbackID = %q, want feedback_modal", got)
	}
	if !strings.Contains(string(calls[0].Body), "feedback_modal") {
		t.Errorf("recorded body missing the view: %s", calls[0].Body)
	}
}

func TestInMemoryOpenView_ErrorInjection(t *testing.T) {
	api := NewInMemorySlackAPI()
	api.SetNextError("OpenView", fmt.Errorf("expired_trigger_id"))

	if _, err := api.OpenView(context.Background(), "trigger-1", goslack.ModalViewRequest{}); err == nil {
		t.Fatal("expected the injected error")
	}
	if _, err := api.OpenView(context.Background(), "trigger-2", goslack.ModalViewRequest{}); err != nil {
		t.Fatalf("second call should succeed: %v", err)
	}
}

func TestInMemoryPostEphemeral_RecordsRecipient(t *testing.T) {
	api := NewInMemorySlackAPI()

	ts, err := api.PostEphemeral(context.Background(), "C1", "U1",
		goslack.MsgOptionText("only you can see this", false))
	if err != nil {
		t.Fatalf("PostEphemeral: %v", err)
	}
	if ts == "" {
		t.Error("no timestamp returned")
	}

	calls := api.Calls()
	if len(calls) != 1 || calls[0].Method != "PostEphemeral" {
		t.Fatalf("calls = %+v, want one PostEphemeral", calls)
	}
	if calls[0].Channel != "C1" {
		t.Errorf("channel = %q, want C1", calls[0].Channel)
	}
	if got := calls[0].Params["user"]; got != "U1" {
		t.Errorf("user = %q, want U1", got)
	}
	if calls[0].Text != "only you can see this" {
		t.Errorf("text = %q", calls[0].Text)
	}
}

func TestInMemoryPostEphemeral_IsNotAStoredMessage(t *testing.T) {
	api := NewInMemorySlackAPI()

	ts, err := api.PostEphemeral(context.Background(), "C1", "U1", goslack.MsgOptionText("hi", false))
	if err != nil {
		t.Fatalf("PostEphemeral: %v", err)
	}
	if _, _, _, err := api.UpdateMessage(context.Background(), "C1", ts,
		goslack.MsgOptionText("edited", false)); err == nil {
		t.Error("an ephemeral must not be updatable")
	}
}

func TestInMemoryPostEphemeral_ErrorInjection(t *testing.T) {
	api := NewInMemorySlackAPI()
	api.SetNextError("PostEphemeral", fmt.Errorf("channel_not_found"))

	if _, err := api.PostEphemeral(context.Background(), "C1", "U1"); err == nil {
		t.Fatal("expected the injected error")
	}
}

func TestUpdateMessage_HistoryReportsTheEdit(t *testing.T) {
	api := NewInMemorySlackAPI()
	api.SeedConversationMessage("C-test", "1700000000.000001", "1700000000.000001", "U1", "draft")
	before := time.Now().Unix()

	if _, _, _, err := api.UpdateMessage(t.Context(), "C-test", "1700000000.000001", goslack.MsgOptionText("final", false)); err != nil {
		t.Fatalf("UpdateMessage: %v", err)
	}
	msgs, err := api.GetConversationReplies(t.Context(), "C-test", "1700000000.000001", 10)
	if err != nil || len(msgs) != 1 {
		t.Fatalf("GetConversationReplies = %+v, %v", msgs, err)
	}
	got := msgs[0]
	if got.Timestamp != "1700000000.000001" || got.Text != "final" {
		t.Errorf("message = ts %q text %q, want the original ts with the new text", got.Timestamp, got.Text)
	}
	if got.Edited == nil {
		t.Fatal("an updated message carries no edit")
	}
	var editedAt int64
	if _, err := fmt.Sscanf(got.Edited.Timestamp, "%d.", &editedAt); err != nil || editedAt < before {
		t.Errorf("edited.ts = %q, want the time of the update (>= %d)", got.Edited.Timestamp, before)
	}
}
