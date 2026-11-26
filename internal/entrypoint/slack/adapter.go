// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package slack

import (
	"bytes"
	"cmp"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"hash/fnv"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/a2ui"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/slackapi"
	goslack "github.com/slack-go/slack"
	"github.com/slack-go/slack/socketmode"
)

func init() {
	entrypoint.RegisterAdapterFactory("slack", func(cfg map[string]any) (entrypoint.GatewayAdapter, error) {
		return NewSlackGatewayAdapter(cfg)
	})
}

const (
	msgTaskCanceled = "🛑 Task canceled."
	msgTaskError    = "❌ An error occurred while the agent was processing your request."
)

const (
	slackRateLimitRetries = 2
	slackMaxRetryAfter    = 2 * time.Second
)

type SlackTaskMeta struct {
	ChannelID string
	ThreadTS  string
	SessionID string
	StatusTS  string
	UserID    string
	TeamID    string
	AgentName string
	OwnerID   string
	IsDM      bool

	taskID          string
	textResumed     bool
	resumed         *resumedQueue
	syncedFromItems atomic.Bool
	refreshRetryAt  atomic.Int64

	mu              sync.Mutex
	ErrorShown      bool
	authPromptTSs   []string
	authPromptItems map[string]string
}

func (m *SlackTaskMeta) addAuthPrompt(item, ts string) {
	m.authPromptTSs = append(m.authPromptTSs, ts)
	if m.authPromptItems == nil {
		m.authPromptItems = make(map[string]string)
	}
	m.authPromptItems[ts] = item
}

