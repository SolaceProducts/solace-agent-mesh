// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package whatsapp

import (
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/a2ui"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/health"
	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/strutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func init() {
	entrypoint.RegisterAdapterFactory("whatsapp", func(cfg map[string]any) (entrypoint.GatewayAdapter, error) {
		return NewAdapter(cfg)
	})
}

const claimStoreTimeout = 5 * time.Second

const foreignNumberWarnWindow = 10 * time.Minute

type taskMeta struct {
	FromPhone string
	SessionID string

	mu                    sync.Mutex
	authPromptIDs         []string
	authPromptsUnrecorded []string
	savedArtifacts        []entrypoint.SavedArtifact
	savesOverflowed       bool
	saveSeq               int
	savedSeq              map[string]int
	savesUnrecorded       map[string]bool
}

func (m *taskMeta) markSaveUnrecorded(filename string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.savesUnrecorded == nil {
		m.savesUnrecorded = make(map[string]bool)
	}
	m.savesUnrecorded[filename] = true
}

func (m *taskMeta) takeAuthPrompts() (recorded, unrecorded []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	recorded, unrecorded = m.authPromptIDs, m.authPromptsUnrecorded
	m.authPromptIDs, m.authPromptsUnrecorded = nil, nil
	return recorded, unrecorded
}

func (m *taskMeta) addAuthPrompt(outboundID string, recorded bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if recorded {
		m.authPromptIDs = append(m.authPromptIDs, outboundID)
		return
	}
	m.authPromptsUnrecorded = append(m.authPromptsUnrecorded, outboundID)
}

func (m *taskMeta) removeAuthPrompt(outboundID string) (known, unrecorded bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	before := len(m.authPromptIDs) + len(m.authPromptsUnrecorded)
	m.authPromptIDs = slices.DeleteFunc(m.authPromptIDs, func(id string) bool { return id == outboundID })
	withoutRow := len(m.authPromptsUnrecorded)
	m.authPromptsUnrecorded = slices.DeleteFunc(m.authPromptsUnrecorded, func(id string) bool { return id == outboundID })
	return before != len(m.authPromptIDs)+len(m.authPromptsUnrecorded), withoutRow != len(m.authPromptsUnrecorded)
}

const maxRecordedSaves = 64

type savedFileChange struct {
	file       entrypoint.SavedArtifact
	seq        int
	removed    bool
	overflowed bool
}

func (m *taskMeta) recordSavedArtifacts(saves []entrypoint.SavedArtifact) []savedFileChange {
	m.mu.Lock()
	defer m.mu.Unlock()
	var changes []savedFileChange
	if m.savedSeq == nil {
		m.savedSeq = make(map[string]int)
	}
	for _, saved := range saves {
		i := slices.IndexFunc(m.savedArtifacts, func(s entrypoint.SavedArtifact) bool { return s.Filename == saved.Filename })
		switch {
		case slices.Contains(saved.Tags, a2a.ArtifactTagWorking):
			if i >= 0 {
				m.savedArtifacts = slices.Delete(m.savedArtifacts, i, i+1)
			}
			delete(m.savedSeq, saved.Filename)
			changes = append(changes, savedFileChange{file: saved, removed: true})
		case i >= 0:
			m.savedArtifacts[i] = saved
			changes = append(changes, savedFileChange{file: saved, seq: m.savedSeq[saved.Filename]})
		case len(m.savedArtifacts) < maxRecordedSaves:
			m.savedArtifacts = append(m.savedArtifacts, saved)
			m.saveSeq++
			m.savedSeq[saved.Filename] = m.saveSeq
			changes = append(changes, savedFileChange{file: saved, seq: m.saveSeq})
		default:
			m.savesOverflowed = true
			changes = append(changes, savedFileChange{overflowed: true})
		}
	}
	return changes
}

type recordedSaves struct {
	files      []entrypoint.SavedArtifact
	overflowed bool
}

func (m *taskMeta) recordedSaves() recordedSaves {
	m.mu.Lock()
	defer m.mu.Unlock()
	return recordedSaves{files: slices.Clone(m.savedArtifacts), overflowed: m.savesOverflowed}
}

func forwardedFromPeer(event *a2a.TaskStatusUpdateEvent) string {
	if event.Status.Message == nil {
		return ""
	}
	peer, _ := event.Status.Message.Metadata["forwarded_from_peer"].(string)
	return peer
}

type Adapter struct {
	core              *entrypoint.GatewayCore
	cfg               *WhatsAppConfig
	rawCfg            map[string]any
	logger            *slog.Logger
	coreTasksOverride entrypoint.TaskRegistry

	server    *Server
	client    *Client
	sessKeys  *store.GatewaySessionKeyRepo
	items     taskItems
	itemsOnce sync.Once

	sender     *senderPool
	senderMu   sync.RWMutex
	senderTune func(*senderPool)
	outbound   *store.WhatsAppOutboundRepo
	ledger     senderQueue

	endingTasks   sync.Map
	conversations *store.WhatsAppConversationRepo
	prompts       *store.WhatsAppPendingPromptRepo

	pendingAuth *store.WhatsAppPendingAuthRepo
	metrics     runtime.Metrics
	fallbackDB  *store.DB
	fallbackDir string

	stopped atomic.Bool

	ingressQueue   broker.Queue
	ingressWorkers []chan *broker.Message
	ingressCtx     context.Context
	ingressCancel  context.CancelFunc
	ingressWG      sync.WaitGroup

	ingressActive       atomic.Bool
	ingressKick         chan struct{}
	ownerWG             sync.WaitGroup
	ownerMu             sync.Mutex
	ingressOwned        bool
	moveMu              sync.Mutex
	moveCancel          context.CancelFunc
	moveBudget          time.Duration
	moveRecheck         time.Duration
	moveIdle            time.Duration
	agentWait           time.Duration
	reprobeInterval     time.Duration
	ingressUnboundSince time.Time

	pendingIngress *broker.Message

	ingressDown atomic.Bool

	health health.State

	claims     messageClaims
	claimLease time.Duration

	claimWait time.Duration
	claimPoll time.Duration

	ingressGrace time.Duration
	receivePoll  time.Duration

	retryDelay time.Duration

	afterClaim func(wamid string) bool

	beforeProcess func(wamid string)

	ingressFailures warnThrottle

	foreignNumbers warnThrottle

	senders             senderGate
	burstySenders       warnThrottle
	unidentifiedSenders warnThrottle
	declinedUploads     warnThrottle

	loginPrompts       warnThrottle
	loginButtonRefused warnThrottle
	signInNotices      warnThrottle

	contactRequests         warnThrottle
	resolvedSenders         warnThrottle
	noContinuityTokenWarned atomic.Bool
	identityGateNotices     warnThrottle
	saturatedNotices        warnThrottle

	mediaTransfers mediaSemaphore

	hilDedup *runtime.MapCache

	now func() time.Time

	promptSweep time.Duration

	typing         typingIndicators
	typingRefresh  time.Duration
	typingLifetime time.Duration
	sweepWG        sync.WaitGroup

	onHandlerReady func(http.Handler)

	ctx    context.Context
	cancel context.CancelFunc
}

func (a *Adapter) HealthProblems() []health.Problem {
	return a.health.Snapshot()
}

var (
	_ entrypoint.GatewayAdapter            = (*Adapter)(nil)
	_ entrypoint.DeferredHTTPAdapter       = (*Adapter)(nil)
	_ entrypoint.AdapterQueueDeprovisioner = (*Adapter)(nil)
	_ entrypoint.TaskStateLoader           = (*Adapter)(nil)
	_ entrypoint.TaskEndedElsewhereHandler = (*Adapter)(nil)
)

func (a *Adapter) taskItems() taskItems {
	a.itemsOnce.Do(func() {
		a.items = taskItems{logger: a.logger}
		if a.core != nil {
			if db := a.core.Store(); db != nil {
				a.items.repo = store.NewEntrypointTaskItemRepo(db)
			}
		}
	})
	return a.items
}

func (a *Adapter) LoadTask(ctx context.Context, taskID string) error {
	if a.getTaskMeta(taskID) != nil {
		return nil
	}
	items := a.taskItems()
	if !items.enabled() {
		return nil
	}
	rows, err := items.list(ctx, taskID)
	if err != nil {
		return err
	}
	meta := metaFromItems(rows, a.logger, taskID)
	if meta == nil {
		if meta, err = a.bridgeFromSession(ctx, taskID); err != nil || meta == nil {
			return err
		}
	}
	a.tasks().SetMetadata(taskID, meta)
	a.logger.Debug("whatsapp task loaded from its items", "taskID", taskID)
	return nil
}

func (a *Adapter) bridgeFromSession(ctx context.Context, taskID string) (*taskMeta, error) {
	if a.sessKeys == nil {
		return nil, nil
	}
	info := a.tasks().Get(taskID)
	if info == nil || info.SessionID == "" {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(ctx, taskItemTimeout)
	defer cancel()
	keys, err := a.sessKeys.GetByGatewayAndSessionIDs(ctx, store.GatewayTypeWhatsApp, []string{info.SessionID})
	if err != nil {
		return nil, err
	}
	sender, ok := senderOfExternalKey(keys[info.SessionID])
	if !ok {
		return nil, nil
	}
	a.logger.Info("whatsapp task destination bridged from its session", "taskID", taskID)
	return &taskMeta{FromPhone: sender, SessionID: info.SessionID}, nil
}

func senderOfExternalKey(key string) (string, bool) {
	rest, ok := strings.CutPrefix(key, "whatsapp:")
	if !ok {
		return "", false
	}
	i := strings.LastIndexByte(rest, ':')
	if i <= 0 {
		return "", false
	}
	return rest[:i], true
}

func (a *Adapter) OnTaskEndedElsewhere(taskID string) {
	a.senders.finishTask(taskID)
	a.typingFinishedTask(taskID)
}

func NewAdapter(rawCfg map[string]any) (*Adapter, error) {
	return &Adapter{rawCfg: rawCfg}, nil
}

func (a *Adapter) SetOnHandlerReady(cb func(http.Handler)) {
	a.onHandlerReady = cb
}

func (a *Adapter) MountIntent() entrypoint.MountSpec {
	return entrypoint.MountSpec{Kind: entrypoint.MountNamed}
}

func (a *Adapter) RelayedSurface() string { return httputil.SurfaceWhatsApp }

func (a *Adapter) Init(_ context.Context, core *entrypoint.GatewayCore) error {
	a.core = core
	a.logger = core.Logger().With("logger_name", "whatsapp-adapter")
	a.hilDedup = runtime.NewMapCache()
	a.ctx, a.cancel = context.WithCancel(context.Background())

	adapterCfg := a.rawCfg
	if adapterCfg == nil {
		if raw, ok := core.RawConfig()["adapter_config"].(map[string]any); ok {
			adapterCfg = raw
		}
	}
	if adapterCfg == nil {
		return a.failInit(fmt.Errorf("whatsapp adapter: no adapter_config found"))
	}

	cfg, err := ParseWhatsAppConfig(adapterCfg)
	if err != nil {
		return a.failInit(err)
	}
	if err := checkTestOnlyConfig(cfg); err != nil {
		return a.failInit(err)
	}
	a.cfg = cfg

	if claimBindingNamespace(core.GatewayID(), a) {
		a.logger.Warn("another whatsapp entrypoint in this process already uses this gateway_id: it is the namespace for this entrypoint's conversations, outbound queue, inbound deduplication and identity bindings, so the two read each other's state and a customer active on both is refused one message per alternation",
			"gatewayID", core.GatewayID())
	}

	a.client = NewClient(cfg, a.logger)
	a.server = NewServer(a.ctx, cfg, a.publishInbound, a.handleStatus, a.logger)

	if a.claimLease == 0 {
		a.claimLease = defaultClaimLease
	}
	if a.retryDelay == 0 {
		a.retryDelay = defaultTransientRetryDelay
	}
	if a.claimWait == 0 {
		a.claimWait = defaultInFlightClaimWait
	}
	if a.claimPoll == 0 {
		a.claimPoll = defaultInFlightClaimPoll
	}
	if a.ingressGrace == 0 {
		a.ingressGrace = ingressDownGrace
	}
	if a.receivePoll == 0 {
		a.receivePoll = ingressReceivePoll
	}
	if db := core.Store(); db != nil {
		a.sessKeys = store.NewGatewaySessionKeyRepo(db)
		a.claims = store.NewWhatsAppProcessedRepo(db)
	} else {
		a.logger.Warn("whatsapp adapter: no gateway store configured — session reuse disabled, every inbound message starts a fresh session")
		a.logger.Warn("whatsapp adapter: no gateway store configured — inbound deduplication is process-local and will not survive a restart")
		a.claims = newMemoryClaims()
	}

	if err := a.initIngress(); err != nil {
		return err
	}

	if err := a.initDurableState(); err != nil {
		return a.failInit(err)
	}

	a.logger.Info("whatsapp adapter initialized",
		"graphApiVersion", cfg.GraphAPIVersion,
		"defaultAgent", cfg.DefaultAgentName,
		"disableFileUploads", cfg.DisableFileUploads,
		"runAs", runAsPrincipalOrEmpty(cfg),
		"feedbackEnabled", cfg.FeedbackEnabled,
	)
	if cfg.FeedbackEnabled {
		a.core.WarnIfFeedbackUnloggable("whatsapp", a.logger)
	}
	a.logStartupAdvisories(adapterCfg)

	if a.onHandlerReady != nil {
		a.onHandlerReady(a.server.Handler())
	}
	return nil
}

func (a *Adapter) gatewayID() string {
	return a.core.GatewayID()
}

func (a *Adapter) initDurableState() error {
	db := a.core.Store()
	if db == nil {
		dsn, dir, err := fallbackOutboundStore()
		if err != nil {
			return err
		}
		fallback, err := store.Open(dsn, a.logger)
		if err != nil {
			_ = os.RemoveAll(dir)
			return fmt.Errorf("open the fallback whatsapp outbound store: %w", err)
		}
		if err := fallback.Migrate(a.ctx); err != nil {
			_ = fallback.Close()
			_ = os.RemoveAll(dir)
			return fmt.Errorf("migrate the fallback whatsapp outbound store: %w", err)
		}
		a.fallbackDB = fallback
		a.fallbackDir = dir
		db = fallback
		a.logger.Warn("whatsapp adapter: no gateway store configured — outbound replies are queued in a scratch database and are lost if the process restarts; set session_service.database_url for durable delivery")
	}
	a.outbound = store.NewWhatsAppOutboundRepo(db)
	a.ledger = a.outbound
	a.conversations = store.NewWhatsAppConversationRepo(db)
	a.prompts = store.NewWhatsAppPendingPromptRepo(db)
	a.pendingAuth = store.NewWhatsAppPendingAuthRepo(db)
	a.metrics = runtime.MetricsOrNoop(a.core.Services().Metrics())
	a.sender = a.newSender()
	return nil
}

func (a *Adapter) newSender() *senderPool {
	pace := pairSendInterval
	if a.cfg.DisableRateLimiting {
		pace = 0
	}
	pool := newSenderPool(senderConfig{
		Queue:     a.outbound,
		Attempt:   a.executeSend,
		GatewayID: a.gatewayID(),
		Logger:    a.logger,
		Metrics:   a.metrics,
		Pace:      pace,
		Now:       a.now,
		OnResult:  a.onSendResult,
		OnCommit:  a.commitSendState,
		OnRetired: a.tellAgentAParkedPromptDied,
	})
	if a.senderTune != nil {
		a.senderTune(pool)
	}
	return pool
}

func (a *Adapter) startSender() {
	a.senderMu.Lock()
	if a.sender == nil {
		a.sender = a.newSender()
	}
	pool := a.sender
	a.senderMu.Unlock()
	pool.Start(a.ctx)
}

func (a *Adapter) stopSender(ctx context.Context) bool {
	a.senderMu.Lock()
	pool := a.sender
	a.sender = nil
	a.senderMu.Unlock()
	if pool == nil {
		return true
	}
	return pool.Stop(ctx, senderStopGrace)
}

func (a *Adapter) runningSender() *senderPool {
	a.senderMu.RLock()
	defer a.senderMu.RUnlock()
	if a.sender == nil || !a.sender.running() {
		return nil
	}
	return a.sender
}

func (a *Adapter) wakeSender(recipient string) {
	if pool := a.runningSender(); pool != nil {
		pool.notify(recipient)
		return
	}
	a.ringDoorbell(recipient)
}

func (a *Adapter) ringDoorbell(recipient string) {
	brk := a.brokerOrNil()
	if brk == nil || recipient == "" {
		return
	}
	topic := a2a.WhatsAppDoorbellTopic(a.core.Namespace(), a.core.GatewayID())
	ctx, cancel := context.WithTimeout(context.WithoutCancel(a.ctx), adapterSettleTimeout)
	defer cancel()
	if err := brk.Publish(ctx, topic, &broker.Message{Topic: topic, Payload: []byte(recipient)}); err != nil {
		a.logger.Warn("whatsapp doorbell not published; the row waits for the sender's rescan", "error", err)
	}
}

func (a *Adapter) failInit(err error) error {
	if a.cancel != nil {
		a.cancel()
	}
	return err
}

func (a *Adapter) Start(ctx context.Context) error {
	a.health.Reset()
	a.ownerWG.Go(a.runIngressOwner)
	a.sweepWG.Go(func() { a.runPromptSweep(cmp.Or(a.promptSweep, promptSweepInterval)) })
	a.logger.Info("whatsapp adapter ready")
	return nil
}

func (a *Adapter) Stop(ctx context.Context) error {
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, adapterStopBudget)
		defer cancel()
	}
	if a.core != nil {
		releaseBindingNamespace(a.core.GatewayID(), a)
	}
	if a.server != nil {
		a.server.Close()
	}
	if a.cancel != nil {
		a.cancel()
	}
	a.ownerWG.Wait()
	a.stopTyping()
	if a.server != nil {
		a.server.Wait()
	}
	a.stopIngressConsumer()
	a.sweepWG.Wait()
	drained := a.stopSender(ctx)
	a.releaseIngressBind()
	a.stopped.Store(true)
	if !drained {
		a.logger.Warn("whatsapp sender still finishing at shutdown; leaving its store to process exit")
	} else {
		if a.fallbackDB != nil {
			if err := a.fallbackDB.Close(); err != nil {
				a.logger.Warn("failed to close the fallback whatsapp outbound store", "error", err)
			}
		}
		if a.fallbackDir != "" {
			if err := os.RemoveAll(a.fallbackDir); err != nil {
				a.logger.Warn("failed to remove the fallback whatsapp outbound store", "error", err)
			}
		}
	}
	if a.hilDedup != nil {
		a.hilDedup.Clear()
	}
	a.logger.Info("whatsapp adapter stopped")
	return nil
}

