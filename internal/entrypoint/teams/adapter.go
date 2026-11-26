// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package teams

import (
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/a2ui"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
)

func (a *TeamsAdapter) metrics() runtime.Metrics {
	if a.core == nil || a.core.Services() == nil {
		return runtime.NoopMetrics{}
	}
	return a.core.Services().Metrics()
}

func init() {
	entrypoint.RegisterAdapterFactory("teams", func(cfg map[string]any) (entrypoint.GatewayAdapter, error) {
		return NewTeamsAdapter(cfg)
	})
}

const MaxTotalFileSizeMB = 50

type InputValidationError struct{ Message string }

func (e *InputValidationError) Error() string { return e.Message }

type TeamsTaskMeta struct {
	mu                    sync.Mutex
	ConversationID        string
	ServiceURL            string
	SessionID             string
	StatusActivityID      string
	CurrentStatus         string
	ErrorShown            bool
	answerActivityID      string
	textResumed           bool
	answerStarted         bool
	textStreamed          bool
	notes                 string
	itemsRead             bool
	nextReadAt            time.Time
	Activity              *Activity
	artifactsSent         map[string]bool
	authPromptActivityIDs []string
	authPromptItems       map[string]string
}

func (m *TeamsTaskMeta) addAuthPrompt(item, activityID string) {
	if _, known := m.authPromptItems[activityID]; known {
		return
	}
	m.authPromptActivityIDs = append(m.authPromptActivityIDs, activityID)
	if m.authPromptItems == nil {
		m.authPromptItems = make(map[string]string)
	}
	m.authPromptItems[activityID] = item
}

func offeredFileKey(filename string, version *int) string {
	if version == nil {
		return filename
	}
	return fmt.Sprintf("%s@%d", filename, *version)
}

func (m *TeamsTaskMeta) markArtifactSent(key string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.artifactsSent == nil {
		m.artifactsSent = make(map[string]bool)
	}
	if m.artifactsSent[key] {
		return false
	}
	m.artifactsSent[key] = true
	return true
}

func (a *TeamsAdapter) offerFile(ctx context.Context, taskID string, meta *TeamsTaskMeta, key, filename string, content []byte, mimeType string, ref *consentArtifactRef) error {
	if err := a.SendFileConsentCard(meta.Activity, filename, content, mimeType, taskID, ref); err != nil {
		return err
	}
	if key != "" {
		a.putItem(ctx, taskID, itemFileOfferedPrefix+key, "")
	}
	return nil
}

type TeamsAdapter struct {
	core   *entrypoint.GatewayCore
	cfg    *TeamsConfig
	rawCfg map[string]any

	server     *TeamsServer
	botAuth    *BotAuthenticator
	botClient  *BotClient
	emailCache *EmailCache
	uploads    *UploadManager
	sessKeys   *store.GatewaySessionKeyRepo
	items      *store.EntrypointTaskItemRepo
	adoptLocks [64]sync.Mutex

	queues   map[string]*MessageQueue
	queuesMu sync.Mutex
	logger   *slog.Logger

	hilDedup *runtime.MapCache

	feedbackCtx *runtime.MapCache

	feedbackCorr *store.TeamsFeedbackCorrelationRepo

	onHandlerReady func(http.Handler)

	queueStopTimeout    time.Duration
	feedbackWorkTimeout time.Duration
	statusPostTimeout   time.Duration

	ctx    context.Context
	cancel context.CancelFunc
}

var _ entrypoint.GatewayAdapter = (*TeamsAdapter)(nil)

func NewTeamsAdapter(rawCfg map[string]any) (*TeamsAdapter, error) {
	return &TeamsAdapter{
		rawCfg: rawCfg,
		queues: make(map[string]*MessageQueue),
	}, nil
}

func (a *TeamsAdapter) Handler() http.Handler {
	if a.server == nil {
		return nil
	}
	return a.server.Handler()
}

func (a *TeamsAdapter) SetOnHandlerReady(cb func(http.Handler)) {
	a.onHandlerReady = cb
}

func (a *TeamsAdapter) MountIntent() entrypoint.MountSpec {
	return entrypoint.MountSpec{Kind: entrypoint.MountNamed}
}

func (a *TeamsAdapter) RelayedSurface() string { return httputil.SurfaceTeams }

