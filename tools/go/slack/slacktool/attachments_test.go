// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package slacktool

import (
	"fmt"
	"strings"
	"testing"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/slackapi"
)

func art(name, content string) sdk.Artifact {
	return sdk.Artifact{
		Filename: name,
		Content:  []byte(content),
		MIMEType: "application/octet-stream",
	}
}

func callsByMethod(calls []slackapi.RecordedCall, method string) []slackapi.RecordedCall {
	var out []slackapi.RecordedCall
	for _, c := range calls {
		if c.Method == method {
			out = append(out, c)
		}
	}
	return out
}

func TestSendMessageWithAttachments_BlocksRejected(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()

	params := SendMessageParams{
		Channel:     "C-test",
		Text:        "Hello",
		Blocks:      strPtr(`[{"type":"section"}]`),
		Attachments: []sdk.Artifact{art("f.txt", "data")},
	}

	result, err := postSlackMessageWithAttachments(t.Context(), api, params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "error" {
		t.Fatalf("expected error, got %q: %s", result.Status, result.Message)
	}
	if !strings.Contains(result.Message, "blocks and attachments") {
		t.Errorf("error should mention blocks and attachments, got: %s", result.Message)
	}
}

func TestSendMessageWithAttachments_MissingChannel(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()

	params := SendMessageParams{
		Text:        "Upload this",
		Attachments: []sdk.Artifact{art("f.txt", "data")},
	}

	result, err := postSlackMessageWithAttachments(t.Context(), api, params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "error" {
		t.Fatalf("expected error, got %q: %s", result.Status, result.Message)
	}
	if !strings.Contains(result.Message, "channel is required") {
		t.Errorf("error should mention channel is required, got: %s", result.Message)
	}
}

func TestSendMessageWithAttachments_MarkdownConvertedForInitialComment(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()

	params := SendMessageParams{
		Channel:     "C-test",
		Text:        "**bold** and [link](https://x.com)",
		Attachments: []sdk.Artifact{art("f.txt", "data")},
	}

	result, err := postSlackMessageWithAttachments(t.Context(), api, params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("expected success, got %q: %s", result.Status, result.Message)
	}

	completeCalls := callsByMethod(api.Calls(), "CompleteUploadExternal")
	if len(completeCalls) != 1 {
		t.Fatalf("expected 1 CompleteUploadExternal, got %d", len(completeCalls))
	}
	comment := completeCalls[0].Params["comment"]
	if !strings.Contains(comment, "*bold*") {
		t.Errorf("comment should contain *bold*, got: %s", comment)
	}
	if !strings.Contains(comment, "<https://x.com|link>") {
		t.Errorf("comment should contain Slack link, got: %s", comment)
	}
}

func TestUploadAttachments_SingleAttachment(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()

	result, err := uploadAttachments(t.Context(), api, "C-test", "", "hello", []sdk.Artifact{art("doc.txt", "content")})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("expected success, got %q: %s", result.Status, result.Message)
	}

	calls := api.Calls()
	getURLCalls := callsByMethod(calls, "GetUploadURLExternal")
	uploadCalls := callsByMethod(calls, "Upload")
	completeCalls := callsByMethod(calls, "CompleteUploadExternal")

	if len(getURLCalls) != 1 {
		t.Errorf("expected 1 GetUploadURLExternal call, got %d", len(getURLCalls))
	}
	if len(uploadCalls) != 1 {
		t.Errorf("expected 1 Upload call, got %d", len(uploadCalls))
	}
	if len(completeCalls) != 1 {
		t.Errorf("expected 1 CompleteUploadExternal call, got %d", len(completeCalls))
	}
	if completeCalls[0].Channel != "C-test" {
		t.Errorf("complete channel = %q, want C-test", completeCalls[0].Channel)
	}
	if completeCalls[0].Params["comment"] != "hello" {
		t.Errorf("complete comment = %q, want hello", completeCalls[0].Params["comment"])
	}
}

func TestUploadAttachments_MultipleSequential(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()

	atts := make([]sdk.Artifact, 3)
	for i := range 3 {
		atts[i] = art(fmt.Sprintf("file%d.txt", i), fmt.Sprintf("content-%d", i))
	}

	result, err := uploadAttachments(t.Context(), api, "C-test", "", "shared comment", atts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("expected success, got %q: %s", result.Status, result.Message)
	}

	calls := api.Calls()
	getURLCalls := callsByMethod(calls, "GetUploadURLExternal")
	uploadCalls := callsByMethod(calls, "Upload")
	completeCalls := callsByMethod(calls, "CompleteUploadExternal")

	if len(getURLCalls) != 3 {
		t.Errorf("expected 3 GetUploadURLExternal calls, got %d", len(getURLCalls))
	}
	if len(uploadCalls) != 3 {
		t.Errorf("expected 3 Upload calls, got %d", len(uploadCalls))
	}
	if len(completeCalls) != 3 {
		t.Errorf("expected 3 CompleteUploadExternal calls (one per file), got %d", len(completeCalls))
	}

	if completeCalls[0].Params["comment"] != "shared comment" {
		t.Errorf("first file comment = %q, want %q", completeCalls[0].Params["comment"], "shared comment")
	}
	for i := 1; i < len(completeCalls); i++ {
		got := completeCalls[i].Params["comment"]
		if got == "shared comment" {
			t.Errorf("file %d should not have initialComment, got %q", i, got)
		}
	}

	fileCount, ok := result.Data["file_count"].(int)
	if !ok || fileCount != 3 {
		t.Errorf("file_count = %v, want 3", result.Data["file_count"])
	}
}

func TestUploadAttachments_ThreadedReply(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()

	threadTS := "1700000000.000042"
	result, err := uploadAttachments(t.Context(), api, "C-test", threadTS, "", []sdk.Artifact{art("a.txt", "x")})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("expected success, got %q: %s", result.Status, result.Message)
	}

	completeCalls := callsByMethod(api.Calls(), "CompleteUploadExternal")
	if len(completeCalls) != 1 {
		t.Fatalf("expected 1 CompleteUploadExternal call, got %d", len(completeCalls))
	}
	if completeCalls[0].ThreadTS != threadTS {
		t.Errorf("ThreadTS = %q, want %q", completeCalls[0].ThreadTS, threadTS)
	}
	if completeCalls[0].Params["thread_ts"] != threadTS {
		t.Errorf("Params[thread_ts] = %q, want %q", completeCalls[0].Params["thread_ts"], threadTS)
	}

	if result.Data["thread_ts"] != threadTS {
		t.Errorf("result thread_ts = %v, want %q", result.Data["thread_ts"], threadTS)
	}
}