func (a *Adapter) forDifferentNumber(value *webhookChangeValue) bool {
	if value == nil || a.cfg == nil {
		return false
	}
	got := value.Metadata.PhoneNumberID
	return got != "" && got != a.cfg.PhoneNumberID
}

func (a *Adapter) logForeignNumberDrop(msg, wamid string, value *webhookChangeValue) {
	fields := []any{
		"wamid", wamid,
		"phoneNumberID", value.Metadata.PhoneNumberID,
		"configuredPhoneNumberID", a.cfg.PhoneNumberID,
	}
	warn, suppressed, _ := a.foreignNumbers.shouldWarn(value.Metadata.PhoneNumberID, foreignNumberWarnWindow)
	if warn {
		a.logger.Warn(msg, append(fields, "suppressedSinceLast", suppressed)...)
		return
	}
	a.logger.Debug(msg, fields...)
}

type warnThrottle struct {
	mu         sync.Mutex
	lastWarn   map[string]time.Time
	suppressed map[string]int

	oldest time.Time

	now func() time.Time
}

const warnThrottleKeyCap = 1024

func (w *warnThrottle) shouldWarn(key string, window time.Duration) (warn bool, suppressedSinceLast int, saturated bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	now := time.Now()
	if w.now != nil {
		now = w.now()
	}
	if last, ok := w.lastWarn[key]; ok && now.Sub(last) < window {
		if w.suppressed == nil {
			w.suppressed = make(map[string]int)
		}
		w.suppressed[key]++
		return false, 0, false
	}
	if _, tracked := w.lastWarn[key]; !tracked && len(w.lastWarn) >= warnThrottleKeyCap {
		w.prune(now, window)
		if len(w.lastWarn) >= warnThrottleKeyCap {
			return false, 0, true
		}
	}
	if w.lastWarn == nil {
		w.lastWarn = make(map[string]time.Time)
	}
	if len(w.lastWarn) == 0 {
		w.oldest = now
	}
	w.lastWarn[key] = now
	suppressedSinceLast = w.suppressed[key]
	delete(w.suppressed, key)
	return true, suppressedSinceLast, false
}