func (a *TeamsAdapter) Init(_ context.Context, core *entrypoint.GatewayCore) error {
	a.core = core
	a.logger = core.Logger().With("logger_name", "teams-adapter")
	a.hilDedup = runtime.NewMapCache()
	a.feedbackCtx = runtime.NewMapCache()
	if a.ctx == nil {
		a.ctx, a.cancel = context.WithCancel(context.Background())
	}

	adapterCfg := a.rawCfg
	if adapterCfg == nil {
		if raw, ok := core.RawConfig()["adapter_config"].(map[string]any); ok {
			adapterCfg = raw
		}
	}
	if adapterCfg == nil {
		return fmt.Errorf("teams adapter: no adapter_config found")
	}

	cfg, err := ParseTeamsConfig(adapterCfg)
	if err != nil {
		return err
	}
	a.cfg = cfg

	a.botAuth = NewBotAuthenticator(cfg.AppID, a.logger)
	a.botClient = NewBotClient(cfg.AppID, cfg.AppPassword, cfg.TenantID, a.logger)
	if cfg.APIBaseURL != "" {
		a.botClient.apiBaseURL = cfg.APIBaseURL
	}

	ttl := time.Duration(cfg.EmailCacheTTLSeconds) * time.Second
	a.emailCache = NewEmailCache(cfg.TenantID, ttl, a.botClient, a.logger)

	a.uploads = NewUploadManager(a.logger)

	if db := core.Store(); db != nil {
		a.sessKeys = store.NewGatewaySessionKeyRepo(db)
		a.items = store.NewEntrypointTaskItemRepo(db)
		a.feedbackCorr = store.NewTeamsFeedbackCorrelationRepo(db)
		if dr := core.DataRetention(); dr != nil {
			dr.WithTeamsFeedbackCorrelation(a.feedbackCorr)
		}
	} else {
		a.logger.Warn("teams adapter: no gateway store configured — session reuse disabled, every inbound message will create a new ephemeral session")
	}

	a.server = NewTeamsServer(cfg, a.botAuth, a.handleActivity, a.logger)
	a.server.receive = a.receiveActivity

	a.logger.Info("teams adapter initialized",
		"appID", cfg.AppID,
		"httpPort", cfg.HTTPPort,
		"defaultAgent", cfg.DefaultAgentName,
	)
	if cfg.FeedbackEnabled {
		a.core.WarnIfFeedbackUnloggable("teams_bot", a.logger)
	}

	if a.onHandlerReady != nil {
		a.onHandlerReady(a.server.Handler())
	}
	return nil
}

func (a *TeamsAdapter) Start(_ context.Context) error {
	if a.ctx == nil {
		a.ctx, a.cancel = context.WithCancel(context.Background())
	}

	a.uploads.Start()

	a.startNotifyDeliverLoop()

	a.core.WarnIfSignInUnreachable(a.logger, "teams")

	a.logger.Info("teams adapter ready")
	return nil
}

func (a *TeamsAdapter) Stop(_ context.Context) error {
	a.logger.Info("stopping teams adapter")

	if a.server != nil {
		a.server.Close()
	}
	if a.cancel != nil {
		a.cancel()
	}
	if a.server != nil {
		a.server.Wait()
	}

	a.queuesMu.Lock()
	queues := make(map[string]*MessageQueue, len(a.queues))
	for k, v := range a.queues {
		queues[k] = v
	}
	a.queuesMu.Unlock()

	for taskID, q := range queues {
		a.logger.Debug("stopping message queue", "taskID", taskID)
		a.stopQueueOnShutdown(q)
	}

	a.uploads.Stop()

	a.logger.Info("teams adapter stopped")
	return nil
}

func (a *TeamsAdapter) stopQueueOnShutdown(q *MessageQueue) {
	if a.items != nil && !q.endingHere() {
		q.Discard(a.queueStopBound())
		return
	}
	q.Stop(a.queueStopBound())
}

func (a *TeamsAdapter) RendersStatusText(string) bool { return true }

var _ entrypoint.StatusTextRenderer = (*TeamsAdapter)(nil)

func (a *TeamsAdapter) OnStatusUpdate(ctx context.Context, taskID string, event *a2a.TaskStatusUpdateEvent, _ []byte) {
	meta := a.getTaskMeta(ctx, taskID)
	if meta == nil {
		a.logger.Warn("status update for unknown task", "taskID", taskID)
		return
	}

	if req, ok := a2ui.ExtractRequest(event); ok {
		a.handleUserInputRequest(ctx, meta, taskID, req)
		return
	}

	if authData, ok := a2a.AuthenticationRequiredFromStatus(event); ok {
		a.handleAuthRequired(ctx, taskID, meta, authData)
		return
	}

	if result, ok := a2a.AuthenticationResultFromStatus(event); ok {
		a.resolveAuthPrompt(ctx, taskID, meta, result.Success)
		return
	}

	q := a.getOrCreateQueue(taskID, meta)

	if event.Status.Message == nil {
		return
	}

	for _, part := range event.Status.Message.Parts {
		switch part.Kind {
		case "text":
			a.streamText(ctx, taskID, meta, q, part.Text)
		case "data":
			a.handleDataPart(ctx, taskID, meta, q, part)
		case "file":
			a.handleFilePart(ctx, taskID, meta, part)
		}
	}
}

