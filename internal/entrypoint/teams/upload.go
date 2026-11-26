// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package teams

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

var httpClient = &http.Client{Timeout: 5 * time.Minute}

type UploadManager struct {
	mu       sync.Mutex
	pending  map[string]*pendingUpload
	started  bool
	logger   *slog.Logger
	stopCh   chan struct{}
	stopOnce sync.Once
	stopped  chan struct{}
}

type pendingUpload struct {
	filename  string
	content   []byte
	tempPath  string
	mimeType  string
	taskID    string
	createdAt time.Time
}

const uploadExpiry = 1 * time.Hour

func NewUploadManager(logger *slog.Logger) *UploadManager {
	return &UploadManager{
		pending: make(map[string]*pendingUpload),
		logger:  logger,
		stopCh:  make(chan struct{}),
		stopped: make(chan struct{}),
	}
}

func (m *UploadManager) Start() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.started {
		return
	}
	m.started = true
	go m.cleanupLoop()
}

func (m *UploadManager) Stop() {
	m.stopOnce.Do(func() { close(m.stopCh) })
	m.mu.Lock()
	started := m.started
	m.mu.Unlock()
	if started {
		<-m.stopped
	}
}

func (m *UploadManager) addPendingInMemory(key, filename string, content []byte, mimeType, taskID string) {
	m.mu.Lock()
	m.pending[key] = &pendingUpload{
		filename:  filename,
		content:   content,
		mimeType:  mimeType,
		taskID:    taskID,
		createdAt: time.Now(),
	}
	m.mu.Unlock()
}

func (m *UploadManager) addPendingTempFile(key, filename, tempPath, mimeType, taskID string) {
	m.mu.Lock()
	m.pending[key] = &pendingUpload{
		filename:  filename,
		tempPath:  tempPath,
		mimeType:  mimeType,
		taskID:    taskID,
		createdAt: time.Now(),
	}
	m.mu.Unlock()
}

func (m *UploadManager) takePending(key string) *pendingUpload {
	m.mu.Lock()
	p, ok := m.pending[key]
	if ok {
		delete(m.pending, key)
	}
	m.mu.Unlock()
	return p
}

func (p *pendingUpload) read() ([]byte, error) {
	if p.tempPath != "" {
		return os.ReadFile(p.tempPath)
	}
	return p.content, nil
}

func (p *pendingUpload) cleanup(logger *slog.Logger) {
	if p.tempPath == "" {
		return
	}
	if err := os.Remove(p.tempPath); err != nil && !os.IsNotExist(err) {
		logger.Warn("failed to cleanup temp file", "path", p.tempPath, "error", err)
	}
}

func (m *UploadManager) cleanupLoop() {
	defer close(m.stopped)
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-m.stopCh:
			m.mu.Lock()
			for _, p := range m.pending {
				p.cleanup(m.logger)
			}
			m.mu.Unlock()
			return
		case <-ticker.C:
			m.mu.Lock()
			now := time.Now()
			for id, p := range m.pending {
				if now.Sub(p.createdAt) > uploadExpiry {
					p.cleanup(m.logger)
					delete(m.pending, id)
					m.logger.Debug("expired pending upload", "key", id, "filename", p.filename)
				}
			}
			m.mu.Unlock()
		}
	}
}

func pendingKey(taskID, filename string) string {
	return taskID + "_" + filename
}

type consentArtifactRef struct {
	AppName   string
	UserID    string
	SessionID string
	Filename  string
	Version   *int
}

func newConsentArtifactRef(loc artifact.Locator, version *int) *consentArtifactRef {
	return &consentArtifactRef{
		AppName:   loc.AppName,
		UserID:    loc.UserID,
		SessionID: loc.SessionID,
		Filename:  loc.Filename,
		Version:   version,
	}
}

func (a *TeamsAdapter) SendFileConsentCard(activity *Activity, filename string, content []byte, mimeType, taskID string, ref *consentArtifactRef) error {
	return a.sendFileConsentCard(activity.ServiceURL, activity.Conversation.ID, filename, content, mimeType, taskID, ref)
}

