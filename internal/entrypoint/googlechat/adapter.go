// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"time"

	chat "google.golang.org/api/chat/v1"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/a2ui"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/delivery"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/ingestlimit"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/health"
	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const EntrypointType = "google_chat"

func init() {
	entrypoint.RegisterAdapterFactory(EntrypointType, func(cfg map[string]any) (entrypoint.GatewayAdapter, error) {
		return NewAdapter(cfg), nil
	})
}

type Adapter struct {
	rawCfg       map[string]any
	cfg          *Config
	core         *entrypoint.GatewayCore
	logger       *slog.Logger
	gatewayID    string
	taskRegistry entrypoint.TaskRegistry

	chatOverride   chatAPI
	subOverride    subscriptionAPI
	threadOverride threadReader

	writer             *chatWriter
	downloader         attachmentDownloader
	attachmentBytes    *ingestlimit.Budget
	dms                directMessages
	sub                subscriptionAPI
	threads            threadReader
	drive              driveAPI
	driveSlots         chan struct{}
	driveBudget        time.Duration
	driveRemoveBackoff time.Duration
	notifyDriveBudget  time.Duration
	spool              *brokerSpool
	verifier           requestVerifier
	onHandlerReady     func(http.Handler)
	claims             eventClaims
	sessKeys           *store.GatewaySessionKeyRepo
	items              taskItemStore
	metrics            runtime.Metrics
	health             health.State

	scratchDB  *store.DB
	scratchDir string

	tasksMu sync.Mutex
	tasks   map[string]*taskState

	gateNotices            noticeThrottle
	refusalNotices         noticeThrottle
	unauthenticatedNotices noticeThrottle
	badEventNotices        noticeThrottle
	historyWarnings        noticeThrottle

	ctx       context.Context
	cancel    context.CancelFunc
	ingressWG sync.WaitGroup
	bgWG      sync.WaitGroup

	lingerWG      sync.WaitGroup
	abandonCtx    context.Context
	abandonCancel context.CancelFunc
	stopMu        sync.Mutex
	stopping      bool
	bgStopped     bool

	lingerTimeout          time.Duration
	sweepInterval          time.Duration
	attachmentTimeout      time.Duration
	attachmentsBudget      time.Duration
	attachmentStoreTimeout time.Duration
}

var (
	_ entrypoint.GatewayAdapter            = (*Adapter)(nil)
	_ entrypoint.DeferredHTTPAdapter       = (*Adapter)(nil)
	_ entrypoint.AdapterQueueDeprovisioner = (*Adapter)(nil)
	_ entrypoint.RelayedSurfaceAdapter     = (*Adapter)(nil)
	_ entrypoint.StatusTextRenderer        = (*Adapter)(nil)
	_ entrypoint.CapabilityProvider        = (*Adapter)(nil)
	_ entrypoint.TaskStateLoader           = (*Adapter)(nil)
	_ entrypoint.TaskEndedElsewhereHandler = (*Adapter)(nil)
	_ entrypoint.TaskRecordKeptUntilAck    = (*Adapter)(nil)
)

func (a *Adapter) KeepsTaskRecordUntilAck() {
}

func NewAdapter(rawCfg map[string]any) *Adapter {
	abandonCtx, abandonCancel := context.WithCancel(context.Background())
	return &Adapter{
		rawCfg:                 rawCfg,
		tasks:                  make(map[string]*taskState),
		abandonCtx:             abandonCtx,
		abandonCancel:          abandonCancel,
		driveBudget:            driveHandOffBudget,
		notifyDriveBudget:      defaultNotifyDriveBudget,
		driveRemoveBackoff:     driveRemoveBackoffDefault,
		lingerTimeout:          replyQueueLingerTimeout,
		sweepInterval:          taskSweepInterval,
		attachmentTimeout:      attachmentDownloadTimeout,
		attachmentsBudget:      attachmentsDownloadBudget,
		attachmentStoreTimeout: attachmentStoreTimeout,
		attachmentBytes:        ingestlimit.Inbound(),
	}
}

