// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package teams

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func TestUploadFileToTeamsURL(t *testing.T) {
	cases := []struct {
		name        string
		status      int
		wantAlready bool
		wantError   bool
	}{
		{name: "200 OK uploads", status: http.StatusOK},
		{name: "201 Created uploads", status: http.StatusCreated},
		{name: "400 Bad Request returns error", status: http.StatusBadRequest, wantError: true},
		{name: "404 session gone was already uploaded", status: http.StatusNotFound, wantAlready: true},
		{name: "416 bytes already received was already uploaded", status: http.StatusRequestedRangeNotSatisfiable, wantAlready: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(tc.status) }))
			t.Cleanup(srv.Close)

			already, err := uploadFileToTeamsURL(t.Context(), srv.URL, []byte("content"))
			if (err != nil) != tc.wantError {
				t.Errorf("err = %v, want error %v", err, tc.wantError)
			}
			if already != tc.wantAlready {
				t.Errorf("alreadyUploaded = %v, want %v", already, tc.wantAlready)
			}
		})
	}

	t.Run("transport error returns error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		deadURL := srv.URL
		srv.Close()

		if _, err := uploadFileToTeamsURL(t.Context(), deadURL, []byte("content")); err == nil {
			t.Error("err = nil, want non-nil for transport failure")
		}
	})
}

func TestHandleFileConsentAccept_HappyPath(t *testing.T) {
	var posts []map[string]any
	var postCount int32
	botSrv := fakeBotServer(t, &posts, &postCount)

	var uploadHit int32
	var uploadBody []byte
	uploadSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("upload request method = %s, want PUT", r.Method)
		}
		b, _ := io.ReadAll(r.Body)
		uploadBody = b
		atomic.StoreInt32(&uploadHit, 1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(uploadSrv.Close)

	a := newTestAdapter(t)
	a.botClient = NewBotClient(a.cfg.AppID, a.cfg.AppPassword, a.cfg.TenantID, a.logger)
	a.botClient.apiBaseURL = botSrv.URL
	a.ctx = context.Background()
	a.uploads = NewUploadManager(a.logger)

	taskID := "task-accept-ok"
	filename := "report.pdf"
	a.uploads.addPendingInMemory(pendingKey(taskID, filename), filename, []byte("file-bytes"), "application/pdf", taskID)

	contextJSON, _ := json.Marshal(map[string]any{"filename": filename, "task_id": taskID})
	consent := &FileConsentCardResponse{
		Action:     "accept",
		Context:    contextJSON,
		UploadInfo: &UploadInfo{UploadURL: uploadSrv.URL, UniqueID: "u-1", FileType: "pdf", ContentURL: "https://example.invalid/" + filename},
	}
	activity := &Activity{
		ID:           "accept-activity-1",
		ServiceURL:   botSrv.URL,
		From:         Account{ID: "user-1"},
		Conversation: Conversation{ID: "conv-1"},
	}

	a.handleFileConsentAccept(activity, consent, &inboundHold{}, false)

	if atomic.LoadInt32(&uploadHit) != 1 {
		t.Fatal("upload PUT was not received by the Teams upload URL")
	}
	if string(uploadBody) != "file-bytes" {
		t.Errorf("upload body = %q, want %q", uploadBody, "file-bytes")
	}
	if p := a.uploads.takePending(pendingKey(taskID, filename)); p != nil {
		t.Error("pending upload still present after successful accept — should have been cleaned up")
	}
	if got := atomic.LoadInt32(&postCount); got != 1 {
		t.Errorf("expected 1 Bot Framework reply (success card), got %d", got)
	}
}