func (a *TeamsAdapter) postInitialStatus(ctx context.Context, taskID string, meta *TeamsTaskMeta) {
	if a.cfg.InitialStatusMessage == "" {
		return
	}
	replyToID := ""
	if meta.Activity != nil {
		replyToID = meta.Activity.ID
	}
	postCtx, cancel := context.WithTimeout(a.ctx, a.statusPostBound())
	defer cancel()
	id, err := sendTeamsMessage(postCtx, a.botClient, meta, a.cfg.InitialStatusMessage, nil, replyToID)
	if err != nil {
		a.logger.Warn("failed to post initial status message; the task goes on without one", "taskID", taskID, "error", err)
		return
	}

	meta.mu.Lock()
	meta.StatusActivityID = id
	meta.CurrentStatus = a.cfg.InitialStatusMessage
	meta.mu.Unlock()
	a.putItem(ctx, taskID, itemStatusMsg, id)

	if a.cfg.EnableTypingIndicator {
		_ = a.botClient.SendTyping(a.ctx, meta.ServiceURL, meta.ConversationID, Account{ID: a.cfg.AppID})
	}
}

func (a *TeamsAdapter) streamText(ctx context.Context, taskID string, meta *TeamsTaskMeta, q *MessageQueue, text string) {
	a.writeAnswer(ctx, taskID, meta, q, text, true)
}

func (a *TeamsAdapter) writeAnswer(ctx context.Context, taskID string, meta *TeamsTaskMeta, q *MessageQueue, text string, agentText bool) {
	if text == "" {
		return
	}
	meta.mu.Lock()
	resumed := meta.textResumed
	first := !resumed && !meta.answerStarted
	notes := ""
	if !resumed {
		meta.answerStarted = true
		if agentText {
			meta.textStreamed = true
		} else {
			meta.notes += text
			notes = meta.notes
		}
	}
	meta.mu.Unlock()
	if resumed {
		return
	}
	if first {
		a.putItem(ctx, taskID, itemTextStarted, "")
	}
	if notes != "" {
		a.putItem(ctx, taskID, itemNotes, notes)
	}
	q.Send(TextUpdateOp{Text: text})
}

func (m *TeamsTaskMeta) streamedHere() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.textStreamed && !m.textResumed
}

func (m *TeamsTaskMeta) finalTextWithNotes(text string) string {
	m.mu.Lock()
	notes := strings.TrimSpace(m.notes)
	m.mu.Unlock()
	if notes == "" {
		return text
	}
	return text + "\n\n" + notes
}

var artifactReturnPattern = regexp.MustCompile(`«artifact_return:([^»]*)»\n?`)

func finalAnswerText(task *a2a.Task) string {
	if task.Status.Message == nil {
		return ""
	}
	var b strings.Builder
	for _, part := range task.Status.Message.Parts {
		if part.Kind == "text" {
			b.WriteString(part.Text)
		}
	}
	return strings.TrimSpace(artifactReturnPattern.ReplaceAllString(b.String(), ""))
}

func (a *TeamsAdapter) OnArtifactUpdate(_ context.Context, taskID string, event *a2a.TaskArtifactUpdateEvent, _ []byte) {
	if event.LastChunk {
		a.logger.Debug("artifact complete", "taskID", taskID, "artifactID", event.Artifact.ArtifactID)
	}
}

func (a *TeamsAdapter) OnFinalResponse(ctx context.Context, taskID string, task *a2a.Task, _ []byte) {
	meta := a.getTaskMeta(ctx, taskID)
	if meta == nil {
		a.logger.Warn("final response for unknown task", "taskID", taskID)
		return
	}

	authOK := task.Status.State != a2a.TaskStateFailed &&
		task.Status.State != a2a.TaskStateRejected &&
		task.Status.State != a2a.TaskStateCancelled
	a.resolveAuthPrompt(ctx, taskID, meta, authOK)

	a.rereadAnswerItems(ctx, taskID, meta)
	q := a.getOrCreateQueue(taskID, meta)
	q.markEnding()

	if task.Status.Message != nil {
		for _, part := range task.Status.Message.Parts {
			if part.Kind == "file" {
				a.handleFilePart(ctx, taskID, meta, part)
			}
		}
	}
	if text := finalAnswerText(task); text != "" && !meta.streamedHere() && task.Status.State != a2a.TaskStateCancelled {
		q.Send(ReplaceTextOp{Text: meta.finalTextWithNotes(text)})
	}

	meta.mu.Lock()
	errorShown := meta.ErrorShown
	statusActivityID := meta.StatusActivityID
	meta.mu.Unlock()

	if !errorShown {
		var finalText string
		switch task.Status.State {
		case a2a.TaskStateCancelled:
			finalText = "🛑 Task canceled."
		case a2a.TaskStateFailed, a2a.TaskStateRejected:
			finalText = "❌ An error occurred while the agent was processing your request."
		}
		switch {
		case finalText != "":
			q.Send(FinalizeOp{StatusActivityID: statusActivityID, Text: finalText})
		case statusActivityID != "":
			q.Send(DeleteOp{StatusActivityID: statusActivityID})
		}
	}

	q.Flush()

	if a.cfg.FeedbackEnabled && taskID != "" && meta.SessionID != "" {
		a.registerFeedback(taskID, meta, q.ContentMessageID(), q.ContentText())
	}

	a.cleanupTask(taskID)
}