func (a *Adapter) MountIntent() entrypoint.MountSpec {
	if ResolveDelivery(a.rawCfg) == DeliveryHTTPS {
		return entrypoint.MountSpec{Kind: entrypoint.MountNamed}
	}
	return entrypoint.MountSpec{Kind: entrypoint.MountNone}
}

func (a *Adapter) SetOnHandlerReady(cb func(http.Handler)) { a.onHandlerReady = cb }

func (a *Adapter) DeprovisionAdapterQueues(context.Context) error {
	if a.core == nil {
		return nil
	}
	brk := a.core.Services().Broker()
	if brk == nil {
		return nil
	}
	name := a2a.GoogleChatIngressQueueName(a.core.Namespace(), a.gatewayID)
	if err := brk.DeprovisionQueue(name); err != nil {
		return fmt.Errorf("deprovision google chat ingress queue %q: %w", name, err)
	}
	return nil
}

func (a *Adapter) RelayedSurface() string { return httputil.SurfaceGoogleChat }

func (a *Adapter) RendersStatusText(string) bool { return true }

func (a *Adapter) HealthProblems() []health.Problem { return a.health.Snapshot() }

func (a *Adapter) Init(ctx context.Context, core *entrypoint.GatewayCore) error {
	a.core = core
	a.gatewayID = core.GatewayID()
	a.taskRegistry = core.Tasks()
	a.logger = core.Logger().With("logger_name", "google-chat-adapter")
	a.ctx, a.cancel = context.WithCancel(context.Background())

	if !featureEnabled(core.Services()) {
		return a.failInit(errFeatureDisabled)
	}

	raw := a.rawCfg
	if raw == nil {
		raw, _ = core.RawConfig()["adapter_config"].(map[string]any)
	}
	if raw == nil {
		return a.failInit(fmt.Errorf("google_chat adapter: no adapter_config found"))
	}
	cfg, err := ParseConfig(raw)
	if err != nil {
		return a.failInit(err)
	}
	a.cfg = cfg
	a.metrics = runtime.MetricsOrNoop(core.Services().Metrics())

	if cfg.Delivery == DeliveryHTTPS && a.subOverride == nil && a.onHandlerReady == nil {
		return a.failInit(errEventsRouteUnmounted)
	}
	if err := a.initClients(ctx); err != nil {
		return a.failInit(err)
	}
	if err := a.initStores(ctx); err != nil {
		return a.failInit(err)
	}

	a.logger.Info("google chat adapter initialized",
		"delivery", cfg.Delivery,
		"defaultAgent", cfg.DefaultAgentName,
		"identityClaimKey", cfg.IdentityClaimKey,
		"allowedEmailDomains", len(cfg.AllowedEmailDomains),
		"useUserIdentityInChannels", cfg.UseUserIdentityInChannels,
		"threadHistoryLimit", cfg.ThreadHistoryLimit,
		"artifactDrive", cfg.ArtifactDriveFolderID != "",
		"sharedTaskState", a.items != nil)
	if a.identityAuthorizer() == nil {
		a.logger.Error("google chat entrypoint has no identity authorizer wired; every sender will be refused (this entrypoint fails closed)")
	}
	core.WarnIfSignInUnreachable(a.logger, EntrypointType)
	if a.spool != nil {
		a.onHandlerReady(a.eventsHandler())
	}
	if cfg.FeedbackEnabled {
		core.WarnIfFeedbackUnloggable(EntrypointType, a.logger)
	}
	return nil
}