func (a *TeamsAdapter) sendFileConsentCard(serviceURL, conversationID, filename string, content []byte, mimeType, taskID string, ref *consentArtifactRef) error {
	fileSize := int64(len(content))

	contextData := map[string]any{
		"filename": filename,
		"task_id":  taskID,
	}

	if ref != nil {
		contextData["app_name"] = ref.AppName
		contextData["user_id"] = ref.UserID
		contextData["session_id"] = ref.SessionID
		contextData["artifact_filename"] = ref.Filename
		if ref.Version != nil {
			contextData["version"] = *ref.Version
		}
		a.logger.Info("file consent card sent", "filename", filename, "fileSize", fileSize, "storage", "artifact_store")
	} else if err := a.stashInlinePending(filename, content, mimeType, taskID); err != nil {
		return err
	}

	description := fmt.Sprintf("%s is ready to download", filename)
	text := fmt.Sprintf("📎 %s (%s) is ready to download", filename, FormatFileSize(fileSize))

	reply := &Activity{
		Type: ActivityTypeMessage,
		Text: text,
		From: Account{ID: a.cfg.AppID},
		Attachments: []Attachment{
			FileConsentCardAttachmentWithDescription(filename, fileSize, description, contextData),
		},
	}

	if _, err := a.botClient.SendActivity(a.ctx, serviceURL, conversationID, reply); err != nil {
		if ref == nil {
			if p := a.uploads.takePending(pendingKey(taskID, filename)); p != nil {
				p.cleanup(a.logger)
			}
		}
		return err
	}
	return nil
}

func (a *TeamsAdapter) stashInlinePending(filename string, content []byte, mimeType, taskID string) error {
	key := pendingKey(taskID, filename)
	fileSize := int64(len(content))
	threshold := int64(a.cfg.LargeFileThresholdMB) * 1024 * 1024

	if fileSize > threshold {
		f, err := os.CreateTemp("", "teams_consent_*_"+sanitizeFilename(filename))
		if err != nil {
			return fmt.Errorf("create temp file: %w", err)
		}
		tempPath := f.Name()
		if _, err := f.Write(content); err != nil {
			_ = f.Close()
			_ = os.Remove(tempPath)
			return fmt.Errorf("write temp file: %w", err)
		}
		if err := f.Close(); err != nil {
			_ = os.Remove(tempPath)
			return fmt.Errorf("close temp file: %w", err)
		}
		a.uploads.addPendingTempFile(key, filename, tempPath, mimeType, taskID)
		a.logger.Info("file consent card sent", "filename", filename, "fileSize", fileSize, "storage", "temp_file", "path", tempPath)
		return nil
	}

	a.uploads.addPendingInMemory(key, filename, content, mimeType, taskID)
	a.logger.Info("file consent card sent", "filename", filename, "fileSize", fileSize, "storage", "in_memory")
	return nil
}

func (a *TeamsAdapter) receiveFileConsentAccept(ctx context.Context, activity *Activity, consent *FileConsentCardResponse) *InvokeResponse {
	hold := &inboundHold{}
	if activity.ID != "" {
		payload, err := json.Marshal(activity)
		if err != nil {
			a.logger.Error("teams file consent click cannot be encoded to record", "error", err)
			return &InvokeResponse{Status: http.StatusServiceUnavailable}
		}
		hold.key = inboundMessageKey(activity.Conversation.ID, activity.ID)
		claim, err := a.core.AcceptInbound(ctx, hold.key, string(payload))
		if err != nil {
			a.logger.Warn("teams file consent click not recorded; refusing it so Bot Framework sends it again", "messageKey", hold.key, "error", err)
			return &InvokeResponse{Status: http.StatusServiceUnavailable}
		}
		if claim.Outcome == entrypoint.InboundSkip {
			a.logger.Debug("dropping a copy of a teams file consent click another instance took", "messageKey", hold.key)
			return nil
		}
		hold.token = claim.Token
	}
	if !a.server.TrackBackground(func() { a.settleInbound(hold, a.handleFileConsentAccept(activity, consent, hold, false)) }) {
		a.logger.Warn("teams file consent click refused during shutdown; it is recorded for another instance")
		return &InvokeResponse{Status: http.StatusServiceUnavailable}
	}
	return nil
}