func TestUploadAttachments_GetUploadURLFails(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()
	api.SetNextError("GetUploadURLExternal", fmt.Errorf("rate_limited"))

	result, err := uploadAttachments(t.Context(), api, "C-test", "", "", []sdk.Artifact{art("f.txt", "data")})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "error" {
		t.Fatalf("expected error, got %q: %s", result.Status, result.Message)
	}
	if !strings.Contains(result.Message, "rate_limited") {
		t.Errorf("error message should mention rate_limited, got: %s", result.Message)
	}
}

func TestUploadAttachments_UploadFails(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()
	api.SetNextError("Upload", fmt.Errorf("connection_reset"))

	result, err := uploadAttachments(t.Context(), api, "C-test", "", "", []sdk.Artifact{art("f.txt", "data")})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "error" {
		t.Fatalf("expected error, got %q: %s", result.Status, result.Message)
	}
	if !strings.Contains(result.Message, "connection_reset") {
		t.Errorf("error message should mention connection_reset, got: %s", result.Message)
	}
}

func TestUploadAttachments_CompleteUploadFails(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()
	api.SetNextError("CompleteUploadExternal", fmt.Errorf("channel_not_found"))

	result, err := uploadAttachments(t.Context(), api, "C-test", "", "", []sdk.Artifact{art("f.txt", "data")})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "error" {
		t.Fatalf("expected error, got %q: %s", result.Status, result.Message)
	}
	if !strings.Contains(result.Message, "complete upload") {
		t.Errorf("error message should mention 'complete upload', got: %s", result.Message)
	}
}