func (a *Adapter) initClients(ctx context.Context) error {
	chatClient := a.chatOverride
	if chatClient == nil {
		c, err := newRestChatAPI(ctx, a.cfg)
		if err != nil {
			return err
		}
		chatClient = c
	}
	a.writer = newChatWriter(chatClient, newSpaceThrottle(a.cfg.MinEditInterval), a.logger)
	if d, ok := chatClient.(attachmentDownloader); ok {
		a.downloader = d
	}
	if d, ok := chatClient.(directMessages); ok {
		a.dms = d
	}

	if a.cfg.ThreadHistoryLimit > 0 {
		a.threads = a.threadOverride
		if a.threads == nil {
			r, err := newRestThreadReader(ctx, a.cfg)
			if err != nil {
				return err
			}
			a.threads = r
		}
	}

	if a.cfg.ArtifactDriveFolderID != "" {
		d, err := newRestDriveAPI(ctx, a.cfg)
		if err != nil {
			return err
		}
		a.drive = d
		a.driveSlots = make(chan struct{}, maxConcurrentDriveHandOffs)
		a.driveRemoveBackoff = a.cfg.DriveRemoveBackoff
	}

	a.sub = a.subOverride
	switch {
	case a.sub != nil:
	case a.cfg.Delivery == DeliveryHTTPS:
		brk := a.core.Services().Broker()
		if brk == nil {
			return fmt.Errorf("google_chat adapter: HTTPS delivery needs a broker to spool events durably")
		}
		verifier, err := newRequestVerifier(ctx, a.cfg, verifierEnv{Logger: a.logger, Health: &a.health})
		if err != nil {
			return err
		}
		spool, err := openBrokerSpool(a.ctx, brk, a.core.Namespace(), a.gatewayID, a.logger)
		if err != nil {
			return err
		}
		a.warnIfSpoolNotDurable(brk)
		a.spool, a.sub, a.verifier = spool, spool, verifier
	default:
		s, err := newRestSubscription(ctx, a.cfg)
		if err != nil {
			return err
		}
		a.sub = s
	}
	return nil
}

func (a *Adapter) warnIfSpoolNotDurable(brk broker.Broker) {
	switch brk.(type) {
	case *broker.DevBroker, *broker.NetworkDevBroker:
		a.logger.Warn("google chat HTTPS delivery is spooling on the dev broker, which is not durable; events received before a restart or a retried failure are lost")
	}
}

func (a *Adapter) initStores(ctx context.Context) error {
	db := a.core.Store()
	if db != nil {
		a.items = store.NewEntrypointTaskItemRepo(db)
	} else {
		scratch, dir, err := openScratchStore(ctx, a.logger)
		if err != nil {
			return err
		}
		a.scratchDB, a.scratchDir, db = scratch, dir, scratch
		a.logger.Warn("google chat adapter: no gateway database configured — deduplication, sessions and reply routing are process-local; run a single replica only, and set session_service.database_url for multi-replica and failover safety")
	}
	a.claims = store.NewGoogleChatProcessedRepo(db)
	a.sessKeys = store.NewGatewaySessionKeyRepo(db)
	return nil
}

func openScratchStore(ctx context.Context, logger *slog.Logger) (*store.DB, string, error) {
	dir, err := os.MkdirTemp("", "sam-google-chat-")
	if err != nil {
		return nil, "", fmt.Errorf("google_chat adapter: create scratch store directory: %w", err)
	}
	db, err := store.Open("sqlite://"+filepath.Join(dir, "googlechat.db"), logger)
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, "", fmt.Errorf("google_chat adapter: open scratch store: %w", err)
	}
	if err := db.Migrate(ctx); err != nil {
		_ = db.Close()
		_ = os.RemoveAll(dir)
		return nil, "", fmt.Errorf("google_chat adapter: migrate scratch store: %w", err)
	}
	return db, dir, nil
}

var errEventsRouteUnmounted = errors.New("google_chat adapter: HTTPS delivery needs the events route mounted, but no handler registration was installed before Init")

var errFeatureDisabled = fmt.Errorf("google_chat adapter: the Google Chat entrypoint is disabled; set %s=true to enable it",
	features.EnvVarName(features.KeyGoogleChatGateway))

func featureEnabled(svc runtime.Services) bool {
	if svc == nil {
		return false
	}
	reg := svc.FeatureRegistry()
	return reg != nil && reg.IsEnabled(features.KeyGoogleChatGateway)
}