func (a *TeamsAdapter) OnError(ctx context.Context, taskID string, taskErr *a2a.Error, _ []byte) {
	meta := a.getTaskMeta(ctx, taskID)
	if meta == nil {
		a.logger.Warn("error for unknown task", "taskID", taskID)
		return
	}

	meta.mu.Lock()
	meta.ErrorShown = true
	statusActivityID := meta.StatusActivityID
	meta.mu.Unlock()

	a.resolveAuthPrompt(ctx, taskID, meta, false)

	q := a.getOrCreateQueue(taskID, meta)
	q.markEnding()

	var errorText string
	switch {
	case entrypoint.IsTaskTimeoutError(taskErr), strings.Contains(strings.ToLower(taskErr.Message), "cancel"):
		errorText = "🛑 Task canceled."
	default:
		errorText = "❌ An error occurred while the agent was processing your request."
		a.logger.Error("task failed", "taskID", taskID, "error", taskErr.Message)
	}

	q.Send(FinalizeOp{
		StatusActivityID: statusActivityID,
		Text:             errorText,
	})
	q.Flush()

	if a.cfg.FeedbackEnabled && taskID != "" && meta.SessionID != "" {
		a.registerFeedback(taskID, meta, q.ContentMessageID(), q.ContentText())
	}

	a.cleanupTask(taskID)
}

func (a *TeamsAdapter) OnAgentDiscovered(card *a2a.AgentCard) {
	a.logger.Info("agent discovered", "agentName", card.Name)
}

func (a *TeamsAdapter) OnAgentRemoved(agentName string) {
	a.logger.Info("agent removed", "agentName", agentName)
}

func (a *TeamsAdapter) replyInputValidationError(activity *Activity, err *InputValidationError) bool {
	reply := &Activity{
		Type:      ActivityTypeMessage,
		Text:      err.Message,
		From:      Account{ID: a.cfg.AppID},
		ReplyToID: activity.ID,
	}
	if _, sendErr := a.botClient.SendActivity(a.ctx, activity.ServiceURL, activity.Conversation.ID, reply); sendErr != nil {
		a.logger.Error("failed to send validation error reply", "error", sendErr)
		return false
	}
	return true
}

func (a *TeamsAdapter) replyUserError(activity *Activity, text string) bool {
	return a.replyUserErrorIn(a.ctx, activity, text)
}

func (a *TeamsAdapter) replyUserErrorIn(ctx context.Context, activity *Activity, text string) bool {
	_, err := a.botClient.SendActivity(ctx, activity.ServiceURL, activity.Conversation.ID, &Activity{
		Type:      ActivityTypeMessage,
		Text:      text,
		From:      Account{ID: a.cfg.AppID},
		ReplyToID: activity.ID,
	})
	if err != nil {
		a.logger.Error("failed to send error reply", "error", err)
		return false
	}
	return true
}

func (a *TeamsAdapter) submitTeamsTask(ctx context.Context, activity *Activity, text string, fileAttachments []Attachment, hold *inboundHold) bool {
	reason, settled := a.trySubmit(ctx, activity, text, fileAttachments, hold)
	a.core.ReportSubmitRefusal(reason)
	return settled
}