type SlackGatewayAdapter struct {
	core   *entrypoint.GatewayCore
	cfg    *SlackConfig
	rawCfg map[string]any

	api          slackapi.SlackAPIAdapter
	client       *goslack.Client
	socketClient *socketmode.Client

	queues                 map[string]*MessageQueue
	queuesMu               sync.Mutex
	emailCache             *EmailCache
	hilDedup               *runtime.MapCache
	approverClickTTL       time.Duration
	historyAttachmentCache *runtime.MapCache

	mentionMu        sync.Mutex
	inFlightMentions map[string]mentionSlot
	sessKeys         *store.GatewaySessionKeyRepo
	items            taskItems
	adoptLocks       [64]sync.Mutex
	logger           *slog.Logger

	contextFetchWarned      sync.Map
	historyAttachmentWarned sync.Map
	sessionStatusWarned     sync.Map

	botUserID        string
	botID            string
	identityResolved bool

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

var _ entrypoint.GatewayAdapter = (*SlackGatewayAdapter)(nil)

func NewSlackGatewayAdapter(rawCfg map[string]any) (*SlackGatewayAdapter, error) {
	return &SlackGatewayAdapter{
		rawCfg: rawCfg,
		queues: make(map[string]*MessageQueue),
	}, nil
}

func (a *SlackGatewayAdapter) MountIntent() entrypoint.MountSpec {
	return entrypoint.MountSpec{Kind: entrypoint.MountNone}
}

func (a *SlackGatewayAdapter) RelayedSurface() string { return httputil.SurfaceSlack }

func newSlackClient(cfg *SlackConfig) *goslack.Client {
	opts := []goslack.Option{
		goslack.OptionAppLevelToken(cfg.AppToken),
		goslack.OptionHTTPClient(&http.Client{Timeout: 30 * time.Second}),
		goslack.OptionRetryConfig(slackRetryConfig()),
	}
	if cfg.APIBaseURL != "" {
		opts = append(opts, goslack.OptionAPIURL(cfg.APIBaseURL))
	}
	return goslack.New(cfg.BotToken, opts...)
}

func slackRetryConfig() goslack.RetryConfig {
	cfg := goslack.RetryConfig{MaxRetries: slackRateLimitRetries, RetryAfterDuration: time.Second}
	cfg.Handlers = []goslack.RetryHandler{shortRetryAfter{goslack.NewRateLimitErrorRetryHandler(cfg)}}
	return cfg
}

type shortRetryAfter struct{ goslack.RetryHandler }

func (h shortRetryAfter) ShouldRetry(state *goslack.RetryState, req *http.Request, resp *http.Response, err error) (bool, time.Duration) {
	retry, wait := h.RetryHandler.ShouldRetry(state, req, resp, err)
	if !retry || wait > slackMaxRetryAfter {
		return false, 0
	}
	return true, wait
}

func (a *SlackGatewayAdapter) Init(_ context.Context, core *entrypoint.GatewayCore) error {
	a.core = core
	a.logger = core.Logger().With("logger_name", "slack-adapter")

	adapterCfg := a.rawCfg
	if adapterCfg == nil {
		if raw, ok := core.RawConfig()["adapter_config"].(map[string]any); ok {
			adapterCfg = raw
		}
	}
	if adapterCfg == nil {
		return fmt.Errorf("slack adapter: no adapter_config found")
	}

	cfg, err := ParseSlackConfig(adapterCfg)
	if err != nil {
		return err
	}
	a.cfg = cfg

	a.client = newSlackClient(cfg)

	if cfg.APIBaseURL == "" || cfg.ForceSocketMode {
		a.socketClient = socketmode.New(a.client,
			socketmode.OptionLog(slog.NewLogLogger(a.logger.Handler(), slog.LevelDebug)),
		)
	}

	var apiOpts []slackapi.Option
	if cfg.DisableRateLimiting {
		apiOpts = append(apiOpts, slackapi.WithDisabledRateLimiting())
	}
	if cfg.APIBaseURL != "" {
		apiOpts = append(apiOpts, slackapi.WithAPIBaseURL(cfg.APIBaseURL))
	}
	a.api = slackapi.NewSlackAPIAdapter(a.client, cfg.BotToken, apiOpts...)

	ttl := time.Duration(cfg.EmailCacheTTLSeconds) * time.Second
	a.emailCache = NewEmailCache(a.api, ttl, a.logger)
	a.hilDedup = runtime.NewMapCache()
	a.historyAttachmentCache = runtime.NewMapCache()
	a.inFlightMentions = make(map[string]mentionSlot)
	a.items.warn = a.logger.Warn
	a.items.debug = a.logger.Debug
	if db := core.Store(); db != nil {
		a.sessKeys = store.NewGatewaySessionKeyRepo(db)
		a.items.repo = store.NewEntrypointTaskItemRepo(db)
	} else {
		a.logger.Warn("slack adapter: no gateway store configured — session reuse disabled, every inbound message will create a new ephemeral session; inbound messages and task progress are kept in memory only, so a message or task this instance holds when it stops is lost")
	}

	a.logger.Info("slack adapter initialized",
		"correctMarkdown", cfg.CorrectMarkdown,
		"feedbackEnabled", cfg.FeedbackEnabled,
		"defaultAgent", cfg.DefaultAgentName,
	)
	if cfg.FeedbackEnabled {
		core.WarnIfFeedbackUnloggable("slack", a.logger)
	}
	return nil
}

func (a *SlackGatewayAdapter) Start(ctx context.Context) error {
	a.ctx, a.cancel = context.WithCancel(ctx)

	a.resolveBotIdentity(a.ctx)

	a.core.WarnIfSignInUnreachable(a.logger, "slack")

	if a.socketClient != nil {
		a.registerSocketHandlers(a.socketClient)

		a.wg.Go(func() {
			a.logger.Info("socket mode connecting...")
			err := a.socketClient.RunContext(a.ctx)
			if a.ctx.Err() == nil {
				a.logger.Error("socket mode error", "error", err)
			}
			a.logger.Info("socket mode goroutine exited")
		})
		a.logger.Info("slack adapter started (Socket Mode)")
	} else {
		a.logger.Info("slack adapter started (no Socket Mode — handler injection mode)")
	}

	a.startNotifyDeliverLoop(a.ctx)

	return nil
}

func (a *SlackGatewayAdapter) resolveBotIdentity(ctx context.Context) {
	if a.client == nil {
		a.logger.Error("slack adapter: no Slack client; self-echo guard failing closed (all bot-authored messages dropped)")
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	resp, err := a.client.AuthTestContext(ctx)
	if err != nil {
		a.logger.Error("slack adapter: auth.test failed; self-echo guard failing closed (all bot-authored messages dropped)", "error", err)
		return
	}
	if resp.UserID == "" {
		a.logger.Error("slack adapter: auth.test returned empty user id; self-echo guard failing closed (all bot-authored messages dropped)")
		return
	}
	a.botUserID = resp.UserID
	a.botID = resp.BotID
	a.identityResolved = true
	a.logger.Info("slack adapter: resolved bot identity", "botUserID", resp.UserID, "botID", resp.BotID)
}

func (a *SlackGatewayAdapter) Stop(_ context.Context) error {
	a.logger.Info("stopping slack adapter")

	if a.cancel != nil {
		a.cancel()
	}

	a.wg.Wait()

	a.queuesMu.Lock()
	queues := make(map[string]*MessageQueue, len(a.queues))
	for k, v := range a.queues {
		queues[k] = v
	}
	a.queuesMu.Unlock()

	for taskID, q := range queues {
		a.logger.Debug("stopping message queue", "taskID", taskID)
		q.Stop(10 * time.Second)
	}

	a.logger.Info("slack adapter stopped")
	return nil
}

func (a *SlackGatewayAdapter) RendersStatusText(string) bool { return true }

var _ entrypoint.StatusTextRenderer = (*SlackGatewayAdapter)(nil)

func (a *SlackGatewayAdapter) OnStatusUpdate(ctx context.Context, taskID string, event *a2a.TaskStatusUpdateEvent, _ []byte) {
	meta := a.getTaskMeta(ctx, taskID)
	if meta == nil {
		a.logger.Warn("status update for unknown task", "taskID", taskID)
		return
	}

	if req, ok := a2ui.ExtractRequest(event); ok {
		a.handleUserInputRequest(meta, taskID, req)
		return
	}

	if authData, ok := a2a.AuthenticationRequiredFromStatus(event); ok {
		a.handleAuthRequired(meta, authData)
		return
	}

	if result, ok := a2a.AuthenticationResultFromStatus(event); ok {
		a.resolveAuthPrompt(meta, result.Success)
		return
	}

	q := a.getOrCreateQueue(taskID, meta)

	if event.Status.Message == nil {
		return
	}

	sentFile := false
	for _, part := range event.Status.Message.Parts {
		switch part.Kind {
		case "text":
			if meta.textResumed {
				continue
			}
			if !q.TextWasStreamed() {
				a.items.put(ctx, taskID, itemTextStarted, "")
			}
			q.MarkTextStreamed()
			q.Send(TextUpdateOp{Text: part.Text})
		case "data":
			if isWorkflowToolNodeTelemetry(event, part) {
				continue
			}
			if a.handleDataPart(ctx, taskID, meta, q, part) {
				sentFile = true
			}
		case "file":
			a.handleFilePart(ctx, taskID, q, part)
			sentFile = true
		}
	}
	if sentFile {
		q.Flush()
	}
}

func (a *SlackGatewayAdapter) OnArtifactUpdate(ctx context.Context, taskID string, event *a2a.TaskArtifactUpdateEvent, _ []byte) {
	meta := a.getTaskMeta(ctx, taskID)
	if meta == nil {
		return
	}

	if event.LastChunk {
		a.logger.Debug("artifact complete", "taskID", taskID, "artifactID", event.Artifact.ArtifactID)
	}
}

func (a *SlackGatewayAdapter) OnFinalResponse(ctx context.Context, taskID string, task *a2a.Task, _ []byte) {
	meta := a.getTaskMeta(ctx, taskID)
	if meta == nil {
		a.logger.Warn("final response for unknown task", "taskID", taskID)
		return
	}

	authOK := task.Status.State != a2a.TaskStateFailed &&
		task.Status.State != a2a.TaskStateRejected &&
		task.Status.State != a2a.TaskStateCancelled
	a.resolveAuthPrompt(meta, authOK)

	q := a.getOrCreateQueue(taskID, meta)

	budgetStopped := entrypoint.IsBudgetStoppedTask(task)
	if task.Status.Message != nil {
		for _, part := range task.Status.Message.Parts {
			switch part.Kind {
			case "text":
				if !q.TextWasStreamed() && !budgetStopped {
					q.Send(TextUpdateOp{Text: part.Text})
				}
			case "file":
				a.handleFilePart(ctx, taskID, q, part)
			}
		}
	}
	q.Send(answerDoneOp{})

	q.WaitDrain()

	meta.mu.Lock()
	errorShownEarly := meta.ErrorShown
	meta.mu.Unlock()

	if q.Streaming() {
		var errText string
		if !errorShownEarly {
			switch task.Status.State {
			case a2a.TaskStateCancelled:
				errText = msgTaskCanceled
			case a2a.TaskStateFailed, a2a.TaskStateRejected:
				errText = failedTaskText(task)
			}
		}
		q.Send(StreamFinalizeOp{ErrorText: errText, Notice: failedTaskNoticeKind(task)})
		a.sendFeedbackButtons(q, taskID, meta)
		q.Flush()
		a.markSessionActive(ctx, taskID, meta)
		a.cleanupTask(taskID)
		return
	}

	meta.mu.Lock()
	errorShown := meta.ErrorShown
	meta.mu.Unlock()

	if !errorShown {
		switch task.Status.State {
		case a2a.TaskStateCancelled:
			q.Send(finalizeStatus(meta.StatusTS, noticeError, msgTaskCanceled))
		case a2a.TaskStateFailed, a2a.TaskStateRejected:
			q.Send(finalizeStatus(meta.StatusTS, failedTaskNoticeKind(task), failedTaskText(task)))
		default:
			if meta.StatusTS != "" {
				q.Send(MessageDeleteOp{TS: meta.StatusTS})
			}
		}
	}

	a.sendFeedbackButtons(q, taskID, meta)

	q.Flush()
	a.markSessionActive(ctx, taskID, meta)
	a.cleanupTask(taskID)
}

func (a *SlackGatewayAdapter) markSessionActive(ctx context.Context, taskID string, meta *SlackTaskMeta) {
	if !meta.IsDM {
		return
	}
	const attempts = 2
	var err error
	for attempt := range attempts {
		if err = a.api.SetSessionStatus(ctx, meta.ChannelID, meta.ThreadTS, "active"); err == nil {
			return
		}
		if attempt < attempts-1 {
			a.logger.Debug("failed to clear agent session processing status; retrying",
				"taskID", taskID, "channelID", meta.ChannelID, "threadTS", meta.ThreadTS, "attempt", attempt, "error", err)
		}
	}
	a.warnSessionStatusFailed(taskID, meta.ChannelID, meta.ThreadTS, err)
}

func (a *SlackGatewayAdapter) sendFeedbackButtons(q *MessageQueue, taskID string, meta *SlackTaskMeta) {
	if !a.cfg.FeedbackEnabled {
		return
	}
	blocks := NativeFeedbackButtons(taskID, "", meta.SessionID)
	if len(blocks) == 0 {
		return
	}
	q.Send(MessagePostOp{
		Text:   "How was this response?",
		Blocks: blocks,
		Notice: noticeInfo,
	})
}

func (a *SlackGatewayAdapter) OnError(ctx context.Context, taskID string, taskErr *a2a.Error, _ []byte) {
	meta := a.getTaskMeta(ctx, taskID)
	if meta == nil {
		a.logger.Warn("error for unknown task", "taskID", taskID)
		return
	}

	meta.mu.Lock()
	meta.ErrorShown = true
	meta.mu.Unlock()

	a.resolveAuthPrompt(meta, false)

	q := a.getOrCreateQueue(taskID, meta)

	q.WaitDrain()

	errorText := fmt.Sprintf("❌ Error: %s", taskErr.Message)
	if entrypoint.IsTaskTimeoutError(taskErr) {
		errorText = taskErr.Message
	} else if strings.Contains(strings.ToLower(taskErr.Message), "cancel") {
		errorText = msgTaskCanceled
	}

	notice := noticeError
	if entrypoint.IsBudgetStop(taskErr) {
		notice = noticeBudget
	}
	if q.Streaming() {
		q.Send(StreamFinalizeOp{ErrorText: errorText, Notice: notice})
	} else {
		q.Send(finalizeStatus(meta.StatusTS, notice, errorText))
	}

	q.Flush()
	a.markSessionActive(ctx, taskID, meta)
	a.cleanupTask(taskID)
}

func (a *SlackGatewayAdapter) OnAgentDiscovered(card *a2a.AgentCard) {
	a.logger.Info("agent discovered", "agentName", card.Name)
}

func (a *SlackGatewayAdapter) OnAgentRemoved(agentName string) {
	a.logger.Info("agent removed", "agentName", agentName)
}

const maxConversationContextBytes = entrypoint.MaxConversationContextBytes

func truncateConversationTail(s string) string {
	return entrypoint.TruncateAttributedConversationTail(s)
}

const slackContextFetchTimeout = 10 * time.Second

func (a *SlackGatewayAdapter) fetchConversationContext(ctx context.Context, channelID, threadTS, answeringTS string, inThread bool, scope entrypoint.ArtifactScope) string {
	ctx, cancel := context.WithTimeout(ctx, slackContextFetchTimeout)
	defer cancel()

	var (
		msgs    []goslack.Message
		err     error
		subject string
	)
	switch {
	case inThread && a.cfg.ThreadHistoryLimit > 0:
		msgs, err = a.api.GetConversationReplies(ctx, channelID, threadTS, a.cfg.ThreadHistoryLimit)
		subject = "Slack thread this message was posted in"
	case !inThread && a.cfg.ChannelHistoryLimit > 0:
		msgs, err = a.api.GetConversationHistory(ctx, channelID, a.cfg.ChannelHistoryLimit)
		subject = "most recent messages in the Slack channel this message was posted in"
	default:
		return ""
	}
	if err != nil {
		a.warnContextFetchFailed(channelID, inThread, err)
		return ""
	}

	if !inThread {
		slices.Reverse(msgs)
	}
	msgs = withoutAnsweredMessage(msgs, answeringTS)

	attachments := a.collectHistoryAttachments(ctx, msgs, scope, channelID, threadTS)

	var b strings.Builder
	for i := range msgs {
		text := strings.TrimSpace(msgs[i].Text)
		atts := attachments[i]
		if text == "" && len(atts) == 0 {
			continue
		}

		var body strings.Builder
		body.WriteString(text)
		for _, att := range atts {
			if body.Len() > 0 {
				body.WriteByte(' ')
			}
			fmt.Fprintf(&body, "[shared %q — call load_artifact with filename=%q to view it]", att.displayName, att.artifactFilename)
		}

		b.WriteString("[")
		b.WriteString(transcriptTime(&msgs[i]))
		b.WriteString("] ")
		b.WriteString(a.transcriptAuthor(&msgs[i]))
		b.WriteString(": ")
		b.WriteString(indentContinuationLines(body.String()))
		b.WriteByte('\n')
	}
	transcript := strings.TrimSpace(b.String())
	if transcript == "" {
		return ""
	}

	transcript = truncateConversationTail(transcript)
	transcript = a.emailCache.ResolveMentions(ctx, transcript)

	return entrypoint.FenceAttributedConversation("SLACK", subject, transcript)
}

const transcriptTimeLayout = "2006-01-02 15:04 UTC"

func transcriptTime(msg *goslack.Message) string {
	ts := msg.Timestamp
	if msg.Edited != nil && msg.Edited.Timestamp != "" {
		ts = msg.Edited.Timestamp
	}
	secs, _, _ := strings.Cut(ts, ".")
	unix, err := strconv.ParseInt(secs, 10, 64)
	if err != nil || unix <= 0 {
		return "unknown time"
	}
	return time.Unix(unix, 0).UTC().Format(transcriptTimeLayout)
}

func (a *SlackGatewayAdapter) transcriptAuthor(msg *goslack.Message) string {
	if kind, ok := a.ownNoticeKind(msg); ok {
		return "gateway notice (" + string(kind) + "; state as of the time shown)"
	}
	if a.isOwnMessage(msg) {
		return "assistant (agent reply)"
	}
	switch {
	case msg.BotID != "":
		return appAuthorLabel(cmp.Or(msg.Username, msg.BotID))
	case msg.User != "":
		return "user <@" + msg.User + ">"
	case msg.Username != "":
		return appAuthorLabel(msg.Username)
	}
	return "unknown author"
}

func appAuthorLabel(name string) string {
	return fmt.Sprintf("app %q", strings.Join(strings.Fields(name), " "))
}

func (a *SlackGatewayAdapter) isOwnMessage(msg *goslack.Message) bool {
	return a.identityResolved && isSelfAuthored(msg.User, msg.BotID, a.botUserID, a.botID)
}

func indentContinuationLines(body string) string {
	return strings.ReplaceAll(entrypoint.NormalizeLineTerminators(body), "\n", "\n  ")
}

func withoutAnsweredMessage(msgs []goslack.Message, ts string) []goslack.Message {
	if ts == "" {
		return msgs
	}
	return slices.DeleteFunc(msgs, func(m goslack.Message) bool { return m.Timestamp == ts })
}

type historyAttachment struct {
	displayName      string
	artifactFilename string
}

func isHistoryAttachmentMIME(mimeType string) bool {
	return strings.HasPrefix(mimeType, "image/") || mimeType == "application/pdf"
}

const historyAttachmentFetchTimeout = 5 * time.Second

const historyAttachmentInFlightTTL = 30 * time.Second

func (a *SlackGatewayAdapter) collectHistoryAttachments(ctx context.Context, msgs []goslack.Message, scope entrypoint.ArtifactScope, channelID, threadTS string) map[int][]historyAttachment {
	if a.cfg.HistoryMaxAttachments <= 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, historyAttachmentFetchTimeout)
	defer cancel()

	artSvc := a.core.Services().Artifacts()
	attemptsLeft := a.cfg.HistoryMaxAttachments
	result := make(map[int][]historyAttachment)
	for i := len(msgs) - 1; i >= 0 && attemptsLeft > 0; i-- {
		for _, f := range msgs[i].Files {
			if attemptsLeft <= 0 {
				break
			}
			if !isHistoryAttachmentMIME(f.Mimetype) {
				continue
			}
			if f.Size > 0 && int64(f.Size) > maxInboundFileBytes {
				a.warnHistoryAttachmentOnce(f.ID, "skipping oversized historical slack file",
					"channelID", channelID, "threadTS", threadTS, "filename", f.Name, "fileID", f.ID, "size", f.Size)
				attemptsLeft--
				continue
			}
			attemptsLeft--
			att, ok := a.resolveHistoryAttachment(ctx, f, artSvc, scope, channelID, threadTS)
			if !ok {
				continue
			}
			result[i] = append(result[i], att)
		}
	}
	return result
}

func (a *SlackGatewayAdapter) warnHistoryAttachmentOnce(fileID, msg string, fields ...any) {
	if _, seen := a.historyAttachmentWarned.LoadOrStore(fileID, struct{}{}); !seen {
		a.logger.Warn(msg, fields...)
		return
	}
	a.logger.Debug(msg, fields...)
}

func historyAttachmentCacheKey(scope entrypoint.ArtifactScope, fileID string) string {
	return scope.AppName + "\x00" + scope.UserID + "\x00" + scope.SessionID + "\x00" + fileID
}

func (a *SlackGatewayAdapter) resolveHistoryAttachment(ctx context.Context, f goslack.File, artSvc *artifact.ScopedService, scope entrypoint.ArtifactScope, channelID, threadTS string) (historyAttachment, bool) {
	displayName := f.Name
	if displayName == "" {
		displayName = f.Title
	}
	if displayName == "" {
		displayName = f.ID
	}
	artifactFilename := f.ID + "_" + displayName
	att := historyAttachment{displayName: displayName, artifactFilename: artifactFilename}

	loc := artSvc.Resolve(scope.AppName, scope.UserID, scope.SessionID, artifactFilename)
	alreadySaved := func() bool {
		meta, err := artSvc.Inner.GetVersionMetadata(ctx, loc, nil)
		return err == nil && meta != nil
	}
	if alreadySaved() {
		return att, true
	}

	cacheKey := historyAttachmentCacheKey(scope, f.ID)
	if !a.historyAttachmentCache.SetIfAbsent(cacheKey, struct{}{}, runtime.WithTTL(historyAttachmentInFlightTTL)) {
		return historyAttachment{}, false
	}
	defer a.historyAttachmentCache.Delete(cacheKey)
	if alreadySaved() {
		return att, true
	}

	data, err := a.fetchSlackFileBytes(ctx, slackFileInfo{
		ID:                 f.ID,
		Name:               f.Name,
		Title:              f.Title,
		MimeType:           f.Mimetype,
		URLPrivate:         f.URLPrivate,
		URLPrivateDownload: f.URLPrivateDownload,
	})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			a.logger.Debug("historical slack file download timed out", "channelID", channelID, "threadTS", threadTS, "filename", displayName, "fileID", f.ID, "error", err)
		} else {
			a.warnHistoryAttachmentOnce(f.ID, "failed to download historical slack file",
				"channelID", channelID, "threadTS", threadTS, "filename", displayName, "fileID", f.ID, "error", err)
		}
		return historyAttachment{}, false
	}

	_, ok := entrypoint.SaveBytesAsArtifact(ctx, artSvc, scope, entrypoint.ArtifactUpload{
		Filename:    artifactFilename,
		MIMEType:    f.Mimetype,
		Content:     data,
		Description: "Slack attachment from earlier in this conversation",
	}, a.logger)
	if !ok {
		return historyAttachment{}, false
	}
	return att, true
}