func (a *Adapter) failInit(err error) error {
	if a.cancel != nil {
		a.cancel()
	}
	return err
}

func (a *Adapter) Start(context.Context) error {
	a.health.Reset()
	a.ingressWG.Go(func() { a.runIngress(a.ctx) })
	if a.items != nil {
		a.bgWG.Go(func() { a.runIdleTaskSweep(a.ctx) })
	}
	a.startNotifyDeliverLoop()
	a.logger.Info("google chat adapter ready", "subscriptionFP", subscriptionFingerprint(a.cfg.PubSubSubscription))
	return nil
}

func (a *Adapter) Stop(ctx context.Context) error {
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, adapterStopBudget)
		defer cancel()
	}
	if a.cancel != nil {
		a.cancel()
	}
	a.stopMu.Lock()
	a.bgStopped = true
	a.stopMu.Unlock()
	waited := make(chan struct{})
	go func() {
		a.ingressWG.Wait()
		a.bgWG.Wait()
		if a.spool != nil {
			a.spool.waitDelayed()
		}
		close(waited)
	}()
	select {
	case <-waited:
	case <-ctx.Done():
		a.logger.Warn("google chat adapter stop timed out waiting for workers")
	}
	a.awaitLingeringReplies(ctx)

	a.tasksMu.Lock()
	ids := make([]string, 0, len(a.tasks))
	for id := range a.tasks {
		ids = append(ids, id)
	}
	a.tasksMu.Unlock()
	for _, id := range ids {
		a.abandonTask(id)
	}

	if a.scratchDB != nil {
		if err := a.scratchDB.Close(); err != nil {
			a.logger.Warn("google chat scratch store close failed", "error", err)
		}
		_ = os.RemoveAll(a.scratchDir)
	}
	return nil
}

func (a *Adapter) awaitLingeringReplies(ctx context.Context) {
	a.stopMu.Lock()
	a.stopping = true
	a.stopMu.Unlock()
	waited := make(chan struct{})
	go func() {
		a.lingerWG.Wait()
		close(waited)
	}()
	select {
	case <-waited:
		return
	case <-ctx.Done():
	}
	a.abandonCancel()
	<-waited
}

func subscriptionFingerprint(name string) string {
	_, sub, _ := strings.Cut(name, pubsubSubscriptionInfix)
	return sub
}

func (a *Adapter) queueFor(taskID string, st *taskState) *replyQueue {
	st.mu.Lock()
	q := st.queue
	st.mu.Unlock()
	if q != nil {
		return q
	}
	q = a.newQueue(taskID, st, nil)
	if meta := st.snapshot(); a.items != nil && meta.StatusMessage == "" && !meta.Prompted {
		q.adopt = a.adoptStatus(taskID)
	}
	st.mu.Lock()
	if st.forgotten {
		st.mu.Unlock()
		q.Close()
		return q
	}
	defer st.mu.Unlock()
	if st.queue != nil {
		go q.Close()
		return st.queue
	}
	st.queue = q
	return q
}

func (a *Adapter) recoverCallback(taskID, callback string) {
	if r := recover(); r != nil {
		a.logPanic(taskID, callback, r)
	}
}

func (a *Adapter) logPanic(taskID, callback string, r any) {
	a.logger.Error("google chat event handler panicked; the event is dropped", "taskID", taskID, "callback", callback,
		"panicType", fmt.Sprintf("%T", r), "stack", string(debug.Stack()))
}

func (a *Adapter) recoverTerminal(taskID, callback string, st *taskState, q *replyQueue, end *taskEnd) {
	r := recover()
	if r == nil {
		return
	}
	a.logPanic(taskID, callback, r)
	defer func() {
		if r := recover(); r != nil {
			a.logPanic(taskID, callback+"_recovery", r)
		}
	}()
	q.Notice(msgAgentError)
	q.ClearControls()
	a.drainTerminal(taskID, st, q, nil, end)
}