func (w *warnThrottle) active(key string, window time.Duration) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	now := time.Now()
	if w.now != nil {
		now = w.now()
	}
	last, ok := w.lastWarn[key]
	return ok && now.Sub(last) < window
}

func (w *warnThrottle) forget(key string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.lastWarn, key)
	delete(w.suppressed, key)
}

func (w *warnThrottle) prune(now time.Time, window time.Duration) {
	if now.Sub(w.oldest) < window {
		return
	}
	var oldest time.Time
	for k, last := range w.lastWarn {
		if now.Sub(last) >= window {
			delete(w.lastWarn, k)
			delete(w.suppressed, k)
			continue
		}
		if oldest.IsZero() || last.Before(oldest) {
			oldest = last
		}
	}
	w.oldest = oldest
}

func (a *Adapter) processInboundMessage(env *ingressEnvelope, sessionEpoch int64, firstDelivery bool) inboundOutcome {
	msg := &env.Message
	if msg.Type == "reaction" {
		if !a.cfg.FeedbackEnabled {
			a.logger.Debug("reaction ignored: feedback capture is disabled", "wamid", msg.ID)
			return outcomeDeclined
		}
		return a.handleReaction(msg)
	}

	if isNonContentType(msg.Type) {
		if msg.Type == "system" {
			a.handleSystemNotification(a.ctx, env)
		}
		a.logger.Info("non-content inbound message ignored", "type", msg.Type, "wamid", msg.ID)
		return outcomeDeclined
	}

	if _, ok := feedbackListReplyID(msg); ok && a.cfg.FeedbackEnabled {
		return a.handleFeedbackListReply(msg)
	}

	authz := a.authorizeSender(a.ctx, env, contactName(&env.Context, msg))
	if authz.Retryable {
		return outcomeAuthzUnavailable
	}
	if authz.Identity == nil {
		return a.refuseUnidentifiedSender(a.ctx, env, authz)
	}
	env.authorized, env.authorizedSource = authz.Identity, authz.Source

	if p := a.livePendingPrompt(msg.senderID()); p != nil {
		p.authorized, p.authorizedSource = env.authorized, env.authorizedSource
		p.answerWamid = msg.ID
		return a.resolvePendingHIL(p, msg, firstDelivery)
	}

	if createsTask(msg.Type) && !a.admitTask(msg.senderID()) {
		a.refuse(msg, &a.burstySenders, backpressureNoticeWindow, refusalTaskLimit, msgTooManyTasks,
			"whatsapp inbound message refused: sender is at the concurrent-task limit")
		return outcomeDeclined
	}

	switch msg.Type {
	case "text":
		text := strings.TrimSpace(msg.Text.Body)
		if text == "" {
			a.logger.Warn("inbound text message with empty body ignored", "wamid", msg.ID)
			return outcomeDeclined
		}
		return a.submitTask(env, text, []a2a.Part{a2a.NewTextPart(text)}, sessionEpoch)
	case "image":
		return a.handleMediaMessage(env, msg.Image, imageFilename(msg.Image), sessionEpoch)
	case "document":
		return a.handleMediaMessage(env, msg.Document, documentFilename(msg.Document), sessionEpoch)
	case "video":
		return a.handleMediaMessage(env, msg.Video, videoFilename(msg.Video), sessionEpoch)
	case "audio":
		return a.handleMediaMessage(env, msg.Audio, audioFilename(msg.Audio), sessionEpoch)
	case "interactive":
		a.logger.Info("interactive reply with no pending prompt declined", "wamid", msg.ID)
		a.reply("", msg.senderID(), msgPromptExpired)
		return outcomeDeclined
	case "contacts":
		return a.acknowledgeContactShare(msg)
	default:
		a.logger.Info("unsupported inbound message type declined", "type", msg.Type, "wamid", msg.ID)
		a.reply("", msg.senderID(), msgUnsupportedType)
		return outcomeDeclined
	}
}