func TestUploadAttachments_EmptyFilenameRejected(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()

	unnamed := sdk.Artifact{Content: []byte("data"), MIMEType: "application/octet-stream"}
	result, err := uploadAttachments(t.Context(), api, "C-test", "", "", []sdk.Artifact{unnamed})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "error" {
		t.Fatalf("expected error, got %q: %s", result.Status, result.Message)
	}
	if !strings.Contains(result.Message, "empty filename") {
		t.Errorf("error should mention empty filename, got: %s", result.Message)
	}
	if len(callsByMethod(api.Calls(), "GetUploadURLExternal")) != 0 {
		t.Error("no Slack upload calls should have been made for empty filename")
	}
}

func TestUploadAttachments_PartialFailure(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()

	atts := []sdk.Artifact{
		art("will-fail.txt", "content-1"),
		art("ok1.txt", "content-2"),
		art("ok2.txt", "content-3"),
	}

	api.SetNextError("GetUploadURLExternal", fmt.Errorf("rate_limited"))

	result, err := uploadAttachments(t.Context(), api, "C-test", "", "", atts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "error" {
		t.Fatalf("expected error status for partial failure, got %q: %s", result.Status, result.Message)
	}
	if !strings.Contains(result.Message, "1 of 3") {
		t.Errorf("error should mention '1 of 3', got: %s", result.Message)
	}
	if !strings.Contains(result.Message, "2 succeeded") {
		t.Errorf("error should mention '2 succeeded', got: %s", result.Message)
	}
	if !strings.Contains(result.Message, "will-fail.txt") {
		t.Errorf("error should mention the failed filename, got: %s", result.Message)
	}

	completeCalls := callsByMethod(api.Calls(), "CompleteUploadExternal")
	if len(completeCalls) != 2 {
		t.Errorf("expected 2 CompleteUploadExternal calls for the 2 successful files, got %d", len(completeCalls))
	}
}

func TestUploadAttachments_ZeroAttachments(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()

	result, err := uploadAttachments(t.Context(), api, "C-test", "", "", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "error" {
		t.Fatalf("expected error, got %q: %s", result.Status, result.Message)
	}
	if len(api.Calls()) != 0 {
		t.Errorf("expected 0 Slack calls, got %d", len(api.Calls()))
	}
}

func TestUploadAttachments_InitialCommentPassedThrough(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()

	comment := "Here is the weekly report"
	result, err := uploadAttachments(t.Context(), api, "C-test", "", comment, []sdk.Artifact{art("report.pdf", "pdf-data")})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("expected success, got %q: %s", result.Status, result.Message)
	}

	completeCalls := callsByMethod(api.Calls(), "CompleteUploadExternal")
	if len(completeCalls) != 1 {
		t.Fatalf("expected 1 CompleteUploadExternal, got %d", len(completeCalls))
	}
	if completeCalls[0].Params["comment"] != comment {
		t.Errorf("comment = %q, want %q", completeCalls[0].Params["comment"], comment)
	}
}

func TestUploadAttachments_FileTitlesMatchFilenames(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()

	atts := []sdk.Artifact{art("alpha.txt", "a"), art("beta.csv", "b"), art("gamma.json", "c")}
	result, err := uploadAttachments(t.Context(), api, "C-test", "", "", atts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("expected success, got %q: %s", result.Status, result.Message)
	}

	uploaded, ok := result.Data["uploaded"].([]map[string]string)
	if !ok {
		t.Fatalf("uploaded is %T, want []map[string]string", result.Data["uploaded"])
	}

	wantTitles := map[string]bool{"alpha.txt": true, "beta.csv": true, "gamma.json": true}
	for _, u := range uploaded {
		title := u["title"]
		if !wantTitles[title] {
			t.Errorf("unexpected uploaded title %q", title)
		}
		delete(wantTitles, title)
	}
	for title := range wantTitles {
		t.Errorf("missing uploaded title %q", title)
	}
}

func TestUploadAttachments_DataPayloadShape(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()

	result, err := uploadAttachments(t.Context(), api, "C-chan", "1700.001", "comment", []sdk.Artifact{art("f.txt", "d")})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("expected success, got %q: %s", result.Status, result.Message)
	}

	if result.Data["channel"] != "C-chan" {
		t.Errorf("channel = %v, want C-chan", result.Data["channel"])
	}
	if result.Data["thread_ts"] != "1700.001" {
		t.Errorf("thread_ts = %v, want 1700.001", result.Data["thread_ts"])
	}
	if result.Data["file_count"] != 1 {
		t.Errorf("file_count = %v, want 1", result.Data["file_count"])
	}

	uploaded, ok := result.Data["uploaded"].([]map[string]string)
	if !ok {
		t.Fatalf("uploaded is %T, want []map[string]string", result.Data["uploaded"])
	}
	if len(uploaded) != 1 {
		t.Fatalf("len(uploaded) = %d, want 1", len(uploaded))
	}
	if uploaded[0]["title"] != "f.txt" {
		t.Errorf("uploaded[0].title = %q, want f.txt", uploaded[0]["title"])
	}
}