func (a *SlackGatewayAdapter) warnContextFetchFailed(channelID string, inThread bool, err error) {
	if errors.Is(err, context.DeadlineExceeded) {
		a.logger.Debug("slack conversation-context fetch timed out; proceeding without context", "channelID", channelID, "inThread", inThread)
		return
	}
	if _, seen := a.contextFetchWarned.LoadOrStore(channelID, struct{}{}); !seen {
		a.logger.Warn("slack conversation-context fetch failed; agent runs without thread/channel context for this channel — ensure the bot has channels:history/groups:history/im:history and is a member of the channel",
			"channelID", channelID, "inThread", inThread, "error", err)
		return
	}
	a.logger.Debug("slack conversation-context fetch failed", "channelID", channelID, "inThread", inThread, "error", err)
}

func (a *SlackGatewayAdapter) warnSessionStatusFailed(taskID, channelID, threadTS string, err error) {
	if _, seen := a.sessionStatusWarned.LoadOrStore(channelID, struct{}{}); !seen {
		a.logger.Warn("failed to set agent session status; this workspace may not have the Agent messaging experience enabled",
			"taskID", taskID, "channelID", channelID, "threadTS", threadTS, "error", err)
		return
	}
	a.logger.Debug("failed to set agent session status", "taskID", taskID, "channelID", channelID, "threadTS", threadTS, "error", err)
}