func (a *Adapter) OnStatusUpdate(ctx context.Context, taskID string, event *a2a.TaskStatusUpdateEvent, _ []byte) {
	defer a.recoverCallback(taskID, "status_update")
	st := a.resolveTask(ctx, taskID, lookupStatus)
	if st == nil {
		return
	}
	q := a.queueFor(taskID, st)
	if added := st.citations.add(entrypoint.CitationSourcesFromMessage(event.Status.Message)); added.changed > 0 {
		if added.collided > 0 {
			a.logger.Debug("google chat task sources with the same id named different pages", "taskID", taskID, "idCount", added.collided)
		}
		q.Rerender()
	}
	if req, ok := a2ui.ExtractRequest(event); ok {
		a.promptForInput(ctx, taskID, st, q, req)
		return
	}
	if authData, ok := a2a.AuthenticationRequiredFromStatus(event); ok {
		a.promptForAuthorization(ctx, taskID, st, q, authData)
		return
	}
	if result, ok := a2a.AuthenticationResultFromStatus(event); ok {
		q.ResolveSignIn(signInOutcome(result.Success))
		return
	}
	if event.Status.Message == nil {
		return
	}
	a.recordSaved(taskID, st, entrypoint.SavedArtifactsFromStatus(event))
	for _, part := range event.Status.Message.Parts {
		switch part.Kind {
		case "text":
			if part.Text == "" {
				continue
			}
			st.streamed.Store(true)
			q.Append(part.Text)
		case "data":
			if text := progressText(part); text != "" {
				q.Progress(msgProgressPrefix + text)
			}
		}
	}
}

func progressText(part a2a.Part) string {
	data, ok := part.Data.(map[string]any)
	if !ok || data["type"] != "agent_progress_update" {
		return ""
	}
	text, _ := data["status_text"].(string)
	return strings.TrimSpace(text)
}

func (a *Adapter) promptForAuthorization(ctx context.Context, taskID string, st *taskState, q *replyQueue, authData *a2a.AuthenticationRequiredData) {
	meta := st.snapshot()
	if !meta.IsDM {
		q.Notice("This request needs your personal sign-in, which I can't do in a shared conversation. Message me directly to continue.")
		return
	}
	text, ok := authorizePrompt(authData)
	if !ok {
		a.logger.Warn("google chat sign-in link is not presentable; cancelling the task", "taskID", taskID)
		q.Halt(msgSignInUnavailable)
		a.cancelTask(taskID, meta.AgentName)
		return
	}
	q.PromptSignIn(msgSignInLead, text, authorizeCard(text, authData.AuthURI), a.stopUnpostedPrompt(taskID, meta.AgentName, msgSignInUnavailable), holdEvent(ctx))
}

func authorizationHeld(state a2a.TaskState) bool {
	switch state {
	case a2a.TaskStateFailed, a2a.TaskStateRejected, a2a.TaskStateCancelled:
		return false
	}
	return true
}

func signInOutcome(authorized bool) string {
	if authorized {
		return msgSignInAuthorized
	}
	return msgSignInIncomplete
}

func (a *Adapter) promptForInput(ctx context.Context, taskID string, st *taskState, q *replyQueue, req *a2a.UserInputRequestData) {
	meta := st.snapshot()
	if !a.buttonsReachAdapter(meta) {
		a.stopUnanswerableQuestion(taskID, meta, q)
		return
	}
	surface, err := a2ui.Parse(req.Surface)
	if err != nil {
		a.logger.Error("google chat input request has an unreadable surface; cancelling the task", "taskID", taskID, "requestID", req.RequestID, "error", err)
		q.Halt(msgQuestionUnavailable)
		a.cancelTask(taskID, meta.AgentName)
		return
	}
	title := cmpLabel(surface.Title, hilDefaultTitle)
	cards, ok := hilCard(surface, req.RequestID, taskID, meta.AgentName)
	if !ok {
		a.logger.Warn("google chat input request does not fit a card; pointing the user at the web UI", "taskID", taskID, "requestID", req.RequestID)
		cards = hilWebUINoticeCard(surface, req.RequestID, a.core.WebUIURL())
	}
	q.PromptOrStop(msgQuestionPrefix+title, title, a.routeButtons(cards, meta),
		a.stopUnpostedPrompt(taskID, meta.AgentName, msgQuestionUnavailable), holdEvent(ctx))
}