func TestHandleFileConsentAccept_UploadFails_SendsErrorReply(t *testing.T) {
	var posts []map[string]any
	var postCount int32
	botSrv := fakeBotServer(t, &posts, &postCount)

	uploadSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	t.Cleanup(uploadSrv.Close)

	a := newTestAdapter(t)
	a.botClient = NewBotClient(a.cfg.AppID, a.cfg.AppPassword, a.cfg.TenantID, a.logger)
	a.botClient.apiBaseURL = botSrv.URL
	a.ctx = context.Background()
	a.uploads = NewUploadManager(a.logger)

	taskID := "task-upload-fails"
	filename := "report.pdf"
	a.uploads.addPendingInMemory(pendingKey(taskID, filename), filename, []byte("file-bytes"), "application/pdf", taskID)

	contextJSON, _ := json.Marshal(map[string]any{"filename": filename, "task_id": taskID})
	consent := &FileConsentCardResponse{
		Action:     "accept",
		Context:    contextJSON,
		UploadInfo: &UploadInfo{UploadURL: uploadSrv.URL, UniqueID: "u-1", FileType: "pdf"},
	}
	activity := &Activity{
		ID:           "accept-fail-activity",
		ServiceURL:   botSrv.URL,
		From:         Account{ID: "user-1"},
		Conversation: Conversation{ID: "conv-1"},
	}

	a.handleFileConsentAccept(activity, consent, &inboundHold{}, false)

	if got := atomic.LoadInt32(&postCount); got != 1 {
		t.Fatalf("expected exactly 1 Bot Framework reply, got %d", got)
	}
	replyText, _ := posts[0]["text"].(string)
	if !strings.Contains(strings.ToLower(replyText), "couldn't upload") {
		t.Errorf("reply text = %q, want it to contain %q", replyText, "couldn't upload")
	}
}

func TestHandleFileConsentDecline_NoPending_DoesNotPanic(t *testing.T) {
	var posts []map[string]any
	var postCount int32
	botSrv := fakeBotServer(t, &posts, &postCount)

	a := newTestAdapter(t)
	a.botClient = NewBotClient(a.cfg.AppID, a.cfg.AppPassword, a.cfg.TenantID, a.logger)
	a.botClient.apiBaseURL = botSrv.URL
	a.ctx = context.Background()
	a.uploads = NewUploadManager(a.logger)

	contextJSON, _ := json.Marshal(map[string]any{"filename": "report.pdf", "task_id": "task-x"})
	consent := &FileConsentCardResponse{Action: "decline", Context: contextJSON}
	activity := &Activity{
		ID:           "decline-no-pending",
		ServiceURL:   botSrv.URL,
		From:         Account{ID: "user-1"},
		Conversation: Conversation{ID: "conv-1"},
	}

	a.handleFileConsentDecline(activity, consent)

	if got := atomic.LoadInt32(&postCount); got != 1 {
		t.Errorf("expected 1 Bot Framework reply even without a pending entry, got %d", got)
	}
}

func TestHandleFileConsentDecline_RemovesPendingAndReplies(t *testing.T) {
	var posts []map[string]any
	var postCount int32
	botSrv := fakeBotServer(t, &posts, &postCount)

	a := newTestAdapter(t)
	a.botClient = NewBotClient(a.cfg.AppID, a.cfg.AppPassword, a.cfg.TenantID, a.logger)
	a.botClient.apiBaseURL = botSrv.URL
	a.ctx = context.Background()
	a.uploads = NewUploadManager(a.logger)

	taskID := "task-decline"
	filename := "report.pdf"
	a.uploads.addPendingInMemory(pendingKey(taskID, filename), filename, []byte("file-bytes"), "application/pdf", taskID)

	contextJSON, _ := json.Marshal(map[string]any{"filename": filename, "task_id": taskID})
	consent := &FileConsentCardResponse{Action: "decline", Context: contextJSON}
	activity := &Activity{
		ID:           "decline-activity-1",
		ServiceURL:   botSrv.URL,
		From:         Account{ID: "user-1"},
		Conversation: Conversation{ID: "conv-1"},
	}

	a.handleFileConsentDecline(activity, consent)

	if p := a.uploads.takePending(pendingKey(taskID, filename)); p != nil {
		t.Error("pending upload still present after decline — should have been cleared")
	}
	if got := atomic.LoadInt32(&postCount); got != 1 {
		t.Fatalf("expected 1 Bot Framework reply, got %d", got)
	}
	replyText, _ := posts[0]["text"].(string)
	if !strings.Contains(strings.ToLower(replyText), "won't upload") {
		t.Errorf("reply text = %q, want it to acknowledge the decline (contains \"won't upload\")", replyText)
	}
}