func (a *TeamsAdapter) handleFileConsentAccept(activity *Activity, consent *FileConsentCardResponse, hold *inboundHold, replayed bool) (settled bool) {
	ctx, cancel := context.WithTimeout(a.ctx, consentUploadTimeout)
	defer cancel()

	cf := parseConsentFields(consent.Context)
	if cf.filename == "" {
		a.logger.Warn("file consent accept missing filename")
		return true
	}

	if consent.UploadInfo == nil || consent.UploadInfo.UploadURL == "" {
		a.logger.Warn("file consent accept missing upload info", "filename", cf.filename)
		return a.replyUserErrorIn(ctx, activity, fmt.Sprintf(msgFileConsentAbandoned, cf.filename))
	}

	if cf.ref != nil && !rbac.IsNonDelegatablePrincipal(cf.ref.UserID) {
		reqUser := a.requestingUserID(ctx, activity)
		if ctx.Err() != nil {
			return false
		}
		if reqUser != "" && reqUser != cf.ref.UserID {
			a.logger.Warn("file consent accept user mismatch — refusing cross-user artifact fetch", "taskID", cf.taskID)
			return a.replyUserErrorIn(ctx, activity, fmt.Sprintf("Sorry, the file '%s' is no longer available. It may have expired.", cf.filename))
		}
	}

	content, ok := a.resolveConsentContent(ctx, cf)
	if ctx.Err() != nil {
		return false
	}
	if !ok {
		a.logger.Warn("no content for consent accept", "filename", cf.filename, "storeBacked", cf.ref != nil)
		return a.replyUserErrorIn(ctx, activity, fmt.Sprintf("Sorry, the file '%s' is no longer available. It may have expired.", cf.filename))
	}

	if !hold.renew(a.core) {
		a.logger.Warn("teams file consent click taken over by another instance before its upload; dropping it here", "messageKey", hold.key)
		return false
	}
	alreadyUploaded, err := uploadFileToTeamsURL(ctx, consent.UploadInfo.UploadURL, content)
	if ctx.Err() != nil {
		return false
	}
	if err != nil {
		a.logger.Error("file upload failed", "filename", cf.filename, "error", err)
		return a.replyUserErrorIn(ctx, activity, fmt.Sprintf(msgFileConsentAbandoned, cf.filename))
	}

	if alreadyUploaded && !replayed {
		a.logger.Warn("file upload session gone before the first upload", "taskID", cf.taskID)
		return a.replyUserErrorIn(ctx, activity, fmt.Sprintf(msgFileConsentAbandoned, cf.filename))
	}
	if alreadyUploaded {
		a.logger.Debug("file upload already finished by another attempt of the same accept", "taskID", cf.taskID)
	}
	a.retireClickedCard(ctx, activity, fmt.Sprintf(msgFileConsentUploaded, cf.filename))

	reply := &Activity{
		Type: ActivityTypeMessage,
		Text: fmt.Sprintf("✅ File uploaded: %s", cf.filename),
		From: Account{ID: a.cfg.AppID},
		Attachments: []Attachment{
			FileInfoCardAttachment(
				cf.filename,
				consent.UploadInfo.UniqueID,
				consent.UploadInfo.FileType,
				consent.UploadInfo.ContentURL,
			),
		},
	}
	if _, err := a.botClient.SendActivity(ctx, activity.ServiceURL, activity.Conversation.ID, reply); err != nil {
		if ctx.Err() != nil {
			return false
		}
		a.logger.Error("failed to send file info card", "error", err)
	}
	return true
}

func (a *TeamsAdapter) resolveConsentContent(ctx context.Context, cf consentFields) ([]byte, bool) {
	if cf.ref != nil {
		if content, ok := a.loadConsentArtifact(ctx, cf.ref); ok {
			return content, true
		}
	}
	if p := a.uploads.takePending(pendingKey(cf.taskID, cf.filename)); p != nil {
		defer p.cleanup(a.logger)
		content, err := p.read()
		if err != nil {
			a.logger.Error("failed to read pending upload", "filename", cf.filename, "error", err)
			return nil, false
		}
		return content, true
	}
	return nil, false
}