func holdEvent(ctx context.Context) func() {
	if d := delivery.From(ctx); d != nil {
		return d.Hold()
	}
	return func() {
	}
}

func (a *Adapter) stopUnanswerableQuestion(taskID string, meta taskMeta, q *replyQueue) {
	a.logger.Warn("google chat question cannot be answered: the add-on Chat app's button clicks are not delivered over pubsub; cancelling the task", "taskID", taskID)
	q.Halt(msgQuestionNeedsButtons)
	a.cancelTask(taskID, meta.AgentName)
}

func (a *Adapter) stopUnpostedPrompt(taskID, agentName, halt string) promptFallback {
	return promptFallback{halt: halt, stop: func(err error) {
		if errors.Is(err, errReplyQueueClosed) {
			a.logger.Warn("google chat prompt was not posted: its reply queue closed first; cancelling the task", "taskID", taskID)
		} else {
			a.logger.Error("google chat prompt could not be posted; cancelling the task", "taskID", taskID, "error", err)
		}
		a.cancelTask(taskID, agentName)
	}}
}

var undeliverableInputCode = tool.DispatchErrorCode(taxonomy.DispatchClassUserInputUndeliverable)

func (a *Adapter) cancelTask(taskID, agentName string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(a.ctx), taskCancelTimeout)
	defer cancel()
	if err := a.core.CancelTaskWithErrorCode(ctx, taskID, agentName, undeliverableInputCode); err != nil {
		a.logger.Error("google chat task could not be cancelled; it waits out its timeout", "taskID", taskID, "error", err)
	}
}

func (a *Adapter) Capabilities() map[string]bool {
	return map[string]bool{tool.CapInteractiveUserInput: true}
}

func (a *Adapter) OnArtifactUpdate(context.Context, string, *a2a.TaskArtifactUpdateEvent, []byte) {
}

func (a *Adapter) OnFinalResponse(ctx context.Context, taskID string, task *a2a.Task, _ []byte) {
	defer a.recoverCallback(taskID, "final_response")
	st := a.resolveTask(ctx, taskID, lookupTerminal)
	if st == nil {
		return
	}
	end := a.holdTaskEnd(ctx, taskID)
	st.end.Store(end)
	defer end.returned()
	q := a.queueFor(taskID, st)
	defer a.recoverTerminal(taskID, "final_response", st, q, end)

	q.RetireSignIn(signInOutcome(authorizationHeld(task.Status.State)))
	var handOff *driveHandOff
	switch task.Status.State {
	case a2a.TaskStateCancelled:
		q.Notice(msgTaskCanceled)
	case a2a.TaskStateFailed, a2a.TaskStateRejected:
		a.logger.Warn("google chat task ended in failure", "taskID", taskID, "state", task.Status.State)
		q.Notice(msgAgentError)
	default:
		handOff = a.writeFinalAnswer(ctx, taskID, st, q, task)
	}
	if cards := a.feedbackCardsFor(taskID, st, task); cards != nil {
		q.Conclude(feedbackPrompt, cards)
	} else {
		q.ClearControls()
	}
	a.drainTerminal(taskID, st, q, handOff, end)
}

func (a *Adapter) feedbackCardsFor(taskID string, st *taskState, task *a2a.Task) []*chat.CardWithId {
	if !a.cfg.FeedbackEnabled || task.Status.State != a2a.TaskStateCompleted {
		return nil
	}
	meta := st.snapshot()
	if meta.SessionID == "" || !a.buttonsReachAdapter(meta) {
		return nil
	}
	return a.routeButtons(feedbackCard(taskID, meta.SessionID), meta)
}