type slackInbound struct {
	channelID    string
	threadTS     string
	userID       string
	teamID       string
	text         string
	files        []slackFileInfo
	inThread     bool
	isDM         bool
	messageTS    string
	mentionMsgTS string
	hold         *inboundHold
}

type mentionSlot struct {
	taskID     string
	reservedAt time.Time
}

var mentionSlotMaxAge = 2 * time.Hour

func mentionKey(channelID, messageTS string) string {
	if messageTS == "" {
		return ""
	}
	return channelID + ":" + messageTS
}

func (a *SlackGatewayAdapter) claimMention(key string) (claimed, pending bool) {
	if key == "" || a.inFlightMentions == nil {
		return true, false
	}
	a.mentionMu.Lock()
	slot, ok := a.inFlightMentions[key]
	if !ok || slot.expired() {
		a.inFlightMentions[key] = mentionSlot{reservedAt: time.Now()}
		a.mentionMu.Unlock()
		return true, false
	}
	if slot.taskID == "" {
		a.mentionMu.Unlock()
		return false, true
	}
	a.mentionMu.Unlock()

	running, known := a.core.Tasks().RunState(slot.taskID)
	busy := running || !known

	a.mentionMu.Lock()
	defer a.mentionMu.Unlock()
	current, still := a.inFlightMentions[key]
	switch {
	case still && current != slot && !current.expired():
		return false, current.taskID == ""
	case still && current == slot && busy:
		return false, false
	}
	a.inFlightMentions[key] = mentionSlot{reservedAt: time.Now()}
	return true, false
}