func TestUploadAttachments_LargeTextArtifact_FullContentRoundTrip(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()

	const lineCount = 256
	var sb strings.Builder
	for i := 0; i < lineCount; i++ {
		fmt.Fprintf(&sb, "line %04d: a quick brown fox jumps over a lazy dog\n", i)
	}
	largeContent := sb.String()
	if len(largeContent) <= 1024 {
		t.Fatalf("test fixture too small (%d bytes); should exercise >1 KiB", len(largeContent))
	}

	result, err := uploadAttachments(t.Context(), api, "C-test", "", "weekly report attached",
		[]sdk.Artifact{art("weekly-report.txt", largeContent)})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("expected success, got %q: %s", result.Status, result.Message)
	}

	uploadCalls := callsByMethod(api.Calls(), "Upload")
	if len(uploadCalls) != 1 {
		t.Fatalf("expected exactly 1 Upload call, got %d", len(uploadCalls))
	}
	if len(uploadCalls[0].Body) != len(largeContent) {
		t.Errorf("adapter received %d bytes, want %d",
			len(uploadCalls[0].Body), len(largeContent))
	}
	if string(uploadCalls[0].Body) != largeContent {
		t.Errorf("adapter byte stream does not match input artifact")
	}
	if uploadCalls[0].Params["filename"] != "weekly-report.txt" {
		t.Errorf("adapter received filename = %q, want %q",
			uploadCalls[0].Params["filename"], "weekly-report.txt")
	}
}

func TestUploadAttachments_StepTwoError_SurfacesExplicitError(t *testing.T) {
	api := slackapi.NewInMemorySlackAPI()
	api.SetNextError("Upload", fmt.Errorf("slack edge returned status 500"))

	result, err := uploadAttachments(t.Context(), api, "C-test", "", "report attached",
		[]sdk.Artifact{art("data.txt", "important contents that must NOT be inlined as chat")})
	if err != nil {
		t.Fatalf("expected status=error result, not Go error, got %v", err)
	}
	if result.Status != "error" {
		t.Fatalf("expected error status, got %q", result.Status)
	}
	if !strings.Contains(result.Message, "data.txt") {
		t.Errorf("error message should name the failed file, got: %s", result.Message)
	}
	if !strings.Contains(result.Message, "slack edge returned status 500") {
		t.Errorf("error message should wrap the underlying Slack error, got: %s", result.Message)
	}

	if got := callsByMethod(api.Calls(), "CompleteUploadExternal"); len(got) != 0 {
		t.Errorf("CompleteUploadExternal ran %d times after step-2 failure; want 0", len(got))
	}
}