func (a *TeamsAdapter) trySubmit(ctx context.Context, activity *Activity, text string, fileAttachments []Attachment, hold *inboundHold) (string, bool) {
	realIdentity := a.emailCache.ResolveIdentity(a.ctx, activity.From.ID, activity.From.Name, activity.From.AadObjectID, activity.ServiceURL, activity.Conversation.ID)

	convType := activity.Conversation.ConversationType
	isMultiParty := convType != ConversationTypePersonal || activity.Conversation.IsGroup
	if convType == "" && !a.cfg.UseUserIdentityInChannels {
		a.logger.Debug("teams activity has empty conversationType; treating as multi-party for run-as",
			"conversationID", activity.Conversation.ID)
	}
	identity, runAs := auth.ForChatUser(a.ctx, realIdentity, a.core, auth.ChannelPolicy{
		IsMultiParty:              isMultiParty,
		UseUserIdentityInChannels: a.cfg.UseUserIdentityInChannels,
		RunAs:                     a.cfg.RunAs,
	})
	submitCtx := a.ctx

	a.publishPresence(submitCtx, realIdentity.UserID(), realIdentity.Email(), teamsProactiveAddress{
		ServiceURL:     activity.ServiceURL,
		ConversationID: activity.Conversation.ID,
		AadObjectID:    activity.From.AadObjectID,
		FromID:         activity.From.ID,
		TenantID:       activity.Conversation.TenantID,
	})

	res := a.resolveTarget(submitCtx, identity, text)
	if res.Misconfigured {
		return taxonomy.RejectAgentNotFound, a.replyUserError(activity, fmt.Sprintf("This Teams entrypoint is configured to route to `%s`, but no such agent is currently available. Contact the entrypoint operator.", a.cfg.DefaultAgentName))
	}
	if res.Refused() {
		return res.RefusalReason, a.replyUserError(activity, res.Refusal)
	}
	agentName := res.Name
	if agentName == "" {
		a.logger.Warn("no target agent found", "userID", identity.UserID())
		return taxonomy.RejectAgentNotFound, a.replyUserError(activity, "Sorry, I couldn't determine which agent to send your request to.")
	}

	var parts []a2a.Part
	var fileSummaryLines []string

	if len(fileAttachments) > 0 {
		var totalBytes int64
		for _, att := range fileAttachments {
			if size := headTeamsAttachmentSize(ctx, att, a.logger); size > 0 {
				totalBytes += size
			}
		}
		if totalBytes > int64(MaxTotalFileSizeMB)*1024*1024 {
			totalMB := float64(totalBytes) / (1024 * 1024)
			msg := fmt.Sprintf(
				"The total size of all uploaded files (%.1f MB) exceeds the maximum allowed limit of %d MB. Please reduce the number or size of files and try again.",
				totalMB, MaxTotalFileSizeMB,
			)
			a.logger.Warn("total file size validation failed", "totalBytes", totalBytes, "limitMB", MaxTotalFileSizeMB)
			return taxonomy.RejectMalformed, a.replyInputValidationError(activity, &InputValidationError{Message: msg})
		}

		for _, att := range fileAttachments {
			file, err := downloadAttachment(ctx, att, a.cfg.MaxDownloadFileSizeMB, a.metrics(), a.logger)
			if err != nil {
				a.logger.Error("file download failed", "filename", att.Name, "error", err)
				return taxonomy.RejectMalformed, a.replyInputValidationError(activity, &InputValidationError{
					Message: fmt.Sprintf("Failed to download %s: %v", att.Name, err),
				})
			}
			b64Content := base64.StdEncoding.EncodeToString(file.Content)
			parts = append(parts, a2a.NewFilePartWithBytes(b64Content, file.Name, file.MIMEType))
			fileSummaryLines = append(fileSummaryLines,
				fmt.Sprintf("- %s (%s, %d bytes)", file.Name, file.MIMEType, len(file.Content)))
		}
	}

	if len(fileSummaryLines) > 0 {
		text = "The user uploaded the following file(s):\n" +
			strings.Join(fileSummaryLines, "\n") +
			"\n\nUser message: " + text
	}

	parts = append(parts, a2a.NewTextPart(text))

	taskID := idgen.New()
	externalKey := buildExternalKey(activity)
	sessionID, err := a.resolveSessionID(submitCtx, externalKey, agentName, identity.UserID())
	if err != nil {
		a.logger.Error("failed to resolve session id", "externalKey", externalKey, "error", err)
		reply := &Activity{
			Type:      ActivityTypeMessage,
			Text:      "Sorry, I couldn't start a session for this conversation. Please try again, or contact your administrator if the problem persists.",
			From:      Account{ID: a.cfg.AppID},
			ReplyToID: activity.ID,
		}
		_, sendErr := a.botClient.SendActivity(a.ctx, activity.ServiceURL, activity.Conversation.ID, reply)
		return taxonomy.RejectInternal, sendErr == nil
	}

	msg := a2a.Message{
		Kind:      "message",
		MessageID: idgen.New(),
		Role:      "user",
		Parts:     parts,
		TaskID:    taskID,
		ContextID: sessionID,
	}

	if runAs {
		msg.Metadata = map[string]any{
			"conversation_context": entrypoint.ChannelRunAsSystemNote,
			"requesting_user":      realIdentity.UserID(),
			"run_as":               identity.UserID(),
		}
	}

	dropped := entrypoint.ConvertInlineFilePartsToArtifacts(
		ctx, &msg, a.core.Services().Artifacts(),
		entrypoint.ArtifactScope{UserID: identity.UserID(), SessionID: sessionID}, a.logger)
	entrypoint.AppendDroppedFilesNote(&msg, dropped)

	meta := &TeamsTaskMeta{
		ConversationID: activity.Conversation.ID,
		ServiceURL:     activity.ServiceURL,
		SessionID:      sessionID,
		Activity:       activity,
	}
	if !hold.renew(a.core) {
		a.logger.Warn("teams message taken over by another instance before its submit; dropping it here", "messageKey", hold.key)
		return "", false
	}
	if err := a.core.Tasks().Register(taskID, agentName, identity.UserID(), entrypoint.TaskIdentity{}); err != nil {
		a.logger.Error("failed to record task", "taskID", taskID, "error", err)
		return entrypoint.SubmitFailureReason(err), a.replyUserError(activity, a.core.SubmitFailureText(err, true))
	}
	if err := a.writeDestination(ctx, taskID, meta); err != nil {
		a.logger.Error("failed to record task destination", "taskID", taskID, "error", err)
		posted := a.replyUserError(activity, a.core.SubmitFailureText(err, true))
		a.core.Tasks().Remove(taskID)
		return entrypoint.SubmitFailureReason(err), posted
	}
	a.core.Tasks().SetMetadata(taskID, meta)
	a.postInitialStatus(ctx, taskID, meta)

	if err := a.core.SubmitTask(submitCtx, agentName, taskID, msg, identity); err != nil {
		entrypoint.LogSubmitFailure(a.logger, slog.LevelError, err, "taskID", taskID)
		posted := a.showSubmitFailure(taskID, activity, meta, a.core.SubmitFailureText(err, true))
		a.core.Tasks().Remove(taskID)
		return entrypoint.SubmitFailureReason(err), posted
	}

	var traceID string
	if info := a.core.Tasks().Get(taskID); info != nil {
		traceID = info.TraceID
	}
	a.logger.Info("task submitted", "taskID", taskID, "agentName", agentName, "userID", identity.UserID(), "traceID", traceID)
	return "", true
}