func (a *Adapter) acknowledgeContactShare(msg *inboundMessage) inboundOutcome {
	if !sharedTheirOwnNumber(msg) {
		a.logger.Info("contact share declined: it is not the sender's own number", "wamid", msg.ID)
		a.reply("", msg.senderID(), msgUnsupportedType)
		return outcomeDeclined
	}
	a.logger.Info("contact share accepted", "wamid", msg.ID)
	a.reply("", msg.senderID(), msgContactShareReceived)
	return outcomeDeclined
}

func isNonContentType(msgType string) bool {
	switch msgType {
	case "reaction", "system", "ephemeral":
		return true
	}
	return false
}

func (a *Adapter) resolvePendingHIL(p *pendingHIL, msg *inboundMessage, firstDelivery bool) inboundOutcome {
	var outcome inboundOutcome
	switch msg.Type {
	case "interactive":
		outcome = a.handleHILReply(p, msg)
	case "text":
		outcome = a.handleHILText(p, strings.TrimSpace(msg.Text.Body))
	default:
		a.logger.Info("inbound message cannot answer the pending interactive prompt",
			"type", msg.Type, "wamid", msg.ID, "taskID", p.taskID)
		a.reply(p.taskID, msg.senderID(), msgReplyToQuestion)
		outcome = outcomeDeclined
	}
	if firstDelivery {
		a.countHILResume(outcome)
	}
	if outcome == outcomeSubmitted {
		a.auditHILAnswer(p, msg)
		a.clearAnsweredPrompt(msg.senderID(), p)
		a.typingShow(p.fromPhone)
	}
	return outcome
}

func (a *Adapter) handleMediaMessage(env *ingressEnvelope, media *mediaObject, filename string, sessionEpoch int64) inboundOutcome {
	msg := &env.Message
	if a.cfg.DisableFileUploads {
		a.refuse(msg, &a.declinedUploads, backpressureNoticeWindow, refusalUploadsDisabled, msgUploadsDisabled,
			"whatsapp inbound message refused: file uploads are disabled on this entrypoint")
		return outcomeDeclined
	}

	if !a.mediaTransfers.acquire(a.ctx) {
		a.logger.Warn("deferred inbound media: adapter shutting down", "type", msg.Type, "wamid", msg.ID)
		return outcomeTransient
	}
	data, mime, ok, failure := a.fetchInboundMedia(msg, media, "file")
	if !ok {
		a.mediaTransfers.release()
		if failure.decline != "" {
			a.reply("", msg.senderID(), failure.decline)
		}
		return failure.outcome
	}
	defer a.mediaTransfers.release()

	caption := strings.TrimSpace(media.Caption)
	partText := caption
	if partText == "" {
		partText = fmt.Sprintf("[Sent a %s: %s]", cmp.Or(msg.Type, "file"), filename)
	}
	return a.submitTask(env, caption, buildInboundParts(partText, filename, mime, data), sessionEpoch)
}

type inboundMediaFailure struct {
	outcome inboundOutcome
	decline string
}