func TestHandleMessageActivity_IgnoresBotSelfMessage(t *testing.T) {
	a := newTestAdapter(t)
	a.core = nil
	a.botClient = nil
	a.emailCache = nil

	activity := &Activity{
		Type: ActivityTypeMessage,
		ID:   "bot-self-msg",
		From: Account{ID: a.cfg.AppID},
		Conversation: Conversation{
			ID:               "conv-1",
			ConversationType: ConversationTypePersonal,
		},
		Text: "this should be ignored",
	}

	a.handleMessageActivity(context.Background(), activity)
}

func TestHandleFilePart_DifferentFilenames_BothDelivered(t *testing.T) {
	var posts []map[string]any
	var postCount int32
	srv := fakeBotServer(t, &posts, &postCount)

	a := newTestAdapter(t)
	a.botClient = NewBotClient(a.cfg.AppID, a.cfg.AppPassword, a.cfg.TenantID, a.logger)
	a.botClient.apiBaseURL = srv.URL
	a.ctx = context.Background()
	a.uploads = NewUploadManager(a.logger)
	a.cfg.LargeFileThresholdMB = 8

	taskID := "task-multi"
	meta := &TeamsTaskMeta{
		ConversationID: "conv-1",
		ServiceURL:     srv.URL,
		Activity: &Activity{
			ID:           "user-msg-1",
			ServiceURL:   srv.URL,
			Conversation: Conversation{ID: "conv-1"},
			From:         Account{ID: "user-1"},
		},
	}

	for _, name := range []string{"file_a.txt", "file_b.txt"} {
		a.handleFilePart(t.Context(), taskID, meta, a2a.Part{
			Kind: "file",
			File: &a2a.FileContent{
				Name:     name,
				MIMEType: "text/plain",
				Bytes:    base64.StdEncoding.EncodeToString([]byte(name + " content")),
			},
		})
	}

	if got := atomic.LoadInt32(&postCount); got != 2 {
		t.Fatalf("expected 2 Bot Framework POSTs (one per filename), got %d", got)
	}
	for _, name := range []string{"file_a.txt", "file_b.txt"} {
		if p := a.uploads.takePending(pendingKey(taskID, name)); p == nil {
			t.Errorf("pending upload for %q is missing", name)
		}
	}
}

func TestHandleFileConsentAccept_MissingPending_SendsExpiredMessage(t *testing.T) {
	var posts []map[string]any
	var postCount int32
	srv := fakeBotServer(t, &posts, &postCount)

	a := newTestAdapter(t)
	a.botClient = NewBotClient(a.cfg.AppID, a.cfg.AppPassword, a.cfg.TenantID, a.logger)
	a.botClient.apiBaseURL = srv.URL
	a.ctx = context.Background()
	a.uploads = NewUploadManager(a.logger)

	contextJSON, err := json.Marshal(map[string]any{
		"filename": "report.pdf",
		"task_id":  "task-expired",
	})
	if err != nil {
		t.Fatalf("marshal context: %v", err)
	}

	consent := &FileConsentCardResponse{
		Action:  "accept",
		Context: contextJSON,
		UploadInfo: &UploadInfo{
			UploadURL: "https://upload.example/should-not-be-hit",
		},
	}
	activity := &Activity{
		ID:           "consent-activity-1",
		ServiceURL:   srv.URL,
		From:         Account{ID: "user-1"},
		Conversation: Conversation{ID: "conv-1"},
	}

	a.handleFileConsentAccept(activity, consent, &inboundHold{}, false)

	if got := atomic.LoadInt32(&postCount); got != 1 {
		t.Fatalf("expected exactly 1 Bot Framework reply, got %d", got)
	}
	replyText, _ := posts[0]["text"].(string)
	if !strings.Contains(strings.ToLower(replyText), "no longer available") {
		t.Errorf("reply text = %q, want it to mention %q", replyText, "no longer available")
	}
	if !strings.Contains(replyText, "report.pdf") {
		t.Errorf("reply text = %q, want it to mention the filename", replyText)
	}
}