func (s mentionSlot) expired() bool {
	return time.Since(s.reservedAt) > mentionSlotMaxAge
}

func (a *SlackGatewayAdapter) bindMention(key, taskID string) {
	if key == "" || a.inFlightMentions == nil {
		return
	}
	a.mentionMu.Lock()
	slot := a.inFlightMentions[key]
	slot.taskID = taskID
	if slot.reservedAt.IsZero() {
		slot.reservedAt = time.Now()
	}
	a.inFlightMentions[key] = slot
	a.mentionMu.Unlock()
}

func (a *SlackGatewayAdapter) releaseMentionKey(key string) {
	if key == "" || a.inFlightMentions == nil {
		return
	}
	a.mentionMu.Lock()
	delete(a.inFlightMentions, key)
	a.mentionMu.Unlock()
}

func (a *SlackGatewayAdapter) releaseMentionTask(taskID string) {
	if taskID == "" || a.inFlightMentions == nil {
		return
	}
	a.mentionMu.Lock()
	for k, slot := range a.inFlightMentions {
		if slot.taskID == taskID {
			delete(a.inFlightMentions, k)
		}
	}
	a.mentionMu.Unlock()
}

func (a *SlackGatewayAdapter) submitSlackTask(in slackInbound) bool {
	reason, settled := a.trySubmit(in)
	a.core.ReportSubmitRefusal(reason)
	return settled
}

func (a *SlackGatewayAdapter) trySubmit(in slackInbound) (string, bool) {
	channelID, threadTS, userID, teamID, text := in.channelID, in.threadTS, in.userID, in.teamID, in.text
	files, inThread, isDM := in.files, in.inThread, in.isDM

	mKey := mentionKey(channelID, in.mentionMsgTS)
	committed := false
	defer func() {
		if !committed {
			a.releaseMentionKey(mKey)
		}
	}()

	realIdentity := a.emailCache.ResolveIdentity(userID, teamID)

	identity, runAs := auth.ForChatUser(a.ctx, realIdentity, a.core, auth.ChannelPolicy{
		IsMultiParty:              !isDM,
		UseUserIdentityInChannels: a.cfg.UseUserIdentityInChannels,
		RunAs:                     a.cfg.RunAs,
	})
	ctx := a.ctx

	a.publishPresence(ctx, realIdentity.UserID(), realIdentity.Email(), slackProactiveAddress{
		TeamID:    teamID,
		UserID:    userID,
		ChannelID: channelID,
	})

	res := a.resolveTarget(ctx, identity, text)
	if res.Misconfigured {
		return taxonomy.RejectAgentNotFound, a.postUserError(channelID, threadTS, noticeRefusal, fmt.Sprintf("This Slack entrypoint is configured to route to `%s`, but no such agent is currently available. Contact the entrypoint operator.", a.cfg.DefaultAgentName))
	}
	if res.Refused() {
		return res.RefusalReason, a.postUserError(channelID, threadTS, noticeKindForReason(res.RefusalReason), res.Refusal)
	}
	agentName := res.Name
	if agentName == "" {
		a.logger.Warn("no target agent found")
		return taxonomy.RejectAgentNotFound, a.postUserError(channelID, threadTS, noticeRefusal, "No agent available to handle this request. Use `!help` to see discovered agents.")
	}

	taskID := idgen.New()
	externalKey := buildExternalKey(channelID, threadTS)
	sessionID, err := a.resolveSessionID(ctx, externalKey, agentName, identity.UserID())
	if err != nil {
		a.logger.Error("failed to resolve session id", "externalKey", externalKey, "error", err)
		return taxonomy.RejectInternal, a.postUserError(channelID, threadTS, noticeError, "Sorry, I couldn't start a session for this thread. Please try again, or contact your administrator if the problem persists.")
	}

	if isDM {
		if err := a.api.SetSessionStatus(ctx, channelID, threadTS, "processing"); err != nil {
			a.warnSessionStatusFailed(taskID, channelID, threadTS, err)
		}
	}

	text = a.emailCache.ResolveMentions(ctx, text)

	convCtx := a.fetchConversationContext(ctx, channelID, threadTS, in.messageTS, inThread,
		entrypoint.ArtifactScope{UserID: identity.UserID(), SessionID: sessionID})

	if runAs {
		if convCtx != "" {
			convCtx = entrypoint.ChannelRunAsSystemNote + "\n\n" + convCtx
		} else {
			convCtx = entrypoint.ChannelRunAsSystemNote
		}
	}

	parts := make([]a2a.Part, 0, len(files)+1)
	parts = append(parts, a2a.NewTextPart(text))
	for _, fi := range files {
		part, err := a.downloadSlackFile(ctx, fi)
		if err != nil {
			a.logger.Warn("failed to download slack file", "filename", fi.Name, "fileID", fi.ID, "error", err)
			if errors.Is(err, errFileTooLarge) {
				a.postUserError(channelID, threadTS, noticeError, err.Error())
			}
			continue
		}
		parts = append(parts, part)
		a.logger.Debug("attached slack file to task", "taskID", taskID, "filename", fi.Name, "mimeType", fi.MimeType)
	}

	msg := a2a.Message{
		Kind:      "message",
		MessageID: idgen.New(),
		Role:      "user",
		Parts:     parts,
		TaskID:    taskID,
		ContextID: sessionID,
	}
	if convCtx != "" {
		msg.Metadata = map[string]any{"conversation_context": convCtx}
	}
	if runAs {
		if msg.Metadata == nil {
			msg.Metadata = map[string]any{}
		}
		msg.Metadata["requesting_user"] = realIdentity.UserID()
		msg.Metadata["run_as"] = identity.UserID()
	}

	dropped := entrypoint.ConvertInlineFilePartsToArtifacts(
		ctx, &msg, a.core.Services().Artifacts(),
		entrypoint.ArtifactScope{UserID: identity.UserID(), SessionID: sessionID}, a.logger)
	if len(dropped) > 0 {
		a.postUserError(channelID, threadTS, noticeError, fmt.Sprintf("Couldn't attach %d file(s) (%s) — the entrypoint has no artifact storage configured.", len(dropped), strings.Join(dropped, ", ")))
		entrypoint.AppendDroppedFilesNote(&msg, dropped)
	}

	meta := &SlackTaskMeta{
		ChannelID: channelID,
		ThreadTS:  threadTS,
		SessionID: sessionID,
		UserID:    userID,
		TeamID:    teamID,
		IsDM:      isDM,
		AgentName: agentName,
		OwnerID:   identity.UserID(),
		taskID:    taskID,
	}
	if !in.hold.renew(a.core) {
		a.logger.Warn("slack message taken over by another instance before its submit; dropping it here", "messageKey", in.hold.key)
		return "", false
	}
	if err := a.core.Tasks().Register(taskID, agentName, identity.UserID(), entrypoint.TaskIdentity{}); err != nil {
		a.logger.Error("failed to record task", "taskID", taskID, "error", err)
		posted := a.postUserError(channelID, threadTS, noticeKindForSubmitFailure(err), a.core.SubmitFailureText(err, true))
		a.markSessionActive(ctx, taskID, meta)
		return entrypoint.SubmitFailureReason(err), posted
	}
	if err := a.items.writeDestination(ctx, taskID, meta); err != nil {
		a.logger.Error("failed to record task destination", "taskID", taskID, "error", err)
		posted := a.postUserError(channelID, threadTS, noticeKindForSubmitFailure(err), a.core.SubmitFailureText(err, true))
		a.markSessionActive(ctx, taskID, meta)
		a.core.Tasks().Remove(taskID)
		return entrypoint.SubmitFailureReason(err), posted
	}
	a.core.Tasks().SetMetadata(taskID, meta)
	a.bindMention(mKey, taskID)

	if a.cfg.ShowActivityTimeline {
		a.openStream(taskID, meta)
	} else if a.cfg.InitialStatusMessage != "" {
		_, ts, err := a.api.PostMessage(a.ctx, channelID,
			goslack.MsgOptionText(a.cfg.InitialStatusMessage, false),
			goslack.MsgOptionTS(threadTS),
			goslack.MsgOptionBlocks(StatusBlocks(a.cfg.InitialStatusMessage)...),
			noticeMetadata(noticeStatus),
		)
		if err != nil {
			a.logger.Warn("failed to post status message", "error", err)
		} else {
			meta.StatusTS = ts
			a.items.put(ctx, taskID, itemStatusMsgTS, ts)
		}
	}

	if err := a.core.SubmitTask(ctx, agentName, taskID, msg, identity); err != nil {
		entrypoint.LogSubmitFailure(a.logger, slog.LevelError, err, "taskID", taskID)
		var posted bool
		submitErr := a.core.SubmitFailureText(err, true)
		notice := noticeKindForSubmitFailure(err)
		if a.cfg.ShowActivityTimeline {
			q := a.getOrCreateQueue(taskID, meta)
			q.Send(StreamFinalizeOp{ErrorText: submitErr, Notice: notice})
			q.Flush()
			a.cleanupTask(taskID)
			posted = a.ctx.Err() == nil
		} else {
			posted = a.postUserError(channelID, threadTS, notice, submitErr)
		}
		a.markSessionActive(ctx, taskID, meta)
		a.core.Tasks().Remove(taskID)
		return entrypoint.SubmitFailureReason(err), posted
	}
	committed = true

	var traceID string
	if info := a.core.Tasks().Get(taskID); info != nil {
		traceID = info.TraceID
	}
	a.logger.Info("task submitted", "taskID", taskID, "agentName", agentName,
		"userID", identity.UserID(), "requestingUser", realIdentity.UserID(), "runAs", runAs, "traceID", traceID)
	return "", true
}