func (a *Adapter) fetchInboundMedia(msg *inboundMessage, media *mediaObject, noun string) (data []byte, mime string, ok bool, failure inboundMediaFailure) {
	if media == nil || media.ID == "" {
		a.logger.Warn("media message missing media object", "type", msg.Type, "wamid", msg.ID)
		return nil, "", false, inboundMediaFailure{
			outcome: outcomeDeclined,
			decline: fmt.Sprintf("Sorry, I couldn't read that %s.", noun),
		}
	}

	data, mime, err := a.client.FetchMedia(a.ctx, media.ID, a.maxMediaBytes())
	if err != nil {
		if errors.Is(err, ErrMediaTooLarge) {
			a.logger.Info("inbound media exceeds size cap", "type", msg.Type, "wamid", msg.ID, "max_mb", a.cfg.MaxMediaSizeMB)
			return nil, "", false, inboundMediaFailure{
				outcome: outcomeDeclined,
				decline: fmt.Sprintf(msgMediaTooLargeFmt, noun, a.cfg.MaxMediaSizeMB),
			}
		}
		a.logger.Error("failed to download inbound media", append([]any{"type", msg.Type, "wamid", msg.ID}, safeGraphErrFields(err)...)...)
		return nil, "", false, inboundMediaFailure{outcome: outcomeTransient}
	}
	if len(data) == 0 {
		a.logger.Warn("inbound media download returned no bytes", "type", msg.Type, "wamid", msg.ID)
		return nil, "", false, inboundMediaFailure{outcome: outcomeTransient}
	}
	if mime == "" {
		mime = media.MimeType
	}
	return data, mime, true, inboundMediaFailure{}
}

const metricWhatsAppSubmitFailures = "whatsapp_submit_failures"

func (a *Adapter) submitTask(env *ingressEnvelope, mentionText string, parts []a2a.Part, sessionEpoch int64) inboundOutcome {
	outcome, refusal := a.trySubmit(env, mentionText, parts, sessionEpoch)
	a.core.ReportSubmitRefusal(refusal)
	if refusal != "" {
		a.meter().Counter(metricWhatsAppSubmitFailures, 1, "gatewayId", a.gatewayID(), "reason", refusal)
	}
	return outcome
}

func (a *Adapter) tasks() entrypoint.TaskRegistry {
	if a.coreTasksOverride != nil {
		return a.coreTasksOverride
	}
	return a.core.Tasks()
}

func (a *Adapter) trySubmit(env *ingressEnvelope, mentionText string, parts []a2a.Part, sessionEpoch int64) (inboundOutcome, string) {
	msg := &env.Message
	identity := env.authorized
	source := env.authorizedSource
	sender := senderIdentity(msg.senderID(), "")
	swapped := source == principalSourceRunAs
	submitCtx := a.ctx
	contentLen, contentSHA := summarizeParts(parts)

	res := a.resolveTarget(submitCtx, identity, mentionText)
	if res.Misconfigured {
		a.reply("", msg.senderID(), fmt.Sprintf(
			"This entrypoint is configured to route to %q, but no such agent is available right now. Please contact the entrypoint operator.",
			a.cfg.DefaultAgentName))
		return outcomeDeclined, taxonomy.RejectAgentNotFound
	}
	if res.Refused() {
		return outcomeUnauthorized, res.RefusalReason
	}
	agentName := res.Name
	if agentName == "" {
		a.logger.Warn("no agent available to route inbound message",
			"wamid", msg.ID,
			"configuredDefault", a.cfg.DefaultAgentName,
			"discovered", a.core.AgentRegistry().Names())
		return outcomeTransient, taxonomy.RejectAgentNotFound
	}

	taskID := idgen.New()
	externalKey := buildExternalKey(msg.senderID(), sessionEpoch)
	sessionID, err := a.resolveSessionID(submitCtx, externalKey, agentName, identity.UserID())
	if err != nil {
		a.logger.Error("failed to resolve session id",
			"externalKeyFP", strutil.PIIFingerprint(externalKey), "error", err)
		return outcomeTransient, taxonomy.RejectInternal
	}

	a2aMsg := a2a.Message{
		Kind:      "message",
		MessageID: idgen.New(),
		Role:      "user",
		Parts:     parts,
		TaskID:    taskID,
		ContextID: sessionID,
	}
	if swapped {
		a2aMsg.Metadata = map[string]any{
			"requesting_user": sender.UserID(),
			"run_as":          identity.UserID(),
		}
	}

	dropped := entrypoint.ConvertInlineFilePartsToArtifacts(
		submitCtx, &a2aMsg, a.core.Services().Artifacts(),
		entrypoint.ArtifactScope{UserID: identity.UserID(), SessionID: sessionID}, a.logger)
	if len(dropped) > 0 {
		entrypoint.AppendDroppedFilesNote(&a2aMsg, dropped)
	}

	if err := a.tasks().Register(taskID, agentName, identity.UserID(), entrypoint.TaskIdentity{}); err != nil {
		a.logger.Warn("failed to record task", "taskID", taskID, "error", err)
		return outcomeTransient, entrypoint.SubmitFailureReason(err)
	}
	a.tasks().SetMetadata(taskID, &taskMeta{FromPhone: msg.senderID(), SessionID: sessionID})
	if err := a.taskItems().writeDestination(submitCtx, taskID, msg.senderID(), sessionID); err != nil {
		a.logger.Warn("failed to record the task's destination", "taskID", taskID, "error", err)
		a.tasks().Remove(taskID)
		return outcomeTransient, taxonomy.RejectInternal
	}

	a.typingTrack(msg.senderID(), taskID, msg.ID)
	if err := a.core.SubmitTask(submitCtx, agentName, taskID, a2aMsg, identity); err != nil {
		a.typingFinished(msg.senderID(), taskID)
		a.tasks().Remove(taskID)
		if entrypoint.IsBudgetRefusal(err) || entrypoint.IsAgentNotAvailable(err) {
			a.reply("", msg.senderID(), a.core.SubmitFailureText(err, true))
			return outcomeDeclined, entrypoint.SubmitFailureReason(err)
		}
		denied := entrypoint.IsAuthorizationDenial(err)
		if !denied {
			entrypoint.LogSubmitFailure(a.logger, slog.LevelWarn, err, "taskID", taskID)
		}
		if denied {
			return outcomeUnauthorized, entrypoint.SubmitFailureReason(err)
		}
		return outcomeTransient, entrypoint.SubmitFailureReason(err)
	}
	if len(dropped) > 0 {
		a.reply(taskID, msg.senderID(), "(An attachment could not be processed and was not included.)")
	}

	a.senders.taskStarted(msg.senderID(), taskID)

	var traceID string
	if info := a.tasks().Get(taskID); info != nil {
		traceID = info.TraceID
	}
	a.logger.Info("task submitted", "taskID", taskID, "agent", agentName,
		"user", maskSenderID(msg.senderID()), "resolvedUserId", resolvedUserID(identity),
		"principalSource", source,
		"runAs", runAsPrincipalOrEmpty(a.cfg), "traceID", traceID)
	a.auditInbound(msg, inboundAudit{
		userID:          identity.UserID(),
		senderID:        sender.UserID(),
		principalSource: source,
		agentName:       agentName,
		taskID:          taskID,
		traceID:         traceID,
		sessionID:       sessionID,
		artifactRef:     firstArtifactRef(a2aMsg.Parts),
		contentLen:      contentLen,
		contentSHA:      contentSHA,
	})

	a.typingShow(msg.senderID())
	return outcomeSubmitted, ""
}

func maskSenderID(waID string) string {
	if len(waID) <= 4 {
		return "whatsapp:****"
	}
	return "whatsapp:***" + waID[len(waID)-4:]
}