func (a *TeamsAdapter) showSubmitFailure(taskID string, activity *Activity, meta *TeamsTaskMeta, text string) bool {
	meta.mu.Lock()
	statusActivityID := meta.StatusActivityID
	meta.mu.Unlock()
	if statusActivityID == "" {
		return a.replyUserError(activity, text)
	}
	if err := updateTeamsMessage(a.ctx, a.botClient, meta, statusActivityID, text, nil); err != nil {
		a.logger.Warn("failed to show submit failure in the status message; replying instead", "taskID", taskID, "error", err)
		return a.replyUserError(activity, text)
	}
	return true
}

func (a *TeamsAdapter) HandleTestActivity(ctx context.Context, activityData map[string]any) {
	data, err := json.Marshal(activityData)
	if err != nil {
		a.logger.Error("test activity marshal error", "error", err)
		return
	}
	var activity Activity
	if err := json.Unmarshal(data, &activity); err != nil {
		a.logger.Error("test activity unmarshal error", "error", err)
		return
	}
	if activity.ServiceURL == "" {
		if su, ok := activityData["serviceUrl"].(string); ok {
			activity.ServiceURL = su
		}
	}
	a.handleActivity(ctx, &activity)
}

func (a *TeamsAdapter) resolveTarget(ctx context.Context, caller *auth.Subject, text string) entrypoint.IngressTarget {
	target := a.core.ResolveIngressTarget(ctx, entrypoint.IngressTargetRequest{
		Caller:           caller,
		Text:             text,
		DefaultAgentName: a.cfg.DefaultAgentName,
		SignInLinkable:   true,
	})
	if target.Misconfigured {
		a.logger.Warn("configured default_agent_name not found in registry",
			"eventType", "teams.default_agent_misconfigured",
			"configured", a.cfg.DefaultAgentName,
			"discovered", a.core.AgentRegistry().Names(),
		)
	}
	return target
}