func (a *SlackGatewayAdapter) resolveTarget(ctx context.Context, caller *auth.Subject, text string) entrypoint.IngressTarget {
	target := a.core.ResolveIngressTarget(ctx, entrypoint.IngressTargetRequest{
		Caller:           caller,
		Text:             text,
		DefaultAgentName: a.cfg.DefaultAgentName,
		SignInLinkable:   true,
	})
	if target.Misconfigured {
		a.logger.Warn("configured default_agent_name not found in registry",
			"eventType", "slack.default_agent_misconfigured",
			"configured", a.cfg.DefaultAgentName,
			"discovered", a.core.AgentRegistry().Names(),
		)
	}
	return target
}

func isWorkflowToolNodeTelemetry(event *a2a.TaskStatusUpdateEvent, part a2a.Part) bool {
	data, ok := part.Data.(map[string]any)
	if !ok {
		return false
	}
	if kind, _ := data["type"].(string); kind != string(a2a.SignalToolInvocationStart) && kind != string(a2a.SignalToolResult) {
		return false
	}
	if nodeType, _ := event.Metadata["node_type"].(string); nodeType == "tool" {
		return true
	}
	if event.Status.Message != nil {
		if nodeType, _ := event.Status.Message.Metadata["node_type"].(string); nodeType == "tool" {
			return true
		}
	}
	return false
}

func (a *SlackGatewayAdapter) handleDataPart(ctx context.Context, taskID string, meta *SlackTaskMeta, q *MessageQueue, part a2a.Part) (finalArtifact bool) {
	data := part.Data
	if data == nil {
		return false
	}

	dataMap, ok := data.(map[string]any)
	if !ok {
		return false
	}

	dataType, _ := dataMap["type"].(string)

	if q.Streaming() {
		if !q.trySend(ActivitySignalOp{Type: dataType, Data: dataMap}) {
			a.logger.Debug("dropped activity signal under backpressure", "taskID", taskID, "type", dataType)
		}
		if dataType == "artifact_creation_progress" {
			return a.handleArtifactProgress(ctx, taskID, meta, q, dataMap)
		}
		return false
	}

	switch dataType {
	case "agent_progress_update":
		statusText, _ := dataMap["status_text"].(string)
		if statusText != "" && meta.StatusTS != "" {
			q.Send(finalizeStatus(meta.StatusTS, noticeStatus, "⏳ "+statusText))
		}

	case "artifact_creation_progress":
		return a.handleArtifactProgress(ctx, taskID, meta, q, dataMap)
	}
	return false
}

func (a *SlackGatewayAdapter) handleArtifactProgress(ctx context.Context, taskID string, meta *SlackTaskMeta, q *MessageQueue, data map[string]any) bool {
	status, _ := data["status"].(string)
	filename, _ := data["filename"].(string)
	if filename == "" {
		filename = "unknown file"
	}

	if entrypoint.DataTagsContainWorking(data) {
		if status == "completed" || status == "failed" {
			q.Send(ArtifactProgressOp{Status: "completed", Filename: filename})
		}
		return false
	}

	switch status {
	case "in-progress":
		if q.Streaming() {
			return false
		}
		bytesTransferred, _ := data["bytes_transferred"].(float64)
		progressText := fmt.Sprintf("%s Creating `%s`...", IconForMimeType(""), filename)
		if bytesTransferred > 0 {
			progressText += fmt.Sprintf(" (%d bytes)", int(bytesTransferred))
		}
		q.Send(artifactProgressOp("in-progress", noticeStatus, filename, progressText))
		return false

	case "completed":
		description, _ := data["description"].(string)
		if description == "" {
			description = "N/A"
		}
		mimeType, _ := data["mime_type"].(string)
		completeText := fmt.Sprintf("%s Artifact Created: `%s`\n*Description*: %s", IconForMimeType(mimeType), filename, description)

		version := entrypoint.DataIntPtr(data["version"])
		q.Send(ArtifactProgressOp{Status: "completed", Filename: filename})
		if !a.loadAndUploadArtifact(ctx, taskID, filename, filename, version, completeText, q) {
			q.Send(MessagePostOp{Text: completeText, Notice: noticeInfo})
		}
		return true

	case "failed":
		q.Send(artifactProgressOp("failed", noticeError, filename, fmt.Sprintf("❌ Failed to create artifact: `%s`", filename)))
		return true
	}
	return false
}