func (a *Adapter) resolveTarget(ctx context.Context, caller *auth.Subject, text string) entrypoint.IngressTarget {
	target := a.core.ResolveIngressTarget(ctx, entrypoint.IngressTargetRequest{
		Caller:           caller,
		Text:             text,
		DefaultAgentName: a.cfg.DefaultAgentName,
		SignInLinkable:   false,
	})
	if target.Misconfigured {
		a.logger.Warn("configured default_agent_name not found in registry",
			"eventType", "whatsapp.default_agent_misconfigured",
			"configured", a.cfg.DefaultAgentName,
			"discovered", a.core.AgentRegistry().Names(),
		)
	}
	return target
}

func (a *Adapter) Capabilities() map[string]bool {
	return map[string]bool{tool.CapInteractiveUserInput: true}
}

func (a *Adapter) OnStatusUpdate(_ context.Context, taskID string, event *a2a.TaskStatusUpdateEvent, _ []byte) {
	if req, ok := a2ui.ExtractRequest(event); ok {
		a.handleUserInputRequest(taskID, req)
		return
	}
	meta := a.getTaskMeta(taskID)
	if meta == nil {
		return
	}
	a.typingActivity(meta.FromPhone, taskID)
	if authData, ok := a2a.AuthenticationRequiredFromStatus(event); ok {
		a.handleAuthRequired(taskID, meta, authData)
		return
	}
	if result, ok := a2a.AuthenticationResultFromStatus(event); ok {
		if result.Success {
			a.typingResumed(meta.FromPhone, taskID)
		}
		a.resolveAuthPrompt(taskID, meta, result.Success)
	}
	a.recordSavedArtifacts(taskID, meta, event)
}

func (a *Adapter) recordSavedArtifacts(taskID string, meta *taskMeta, event *a2a.TaskStatusUpdateEvent) {
	saves := entrypoint.SavedArtifactsFromStatus(event)
	if len(saves) == 0 {
		return
	}
	if peer := forwardedFromPeer(event); peer != "" {
		if info := a.tasks().Get(taskID); info == nil || info.AgentName != peer {
			return
		}
	}
	for _, change := range meta.recordSavedArtifacts(saves) {
		if !a.recordSavedFileItem(taskID, change) && !change.overflowed {
			meta.markSaveUnrecorded(change.file.Filename)
		}
	}
}

func (a *Adapter) recordSavedFileItem(taskID string, change savedFileChange) (written bool) {
	items := a.taskItems()
	if !items.enabled() {
		return true
	}
	ctx := context.WithoutCancel(a.ctx)
	switch {
	case change.overflowed:
		return items.put(ctx, taskID, itemSavedFilesOverflowed, "")
	case change.removed:
		return items.delete(ctx, taskID, itemSavedFilePrefix+change.file.Filename)
	default:
		value, err := json.Marshal(savedFileItem{MIMEType: change.file.MIMEType, Version: change.file.Version, Seq: change.seq})
		if err != nil {
			a.logger.Warn("failed to encode a saved-file item", "taskID", taskID, "error", err)
			return false
		}
		return items.put(ctx, taskID, itemSavedFilePrefix+change.file.Filename, string(value))
	}
}

func (a *Adapter) recordedSaves(ctx context.Context, taskID string, meta *taskMeta) recordedSaves {
	items := a.taskItems()
	if !items.enabled() {
		return meta.recordedSaves()
	}
	rows, err := items.list(ctx, taskID)
	if err != nil {
		a.logger.Warn("whatsapp saved-file items unreadable; delivering what this instance recorded", "taskID", taskID, "error", err)
		return meta.recordedSaves()
	}
	loaded := metaFromItems(rows, a.logger, taskID)
	if loaded == nil {
		return meta.recordedSaves()
	}
	return mergeUnrecordedSaves(loaded, meta)
}

func mergeUnrecordedSaves(loaded, meta *taskMeta) recordedSaves {
	meta.mu.Lock()
	defer meta.mu.Unlock()
	files := loaded.savedArtifacts
	for name := range meta.savesUnrecorded {
		files = slices.DeleteFunc(files, func(s entrypoint.SavedArtifact) bool { return s.Filename == name })
		if i := slices.IndexFunc(meta.savedArtifacts, func(s entrypoint.SavedArtifact) bool { return s.Filename == name }); i >= 0 {
			files = append(files, meta.savedArtifacts[i])
		}
	}
	return recordedSaves{files: files, overflowed: loaded.savesOverflowed || meta.savesOverflowed}
}

func (a *Adapter) OnArtifactUpdate(_ context.Context, taskID string, _ *a2a.TaskArtifactUpdateEvent, _ []byte) {
	if meta := a.getTaskMeta(taskID); meta != nil {
		a.typingActivity(meta.FromPhone, taskID)
	}
}

func (a *Adapter) OnFinalResponse(_ context.Context, taskID string, task *a2a.Task, _ []byte) {
	meta := a.getTaskMeta(taskID)
	if meta == nil {
		a.logger.Warn("final response for unknown task", "taskID", taskID)
		return
	}
	defer a.endingTask(taskID)()
	a.typingFinished(meta.FromPhone, taskID)
	defer a.tasks().Remove(taskID)
	defer a.senders.taskFinished(meta.FromPhone, taskID)
	defer a.clearPendingHIL(meta.FromPhone, taskID)

	authOK := task.Status.State != a2a.TaskStateFailed &&
		task.Status.State != a2a.TaskStateRejected &&
		task.Status.State != a2a.TaskStateCancelled
	a.resolveAuthPrompt(taskID, meta, authOK)

	switch task.Status.State {
	case a2a.TaskStateCancelled:
		a.reply(taskID, meta.FromPhone, msgTaskCanceled)
		return
	case a2a.TaskStateFailed, a2a.TaskStateRejected:
		a.logger.Error("task ended in failure state",
			"taskID", taskID, "state", task.Status.State, "detailLen", len(finalText(task)))
		a.reply(taskID, meta.FromPhone, msgAgentError)
		return
	}

	specs, text := extractArtifactReturns(finalText(task))
	text = renderForWhatsApp(text)
	target := a.targetForMeta(taskID, meta)
	if text != "" {
		a.replyAgentText(target, text)
	}
	queued := a.deliverArtifacts(target, task, specs, a.recordedSaves(context.WithoutCancel(a.ctx), taskID, meta))
	if text == "" && queued == 0 {
		a.logger.Warn("final response had no deliverable content", "taskID", taskID)
		a.reply(taskID, meta.FromPhone, msgNoDisplayableReply)
	}
}

var artifactReturnPattern = regexp.MustCompile(`«artifact_return:([^»]*)»\n?`)

func extractArtifactReturns(text string) ([]string, string) {
	var specs []string
	clean := artifactReturnPattern.ReplaceAllStringFunc(text, func(match string) string {
		sub := artifactReturnPattern.FindStringSubmatch(match)
		specs = append(specs, sub[1])
		return ""
	})
	return specs, strings.TrimSpace(clean)
}