func (a *TeamsAdapter) handleDataPart(ctx context.Context, taskID string, meta *TeamsTaskMeta, q *MessageQueue, part a2a.Part) {
	data := part.Data
	if data == nil {
		return
	}

	dataMap, ok := data.(map[string]any)
	if !ok {
		return
	}

	dataType, _ := dataMap["type"].(string)

	switch dataType {
	case "agent_progress_update":
		statusText, _ := dataMap["status_text"].(string)
		if statusText != "" {
			a.handleStatusUpdate(meta, q, statusText)
		}
	case "artifact_creation_progress":
		if entrypoint.DataTagsContainWorking(dataMap) {
			return
		}
		filename, _ := dataMap["filename"].(string)
		displayName := filename
		if displayName == "" {
			displayName = "unknown file"
		}
		status, _ := dataMap["status"].(string)
		switch status {
		case "in-progress":
			progressText := fmt.Sprintf("💾 Creating `%s`...", displayName)
			if b, ok := dataMap["bytes_transferred"].(float64); ok && b > 0 {
				progressText += fmt.Sprintf(" (%d bytes)", int64(b))
			}
			a.handleStatusUpdate(meta, q, progressText)
		case "completed":
			a.logger.Debug("artifact creation completed", "filename", displayName)
			version := entrypoint.DataIntPtr(dataMap["version"])
			key := ""
			if filename != "" {
				key = offeredFileKey(filename, version)
				if !meta.markArtifactSent(key) {
					return
				}
			}
			mimeFromData, _ := dataMap["mime_type"].(string)
			if content, mimeFromSvc, loc, loaded := a.loadArtifactBytes(a.ctx, taskID, filename, version); loaded {
				effectiveMIME := mimeFromData
				if effectiveMIME == "" {
					effectiveMIME = mimeFromSvc
				}
				ref := newConsentArtifactRef(loc, version)
				if err := a.offerFile(ctx, taskID, meta, key, filename, content, effectiveMIME, ref); err != nil {
					a.logger.Error("failed to send file consent card for completed artifact",
						"taskID", taskID, "filename", filename, "error", err)
					a.noteInAnswer(ctx, taskID, meta, q, fmt.Sprintf("\n\n✅ Artifact created: `%s`", displayName))
				}
			} else {
				a.noteInAnswer(ctx, taskID, meta, q, fmt.Sprintf("\n\n✅ Artifact created: `%s`", displayName))
			}
		case "failed":
			a.noteInAnswer(ctx, taskID, meta, q, fmt.Sprintf("❌ Failed to create artifact: `%s`", displayName))
		}
	}
}

func (a *TeamsAdapter) handleStatusUpdate(meta *TeamsTaskMeta, q *MessageQueue, statusText string) {
	newStatus := "💭 " + statusText

	meta.mu.Lock()
	statusActivityID := meta.StatusActivityID
	if statusActivityID == "" || meta.CurrentStatus == newStatus {
		meta.mu.Unlock()
		return
	}
	meta.CurrentStatus = newStatus
	meta.mu.Unlock()

	q.Send(StatusUpdateOp{
		StatusActivityID: statusActivityID,
		Text:             newStatus,
	})
}

func (a *TeamsAdapter) handleFilePart(ctx context.Context, taskID string, meta *TeamsTaskMeta, part a2a.Part) {
	fc := part.File
	if fc == nil {
		return
	}

	if fc.Bytes != "" {
		key := offeredFileKey(fc.Name, nil)
		if !meta.markArtifactSent(key) {
			return
		}
		content, err := base64.StdEncoding.DecodeString(fc.Bytes)
		if err != nil {
			a.logger.Error("failed to decode file bytes", "taskID", taskID, "error", err)
			return
		}
		if err := a.offerFile(ctx, taskID, meta, key, fc.Name, content, fc.MIMEType, nil); err != nil {
			a.logger.Error("failed to send file consent card", "taskID", taskID, "error", err)
		}
		return
	}

	if fc.URI == "" {
		a.logger.Warn("file part has neither Bytes nor URI — nothing rendered",
			"taskID", taskID, "filename", fc.Name)
		return
	}

	if filename, version, ok := entrypoint.ParseArtifactURI(fc.URI); ok && filename != "" {
		displayName := fc.Name
		if displayName == "" {
			displayName = filename
		}
		key := offeredFileKey(displayName, version)
		if !meta.markArtifactSent(key) {
			return
		}
		if content, mimeType, loc, loaded := a.loadArtifactBytes(a.ctx, taskID, filename, version); loaded {
			effectiveMIME := fc.MIMEType
			if effectiveMIME == "" {
				effectiveMIME = mimeType
			}
			ref := newConsentArtifactRef(loc, version)
			if err := a.offerFile(ctx, taskID, meta, key, displayName, content, effectiveMIME, ref); err != nil {
				a.logger.Error("failed to send file consent card for artifact",
					"taskID", taskID, "filename", displayName, "error", err)
			}
			return
		}
	}

	q := a.getOrCreateQueue(taskID, meta)
	a.noteInAnswer(ctx, taskID, meta, q, fmt.Sprintf("\n\nArtifact available: [%s](%s)", fc.Name, fc.URI))
}