func TestHandleFileConsentAccept_StoreBacked_RefetchesAfterRestart(t *testing.T) {
	var posts []map[string]any
	var postCount int32
	botSrv := fakeBotServer(t, &posts, &postCount)

	var uploadHit int32
	var uploadBody []byte
	uploadSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		uploadBody = b
		atomic.StoreInt32(&uploadHit, 1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(uploadSrv.Close)

	a, memStore := newAdapterWithArtifacts(t, botSrv)
	a.botClient.apiBaseURL = botSrv.URL

	filename := "lion.png"
	loc := artifact.Locator{AppName: "test-agent", UserID: "user-1", SessionID: "sess-1", Filename: filename}
	if _, err := memStore.Save(t.Context(), loc, []byte("png-bytes"), "image/png"); err != nil {
		t.Fatalf("save artifact: %v", err)
	}

	contextJSON, _ := json.Marshal(map[string]any{
		"filename":          filename,
		"task_id":           "task-gone",
		"app_name":          loc.AppName,
		"user_id":           loc.UserID,
		"session_id":        loc.SessionID,
		"artifact_filename": loc.Filename,
		"version":           0,
	})
	consent := &FileConsentCardResponse{
		Action:     "accept",
		Context:    contextJSON,
		UploadInfo: &UploadInfo{UploadURL: uploadSrv.URL, UniqueID: "u-1", FileType: "png"},
	}
	activity := &Activity{
		ID:           "accept-restart",
		ServiceURL:   botSrv.URL,
		From:         Account{ID: "user-1"},
		Conversation: Conversation{ID: "conv-1"},
	}

	a.handleFileConsentAccept(activity, consent, &inboundHold{}, false)

	if atomic.LoadInt32(&uploadHit) != 1 {
		t.Fatal("upload PUT was not received — store-backed re-fetch did not happen")
	}
	if string(uploadBody) != "png-bytes" {
		t.Errorf("upload body = %q, want %q (re-fetched from artifact store)", uploadBody, "png-bytes")
	}
	if got := atomic.LoadInt32(&postCount); got != 1 {
		t.Errorf("expected 1 Bot Framework reply (success card), got %d", got)
	}
}

func TestHandleFileConsentAccept_StoreBacked_MissingArtifact_SendsExpired(t *testing.T) {
	var posts []map[string]any
	var postCount int32
	botSrv := fakeBotServer(t, &posts, &postCount)

	a, _ := newAdapterWithArtifacts(t, botSrv)
	a.botClient.apiBaseURL = botSrv.URL

	contextJSON, _ := json.Marshal(map[string]any{
		"filename":          "lion.png",
		"task_id":           "task-gone",
		"app_name":          "test-agent",
		"user_id":           "user-1",
		"session_id":        "sess-1",
		"artifact_filename": "lion.png",
	})
	consent := &FileConsentCardResponse{
		Action:     "accept",
		Context:    contextJSON,
		UploadInfo: &UploadInfo{UploadURL: "https://upload.example/should-not-be-hit"},
	}
	activity := &Activity{
		ID:           "accept-missing",
		ServiceURL:   botSrv.URL,
		From:         Account{ID: "user-1"},
		Conversation: Conversation{ID: "conv-1"},
	}

	a.handleFileConsentAccept(activity, consent, &inboundHold{}, false)

	if got := atomic.LoadInt32(&postCount); got != 1 {
		t.Fatalf("expected exactly 1 Bot Framework reply, got %d", got)
	}
	replyText, _ := posts[0]["text"].(string)
	if !strings.Contains(strings.ToLower(replyText), "no longer available") {
		t.Errorf("reply text = %q, want it to mention %q", replyText, "no longer available")
	}
}

func TestHandleFileConsentAccept_StoreBacked_OtherUser_Refused(t *testing.T) {
	var posts []map[string]any
	var postCount int32
	botSrv := fakeBotServer(t, &posts, &postCount)

	var uploadHit int32
	uploadSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.StoreInt32(&uploadHit, 1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(uploadSrv.Close)

	a, memStore := newAdapterWithArtifacts(t, botSrv)
	a.botClient.apiBaseURL = botSrv.URL
	a.emailCache = NewEmailCache("", 0, nil, a.logger)

	filename := "lion.png"
	owner := "victim@example.com"
	loc := artifact.Locator{AppName: "test-agent", UserID: owner, SessionID: "sess-1", Filename: filename}
	if _, err := memStore.Save(t.Context(), loc, []byte("png-bytes"), "image/png"); err != nil {
		t.Fatalf("save artifact: %v", err)
	}

	contextJSON, _ := json.Marshal(map[string]any{
		"filename":          filename,
		"task_id":           "task-gone",
		"app_name":          loc.AppName,
		"user_id":           owner,
		"session_id":        loc.SessionID,
		"artifact_filename": loc.Filename,
		"version":           0,
	})
	consent := &FileConsentCardResponse{
		Action:     "accept",
		Context:    contextJSON,
		UploadInfo: &UploadInfo{UploadURL: uploadSrv.URL, UniqueID: "u-1", FileType: "png"},
	}
	activity := &Activity{
		ID:           "accept-other-user",
		ServiceURL:   botSrv.URL,
		From:         Account{ID: "attacker-id", AadObjectID: "attacker"},
		Conversation: Conversation{ID: "group-conv"},
	}

	a.handleFileConsentAccept(activity, consent, &inboundHold{}, false)

	if atomic.LoadInt32(&uploadHit) != 0 {
		t.Fatal("upload PUT was hit for a cross-user accept — artifact leaked")
	}
	if got := atomic.LoadInt32(&postCount); got != 1 {
		t.Fatalf("expected exactly 1 Bot Framework reply, got %d", got)
	}
	replyText, _ := posts[0]["text"].(string)
	if !strings.Contains(strings.ToLower(replyText), "no longer available") {
		t.Errorf("reply text = %q, want opaque \"no longer available\" refusal", replyText)
	}
}

func TestHandleFilePart_InlineBytes_StashThenAccept_Uploads(t *testing.T) {
	var postCount int32
	botSrv := fakeBotServer(t, nil, &postCount)

	var uploadBody []byte
	var uploadHit int32
	uploadSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uploadBody, _ = io.ReadAll(r.Body)
		atomic.StoreInt32(&uploadHit, 1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(uploadSrv.Close)

	a, _ := newAdapterWithArtifacts(t, botSrv)
	a.botClient.apiBaseURL = botSrv.URL

	taskID := "task-inline"
	filename := "note.txt"
	meta := &TeamsTaskMeta{
		ConversationID: "conv-1",
		ServiceURL:     botSrv.URL,
		Activity:       &Activity{ID: "user-msg-1", Conversation: Conversation{ID: "conv-1"}, ServiceURL: botSrv.URL},
	}

	a.handleFilePart(t.Context(), taskID, meta, a2a.Part{
		File: &a2a.FileContent{
			Name:     filename,
			MIMEType: "text/plain",
			Bytes:    base64.StdEncoding.EncodeToString([]byte("inline-content")),
		},
	})

	contextJSON, _ := json.Marshal(map[string]any{"filename": filename, "task_id": taskID})
	consent := &FileConsentCardResponse{
		Action:     "accept",
		Context:    contextJSON,
		UploadInfo: &UploadInfo{UploadURL: uploadSrv.URL, UniqueID: "u-1", FileType: "txt"},
	}
	activity := &Activity{
		ID:           "accept-inline",
		ServiceURL:   botSrv.URL,
		From:         Account{ID: "user-1"},
		Conversation: Conversation{ID: "conv-1"},
	}
	a.handleFileConsentAccept(activity, consent, &inboundHold{}, false)

	if atomic.LoadInt32(&uploadHit) != 1 {
		t.Fatal("inline accept did not upload — stash/accept key wiring broken")
	}
	if string(uploadBody) != "inline-content" {
		t.Errorf("upload body = %q, want %q", uploadBody, "inline-content")
	}
}

func TestHandleFilePart_InlineLargeFile_TempFileRoundTrip(t *testing.T) {
	var postCount int32
	botSrv := fakeBotServer(t, nil, &postCount)

	var uploadBody []byte
	var uploadHit int32
	uploadSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uploadBody, _ = io.ReadAll(r.Body)
		atomic.StoreInt32(&uploadHit, 1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(uploadSrv.Close)

	a, _ := newAdapterWithArtifacts(t, botSrv)
	a.botClient.apiBaseURL = botSrv.URL
	a.cfg.LargeFileThresholdMB = 0

	taskID := "task-temp"
	filename := "big.bin"
	meta := &TeamsTaskMeta{
		ConversationID: "conv-1",
		ServiceURL:     botSrv.URL,
		Activity:       &Activity{ID: "user-msg-1", Conversation: Conversation{ID: "conv-1"}, ServiceURL: botSrv.URL},
	}

	a.handleFilePart(t.Context(), taskID, meta, a2a.Part{
		File: &a2a.FileContent{
			Name:     filename,
			MIMEType: "application/octet-stream",
			Bytes:    base64.StdEncoding.EncodeToString([]byte("spilled-to-disk")),
		},
	})

	a.uploads.mu.Lock()
	p := a.uploads.pending[pendingKey(taskID, filename)]
	tempPath := ""
	if p != nil {
		tempPath = p.tempPath
	}
	a.uploads.mu.Unlock()
	if tempPath == "" {
		t.Fatal("expected inline large file to spill to a temp file")
	}

	contextJSON, _ := json.Marshal(map[string]any{"filename": filename, "task_id": taskID})
	consent := &FileConsentCardResponse{
		Action:     "accept",
		Context:    contextJSON,
		UploadInfo: &UploadInfo{UploadURL: uploadSrv.URL, UniqueID: "u-1", FileType: "bin"},
	}
	activity := &Activity{ID: "accept-temp", ServiceURL: botSrv.URL, From: Account{ID: "user-1"}, Conversation: Conversation{ID: "conv-1"}}
	a.handleFileConsentAccept(activity, consent, &inboundHold{}, false)

	if atomic.LoadInt32(&uploadHit) != 1 {
		t.Fatal("temp-file inline accept did not upload")
	}
	if string(uploadBody) != "spilled-to-disk" {
		t.Errorf("upload body = %q, want %q", uploadBody, "spilled-to-disk")
	}
	if _, err := os.Stat(tempPath); !os.IsNotExist(err) {
		t.Errorf("temp file %q not cleaned up after accept (stat err = %v)", tempPath, err)
	}
}

func TestHandleFileConsentAccept_MissingUploadInfo_Replies(t *testing.T) {
	var posts []map[string]any
	var postCount int32
	botSrv := fakeBotServer(t, &posts, &postCount)

	a, _ := newAdapterWithArtifacts(t, botSrv)
	a.botClient.apiBaseURL = botSrv.URL

	contextJSON, _ := json.Marshal(map[string]any{"filename": "report.pdf", "task_id": "task-x"})
	consent := &FileConsentCardResponse{Action: "accept", Context: contextJSON, UploadInfo: nil}
	activity := &Activity{ID: "accept-no-uploadinfo", ServiceURL: botSrv.URL, From: Account{ID: "user-1"}, Conversation: Conversation{ID: "conv-1"}}

	a.handleFileConsentAccept(activity, consent, &inboundHold{}, false)

	if got := atomic.LoadInt32(&postCount); got != 1 {
		t.Fatalf("expected 1 reply, got %d", got)
	}
	replyText, _ := posts[0]["text"].(string)
	if !strings.Contains(strings.ToLower(replyText), "couldn't upload") {
		t.Errorf("reply text = %q, want it to contain %q", replyText, "couldn't upload")
	}
}

func TestHandleFileConsentAccept_StoreBacked_NoArtifactService_Expired(t *testing.T) {
	var posts []map[string]any
	var postCount int32
	botSrv := fakeBotServer(t, &posts, &postCount)

	a, _ := newAdapterWithArtifacts(t, botSrv)
	a.botClient.apiBaseURL = botSrv.URL
	a.core.SetServices(&stubServices{arts: nil})

	contextJSON, _ := json.Marshal(map[string]any{
		"filename": "lion.png", "task_id": "task-gone",
		"app_name": "test-agent", "user_id": "user-1",
		"session_id": "sess-1", "artifact_filename": "lion.png",
	})
	consent := &FileConsentCardResponse{
		Action:     "accept",
		Context:    contextJSON,
		UploadInfo: &UploadInfo{UploadURL: "https://upload.example/should-not-be-hit"},
	}
	activity := &Activity{ID: "accept-no-svc", ServiceURL: botSrv.URL, From: Account{ID: "user-1"}, Conversation: Conversation{ID: "conv-1"}}

	a.handleFileConsentAccept(activity, consent, &inboundHold{}, false)

	if got := atomic.LoadInt32(&postCount); got != 1 {
		t.Fatalf("expected 1 reply, got %d", got)
	}
	replyText, _ := posts[0]["text"].(string)
	if !strings.Contains(strings.ToLower(replyText), "no longer available") {
		t.Errorf("reply text = %q, want %q", replyText, "no longer available")
	}
}

func TestSendFileConsentCard_SendFails_DropsInlinePending(t *testing.T) {
	botSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/oauth2/") {
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok", "token_type": "Bearer", "expires_in": 3600})
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(botSrv.Close)

	a, _ := newAdapterWithArtifacts(t, botSrv)
	a.botClient.apiBaseURL = botSrv.URL

	taskID := "task-sendfail"
	filename := "note.txt"
	activity := &Activity{ID: "user-msg-1", ServiceURL: botSrv.URL, Conversation: Conversation{ID: "conv-1"}, From: Account{ID: "user-1"}}

	err := a.SendFileConsentCard(activity, filename, []byte("inline-content"), "text/plain", taskID, nil)
	if err == nil {
		t.Fatal("expected SendFileConsentCard to return the send error")
	}
	if p := a.uploads.takePending(pendingKey(taskID, filename)); p != nil {
		t.Error("inline pending entry was not dropped after send failure — leak")
	}
}

func TestHandleFileConsentAccept_StoreBacked_OwnerAccepts_Uploads(t *testing.T) {
	var postCount int32
	botSrv := fakeBotServer(t, nil, &postCount)

	var uploadHit int32
	uploadSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.StoreInt32(&uploadHit, 1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(uploadSrv.Close)

	a, memStore := newAdapterWithArtifacts(t, botSrv)
	a.botClient.apiBaseURL = botSrv.URL
	a.emailCache = NewEmailCache("", 0, nil, a.logger)

	owner := "owner@example.com"
	loc := artifact.Locator{AppName: "test-agent", UserID: owner, SessionID: "sess-1", Filename: "lion.png"}
	if _, err := memStore.Save(t.Context(), loc, []byte("png-bytes"), "image/png"); err != nil {
		t.Fatalf("save artifact: %v", err)
	}

	contextJSON, _ := json.Marshal(map[string]any{
		"filename": "lion.png", "task_id": "task-gone",
		"app_name": loc.AppName, "user_id": owner,
		"session_id": loc.SessionID, "artifact_filename": loc.Filename, "version": 0,
	})
	consent := &FileConsentCardResponse{
		Action:     "accept",
		Context:    contextJSON,
		UploadInfo: &UploadInfo{UploadURL: uploadSrv.URL, UniqueID: "u-1", FileType: "png"},
	}
	activity := &Activity{
		ID:           "accept-owner",
		ServiceURL:   botSrv.URL,
		From:         Account{ID: "owner-id", AadObjectID: owner},
		Conversation: Conversation{ID: "conv-1"},
	}

	a.handleFileConsentAccept(activity, consent, &inboundHold{}, false)

	if atomic.LoadInt32(&uploadHit) != 1 {
		t.Fatal("owner accept did not upload — identity check wrongly refused the owner")
	}
}

func TestHandleFileConsentAccept_StoreBacked_RunAsPrincipal_SkipsOwnerGuard(t *testing.T) {
	var posts []map[string]any
	var postCount int32
	botSrv := fakeBotServer(t, &posts, &postCount)

	var uploadHit int32
	uploadSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.StoreInt32(&uploadHit, 1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(uploadSrv.Close)

	a, memStore := newAdapterWithArtifacts(t, botSrv)
	a.botClient.apiBaseURL = botSrv.URL
	a.emailCache = NewEmailCache("", 0, nil, a.logger)

	filename := "shared.png"
	loc := artifact.Locator{AppName: "test-agent", UserID: rbac.DefaultSystemUserSub, SessionID: "sess-chan", Filename: filename}
	if _, err := memStore.Save(t.Context(), loc, []byte("png-bytes"), "image/png"); err != nil {
		t.Fatalf("save artifact: %v", err)
	}

	contextJSON, _ := json.Marshal(map[string]any{
		"filename":          filename,
		"task_id":           "task-chan",
		"app_name":          loc.AppName,
		"user_id":           rbac.DefaultSystemUserSub,
		"session_id":        loc.SessionID,
		"artifact_filename": loc.Filename,
		"version":           0,
	})
	consent := &FileConsentCardResponse{
		Action:     "accept",
		Context:    contextJSON,
		UploadInfo: &UploadInfo{UploadURL: uploadSrv.URL, UniqueID: "u-1", FileType: "png"},
	}
	activity := &Activity{
		ID:           "accept-participant",
		ServiceURL:   botSrv.URL,
		From:         Account{ID: "asker-id", AadObjectID: "asker"},
		Conversation: Conversation{ID: "group-conv"},
	}

	a.handleFileConsentAccept(activity, consent, &inboundHold{}, false)

	if atomic.LoadInt32(&uploadHit) != 1 {
		t.Fatal("upload PUT was NOT hit — run-as artifact refused for a real participant (the blocker)")
	}
	replyText, _ := posts[0]["text"].(string)
	if strings.Contains(strings.ToLower(replyText), "no longer available") {
		t.Errorf("reply = %q, want success, not the ownership refusal, for a shared channel-user artifact", replyText)
	}
}

func acceptRig(t *testing.T, status int) (a *TeamsAdapter, accept func(replayed bool) bool, uploads *int32, posts *[]map[string]any) {
	t.Helper()
	posts = &[]map[string]any{}
	var postCount int32
	botSrv := fakeBotServer(t, posts, &postCount)
	uploads = new(int32)
	uploadSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(uploads, 1)
		w.WriteHeader(status)
	}))
	t.Cleanup(uploadSrv.Close)

	a = newTestAdapter(t)
	a.botClient = NewBotClient(a.cfg.AppID, a.cfg.AppPassword, a.cfg.TenantID, a.logger)
	a.botClient.apiBaseURL = botSrv.URL
	a.ctx = t.Context()
	a.uploads = NewUploadManager(a.logger)

	const taskID, filename = "task-accept", "report.pdf"
	a.uploads.addPendingInMemory(pendingKey(taskID, filename), filename, []byte("file-bytes"), "application/pdf", taskID)
	contextJSON, _ := json.Marshal(map[string]any{"filename": filename, "task_id": taskID})
	consent := &FileConsentCardResponse{
		Action:     "accept",
		Context:    contextJSON,
		UploadInfo: &UploadInfo{UploadURL: uploadSrv.URL, UniqueID: "u-1", FileType: "pdf", ContentURL: "https://example.invalid/" + filename},
	}
	activity := &Activity{ID: "accept-1", ServiceURL: botSrv.URL, From: Account{ID: "user-1"}, Conversation: Conversation{ID: "conv-1"}}
	return a, func(replayed bool) bool {
		return a.handleFileConsentAccept(activity, consent, &inboundHold{}, replayed)
	}, uploads, posts
}

func requireOnlyReply(t *testing.T, posts []map[string]any, prefix string) {
	t.Helper()
	if len(posts) != 1 {
		t.Fatalf("replies = %v, want one", posts)
	}
	if text, _ := posts[0]["text"].(string); !strings.HasPrefix(text, prefix) {
		t.Errorf("reply = %q, want it to start with %q", text, prefix)
	}
}

func TestHandleFileConsentAccept_CutShortIsNotSettled(t *testing.T) {
	a, accept, _, posts := acceptRig(t, http.StatusCreated)
	ctx, cancel := context.WithCancel(t.Context())
	a.ctx = ctx
	cancel()

	if accept(false) {
		t.Error("an accept cut short was settled")
	}
	if len(*posts) != 0 {
		t.Errorf("replies = %v, want none", *posts)
	}
}

func TestHandleFileConsentAccept_ReplayedAfterTheUploadConfirms(t *testing.T) {
	_, accept, _, posts := acceptRig(t, http.StatusNotFound)

	if !accept(true) {
		t.Error("accept not settled")
	}

	requireOnlyReply(t, *posts, "✅ File uploaded")
}

func TestHandleFileConsentAccept_FirstAttemptFindsTheSessionGone(t *testing.T) {
	_, accept, _, posts := acceptRig(t, http.StatusNotFound)

	if !accept(false) {
		t.Error("accept not settled")
	}

	requireOnlyReply(t, *posts, "Sorry, I couldn't upload the file")
}