func (a *Adapter) deliverArtifacts(target outboundTarget, task *a2a.Task, specs []string, saves recordedSaves) int {
	queued := 0
	named := make(map[string]bool)
	for _, spec := range specs {
		filename, version := agent.ParseArtifactSpec(spec)
		if filename == "" {
			continue
		}
		named[filename] = true
		if a.queueArtifact(target, filename, filename, version, "") {
			queued++
		}
	}
	if task.Status.Message != nil {
		for i := range task.Status.Message.Parts {
			part := &task.Status.Message.Parts[i]
			if part.Kind != "file" || part.File == nil {
				continue
			}
			named[filePartStoreName(part.File)] = true
			if a.queueFilePart(target, part.File) {
				queued++
			}
		}
	}
	return queued + a.deliverSavedArtifacts(target, saves, named)
}

const maxDeliveredSaves = 5

func (a *Adapter) deliverSavedArtifacts(target outboundTarget, saves recordedSaves, named map[string]bool) int {
	queued, unsent := 0, 0
	for _, saved := range saves.files {
		switch {
		case named[saved.Filename]:
		case queued == maxDeliveredSaves:
			unsent++
		case a.queueSavedArtifact(target, saved):
			queued++
		}
	}
	if unsent > 0 || saves.overflowed {
		a.reply(target.taskID, target.recipient, moreSavedFilesNotice(unsent, saves.overflowed))
	}
	return queued
}

func moreSavedFilesNotice(unsent int, overflowed bool) string {
	switch {
	case overflowed:
		return msgManyMoreSavedFiles
	case unsent == 1:
		return msgOneMoreSavedFile
	default:
		return fmt.Sprintf(msgMoreSavedFilesFmt, unsent)
	}
}

func filePartStoreName(fc *a2a.FileContent) string {
	if filename, _, parsed := entrypoint.ParseArtifactURI(fc.URI); parsed {
		return filename
	}
	return cmp.Or(fc.Name, inlineFileDefaultName)
}

const inlineFileDefaultName = "attachment"

func (a *Adapter) queueFilePart(target outboundTarget, fc *a2a.FileContent) bool {
	if fc.Bytes != "" {
		return a.queueInlineFileBytes(target, fc)
	}
	if fc.URI == "" {
		return false
	}
	filename, version, parsed := entrypoint.ParseArtifactURI(fc.URI)
	if !parsed || filename == "" {
		a.logger.Warn("outbound file part has unsupported URI", "taskID", target.taskID, "uri", fc.URI)
		return false
	}
	displayName := cmp.Or(fc.Name, filename)
	return a.queueArtifact(target, filename, displayName, version, fc.MIMEType)
}

func (a *Adapter) queueInlineFileBytes(target outboundTarget, fc *a2a.FileContent) bool {
	decoded, err := base64.StdEncoding.DecodeString(fc.Bytes)
	if err != nil {
		a.logger.Error("failed to decode outbound file bytes", "taskID", target.taskID, "error", err)
		return false
	}
	name := cmp.Or(fc.Name, inlineFileDefaultName)
	policy := a.checkOutboundMedia(fc.MIMEType, name, int64(len(decoded)))
	if !policy.ok {
		a.logger.Info("outbound file part rejected by policy",
			"taskID", target.taskID, "mime", policy.mime, "size", len(decoded))
		a.reply(target.taskID, target.recipient, policy.reason)
		return false
	}
	uri, ok := entrypoint.SaveBytesAsArtifact(a.ctx, a.core.Services().Artifacts(),
		entrypoint.ArtifactScope{AppName: target.agentName, UserID: target.userID, SessionID: target.sessionID},
		entrypoint.ArtifactUpload{Filename: name, MIMEType: policy.mime, Content: decoded, DeriveMIME: true},
		a.logger)
	if !ok {
		a.reply(target.taskID, target.recipient, "(An attachment could not be prepared and was not sent.)")
		return false
	}
	return a.enqueue(kindMedia, target, outboundPayload{Media: &mediaPayload{
		Filename:  name,
		MIMEType:  policy.mime,
		MediaType: policy.mediaType,
	}}, uri) != ""
}

type storedMedia struct {
	payload mediaPayload
	ref     string
	mime    string
	size    int64
	found   bool
	refusal string
}

func (a *Adapter) resolveStoredMedia(target outboundTarget, storeName, displayName string, version *int, declaredMIME string) (storedMedia, bool) {
	artSvc := a.core.Services().Artifacts()
	if artSvc == nil {
		a.logger.Warn("cannot deliver artifact: no artifact service configured",
			"taskID", target.taskID, "filename", storeName)
		return storedMedia{}, false
	}
	loc := artSvc.Resolve(target.agentName, target.userID, target.sessionID, storeName)

	m := storedMedia{mime: declaredMIME}
	if md, err := artSvc.Inner.GetVersionMetadata(a.ctx, loc, version); err == nil && md != nil {
		m.found, m.size = true, md.SizeBytes
		if md.MIMEType != "" {
			m.mime = md.MIMEType
		}
	}
	if max := a.maxMediaBytes(); max > 0 && m.size > max {
		m.refusal = fmt.Sprintf("(An attachment was too large to send — over the %d MB limit.)", a.cfg.MaxMediaSizeMB)
		return m, true
	}
	policy := a.checkOutboundMedia(m.mime, displayName, m.size)
	m.mime = policy.mime
	if !policy.ok {
		m.refusal = policy.reason
		return m, true
	}
	m.payload = mediaPayload{Filename: displayName, MIMEType: policy.mime, MediaType: policy.mediaType}
	m.ref = artifactURIForDelivery(loc, storeName, version)
	return m, true
}

func (a *Adapter) queueArtifact(target outboundTarget, storeName, displayName string, version *int, declaredMIME string) bool {
	m, ok := a.resolveStoredMedia(target, storeName, displayName, version, declaredMIME)
	if !ok {
		return false
	}
	if m.refusal != "" {
		a.logger.Info("outbound artifact rejected by policy",
			"taskID", target.taskID, "filename", storeName, "mime", m.mime, "size", m.size)
		a.reply(target.taskID, target.recipient, m.refusal)
		return false
	}
	return a.enqueue(kindMedia, target, outboundPayload{Media: &m.payload}, m.ref) != ""
}

func (a *Adapter) queueSavedArtifact(target outboundTarget, saved entrypoint.SavedArtifact) bool {
	m, ok := a.resolveStoredMedia(target, saved.Filename, saved.Filename, saved.Version, saved.MIMEType)
	if !ok {
		return false
	}
	if !m.found || m.refusal != "" {
		a.logger.Info("saved artifact not delivered",
			"taskID", target.taskID, "filename", saved.Filename, "found", m.found, "mime", m.mime, "size", m.size)
		return false
	}
	return a.enqueue(kindMedia, target, outboundPayload{Media: &m.payload}, m.ref) != ""
}