func (a *TeamsAdapter) loadArtifactBytes(ctx context.Context, taskID string, filename string, version *int) (content []byte, mimeType string, loc artifact.Locator, ok bool) {
	if a.core == nil || a.core.Services() == nil {
		return nil, "", artifact.Locator{}, false
	}
	artSvc := a.core.Services().Artifacts()
	if artSvc == nil {
		a.logger.Debug("artifact load skipped: no artifact service configured",
			"taskID", taskID, "filename", filename)
		return nil, "", artifact.Locator{}, false
	}
	info := a.core.Tasks().Get(taskID)
	if info == nil {
		a.logger.Warn("artifact load skipped: task not tracked", "taskID", taskID, "filename", filename)
		return nil, "", artifact.Locator{}, false
	}
	meta := a.getTaskMeta(ctx, taskID)
	if meta == nil {
		a.logger.Warn("artifact load skipped: task meta missing",
			"taskID", taskID, "filename", filename)
		return nil, "", artifact.Locator{}, false
	}

	loc = artSvc.Resolve(info.AgentName, info.UserID, meta.SessionID, filename)
	content, mimeType, err := artSvc.Inner.Load(ctx, loc, version)
	if err != nil {
		a.logger.Error("failed to load artifact for teams file card",
			"taskID", taskID, "agentName", info.AgentName, "filename", filename, "error", err)
		return nil, "", artifact.Locator{}, false
	}
	if len(content) == 0 {
		a.logger.Warn("artifact load returned empty content",
			"taskID", taskID, "agentName", info.AgentName, "filename", filename)
		return nil, "", artifact.Locator{}, false
	}
	return content, mimeType, loc, true
}

func (a *TeamsAdapter) getOrCreateQueue(taskID string, meta *TeamsTaskMeta) *MessageQueue {
	a.queuesMu.Lock()
	defer a.queuesMu.Unlock()

	if q, ok := a.queues[taskID]; ok {
		return q
	}

	q := NewMessageQueue(taskID, meta.ConversationID, meta.ServiceURL, a.cfg.AppID, meta.Activity.ID, a.botClient, a.logger)
	q.feedbackEnabled = a.cfg.FeedbackEnabled && a.core.Store() != nil
	if a.cfg.APIBaseURL != "" {
		q.SetMinThrottle(0)
		q.SetBufferInterval(0)
	} else {
		q.SetMinThrottle(a.cfg.MinAPIInterval)
		q.SetBufferInterval(a.cfg.BufferUpdateInterval)
	}
	meta.mu.Lock()
	q.setContentID(meta.answerActivityID)
	meta.mu.Unlock()
	if a.items != nil {
		q.recordContentID = func(id string) { a.putItem(context.Background(), taskID, itemAnswerMsg, id) }
	}
	q.Start()
	a.queues[taskID] = q
	return q
}

func (a *TeamsAdapter) OnTaskEndedElsewhere(taskID string) {
	a.server.TrackBackground(func() { a.dropTask(taskID) })
}

var _ entrypoint.TaskEndedElsewhereHandler = (*TeamsAdapter)(nil)

func (a *TeamsAdapter) cleanupTask(taskID string) {
	if q := a.takeQueue(taskID); q != nil {
		q.Stop(a.queueStopBound())
	}
}

func (a *TeamsAdapter) dropTask(taskID string) {
	if q := a.takeQueue(taskID); q != nil {
		q.Discard(a.queueStopBound())
	}
}

func (a *TeamsAdapter) takeQueue(taskID string) *MessageQueue {
	a.queuesMu.Lock()
	defer a.queuesMu.Unlock()
	q, ok := a.queues[taskID]
	if ok {
		delete(a.queues, taskID)
	}
	return q
}

func (a *TeamsAdapter) noteInAnswer(ctx context.Context, taskID string, meta *TeamsTaskMeta, q *MessageQueue, text string) {
	meta.mu.Lock()
	resumed := meta.textResumed
	meta.mu.Unlock()
	if !resumed {
		a.writeAnswer(ctx, taskID, meta, q, text, false)
		return
	}
	if _, err := sendTeamsMessage(ctx, a.botClient, meta, strings.TrimSpace(text), nil, ""); err != nil {
		a.logger.Warn("failed to post a note for a task taken over", "taskID", taskID, "error", err)
	}
}

const defaultQueueStopTimeout = 10 * time.Second

const defaultStatusPostTimeout = time.Minute

func (a *TeamsAdapter) statusPostBound() time.Duration {
	return cmp.Or(a.statusPostTimeout, defaultStatusPostTimeout)
}

func (a *TeamsAdapter) queueStopBound() time.Duration {
	return cmp.Or(a.queueStopTimeout, defaultQueueStopTimeout)
}