func artifactProgressOp(status string, kind noticeKind, filename, text string) ArtifactProgressOp {
	return ArtifactProgressOp{
		Status:   status,
		Filename: filename,
		Text:     text,
		Blocks:   ContentBlocks(text),
		Notice:   kind,
	}
}

func (a *SlackGatewayAdapter) loadAndUploadArtifact(ctx context.Context, taskID, filename, displayName string, version *int, initialComment string, q *MessageQueue) bool {
	artSvc := a.core.Services().Artifacts()
	if artSvc == nil {
		a.logger.Debug("artifact upload skipped: no artifact service configured",
			"taskID", taskID, "filename", filename)
		return false
	}
	meta := a.getTaskMeta(ctx, taskID)
	if meta == nil {
		a.logger.Warn("artifact upload skipped: task meta missing",
			"taskID", taskID, "filename", filename)
		return false
	}
	agentName, userID := meta.AgentName, meta.OwnerID
	if agentName == "" || userID == "" {
		info := a.core.Tasks().Get(taskID)
		if info == nil {
			a.logger.Warn("artifact upload skipped: task not tracked", "taskID", taskID, "filename", filename)
			return false
		}
		agentName, userID = info.AgentName, info.UserID
	}

	loc := artSvc.Resolve(agentName, userID, meta.SessionID, filename)
	content, mimeType, err := artSvc.Inner.Load(ctx, loc, version)
	if err != nil {
		a.logger.Error("failed to load artifact for slack upload",
			"taskID", taskID, "agentName", agentName, "filename", filename, "error", err)
		return false
	}
	if len(content) == 0 {
		a.logger.Warn("artifact load returned empty content",
			"taskID", taskID, "agentName", agentName, "filename", filename)
		return false
	}

	q.Send(FileUploadOp{
		Filename:       displayName,
		Content:        content,
		InitialComment: initialComment,
		MimeType:       mimeType,
	})
	return true
}

func (a *SlackGatewayAdapter) handleFilePart(ctx context.Context, taskID string, q *MessageQueue, part a2a.Part) {
	fc := part.File
	if fc == nil {
		return
	}

	if fc.Bytes != "" {
		content, err := base64.StdEncoding.DecodeString(fc.Bytes)
		if err != nil {
			a.logger.Error("failed to decode file bytes", "taskID", taskID, "error", err)
			return
		}
		q.Send(FileUploadOp{
			Filename:       fc.Name,
			Content:        content,
			InitialComment: fmt.Sprintf("File: %s", fc.Name),
			MimeType:       fc.MIMEType,
		})
		return
	}
	if fc.URI == "" {
		return
	}

	filename, version, ok := entrypoint.ParseArtifactURI(fc.URI)
	if ok && filename != "" {
		displayName := fc.Name
		if displayName == "" {
			displayName = filename
		}
		comment := fmt.Sprintf("File: %s", displayName)
		if a.loadAndUploadArtifact(ctx, taskID, filename, displayName, version, comment, q) {
			return
		}
	}
	q.Send(MessagePostOp{
		Text:   fmt.Sprintf(":link: Artifact available: %s — %s", fc.Name, fc.URI),
		Notice: noticeInfo,
	})
}

var allowedSlackFileHostSuffixes = []string{
	"slack.com",
	"slack-files.com",
	"files.slack.com",
}

func validateSlackFileURL(rawURL string, extraAllowedOrigins ...string) error {
	if rawURL == "" {
		return fmt.Errorf("empty URL")
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("parse URL: %w", err)
	}
	if u.Hostname() == "" {
		return fmt.Errorf("URL has no host")
	}
	reqOrigin := strings.ToLower(u.Scheme + "://" + u.Host)
	for _, allowed := range extraAllowedOrigins {
		if strings.ToLower(allowed) == reqOrigin {
			return nil
		}
	}
	if u.Scheme != "https" {
		return fmt.Errorf("URL must use https scheme, got %q", u.Scheme)
	}
	host := strings.ToLower(u.Hostname())
	for _, suffix := range allowedSlackFileHostSuffixes {
		if host == suffix || strings.HasSuffix(host, "."+suffix) {
			return nil
		}
	}
	return fmt.Errorf("refusing to download from unexpected host %q", host)
}

func (a *SlackGatewayAdapter) configuredExtraAllowedOrigins() []string {
	if a.cfg == nil || a.cfg.APIBaseURL == "" {
		return nil
	}
	u, err := url.Parse(a.cfg.APIBaseURL)
	if err != nil || u.Host == "" {
		return nil
	}
	return []string{u.Scheme + "://" + u.Host}
}

func (a *SlackGatewayAdapter) fetchSlackFileBytes(ctx context.Context, fi slackFileInfo) ([]byte, error) {
	rawURL := fi.URLPrivateDownload
	if rawURL == "" {
		rawURL = fi.URLPrivate
	}
	if rawURL == "" {
		return nil, fmt.Errorf("file %q has no private URL", fi.Name)
	}
	if err := validateSlackFileURL(rawURL, a.configuredExtraAllowedOrigins()...); err != nil {
		return nil, fmt.Errorf("file %q: %w", fi.Name, err)
	}

	var buf bytes.Buffer
	if err := a.api.GetFile(ctx, rawURL, newLimitedWriter(&buf, maxInboundFileBytes)); err != nil {
		if errors.Is(err, errFileTooLarge) {
			return nil, fmt.Errorf("file %q is too large (max %d MB): %w", fi.Name, maxInboundFileBytes>>20, errFileTooLarge)
		}
		return nil, fmt.Errorf("get file %q: %w", fi.Name, err)
	}
	return buf.Bytes(), nil
}

func (a *SlackGatewayAdapter) downloadSlackFile(ctx context.Context, fi slackFileInfo) (a2a.Part, error) {
	data, err := a.fetchSlackFileBytes(ctx, fi)
	if err != nil {
		return a2a.Part{}, err
	}

	mime := fi.MimeType
	if mime == "" {
		mime = "application/octet-stream"
	}
	name := fi.Name
	if name == "" {
		name = fi.Title
	}
	if name == "" {
		name = fi.ID
	}
	return a2a.NewFilePartWithBytes(base64.StdEncoding.EncodeToString(data), name, mime), nil
}

func (a *SlackGatewayAdapter) LoadTask(ctx context.Context, taskID string) error {
	_, err := a.taskMeta(ctx, taskID)
	return err
}

var _ entrypoint.TaskStateLoader = (*SlackGatewayAdapter)(nil)

func (a *SlackGatewayAdapter) getTaskMeta(ctx context.Context, taskID string) *SlackTaskMeta {
	meta, err := a.taskMeta(ctx, taskID)
	if err != nil {
		a.logger.Warn("slack task items unreadable", "taskID", taskID, "error", err)
	}
	return meta
}