func (a *Adapter) loadArtifactBytes(ctx context.Context, row *store.WhatsAppOutbound, declaredMIME string) ([]byte, string, bool) {
	artSvc := a.core.Services().Artifacts()
	if artSvc == nil {
		a.logger.Warn("cannot deliver artifact: no artifact service configured", "outboundID", row.ID)
		return nil, "", false
	}
	filename, version, parsed := entrypoint.ParseArtifactURI(row.ArtifactRef)
	if !parsed || filename == "" {
		a.logger.Warn("queued media row has an unsupported artifact reference", "outboundID", row.ID)
		return nil, "", false
	}
	loc := artSvc.Resolve(row.AgentName, row.UserID, row.SessionID, filename)
	content, mimeType, err := artSvc.Inner.Load(ctx, loc, version)
	if err != nil {
		a.logger.Error("failed to load artifact for whatsapp delivery",
			"outboundID", row.ID, "filename", filename, "error", err)
		return nil, "", false
	}
	if len(content) == 0 {
		a.logger.Warn("artifact for whatsapp delivery not found or empty",
			"outboundID", row.ID, "filename", filename)
		return nil, "", false
	}
	return content, cmp.Or(mimeType, declaredMIME), true
}

func (a *Adapter) maxMediaBytes() int64 {
	return int64(a.cfg.MaxMediaSizeMB) * 1024 * 1024
}

func artifactURIForDelivery(loc artifact.Locator, filename string, version *int) string {
	uri := fmt.Sprintf("artifact://%s/%s/%s/%s",
		url.PathEscape(loc.AppName), url.PathEscape(loc.UserID),
		url.PathEscape(loc.SessionID), url.PathEscape(filename))
	if version != nil {
		uri += fmt.Sprintf("?version=%d", *version)
	}
	return uri
}

const (
	mimeOctetStream = "application/octet-stream"
	mimeTextPlain   = "text/plain"
)

var metaAcceptedMediaMIME = map[string]bool{
	"image/jpeg": true, "image/png": true,
	"application/pdf":               true,
	mimeTextPlain:                   true,
	"application/msword":            true,
	"application/vnd.ms-excel":      true,
	"application/vnd.ms-powerpoint": true,
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document":   true,
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":         true,
	"application/vnd.openxmlformats-officedocument.presentationml.presentation": true,
	"audio/aac": true, "audio/mp4": true, "audio/mpeg": true,
	"audio/amr": true, "audio/ogg": true,
	"video/mp4": true, "video/3gpp": true,
}

var metaMediaTypeCapBytes = map[string]int64{
	"image":    5 * 1024 * 1024,
	"audio":    16 * 1024 * 1024,
	"video":    16 * 1024 * 1024,
	"document": 100 * 1024 * 1024,
}

func (a *Adapter) outboundMediaCapBytes(mediaType string) int64 {
	limit := a.maxMediaBytes()
	if typeCap, ok := metaMediaTypeCapBytes[mediaType]; ok && (limit <= 0 || typeCap < limit) {
		return typeCap
	}
	return limit
}

func resolveOutboundMIME(mimeType, name string) string {
	mimeType = strings.ToLower(stripMIMEParams(mimeType))
	if mimeType == "" || mimeType == mimeOctetStream {
		if ext := filepath.Ext(name); ext != "" {
			if inferred := stripMIMEParams(mime.TypeByExtension(ext)); inferred != "" {
				mimeType = inferred
			}
		}
	}
	if strings.HasPrefix(mimeType, "text/") && mimeType != mimeTextPlain {
		mimeType = mimeTextPlain
	}
	if mimeType == "audio/opus" {
		mimeType = "audio/ogg"
	}
	if mimeType == "" {
		mimeType = mimeOctetStream
	}
	return mimeType
}

func stripMIMEParams(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, ';'); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	return s
}

func mediaTypeForMIME(mime string) string {
	switch m := strings.ToLower(mime); {
	case strings.HasPrefix(m, "image/"):
		return "image"
	case strings.HasPrefix(m, "audio/"):
		return "audio"
	case strings.HasPrefix(m, "video/"):
		return "video"
	default:
		return "document"
	}
}

func (a *Adapter) OnError(_ context.Context, taskID string, taskErr *a2a.Error, _ []byte) {
	meta := a.getTaskMeta(taskID)
	if meta == nil {
		a.logger.Warn("error for unknown task", "taskID", taskID)
		return
	}
	defer a.endingTask(taskID)()
	a.typingFinished(meta.FromPhone, taskID)
	defer a.tasks().Remove(taskID)
	defer a.senders.taskFinished(meta.FromPhone, taskID)
	defer a.clearPendingHIL(meta.FromPhone, taskID)
	a.resolveAuthPrompt(taskID, meta, false)

	if entrypoint.IsTaskTimeoutError(taskErr) {
		a.reply(taskID, meta.FromPhone, msgTaskCanceled)
		return
	}
	a.logger.Error("task failed", "taskID", taskID, "errorCode", taskErr.Code, "msgLen", len(taskErr.Message))
	a.reply(taskID, meta.FromPhone, msgAgentError)
}

func (a *Adapter) OnAgentDiscovered(card *a2a.AgentCard) {
	a.logger.Info("agent discovered", "agentName", card.Name)
}

func (a *Adapter) OnAgentRemoved(agentName string) {
	a.logger.Info("agent removed", "agentName", agentName)
}

func (a *Adapter) endingTask(taskID string) func() {
	a.endingTasks.Store(taskID, struct{}{})
	return func() { a.endingTasks.Delete(taskID) }
}

func (a *Adapter) getTaskMeta(taskID string) *taskMeta {
	meta, _ := a.tasks().Metadata(taskID).(*taskMeta)
	return meta
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

func buildInboundParts(caption, filename, mimeType string, data []byte) []a2a.Part {
	var parts []a2a.Part
	if caption != "" {
		parts = append(parts, a2a.NewTextPart(caption))
	}
	if mimeType == "" {
		mimeType = mimeOctetStream
	}
	b64 := base64.StdEncoding.EncodeToString(data)
	parts = append(parts, a2a.NewFilePartWithBytes(b64, filename, mimeType))
	return parts
}

func audioFilename(m *mediaObject) string {
	ext := "ogg"
	if m != nil {
		mime := stripMIMEParams(m.MimeType)
		if i := strings.LastIndex(mime, "/"); i >= 0 && i+1 < len(mime) {
			ext = mime[i+1:]
		}
	}
	return "voice." + ext
}

func documentFilename(m *mediaObject) string {
	if m == nil || m.Filename == "" {
		return "document"
	}
	name := filepath.Base(m.Filename)
	if name == "." || name == "/" || name == "" {
		return "document"
	}
	return name
}

func videoFilename(m *mediaObject) string {
	ext := "mp4"
	if m != nil {
		switch sub := strings.TrimPrefix(strings.ToLower(stripMIMEParams(m.MimeType)), "video/"); sub {
		case "mp4", "3gpp":
			ext = sub
		}
	}
	return "video." + ext
}

func imageFilename(m *mediaObject) string {
	ext := "jpg"
	if m != nil {
		if i := strings.LastIndex(m.MimeType, "/"); i >= 0 && i+1 < len(m.MimeType) {
			switch sub := m.MimeType[i+1:]; sub {
			case "jpeg", "jpg", "png", "gif", "webp":
				ext = sub
			}
		}
	}
	return "image." + ext
}

func contactName(value *webhookChangeValue, msg *inboundMessage) string {
	if msg == nil || msg.senderID() == "" {
		return ""
	}
	if c, _ := contactFor(value, msg); c != nil {
		return c.Profile.Name
	}
	return ""
}