func (a *Adapter) writeFinalAnswer(ctx context.Context, taskID string, st *taskState, q *replyQueue, task *a2a.Task) *driveHandOff {
	a.mergeRecordedSaves(ctx, taskID, st)
	raw := finalText(task)
	text := stripArtifactReturns(raw)
	names := artifactNames(raw, task, st.savedArtifacts())
	answered := true
	switch {
	case st.streamed.Load() && !st.recovered:
	case text != "":
		q.Replace(text)
	case len(names) == 0:
		q.Notice(msgNoDisplayableReply)
	default:
		answered = false
	}
	saved := st.savedArtifacts()
	handOff := a.planDriveHandOff(taskID, st, q, saved, names)
	toDrive := handOff.names()
	notToDrive := slices.DeleteFunc(saved, func(s entrypoint.SavedArtifact) bool { return slices.Contains(toDrive, s.Filename) })
	shown := a.inlineArtifacts(ctx, taskID, st, q, notToDrive)
	names = slices.DeleteFunc(names, func(name string) bool { return slices.Contains(toDrive, name) || slices.Contains(shown, name) })
	if handOff != nil && !answered {
		q.Notice(msgDriveWorking)
	}
	if len(names) > 0 {
		q.Notice(artifactNotice(names, a.signInURL()))
	}
	return handOff
}

func (a *Adapter) signInURL() string {
	if a.core == nil {
		return ""
	}
	return a.core.SignInURL()
}

func (a *Adapter) OnError(ctx context.Context, taskID string, taskErr *a2a.Error, _ []byte) {
	defer a.recoverCallback(taskID, "error")
	st := a.resolveTask(ctx, taskID, lookupTerminal)
	if st == nil {
		return
	}
	end := a.holdTaskEnd(ctx, taskID)
	st.end.Store(end)
	defer end.returned()
	q := a.queueFor(taskID, st)
	defer a.recoverTerminal(taskID, "error", st, q, end)
	q.RetireSignIn(signInOutcome(false))
	notice := msgAgentError
	if entrypoint.IsTaskTimeoutError(taskErr) || strings.Contains(strings.ToLower(taskErr.Message), "cancel") {
		notice = msgTaskCanceled
	} else {
		a.logger.Warn("google chat task failed", "taskID", taskID, "errorCode", taskErr.Code)
	}
	q.Notice(notice)
	q.ClearControls()
	a.drainTerminal(taskID, st, q, nil, end)
}

func (a *Adapter) drainTerminal(taskID string, st *taskState, q *replyQueue, handOff *driveHandOff, end *taskEnd) {
	st.mu.Lock()
	if st.queue == q {
		st.queue = nil
	}
	st.mu.Unlock()
	files := 0
	if handOff != nil {
		files = len(handOff.files)
		handOff.held = end.held()
	}
	lingered := a.goLinger(func() {
		defer a.recoverLinger(taskID, end)
		abandoned := a.lingerReply(taskID, q, files, end.held())
		if handOff != nil && !abandoned {
			abandoned = a.handOffToDrive(handOff)
		}
		end.settle(abandoned)
	})
	if lingered {
		end.endsInBackground()
		return
	}
	fields := []any{"taskID", taskID}
	if handOff != nil {
		fields = append(fields, "files", len(handOff.files))
	}
	a.logUnfinished(end.held(), logReplyUnfinished, fields...)
	q.Abandon()
	end.settle(true)
}

func (a *Adapter) recoverLinger(taskID string, end *taskEnd) {
	r := recover()
	if r == nil {
		return
	}
	a.logger.Error("google chat task end panicked; whatever was not yet written is given up", "taskID", taskID,
		"panicType", fmt.Sprintf("%T", r), "stack", string(debug.Stack()))
	end.settle(false)
}

const logReplyUnfinished = "google chat reply unfinished: the adapter stopped before the task's last messages were written"

func (a *Adapter) goLinger(fn func()) bool {
	a.stopMu.Lock()
	defer a.stopMu.Unlock()
	if a.stopping {
		return false
	}
	a.lingerWG.Go(fn)
	return true
}