func (a *SlackGatewayAdapter) taskMeta(ctx context.Context, taskID string) (*SlackTaskMeta, error) {
	meta, _ := a.core.Tasks().Metadata(taskID).(*SlackTaskMeta)
	if !a.items.enabled() {
		return meta, nil
	}
	if meta != nil && !meta.refreshDue() {
		return meta, nil
	}
	lock := a.taskLock(taskID)
	lock.Lock()
	if meta, _ = a.core.Tasks().Metadata(taskID).(*SlackTaskMeta); meta != nil {
		var resume func()
		if meta.refreshDue() {
			resume = a.refreshFromItems(ctx, meta)
		}
		lock.Unlock()
		if resume != nil {
			resume()
		}
		return meta, nil
	}
	defer lock.Unlock()
	loaded, err := a.items.load(ctx, taskID)
	if err != nil || loaded == nil {
		return nil, err
	}
	a.core.Tasks().SetMetadata(taskID, loaded)
	meta, _ = a.core.Tasks().Metadata(taskID).(*SlackTaskMeta)
	if meta != nil {
		a.logger.Debug("took slack task submitted by another instance", "taskID", taskID)
	}
	return meta, nil
}

const refreshRetryDelay = 5 * time.Second

func (m *SlackTaskMeta) refreshDue() bool {
	return !m.syncedFromItems.Load() && time.Now().UnixMilli() >= m.refreshRetryAt.Load()
}

func (a *SlackGatewayAdapter) taskLock(taskID string) *sync.Mutex {
	h := fnv.New32a()
	_, _ = h.Write([]byte(taskID))
	return &a.adoptLocks[h.Sum32()%uint32(len(a.adoptLocks))]
}

func (a *SlackGatewayAdapter) refreshFromItems(ctx context.Context, meta *SlackTaskMeta) func() {
	stored, err := a.items.load(ctx, meta.taskID)
	if err != nil {
		meta.refreshRetryAt.Store(time.Now().Add(refreshRetryDelay).UnixMilli())
		a.logger.Warn("slack task items unreadable; using this instance's copy", "taskID", meta.taskID, "error", err)
		return nil
	}
	if stored == nil {
		meta.syncedFromItems.Store(true)
		return nil
	}
	a.queuesMu.Lock()
	q := a.queues[meta.taskID]
	if q == nil {
		meta.resumed = stored.resumed
	}
	a.queuesMu.Unlock()
	meta.textResumed = stored.textResumed && (q == nil || !q.TextWasStreamed())
	if meta.StatusTS == "" {
		meta.StatusTS = stored.StatusTS
	}
	meta.mu.Lock()
	for _, ts := range stored.authPromptTSs {
		if _, ok := meta.authPromptItems[ts]; !ok {
			meta.addAuthPrompt(stored.authPromptItems[ts], ts)
		}
	}
	meta.mu.Unlock()
	meta.syncedFromItems.Store(true)
	if q == nil {
		return nil
	}
	return func() { q.Send(resumeOp{r: stored.resumed}) }
}

const streamStartWait = 5 * time.Second

func (a *SlackGatewayAdapter) openStream(taskID string, meta *SlackTaskMeta) {
	q := a.getOrCreateQueue(taskID, meta)
	if !a.items.enabled() {
		q.Send(StreamStartOp{})
		return
	}
	started := make(chan struct{})
	q.Send(StreamStartOp{started: started})
	select {
	case <-started:
	case <-time.After(streamStartWait):
		q.streamLate.Store(true)
		a.logger.Warn("activity stream did not open in time; submitting without it", "taskID", taskID)
	}
}

func (a *SlackGatewayAdapter) getOrCreateQueue(taskID string, meta *SlackTaskMeta) *MessageQueue {
	a.queuesMu.Lock()
	defer a.queuesMu.Unlock()

	if q, ok := a.queues[taskID]; ok {
		return q
	}

	q := NewMessageQueue(taskID, meta.ChannelID, meta.ThreadTS, a.api, a.cfg.CorrectMarkdown, a.logger)
	if a.items.enabled() {
		q.record = func(item, value string) { a.items.put(context.Background(), taskID, item, value) }
		q.forget = func(item string) { a.items.delete(context.Background(), taskID, item) }
	}
	if a.cfg.ShowActivityTimeline {
		displayMode := goslack.TaskDisplayModePlan
		if a.cfg.ActivityDisplayMode == "timeline" {
			displayMode = goslack.TaskDisplayModeTimeline
		}
		q.EnableStreaming(newActivityTimeline(a.logger), meta.UserID, meta.TeamID, displayMode, a.cfg.InitialStatusMessage)
	}
	q.Start()
	q.Send(resumeOp{r: meta.resumed})
	a.queues[taskID] = q
	return q
}

func (a *SlackGatewayAdapter) OnTaskEndedElsewhere(taskID string) {
	a.wg.Go(func() { a.cleanupTask(taskID) })
}

var _ entrypoint.TaskEndedElsewhereHandler = (*SlackGatewayAdapter)(nil)

func (a *SlackGatewayAdapter) cleanupTask(taskID string) {
	a.releaseMentionTask(taskID)

	a.queuesMu.Lock()
	q, ok := a.queues[taskID]
	if ok {
		delete(a.queues, taskID)
	}
	a.queuesMu.Unlock()

	if q != nil {
		q.Stop(10 * time.Second)
	}
}

func finalizeStatus(statusTS string, kind noticeKind, text string) FinalizeOp {
	return FinalizeOp{StatusTS: statusTS, Text: text, Blocks: StatusBlocks(text), Notice: kind}
}

func failedTaskNoticeKind(task *a2a.Task) noticeKind {
	if entrypoint.IsBudgetStoppedTask(task) {
		return noticeBudget
	}
	return noticeError
}

func failedTaskText(task *a2a.Task) string {
	if !entrypoint.IsBudgetStoppedTask(task) || task.Status.Message == nil {
		return msgTaskError
	}
	var texts []string
	for _, part := range task.Status.Message.Parts {
		if part.Kind == "text" && strings.TrimSpace(part.Text) != "" {
			texts = append(texts, part.Text)
		}
	}
	if len(texts) == 0 {
		return msgTaskError
	}
	return "❌ " + strings.Join(texts, "\n")
}

func (a *SlackGatewayAdapter) postUserError(channelID, threadTS string, kind noticeKind, text string) bool {
	if _, _, err := a.api.PostMessage(a.ctx, channelID,
		goslack.MsgOptionText(text, false),
		goslack.MsgOptionTS(threadTS),
		noticeMetadata(kind),
	); err != nil {
		a.logger.Error("failed to post error message", "error", err)
		return false
	}
	return true
}

func buildExternalKey(channelID, threadTS string) string {
	if threadTS == "" {
		return channelID + ":_"
	}
	return channelID + ":" + threadTS
}

func (a *SlackGatewayAdapter) resolveSessionID(ctx context.Context, externalKey, agentName, userID string) (string, error) {
	if a.sessKeys == nil {
		return idgen.New(), nil
	}
	return a.sessKeys.GetOrCreateByExternalKey(ctx, store.GetOrCreateByExternalKeyArgs{
		GatewayType: store.GatewayTypeSlack,
		ExternalKey: externalKey,
		UserID:      userID,
		AgentID:     &agentName,
	})
}