func (a *TeamsAdapter) loadConsentArtifact(ctx context.Context, ref *consentArtifactRef) ([]byte, bool) {
	if a.core == nil || a.core.Services() == nil {
		return nil, false
	}
	artSvc := a.core.Services().Artifacts()
	if artSvc == nil {
		a.logger.Warn("consent re-fetch skipped: no artifact service configured", "filename", ref.Filename)
		return nil, false
	}
	loc := artifact.Locator{
		AppName:   ref.AppName,
		UserID:    ref.UserID,
		SessionID: ref.SessionID,
		Filename:  ref.Filename,
	}
	content, _, err := artSvc.Inner.Load(ctx, loc, ref.Version)
	if err != nil {
		a.logger.Error("consent re-fetch from artifact store failed", "filename", ref.Filename, "error", err)
		return nil, false
	}
	if len(content) == 0 {
		a.logger.Warn("consent re-fetch returned empty content", "filename", ref.Filename)
		return nil, false
	}
	return content, true
}

func (a *TeamsAdapter) requestingUserID(ctx context.Context, activity *Activity) string {
	if a.emailCache == nil {
		return ""
	}
	id := a.emailCache.ResolveIdentity(ctx, activity.From.ID, activity.From.Name,
		activity.From.AadObjectID, activity.ServiceURL, activity.Conversation.ID)
	return id.UserID()
}

func (a *TeamsAdapter) handleFileConsentDecline(activity *Activity, consent *FileConsentCardResponse) {
	cf := parseConsentFields(consent.Context)
	filename := cf.filename
	if filename == "" {
		filename = "unknown_file"
	}

	if p := a.uploads.takePending(pendingKey(cf.taskID, filename)); p != nil {
		p.cleanup(a.logger)
	}

	a.logger.Info("file consent declined", "filename", filename, "taskID", cf.taskID)

	a.retireClickedCard(a.ctx, activity, msgFileConsentDeclined)

	reply := &Activity{
		Type:      ActivityTypeMessage,
		Text:      fmt.Sprintf("Okay, I won't upload '%s'.", filename),
		From:      Account{ID: a.cfg.AppID},
		ReplyToID: activity.ID,
	}
	if _, err := a.botClient.SendActivity(a.ctx, activity.ServiceURL, activity.Conversation.ID, reply); err != nil {
		a.logger.Error("failed to send decline reply", "error", err)
	}
}

type consentFields struct {
	filename string
	taskID   string
	ref      *consentArtifactRef
}

func parseConsentFields(raw json.RawMessage) consentFields {
	var cf consentFields
	if len(raw) == 0 {
		return cf
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return cf
	}
	cf.filename, _ = m["filename"].(string)
	cf.taskID, _ = m["task_id"].(string)

	appName, _ := m["app_name"].(string)
	artifactFilename, _ := m["artifact_filename"].(string)
	if appName != "" && artifactFilename != "" {
		ref := &consentArtifactRef{AppName: appName, Filename: artifactFilename}
		ref.UserID, _ = m["user_id"].(string)
		ref.SessionID, _ = m["session_id"].(string)
		if v, ok := m["version"].(float64); ok {
			n := int(v)
			ref.Version = &n
		}
		cf.ref = ref
	}
	return cf
}

const consentUploadTimeout = 5 * time.Minute

func uploadFileToTeamsURL(ctx context.Context, uploadURL string, content []byte) (alreadyUploaded bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, uploadURL, bytes.NewReader(content))
	if err != nil {
		return false, fmt.Errorf("create upload request: %w", redactURLError(err))
	}
	fileSize := len(content)
	req.ContentLength = int64(fileSize)
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Content-Length", fmt.Sprintf("%d", fileSize))
	if fileSize > 0 {
		req.Header.Set("Content-Range", fmt.Sprintf("bytes 0-%d/%d", fileSize-1, fileSize))
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return false, fmt.Errorf("upload request failed: %w", redactURLError(err))
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.ReadAll(resp.Body)

	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated:
		return false, nil
	case http.StatusNotFound, http.StatusRequestedRangeNotSatisfiable:
		return true, nil
	}
	return false, fmt.Errorf("upload returned HTTP %d", resp.StatusCode)
}