func (a *Adapter) lingerReply(taskID string, q *replyQueue, files int, held bool) (abandoned bool) {
	defer q.Close()
	drained := make(chan drainResult, 1)
	go func() { drained <- q.Drain(a.lingerTimeout) }()
	return a.awaitLinger(taskID, drained, files, held)
}

func (a *Adapter) awaitLinger(taskID string, drained <-chan drainResult, files int, held bool) (abandoned bool) {
	select {
	case res := <-drained:
		a.reportLingered(taskID, res)
	case <-a.abandonCtx.Done():
		select {
		case res := <-drained:
			a.reportLingered(taskID, res)
		default:
			fields := []any{"taskID", taskID}
			if files > 0 {
				fields = append(fields, "files", files)
			}
			a.logUnfinished(held, logReplyUnfinished, fields...)
			return true
		}
	}
	return false
}

func (a *Adapter) reportLingered(taskID string, res drainResult) {
	switch res {
	case drainWritten:
	case drainIncomplete:
		a.logReplyRejected(taskID)
	default:
		a.logger.Error("google chat reply lost: the task's last messages could not be written", "taskID", taskID)
	}
}

func (a *Adapter) logReplyRejected(taskID string) {
	a.logger.Error("google chat reply lost: Chat rejected part of the task's last messages", "taskID", taskID)
}

func (a *Adapter) finishTask(taskID string) {
	a.taskRegistry.Remove(taskID)
	a.forgetTask(taskID)
}

type taskEnd struct {
	a            *Adapter
	taskID       string
	release      func()
	once         sync.Once
	inBackground bool

	mu      sync.Mutex
	settled bool
	kept    bool
	joined  []func()
}

func (e *taskEnd) held() bool { return e != nil && e.release != nil }

func (e *taskEnd) join(release func()) {
	e.mu.Lock()
	if !e.settled {
		e.joined = append(e.joined, release)
		e.mu.Unlock()
		return
	}
	kept := e.kept
	e.mu.Unlock()
	if !kept {
		release()
	}
}

func (a *Adapter) logUnfinished(held bool, msg string, fields ...any) {
	if held {
		a.logger.Warn(msg, fields...)
		return
	}
	a.logger.Error(msg, fields...)
}

func (a *Adapter) holdTaskEnd(ctx context.Context, taskID string) *taskEnd {
	end := &taskEnd{a: a, taskID: taskID}
	if d := delivery.From(ctx); d != nil {
		end.release = d.Hold()
	}
	return end
}

func (e *taskEnd) endsInBackground() {
	if e != nil {
		e.inBackground = true
	}
}

func (e *taskEnd) returned() {
	if e.release == nil && !e.inBackground {
		e.settle(false)
	}
}

func (e *taskEnd) settle(abandoned bool) {
	if e == nil {
		return
	}
	e.once.Do(func() {
		kept := abandoned && e.release != nil
		e.mu.Lock()
		e.settled, e.kept = true, kept
		joined := e.joined
		e.joined = nil
		e.mu.Unlock()
		if kept {
			return
		}
		defer func() {
			if e.release != nil {
				e.release()
			}
			for _, release := range joined {
				release()
			}
		}()
		e.a.finishTask(e.taskID)
	})
}

func (a *Adapter) OnAgentDiscovered(card *a2a.AgentCard) {
	a.logger.Debug("agent discovered", "agentName", card.Name)
}

func (a *Adapter) OnAgentRemoved(agentName string) {
	a.logger.Debug("agent removed", "agentName", agentName)
}

func finalText(task *a2a.Task) string {
	if task.Status.Message == nil {
		return ""
	}
	var b strings.Builder
	for _, part := range task.Status.Message.Parts {
		if part.Kind == "text" {
			b.WriteString(part.Text)
		}
	}
	return strings.TrimSpace(b.String())
}

var artifactReturnPattern = regexp.MustCompile(`«artifact_return:([^»]*)»\n?`)

func stripArtifactReturns(text string) string {
	return strings.TrimSpace(artifactReturnPattern.ReplaceAllString(text, ""))
}
