// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/extensions"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/extensions/sessioncontrol"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/structured"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth/toolauth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/health"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm/modelinfo"
	"github.com/SolaceProducts/solace-agent-mesh/internal/observability"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
	"github.com/SolaceProducts/solace-agent-mesh/internal/skillruntime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
	"github.com/SolaceProducts/solace-agent-mesh/internal/strutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool/remote"
	"github.com/SolaceProducts/solace-agent-mesh/internal/visionimage"
)

const DefaultMaxLLMCallsPerTask = 30

func (c *AgentConfig) EmbedResolutionEnabled() bool {
	return c.EnableEmbedResolution == nil || *c.EnableEmbedResolution
}

func (c *AgentConfig) ArtifactContentInstructionEnabled() bool {
	return c.EnableArtifactContentInstruction == nil || *c.EnableArtifactContentInstruction
}

func ApplyDefaultPromptFlags(ac *AgentConfig) {
	off := false
	if ac.EnableExamples == nil {
		ac.EnableExamples = &off
	}
}

type AgentConfig struct {
	Name string

	DisplayName string

	AgentType string

	Namespace string

	Description string

	Version string

	Model string

	OriginalModel string

	ModelAPIBase string

	ModelAlias string

	MaxTokens int

	MaxLLMCalls int

	CacheStrategy string

	ReasoningBudgetTokens int
	ReasoningEffort       string

	EnableAutoContinuation *bool

	AutoSummarization *CompactionConfig

	SystemPrompt string

	GlobalInstruction string

	InjectCurrentTime *bool

	InjectSystemPurpose bool

	InjectResponseFormat bool

	InjectUserProfile bool

	SupportsStreaming bool

	StreamBatchingThresholdBytes int

	StreamBatchingFlushIntervalMs int

	MaxMessageSizeBytes int

	ArtifactHandlingMode string

	EnableInlineVision bool

	MaxInlineVisionImages int

	MaxInlineVisionBytes int

	EnableInlinePDF bool

	MaxInlinePDFBytes int

	MaxInlinePDFPages int

	MaxInlinePDFCount int

	MaxInlineArtifactBytes int

	TextArtifactContentMaxLength int

	EnableEmbedResolution *bool

	EnableArtifactContentInstruction *bool

	EnableTemplateInstructions *bool

	EnableExamples *bool

	Headless *bool

	AutoInjectArtifactTools *bool

	AutoInjectDataAnalysisTools *bool

	ToolOutputSaveThresholdBytes int

	ToolOutputLLMReturnMaxBytes int

	ToolResultProcessingEnabled *bool

	ToolResultPreviewLengthChars int

	SchemaMaxKeys int

	SchemaInferenceDepth int

	MCPToolResponseSaveThresholdBytes int

	MCPToolLLMReturnMaxBytes int

	MCPIntelligentProcessing map[string]any

	Skills []a2a.AgentSkill

	DefaultInputModes []string

	DefaultOutputModes []string

	DocumentationURL string

	Provider *a2a.AgentProvider

	Welcome *WelcomeConfig

	DeploymentID string

	CardPublishIntervalSeconds int

	DiscoveryEnabled *bool

	HealthCheckTTLSeconds int

	HealthCheckIntervalSeconds int

	PeerAllowList []string

	PeerDenyList []string

	PeerRequestTimeoutSeconds int

	WorkflowRequestTimeoutSeconds int

	SubTaskTimeoutSeconds int

	MaxCallDepth int

	MaxSubTaskFanout int

	MaxMessageRedelivery int

	RespectMessageTTL bool

	InputSchema map[string]any

	OutputSchema map[string]any

	ValidationMaxRetries int

	SkillRefs []SkillRef

	SkillsBasePath string

	SkillsCachePath string

	HILToolConfigs map[string]HILToolConfig

	HILDefaultTimeout time.Duration

	AuthPendingTimeout time.Duration

	AuthMaxAttempts int

	Internal bool

	ShowSessions bool

	Ephemeral bool

	Priority int

	RequiredScopes []string

	VolumeSlots []VolumeSlot

	ToolVolumeBindings map[string]map[string]string

	NetworkPolicies []NetworkPolicy

	ToolNetworkBindings map[string]map[string]string
}

type NetworkPolicy struct {
	Name string

	Description string

	Allowlist []string
}

type VolumeSlot struct {
	Name string

	Provision string

	VolumeID string

	TTL string

	Description string

	PromptMessage string

	AllowExec bool
}

type SkillRef struct {
	Name string
	Path string

	ToolConfig map[string]map[string]any
}

type WelcomeSuggestion struct {
	Label    string
	Prompt   string
	AutoSend bool
}

type WelcomeConfig struct {
	Message     string
	Suggestions []WelcomeSuggestion
}

type Startable interface {
	Start(ctx context.Context) error
	Stop()
}

type LoadedSkillInfo struct {
	Name               string
	InstructionContent string
	ReferenceFileCount int
	AssetFileCount     int
}

type AvailableSkill struct {
	Name        string
	Description string
}

type skillManager interface {
	GetInstructions(loadedNames []string) []LoadedSkillInfo
	IsHeld(name string) bool
	SkillToolOwners(toolName string) []string
	SkillToolNames(skillName string) []string
	HasAnyResources() bool
	HasAnyAssets() bool
	AvailableSkillsForCard() []AvailableSkill
}

type SkillRehydrator interface {
	RehydrateSkills(ctx context.Context, toolCtx *tool.Context, names []string) []string
}

var SkillStateFactory func(sess *session.Session) tool.SkillStateProvider

func (a *Agent) buildToolConfig() map[string]any {
	cfg := map[string]any{
		"tool_output_save_threshold_bytes":       a.cfg.ToolOutputSaveThresholdBytes,
		"tool_output_llm_return_max_bytes":       a.cfg.ToolOutputLLMReturnMaxBytes,
		"schema_max_keys":                        a.cfg.SchemaMaxKeys,
		"schema_inference_depth":                 a.cfg.SchemaInferenceDepth,
		"mcp_tool_response_save_threshold_bytes": a.cfg.MCPToolResponseSaveThresholdBytes,
		"mcp_tool_llm_return_max_bytes":          a.cfg.MCPToolLLMReturnMaxBytes,
		"mcp_intelligent_processing":             a.cfg.MCPIntelligentProcessing,
	}
	if a.cfg.HILDefaultTimeout > 0 {
		cfg["hil_default_timeout"] = a.cfg.HILDefaultTimeout.String()
	}
	return cfg
}

func (a *Agent) omitSkillToolInstructions(promptSkills, undelivered []string) func(toolName string) bool {
	return func(toolName string) bool {
		if a.skillManager == nil {
			return false
		}
		owners := a.skillManager.SkillToolOwners(toolName)
		if len(owners) == 0 || slices.Contains(undelivered, toolName) {
			return false
		}
		return !slices.ContainsFunc(owners, func(owner string) bool { return slices.Contains(promptSkills, owner) })
	}
}

func (a *Agent) skillToolHidden(toolName string, skillState tool.SkillStateProvider) ([]string, bool) {
	owners := a.skillManager.SkillToolOwners(toolName)
	if len(owners) == 0 || slices.ContainsFunc(owners, skillState.IsLoaded) {
		return nil, false
	}
	return owners, true
}

func (a *Agent) refuseUnloadedSkillTool(toolName string, toolCtx *tool.Context) *tool.Result {
	if a.skillManager == nil || toolCtx.SkillState == nil {
		return nil
	}
	owners, hidden := a.skillToolHidden(toolName, toolCtx.SkillState)
	if !hidden {
		return nil
	}
	a.logger.Debug("refused a call to the tool of a skill the session has not loaded",
		"tool", toolName, "skills", owners, "sessionID", toolCtx.SessionID, "traceID", toolCtx.TraceID)
	if len(owners) == 1 {
		return tool.Error(fmt.Sprintf("tool %q belongs to skill %q, which is not loaded in this session; load it with load_skill, then call this tool on your next step", toolName, owners[0]))
	}
	return tool.Error(fmt.Sprintf("tool %q belongs to skills %s, none of which is loaded in this session; load one of them with load_skill, then call this tool on your next step", toolName, quotedList(owners)))
}

func quotedList(names []string) string {
	quoted := make([]string, len(names))
	for i, name := range names {
		quoted[i] = strconv.Quote(name)
	}
	return strings.Join(quoted, ", ")
}

func isResourceTool(name string) bool {
	switch name {
	case skillruntime.ListResourcesToolName, skillruntime.ReadResourceToolName, skillruntime.GrepResourcesToolName:
		return true
	}
	return false
}

type Agent struct {
	cfg    AgentConfig
	svc    runtime.Services
	tools  *tool.Set
	logger *slog.Logger

	tempPolicy   llm.TemperaturePolicy
	ctx          context.Context
	cancelFn     context.CancelFunc
	queue        broker.Queue
	requestQueue broker.Queue
	peers        peerView
	peerRouter   *PeerRouter
	extensions   *extensions.Dispatcher

	seqGen *EventSeqGen

	extensionMethodsMu sync.RWMutex
	extensionMethods   map[string]string

	compactionLocks      *sessionCompactionLocks
	subComponentsMu      sync.Mutex
	subComponents        []Startable
	subComponentsStopped bool
	structuredHandler    *structured.Handler

	toolRegistrationsMu sync.RWMutex
	toolRegistrations   []ToolEntryRegistration

	toolProvenanceMu sync.RWMutex
	toolProvenance   map[string]ToolProvenance

	authHandlersMu sync.RWMutex
	authHandlers   map[string]tool.AuthResponseHandler

	authInFlightMu sync.Mutex
	authInFlight   map[string]time.Time

	authFailures map[string]*authFailureState

	processedAuthRespsMu   sync.Mutex
	processedAuthResps     map[string]struct{}
	processedAuthRespsRing []string
	processedAuthRespsHead int
	processedAuthRespsLen  int
	authRespRingWarnOnce   sync.Once

	skillManager    skillManager
	skillRehydrator SkillRehydrator

	activeTasksMu sync.Mutex
	activeTasks   map[string]*activeTaskEntry

	controlEntries map[*activeTaskEntry]string

	drainTimeout time.Duration

	pendingHILMu sync.Mutex
	pendingHIL   map[string]pendingHILRequest

	early earlyAnswers

	pausedTasks *taskStateStore

	runningTasks *runningTasks

	checkpoints *taskCheckpointer

	orphanAdoptions chan orphanAdoption
	orphanAnswers   chan orphanAdoption
	orphanRefusals  orphanRefusalLog

	planWaiters *planWaiterRegistry

	resolvedVolumesMu sync.Mutex
	resolvedVolumes   map[string]string

	bindingEffortMu           sync.Mutex
	lastRejectedBindingEffort string
	haveRejectedBindingEffort bool

	wg sync.WaitGroup

	stopping atomic.Bool

	exitingProcess atomic.Bool

	handingOffPausedTasks atomic.Bool

	removing atomic.Bool

	stopRequestIntake context.CancelFunc
	requestIntakeDone chan struct{}

	requestsInProcess map[string]int

	health health.State

	runCtx context.Context

	resolvedModelName atomic.Pointer[string]

	resolvedModelAPIBase atomic.Pointer[string]

	instanceID string

	cardMu      sync.Mutex
	cardRetired bool

	spendPublisher       *spend.Publisher
	countsContextSavings bool
}

func New(cfg AgentConfig) *Agent {
	return &Agent{
		cfg:                    cfg,
		instanceID:             idgen.New(),
		tools:                  tool.NewSet(),
		extensions:             extensions.NewDispatcher(),
		extensionMethods:       make(map[string]string),
		activeTasks:            make(map[string]*activeTaskEntry),
		controlEntries:         make(map[*activeTaskEntry]string),
		requestsInProcess:      make(map[string]int),
		pendingHIL:             make(map[string]pendingHILRequest),
		authHandlers:           make(map[string]tool.AuthResponseHandler),
		authInFlight:           make(map[string]time.Time),
		authFailures:           make(map[string]*authFailureState),
		processedAuthResps:     make(map[string]struct{}, authRespDedupMaxEntries),
		processedAuthRespsRing: make([]string, authRespDedupMaxEntries),
		pausedTasks:            newTaskStateStore(),
		runningTasks:           newRunningTasks(),
		planWaiters:            newPlanWaiterRegistry(),
		resolvedVolumes:        make(map[string]string),
		seqGen:                 NewEventSeqGen(),
		drainTimeout:           defaultDrainTimeout,
	}
}

type taskUsageSnapshot struct {
	usage           llm.Usage
	lastInputTokens int
}

func (s *taskUsageSnapshot) add(u *llm.Usage) {
	if u == nil {
		return
	}
	s.usage.PromptTokens += u.PromptTokens
	s.usage.CompletionTokens += u.CompletionTokens
	s.usage.TotalTokens += u.TotalTokens
	s.usage.CachedTokens += u.CachedTokens
	s.usage.CacheWriteTokens += u.CacheWriteTokens
	s.usage.ReasoningTokens += u.ReasoningTokens
	s.lastInputTokens = u.PromptTokens
}

var errTaskCancelled = fmt.Errorf("task cancelled: %w", context.Canceled)

type stopTaskFunc func()

func cancellableTaskContext(parent context.Context) (ctx context.Context, endTurn context.CancelFunc, stopTask stopTaskFunc) {
	ctx, cancel := context.WithCancelCause(parent)
	return ctx, func() { cancel(nil) }, func() { cancel(errTaskCancelled) }
}

func taskWasCancelled(ctx context.Context) bool {
	return errors.Is(context.Cause(ctx), errTaskCancelled)
}

type activeTaskEntry struct {
	cancel       stopTaskFunc
	replyTo      string
	contextID    string
	userProps    a2a.UserProperties
	publishOnce  *sync.Once
	kind         string
	checkpointed *atomic.Bool
	usage        atomic.Pointer[taskUsageSnapshot]
	facts        atomic.Pointer[taskUsageFacts]
}

func (e *activeTaskEntry) recordUsage(snap taskUsageSnapshot) {
	e.usage.Store(&snap)
}

func (e *activeTaskEntry) usageSnapshot() taskUsageSnapshot {
	if snap := e.usage.Load(); snap != nil {
		return *snap
	}
	return taskUsageSnapshot{}
}

func (e *activeTaskEntry) recordFacts(facts *taskUsageFacts) {
	e.facts.Store(facts)
}

func (e *activeTaskEntry) usageFacts() *taskUsageFacts {
	return e.facts.Load()
}

func (a *Agent) releaseActiveTask(taskID string, entry *activeTaskEntry) {
	a.activeTasksMu.Lock()
	if a.activeTasks[taskID] == entry {
		delete(a.activeTasks, taskID)
	}
	a.activeTasksMu.Unlock()
}

func (a *Agent) trackControlEntry(taskID string, entry *activeTaskEntry) {
	a.activeTasksMu.Lock()
	a.controlEntries[entry] = taskID
	a.activeTasksMu.Unlock()
}

func (a *Agent) releaseControlEntry(entry *activeTaskEntry) {
	a.activeTasksMu.Lock()
	delete(a.controlEntries, entry)
	a.activeTasksMu.Unlock()
}

func (a *Agent) controlEntriesForLocked(taskID string) []*activeTaskEntry {
	var entries []*activeTaskEntry
	for entry, id := range a.controlEntries {
		if id == taskID {
			entries = append(entries, entry)
		}
	}
	return entries
}

func (a *Agent) recordActiveTaskUsage(taskID string, snap taskUsageSnapshot) {
	a.activeTasksMu.Lock()
	entry := a.activeTasks[taskID]
	a.activeTasksMu.Unlock()
	if entry != nil {
		entry.recordUsage(snap)
	}
}

func (a *Agent) foldToolLLMUsage(state *TaskExecutionState) {
	if state.foldToolLLMUsage() {
		a.recordActiveTaskUsage(state.TaskID, state.usageSnapshot())
	}
}

func (a *Agent) registerActiveTaskFacts(taskID string, facts *taskUsageFacts) {
	a.activeTasksMu.Lock()
	entry := a.activeTasks[taskID]
	a.activeTasksMu.Unlock()
	if entry != nil {
		entry.recordFacts(facts)
	}
}

func (a *Agent) pausedTaskUsage(state *TaskExecutionState) taskUsageSnapshot {
	a.activeTasksMu.Lock()
	entry := a.activeTasks[state.TaskID]
	a.activeTasksMu.Unlock()
	if entry != nil {
		return entry.usageSnapshot()
	}
	return state.usageSnapshot()
}

const authRespDedupMaxEntries = 256

const hostNotifyCallIDPrefix = "host-notify-"

const (
	mimeOctetStream = "application/octet-stream"
	mimeJSON        = "application/json"
	mimeXML         = "application/xml"
	mimeYAML        = "application/yaml"
	mimePDF         = "application/pdf"
)

const userFileContextPreamble = "\nThe user has provided the following file as context for your task. "

const codeExecToolName = "execute_python_code"

const loadArtifactToolName = "load_artifact"

func (a *Agent) artifactToolHint(filename string) string {
	if a.tools != nil && a.tools.GetTool(codeExecToolName) != nil {
		return fmt.Sprintf(" To process it with %s, pass input_artifacts=[%q] and read it via INPUT_ARTIFACTS[%q].",
			codeExecToolName, filename, filename)
	}
	return ""
}

func (a *Agent) imageViewHint(filename, mimeType string) string {
	if !visionimage.Renderable(mimeType) {
		return ""
	}
	if a.tools == nil || a.tools.GetTool(loadArtifactToolName) == nil {
		return ""
	}
	return fmt.Sprintf("\nTo view this image, call %s with filename=%q.", loadArtifactToolName, filename)
}

func (a *Agent) markAuthResponseProcessed(functionCallID string) bool {
	a.processedAuthRespsMu.Lock()
	defer a.processedAuthRespsMu.Unlock()
	if _, exists := a.processedAuthResps[functionCallID]; exists {
		return true
	}
	ringCap := len(a.processedAuthRespsRing)
	if ringCap == 0 {
		a.processedAuthRespsRing = make([]string, authRespDedupMaxEntries)
		ringCap = authRespDedupMaxEntries
		a.authRespRingWarnOnce.Do(func() {
			lg := a.logger
			if lg == nil {
				lg = slog.Default()
			}
			lg.Warn("agent: auth-response dedup ring was uninitialised; allocated on first use (Agent was not constructed via New())",
				"allocatedCap", ringCap,
			)
		})
	}
	if a.processedAuthRespsLen == ringCap {
		oldest := a.processedAuthRespsRing[a.processedAuthRespsHead]
		if oldest != "" {
			delete(a.processedAuthResps, oldest)
		}
	} else {
		a.processedAuthRespsLen++
	}
	a.processedAuthRespsRing[a.processedAuthRespsHead] = functionCallID
	a.processedAuthRespsHead = (a.processedAuthRespsHead + 1) % ringCap
	a.processedAuthResps[functionCallID] = struct{}{}
	return false
}

func authResponseDedupKey(functionCallID, code string) string {
	if functionCallID == "" || code == "" {
		return ""
	}
	return functionCallID + ":" + code
}

func (a *Agent) Name() string { return a.cfg.Name }

func (a *Agent) DisplayName() string { return a.cfg.DisplayName }

func (a *Agent) Namespace() string { return a.cfg.Namespace }

func (a *Agent) publishNotification(ctx context.Context, req a2a.NotifyRequest) error {
	data, err := json.Marshal(req)
	if err != nil {
		a.logger.Error("notify: marshal NotifyRequest failed", "error", err, "requestID", req.RequestID)
		return fmt.Errorf("marshal notify request: %w", err)
	}
	userProps := map[string]string{}
	if req.SenderUserID != "" {
		userProps[a2a.PropUserID] = req.SenderUserID
	}
	if tid, ok := req.Metadata["trace_id"].(string); ok && tid != "" {
		userProps[a2a.PropTraceID] = tid
	}
	topic := a2a.NotifyRequestTopic(a.cfg.Namespace, req.RecipientUserID)
	if err := a.svc.Broker().PublishGuaranteed(ctx, topic, &broker.Message{Payload: data, UserProperties: userProps}); err != nil {
		a.logger.Error("notify: publish NotifyRequest failed", "error", err, "topic", topic, "requestID", req.RequestID)
		return fmt.Errorf("publish notify request: %w", err)
	}
	a.logger.Info("notify: published NotifyRequest",
		"requestID", req.RequestID, "recipient", req.RecipientUserID, "mode", req.Mode)
	return nil
}

const scheduleReplyTimeout = 15 * time.Second

func (a *Agent) publishScheduleRequest(ctx context.Context, req a2a.ScheduleRequest) error {
	brk := a.svc.Broker()
	replyTopic := a2a.ScheduleReplyTopic(a.cfg.Namespace, req.RequestID)

	replyQ, err := brk.CreateTemporaryQueue()
	if err == nil {
		err = brk.Subscribe(replyQ, replyTopic)
		if err != nil {
			_ = brk.DeleteQueue(replyQ.Name())
		}
	}
	if err != nil {
		a.logger.Warn("schedule: reply channel unavailable; publishing without confirmation",
			"error", err, "requestID", req.RequestID)
		return a.publishScheduleFireAndForget(ctx, req)
	}
	defer func() {
		_ = brk.Unsubscribe(replyQ, replyTopic)
		_ = brk.DeleteQueue(replyQ.Name())
	}()
	req.ReplyTo = replyTopic

	data, err := json.Marshal(req)
	if err != nil {
		a.logger.Error("schedule: marshal ScheduleRequest failed", "error", err, "requestID", req.RequestID)
		return fmt.Errorf("marshal schedule request: %w", err)
	}
	userProps := map[string]string{}
	if req.SenderUserID != "" {
		userProps[a2a.PropUserID] = req.SenderUserID
	}
	topic := a2a.ScheduleRequestTopic(a.cfg.Namespace)
	if pubErr := brk.PublishGuaranteed(ctx, topic, &broker.Message{
		Payload: data, UserProperties: userProps, ReplyTo: replyTopic, CorrelationID: req.RequestID,
	}); pubErr != nil {
		a.logger.Error("schedule: publish ScheduleRequest failed", "error", pubErr, "topic", topic, "requestID", req.RequestID)
		return fmt.Errorf("publish schedule request: %w", pubErr)
	}
	a.logger.Debug("schedule: published ScheduleRequest; awaiting accept/reject",
		"requestID", req.RequestID, "name", req.Name, "target", req.TargetName)

	const couldNotConfirm = "scheduling could not be confirmed. It may still have been created — check your scheduled tasks before trying again"
	waitCtx, cancel := context.WithTimeout(ctx, scheduleReplyTimeout)
	defer cancel()
	for {
		msg, rerr := brk.Receive(waitCtx, replyQ)
		if rerr != nil {
			a.logger.Warn("schedule: reply receive failed before confirmation",
				"requestID", req.RequestID, "name", req.Name, "error", rerr)
			return errors.New(couldNotConfirm)
		}
		if msg == nil {
			if waitCtx.Err() != nil {
				a.logger.Warn("schedule: no accept/reject before timeout",
					"requestID", req.RequestID, "name", req.Name)
				return errors.New(couldNotConfirm)
			}
			continue
		}
		if msg.CorrelationID != "" && msg.CorrelationID != req.RequestID {
			msg.Ack()
			continue
		}
		var resp a2a.ScheduleResponse
		if uErr := json.Unmarshal(msg.Payload, &resp); uErr != nil {
			msg.Ack()
			return fmt.Errorf("schedule: unreadable response: %w", uErr)
		}
		msg.Ack()
		if !resp.OK {
			if resp.Error != "" {
				return errors.New(resp.Error)
			}
			return errors.New("the scheduled task was rejected")
		}
		a.logger.Info("schedule: task created", "requestID", req.RequestID, "name", req.Name, "taskID", resp.TaskID)
		return nil
	}
}

func (a *Agent) publishScheduleFireAndForget(ctx context.Context, req a2a.ScheduleRequest) error {
	req.ReplyTo = ""
	data, err := json.Marshal(req)
	if err != nil {
		a.logger.Error("schedule: marshal ScheduleRequest failed", "error", err, "requestID", req.RequestID)
		return fmt.Errorf("marshal schedule request: %w", err)
	}
	userProps := map[string]string{}
	if req.SenderUserID != "" {
		userProps[a2a.PropUserID] = req.SenderUserID
	}
	topic := a2a.ScheduleRequestTopic(a.cfg.Namespace)
	if pubErr := a.svc.Broker().PublishGuaranteed(ctx, topic, &broker.Message{Payload: data, UserProperties: userProps}); pubErr != nil {
		a.logger.Error("schedule: publish ScheduleRequest failed", "error", pubErr, "topic", topic, "requestID", req.RequestID)
		return fmt.Errorf("publish schedule request: %w", pubErr)
	}
	a.logger.Info("schedule: published ScheduleRequest (fire-and-forget; no confirmation)",
		"requestID", req.RequestID, "name", req.Name, "target", req.TargetName)
	return nil
}

func (a *Agent) Kind() string { return "agent" }

func (a *Agent) SetHILToolConfigs(configs map[string]HILToolConfig) {
	a.cfg.HILToolConfigs = configs
	if a.tools != nil {
		a.tools.SetHILConfigs(configs)
	}
}

func (a *Agent) HILToolConfigForTool(name string) (HILToolConfig, bool) {
	if a.tools == nil {
		cfg, ok := a.cfg.HILToolConfigs[name]
		return cfg, ok
	}
	return a.tools.HILConfigFor(name)
}

func (a *Agent) SetToolVolumeBindings(bindings map[string]map[string]string) {
	a.cfg.ToolVolumeBindings = bindings
}

func (a *Agent) SetToolNetworkBindings(bindings map[string]map[string]string) {
	a.cfg.ToolNetworkBindings = bindings
}

type ToolEntryRegistration struct {
	ToolType    string
	ConnectorID string
	Name        string
	Status      string
	Error       string
	ToolCount   int
	ToolNames   []string
}

func (a *Agent) SetToolRegistrations(regs []ToolEntryRegistration) {
	a.toolRegistrationsMu.Lock()
	defer a.toolRegistrationsMu.Unlock()
	a.toolRegistrations = regs
}

func (a *Agent) ReplaceToolRegistration(index int, reg ToolEntryRegistration) {
	a.toolRegistrationsMu.Lock()
	defer a.toolRegistrationsMu.Unlock()
	if index < 0 || index >= len(a.toolRegistrations) {
		return
	}
	previous := a.toolRegistrations[index]
	a.toolRegistrations[index] = reg
	if previous.Status != toolEntryStatusFailed || reg.Status == toolEntryStatusFailed {
		return
	}
	id := toolSourceHealthID(index, previous)
	for i, other := range a.toolRegistrations {
		if other.Status == toolEntryStatusFailed && toolSourceHealthID(i, other) == id {
			return
		}
	}
	a.health.Clear(id)
}

const (
	toolSourceHealthPrefix = "toolSource:"
	toolEntryStatusFailed  = "failed"
)

func (a *Agent) reportFailedToolRegistrations() {
	a.toolRegistrationsMu.RLock()
	defer a.toolRegistrationsMu.RUnlock()
	for i, reg := range a.toolRegistrations {
		if reg.Status != toolEntryStatusFailed {
			continue
		}
		a.health.Set(toolSourceHealthID(i, reg), health.Fatal, health.DetailToolSourceUnavailable)
	}
}

func toolSourceHealthID(index int, reg ToolEntryRegistration) string {
	if reg.ConnectorID != "" {
		return toolSourceHealthPrefix + reg.ConnectorID
	}
	return fmt.Sprintf("%s%s:%d", toolSourceHealthPrefix, reg.ToolType, index)
}

type ToolProvenance struct {
	Category          string
	ConnectorCategory string
	BuiltinCategory   string
}

func (a *Agent) RecordToolProvenance(toolName string, provenance ToolProvenance) {
	a.toolProvenanceMu.Lock()
	defer a.toolProvenanceMu.Unlock()
	if a.toolProvenance == nil {
		a.toolProvenance = make(map[string]ToolProvenance)
	}
	a.toolProvenance[toolName] = provenance
}

func (a *Agent) ToolProvenanceFor(toolName string) (ToolProvenance, bool) {
	a.toolProvenanceMu.RLock()
	defer a.toolProvenanceMu.RUnlock()
	provenance, classified := a.toolProvenance[toolName]
	return provenance, classified
}

func (a *Agent) SetNetworkPolicies(policies []NetworkPolicy) {
	a.cfg.NetworkPolicies = policies
}

func (a *Agent) SetHILDefaultTimeout(d time.Duration) {
	a.cfg.HILDefaultTimeout = d
}

func (a *Agent) SetAuthPendingTimeout(d time.Duration) {
	a.cfg.AuthPendingTimeout = d
}

func (a *Agent) authPendingTimeout() time.Duration {
	return cmp.Or(a.cfg.AuthPendingTimeout, defaultAuthPendingTimeout)
}

func (a *Agent) SetAuthMaxAttempts(n int) {
	a.cfg.AuthMaxAttempts = n
}

func (a *Agent) authMaxAttempts() int {
	return cmp.Or(a.cfg.AuthMaxAttempts, defaultAuthMaxAttempts)
}

func authFlightKey(userID, traceID, credentialKey string) string {
	return userID + "\x00" + traceID + "\x00" + credentialKey
}

type authFailureState struct {
	count   int
	updated time.Time
}

const authFailureTTL = time.Hour

func (a *Agent) recordAuthFailure(userID, traceID, credentialKey string) int {
	key := authFlightKey(userID, traceID, credentialKey)
	now := time.Now()
	a.authInFlightMu.Lock()
	defer a.authInFlightMu.Unlock()
	for k, st := range a.authFailures {
		if k != key && now.Sub(st.updated) > authFailureTTL {
			delete(a.authFailures, k)
		}
	}
	st := a.authFailures[key]
	if st == nil {
		st = &authFailureState{}
		a.authFailures[key] = st
	}
	st.count++
	st.updated = now
	return st.count
}

func (a *Agent) authGaveUp(userID, traceID, credentialKey string) bool {
	key := authFlightKey(userID, traceID, credentialKey)
	a.authInFlightMu.Lock()
	defer a.authInFlightMu.Unlock()
	st := a.authFailures[key]
	return st != nil && st.count >= a.authMaxAttempts()
}

func (a *Agent) clearAuthFailures(userID, traceID, credentialKey string) {
	key := authFlightKey(userID, traceID, credentialKey)
	a.authInFlightMu.Lock()
	delete(a.authFailures, key)
	a.authInFlightMu.Unlock()
}

func (a *Agent) beginAuthFlow(userID, traceID, credentialKey string) bool {
	key := authFlightKey(userID, traceID, credentialKey)
	a.authInFlightMu.Lock()
	defer a.authInFlightMu.Unlock()
	if started, ok := a.authInFlight[key]; ok && time.Since(started) < a.authPendingTimeout() {
		return false
	}
	a.authInFlight[key] = time.Now()
	return true
}

func (a *Agent) endAuthFlow(userID, traceID, credentialKey string) {
	key := authFlightKey(userID, traceID, credentialKey)
	a.authInFlightMu.Lock()
	delete(a.authInFlight, key)
	a.authInFlightMu.Unlock()
}

func (a *Agent) Init(_ context.Context, _ config.Config, svc runtime.Services) error {
	a.svc = svc
	a.logger = svc.Logger().With("logger_name", "agent", "agentName", a.cfg.Name)
	a.tools.SetLogger(a.logger)
	a.checkpoints = newTaskCheckpointer(svc, a.cfg.Name, a.delegationBackstops(), a.logger)
	if a.checkpoints != nil {
		a.logger.Info("durable task resume enabled")
	}

	h, err := structured.NewHandler(structured.HandlerConfig{
		InputSchema:  a.cfg.InputSchema,
		OutputSchema: a.cfg.OutputSchema,
		MaxRetries:   a.cfg.ValidationMaxRetries,
		Logger:       a.logger,
	})
	if err != nil {
		return fmt.Errorf("structured invocation handler: %w", err)
	}
	a.structuredHandler = h

	a.peerRouter = NewPeerRouter(a.svc.Broker(), a.cfg.Namespace, a.logger).WithSeqGen(a.seqGen)
	if a.checkpoints != nil {
		a.orphanAdoptions = make(chan orphanAdoption, orphanAdoptionQueueDepth)
		a.orphanAnswers = make(chan orphanAdoption, orphanAnswerQueueDepth)
		a.peerRouter.WithOrphanHandler(a.resumeOrphanPeerResponse)
		a.peerRouter.WithOrphanStatusHandler(a.noteOrphanPeerStatus)
	}

	a.tools.SetAuthorizer(a.svc.ToolAuthorizer())

	if err := a.tools.Register(&subTaskTool{agent: a}); err != nil {
		return fmt.Errorf("register sub_task tool: %w", err)
	}

	if a.svc.Sessions() != nil {
		a.RegisterExtensionMethod(sessioncontrol.URI, sessioncontrol.MethodCompact,
			sessioncontrol.HandlerFor(a))
	}

	return nil
}

func (a *Agent) internalLLMCall(ctx context.Context, sinks internalCallSinks, model, systemPrompt, userPrompt string, temperature float64) (string, error) {
	if model == "" {
		model = a.cfg.Model
	}
	req := &llm.ChatRequest{
		Model: model,
		Messages: []llm.ChatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
		Temperature: &temperature,
	}
	return a.runInternalLLMCall(ctx, sinks, req, "internal LLM call failed")
}

type internalCallSinks struct {
	sp          *StatusPublisher
	attribution *llm.SpendAttribution
	usage       *toolLLMUsage
}

func (a *Agent) runInternalLLMCall(ctx context.Context, sinks internalCallSinks, req *llm.ChatRequest, errPrefix string) (string, error) {
	requestedModel := req.Model
	client := a.internalCallClient(ctx, req, sinks.attribution)

	if req.Observability.ComponentName == "" {
		req.Observability.ComponentName = "agent-" + agentDisplayName(&a.cfg)
	}

	reportModel := a.internalCallReportModel(ctx, requestedModel)
	a.tempPolicy.Apply(req, reportModel)
	if sinks.sp != nil {
		sinks.sp.PublishLLMInvocation(ctx, req, reportModel)
	}
	resp, err := llm.ChatCompletionWithTemperatureFallback(
		ctx, client, req, &a.tempPolicy, reportModel, a.logger)
	if err != nil {
		return "", fmt.Errorf("%s: %w", errPrefix, err)
	}
	if sinks.usage != nil && resp.Usage != nil {
		sinks.usage.record(reportModel, *resp.Usage)
	}
	if sinks.sp != nil {
		sinks.sp.PublishLLMResponse(ctx, reportModel, resp.Content, resp.Usage)
	}
	return resp.Content, nil
}

func (a *Agent) internalCallReportModel(ctx context.Context, model string) string {
	if model == "" || model == a.cfg.Model {
		return a.reportModelCtx(ctx)
	}
	return model
}

func (a *Agent) wireInternalLLMCallbacks(toolCtx *tool.Context, sp *StatusPublisher, state *TaskExecutionState, spendTemplate *llm.SpendAttribution) {
	a.installInternalLLMCallbacks(toolCtx, sp, state, spendTemplate)
	toolCtx.BindToolCall = func(callCtx *tool.Context, toolName string) {
		attribution := *spendTemplate
		attribution.ToolName = toolName
		callCtx.RemoteSpend = RemoteToolSpend(&attribution)
		a.installInternalLLMCallbacks(callCtx, sp, state, &attribution)
	}
}

func (a *Agent) installInternalLLMCallbacks(toolCtx *tool.Context, sp *StatusPublisher, state *TaskExecutionState, attribution *llm.SpendAttribution) {
	sinks := internalCallSinks{sp: sp, attribution: attribution, usage: &state.toolLLMUsage}
	toolCtx.InternalLLMCall = func(innerCtx context.Context, systemPrompt, userPrompt string, temperature float64) (string, error) {
		return a.internalLLMCall(innerCtx, sinks, "", systemPrompt, userPrompt, temperature)
	}
	toolCtx.InternalLLMCallWithModel = func(innerCtx context.Context, model, systemPrompt, userPrompt string, temperature float64) (string, error) {
		state.usageFacts.recordToolModel(a.toolSelectedModel(model))
		return a.internalLLMCall(innerCtx, sinks, model, systemPrompt, userPrompt, temperature)
	}
	toolCtx.InternalLLMCallMultimodal = func(innerCtx context.Context, systemPrompt string, parts []tool.MultimodalPart, opts tool.InternalLLMCallMultimodalOpts) (string, error) {
		state.usageFacts.recordToolModel(a.toolSelectedModel(opts.Model))
		return a.internalLLMCallMultimodal(innerCtx, sinks, systemPrompt, parts, opts)
	}
}

func (a *Agent) internalCallClient(ctx context.Context, req *llm.ChatRequest, attribution *llm.SpendAttribution) llm.Client {
	if req.Model == a.cfg.Model {
		key, configID := a.agentModelRef(ctx)
		req.Spend = internalCallSpend(attribution, key, configID)
		return a.llmClient(ctx)
	}
	if client, raw, ok := a.boundToolModel(ctx, req.Model); ok {
		req.Spend = internalCallSpend(attribution, req.Model, samapp.ModelConfigID(raw))
		boundModel, _ := raw["model"].(string)
		a.logger.Debug("tool-named model routed to its binding", "alias", req.Model, "boundModel", boundModel)
		req.Model = ""
		return a.meteredClient(client, boundModel)
	}
	req.Spend = internalCallSpend(attribution, "", "")
	return a.meteredClient(a.svc.LLM(req.Model), req.Model)
}

func (a *Agent) boundToolModel(ctx context.Context, name string) (llm.Client, map[string]any, bool) {
	reg := a.svc.Bindings()
	if reg == nil || name == "" {
		return nil, nil, false
	}
	client, err := reg.ModelClient(name)
	if err != nil {
		return nil, nil, false
	}
	raw, err := reg.ResolveRawOrFetch(ctx, name)
	if err != nil {
		a.warnModelConfigIDUnavailable(ctx, name, err)
		return client, nil, true
	}
	return client, raw, true
}

func internalCallSpend(base *llm.SpendAttribution, key, configID string) *llm.SpendAttribution {
	if base == nil {
		return nil
	}
	attribution := *base
	attribution.ModelKey = key
	attribution.ModelConfigID = configID
	return &attribution
}

func (a *Agent) toolSelectedModel(model string) string {
	if model == "" || model == a.cfg.Model {
		return ""
	}
	return model
}

func (a *Agent) internalLLMCallMultimodal(ctx context.Context, sinks internalCallSinks, systemPrompt string, parts []tool.MultimodalPart, opts tool.InternalLLMCallMultimodalOpts) (string, error) {
	blocks := make([]llm.ContentBlock, 0, len(parts))
	for _, p := range parts {
		switch v := p.(type) {
		case tool.TextPart:
			if v.Text == "" {
				continue
			}
			blocks = append(blocks, llm.ContentBlock{Type: "text", Text: v.Text})
		case tool.ImagePart:
			if len(v.Data) == 0 {
				continue
			}
			mime := v.MIMEType
			if mime == "" {
				mime = mimeOctetStream
			}
			dataURL := visionimage.DataURL(mime, v.Data)
			blocks = append(blocks, llm.ContentBlock{Type: "image_url", ImageURL: dataURL})
			if v.Description != "" {
				blocks = append(blocks, llm.ContentBlock{Type: "text", Text: v.Description})
			}
		case tool.AudioPart:
			if len(v.Data) == 0 {
				continue
			}
			blocks = append(blocks, llm.ContentBlock{
				Type:        "input_audio",
				AudioData:   base64.StdEncoding.EncodeToString(v.Data),
				AudioFormat: v.Format,
			})
			if v.Description != "" {
				blocks = append(blocks, llm.ContentBlock{Type: "text", Text: v.Description})
			}
		default:
			return "", fmt.Errorf("unsupported multimodal part type: %T", p)
		}
	}
	if len(blocks) == 0 {
		return "", fmt.Errorf("multimodal LLM call: no content parts provided")
	}

	model := opts.Model
	if model == "" {
		model = a.cfg.Model
	}

	req := &llm.ChatRequest{
		Model: model,
		Messages: []llm.ChatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", ContentBlocks: blocks},
		},
		Temperature: opts.Temperature,
	}
	return a.runInternalLLMCall(ctx, sinks, req, "internal multimodal LLM call failed")
}

func (a *Agent) llmClient(ctx context.Context) llm.Client {
	return a.meteredClient(a.resolveLLMClient(ctx), a.boundModelName())
}

func (a *Agent) meteredClient(client llm.Client, model string) llm.Client {
	return spend.NewMeteredClient(client, model, a.spendPublisher, a.svc.BudgetGate())
}

func (a *Agent) resolveLLMClient(ctx context.Context) llm.Client {
	if a.cfg.ModelAlias != "" {
		if reg := a.svc.Bindings(); reg != nil {
			c, err := reg.ResolveOrFetch(ctx, a.cfg.ModelAlias)
			if err == nil {
				a.refreshResolvedModelName(ctx, reg)
				return c
			}
			a.logger.Error("model binding unavailable at LLM-call time",
				"alias", a.cfg.ModelAlias, "error", err)
			return unboundLLMClient{alias: a.cfg.ModelAlias, err: err}
		}
	}
	return a.svc.LLM(a.cfg.Model)
}

func (a *Agent) boundModelName() string {
	if a.cfg.Model != "" {
		return a.cfg.Model
	}
	if v := a.resolvedModelName.Load(); v != nil {
		return *v
	}
	return ""
}

func (a *Agent) SetSpendPublisher(p *spend.Publisher) {
	a.spendPublisher = p
}

func (a *Agent) refreshResolvedModelName(ctx context.Context, reg runtime.ModelBindings) {
	raw, err := reg.ResolveRawOrFetch(ctx, a.cfg.ModelAlias)
	if err != nil || raw == nil {
		return
	}
	name, _ := raw["model"].(string)
	if name == "" {
		return
	}
	a.resolvedModelName.Store(&name)
	base, _ := raw["api_base"].(string)
	a.resolvedModelAPIBase.Store(&base)
}

func (a *Agent) reportedModelAPIBase() string {
	if a.cfg.OriginalModel != "" || a.cfg.Model != "" {
		return a.cfg.ModelAPIBase
	}
	if v := a.resolvedModelAPIBase.Load(); v != nil {
		return *v
	}
	return ""
}

func (a *Agent) resolveMaxInputTokens(ctx context.Context, modelID string) (int, bool) {
	if a.cfg.ModelAlias != "" {
		if reg := a.svc.Bindings(); reg != nil {
			raw, err := reg.ResolveRawOrFetch(ctx, a.cfg.ModelAlias)
			if err == nil && raw != nil {
				if v, ok := readIntValue(raw["max_input_tokens"]); ok && v > 0 {
					return v, true
				}
			}
		}
	}
	if v, ok := modelinfo.MaxInputTokens(modelID); ok {
		return v, true
	}
	return 0, false
}

func (a *Agent) buildByModelUsage(ctx context.Context, state *TaskExecutionState) map[string]any {
	byModel := make(map[string]llm.Usage, len(state.UsageByModel)+1)
	var attributed llm.Usage
	for model, u := range state.UsageByModel {
		byModel[model] = u
		AddUsage(&attributed, u)
	}
	if rest := usageRemainder(state.TotalUsage, attributed); UsageRecorded(rest) || len(byModel) == 0 {
		own := a.reportModel()
		u := byModel[own]
		AddUsage(&u, rest)
		byModel[own] = u
	}
	out := make(map[string]any, len(byModel))
	for model, u := range byModel {
		entry := ByModelUsageEntry(u)
		if maxIn, ok := a.byModelMaxInput(ctx, state, model); ok {
			entry["max_input_tokens"] = maxIn
		}
		if servedOnlyTools(u, state.ToolUsageByModel[model]) {
			entry["kind"] = byModelKindTool
		}
		out[model] = entry
	}
	return out
}

func ByModelUsageEntry(u llm.Usage) map[string]any {
	return map[string]any{
		"input_tokens":        u.PromptTokens,
		"output_tokens":       u.CompletionTokens,
		"cached_input_tokens": u.CachedTokens,
		"cache_write_tokens":  u.CacheWriteTokens,
		"reasoning_tokens":    u.ReasoningTokens,
	}
}

const byModelKindTool = "tool"

func servedOnlyTools(entry, tool llm.Usage) bool {
	return UsageRecorded(tool) && !UsageRecorded(usageRemainder(entry, tool))
}

func (a *Agent) byModelMaxInput(ctx context.Context, state *TaskExecutionState, model string) (int, bool) {
	switch {
	case model == a.reportModel():
		return a.resolveMaxInputTokens(ctx, model)
	case state.ModelOverride != nil && model == state.ModelOverrideReportModel && state.ModelOverrideMaxInputTokens > 0:
		return state.ModelOverrideMaxInputTokens, true
	}
	return modelinfo.MaxInputTokens(model)
}

func AddUsage(dst *llm.Usage, u llm.Usage) {
	dst.PromptTokens += u.PromptTokens
	dst.CompletionTokens += u.CompletionTokens
	dst.TotalTokens += u.TotalTokens
	dst.CachedTokens += u.CachedTokens
	dst.CacheWriteTokens += u.CacheWriteTokens
	dst.ReasoningTokens += u.ReasoningTokens
}

func usageRemainder(total, part llm.Usage) llm.Usage {
	return llm.Usage{
		PromptTokens:     max(total.PromptTokens-part.PromptTokens, 0),
		CompletionTokens: max(total.CompletionTokens-part.CompletionTokens, 0),
		TotalTokens:      max(total.TotalTokens-part.TotalTokens, 0),
		CachedTokens:     max(total.CachedTokens-part.CachedTokens, 0),
		CacheWriteTokens: max(total.CacheWriteTokens-part.CacheWriteTokens, 0),
		ReasoningTokens:  max(total.ReasoningTokens-part.ReasoningTokens, 0),
	}
}

func (a *Agent) callReportModel(ctx context.Context, state *TaskExecutionState) string {
	if state != nil && state.ModelOverride != nil {
		return cmp.Or(state.ModelOverrideReportModel, state.ModelOverride.Model)
	}
	return a.reportModelCtx(ctx)
}

func UsageRecorded(u llm.Usage) bool {
	return u.TotalTokens > 0 || u.PromptTokens > 0 || u.CompletionTokens > 0 ||
		u.CachedTokens > 0 || u.CacheWriteTokens > 0 || u.ReasoningTokens > 0
}

func TokenUsageTotals(u llm.Usage) map[string]any {
	total := u.PromptTokens + u.CompletionTokens
	if u.TotalTokens > total {
		total = u.TotalTokens
	}
	return map[string]any{
		"total_input_tokens":        u.PromptTokens,
		"total_output_tokens":       u.CompletionTokens,
		"total_cached_input_tokens": u.CachedTokens,
		"total_cache_write_tokens":  u.CacheWriteTokens,
		"total_reasoning_tokens":    u.ReasoningTokens,
		"total_tokens":              total,
		"by_source": map[string]any{
			"agent": map[string]any{
				"input_tokens":        u.PromptTokens,
				"output_tokens":       u.CompletionTokens,
				"cached_input_tokens": u.CachedTokens,
				"cache_write_tokens":  u.CacheWriteTokens,
				"reasoning_tokens":    u.ReasoningTokens,
			},
		},
	}
}

func readIntValue(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), true
	case int:
		return n, true
	case int64:
		return int(n), true
	case json.Number:
		i, err := n.Int64()
		if err != nil {
			return 0, false
		}
		return int(i), true
	default:
		return 0, false
	}
}

type unboundLLMClient struct {
	alias string
	err   error
}

func (u unboundLLMClient) ChatCompletion(_ context.Context, _ *llm.ChatRequest) (*llm.ChatResponse, error) {
	return nil, fmt.Errorf("model alias %q unbound: %w", u.alias, u.err)
}

func (u unboundLLMClient) ChatCompletionStream(_ context.Context, _ *llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	return nil, fmt.Errorf("model alias %q unbound: %w", u.alias, u.err)
}

func (a *Agent) Start(ctx context.Context) error {
	a.stopping.Store(false)
	a.exitingProcess.Store(false)
	a.handingOffPausedTasks.Store(false)
	a.removing.Store(false)

	a.health.Reset()
	a.reportFailedToolRegistrations()

	ctx, cancel := context.WithCancel(ctx)
	a.ctx = ctx
	a.cancelFn = cancel
	a.runCtx = ctx

	brk := a.svc.Broker()

	requestQueueName := a2a.QueueName(a.cfg.Namespace, "a2a", a.cfg.Name)
	var maxRedelivery uint
	if a.cfg.MaxMessageRedelivery > 0 {
		maxRedelivery = uint(a.cfg.MaxMessageRedelivery)
	}
	rq, err := brk.CreateQueue(requestQueueName, broker.QueueOptions{
		Named:         true,
		MaxRedelivery: maxRedelivery,
		RespectTTL:    a.cfg.RespectMessageTTL,
	})
	if err != nil {
		cancel()
		return fmt.Errorf("create request queue: %w", err)
	}
	a.requestQueue = rq

	dq, err := brk.CreateDirectSubscription()
	if err != nil {
		_ = brk.DeleteQueue(requestQueueName)
		cancel()
		return fmt.Errorf("create direct subscription: %w", err)
	}
	a.queue = dq

	cleanupQueues := func() {
		_ = brk.DeleteQueue(requestQueueName)
		_ = brk.DeleteQueue(dq.Name())
		a.requestQueue = nil
		a.queue = nil
		cancel()
	}

	topic := a2a.AgentRequestTopic(a.cfg.Namespace, a.cfg.Name)
	if err := brk.Subscribe(rq, topic); err != nil {
		cleanupQueues()
		return fmt.Errorf("subscribe request topic: %w", err)
	}

	responseSub := a2a.PeerResponseSubscription(a.cfg.Namespace, a.cfg.Name)
	if err := brk.Subscribe(dq, responseSub); err != nil {
		cleanupQueues()
		return fmt.Errorf("subscribe peer response: %w", err)
	}

	statusSub := a2a.PeerStatusSubscription(a.cfg.Namespace, a.cfg.Name)
	if err := brk.Subscribe(dq, statusSub); err != nil {
		cleanupQueues()
		return fmt.Errorf("subscribe peer status: %w", err)
	}

	if err := brk.Subscribe(dq, a.forwardTopic()); err != nil {
		cleanupQueues()
		return fmt.Errorf("subscribe forwarded control messages: %w", err)
	}

	deepResearchEventsSub := a2a.SamEventsSubscription(a.cfg.Namespace, "deep_research")
	if err := brk.Subscribe(dq, deepResearchEventsSub); err != nil {
		cleanupQueues()
		return fmt.Errorf("subscribe deep_research events: %w", err)
	}

	if a.notifyHILEnabled() || a.checkpoints != nil {
		notifyRespSub := a2a.NotifyResponseSubscription(a.cfg.Namespace, a.cfg.Name)
		if err := brk.Subscribe(dq, notifyRespSub); err != nil {
			cleanupQueues()
			return fmt.Errorf("subscribe notify HIL responses: %w", err)
		}
		if a.checkpoints != nil {
			if err := brk.Subscribe(rq, notifyRespSub); err != nil {
				cleanupQueues()
				return fmt.Errorf("subscribe notify HIL responses on the request queue: %w", err)
			}
		}
	}

	if a.cfg.EnableInlineVision {
		a.logger.Info("inline vision enabled: image files will be passed directly to the LLM",
			"maxImages", a.cfg.MaxInlineVisionImages,
			"max_bytes", a.cfg.MaxInlineVisionBytes,
		)
	}

	if a.cfg.EnableInlinePDF {
		a.logger.Info("inline PDF enabled: image-based PDF files will be passed directly to the LLM",
			"maxCount", a.cfg.MaxInlinePDFCount,
			"maxPages", a.cfg.MaxInlinePDFPages,
			"max_bytes", a.cfg.MaxInlinePDFBytes,
		)
	}

	a.startCardPublisher(ctx)

	a.startPeerDiscovery()

	if withheld := withheldPromptSections(&a.cfg); len(withheld) > 0 {
		a.logger.Info("prompt sections withheld by config",
			"withheld", withheld,
			"headless", a.cfg.Headless != nil && *a.cfg.Headless)
	}

	if a.checkpoints != nil {
		a.installRemoteToolOrphanHandlers()
	}

	a.subComponentsMu.Lock()
	a.subComponentsStopped = false
	a.subComponentsMu.Unlock()
	for _, sc := range a.subComponentsSnapshot() {
		if err := sc.Start(ctx); err != nil {
			cleanupQueues()
			a.stopPeerDiscovery()
			return fmt.Errorf("start sub-component: %w", err)
		}
	}

	intakeCtx, stopIntake := context.WithCancel(ctx)
	a.stopRequestIntake = stopIntake
	a.requestIntakeDone = make(chan struct{})
	requestQueue, intakeDone := a.requestQueue, a.requestIntakeDone
	a.wg.Go(func() {
		defer close(intakeDone)
		a.receiveLoop(intakeCtx, ctx, requestQueue, true)
	})
	a.wg.Go(func() {
		a.receiveLoop(ctx, ctx, a.queue, false)
	})
	a.wg.Go(func() {
		a.maintainRunningTasks(ctx)
	})
	if a.checkpoints != nil {
		a.wg.Go(func() {
			a.maintainCheckpoints(ctx)
		})
		for range orphanAdoptionWorkers {
			a.wg.Go(func() {
				a.runOrphanAdoptions(ctx)
			})
		}
	}
	return nil
}

func (a *Agent) Stop(ctx context.Context) error {
	a.publishRemovalCard(ctx)

	a.stopping.Store(true)
	if a.requestQueue != nil {
		_ = a.svc.Broker().DeleteQueue(a.requestQueue.Name())
	}

	drainCtx, cancelDrain := context.WithTimeout(ctx, a.controlDrainBudget())
	if remaining := a.drainActiveTasks(drainCtx); remaining > 0 {
		a.logger.Warn("shutdown deadline reached with tasks still in-flight", "remaining", remaining)
	}
	cancelDrain()

	a.flushPausedTasksOnShutdown(ctx)

	if a.peerRouter != nil {
		a.peerRouter.CancelAllWithPropagation(a.cfg.Namespace)
	}
	a.teardown()
	return nil
}

func (a *Agent) StopForProcessExit(ctx context.Context) error {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, a.controlDrainBudget())
		defer cancel()
	}
	a.stopping.Store(true)
	a.exitingProcess.Store(true)
	a.pauseRequestIntake(ctx)
	remaining := a.drainActiveTasks(ctx)
	a.releasePausedTasks(ctx)
	if remaining > 0 {
		a.logger.Warn("shutdown deadline reached; leaving running tasks unacknowledged for redelivery",
			"remaining", remaining)
		return nil
	}
	a.teardown()
	return nil
}

func (a *Agent) StopForReplace(ctx context.Context) error {
	a.publishRemovalCard(ctx)
	a.stopping.Store(true)
	a.exitingProcess.Store(true)
	drainCtx, cancelDrain := context.WithTimeout(ctx, a.controlDrainBudget())
	a.pauseRequestIntake(drainCtx)
	if remaining := a.drainActiveTasks(drainCtx); remaining > 0 {
		a.logger.Warn("replace deadline reached; leaving running tasks unacknowledged for the new copy",
			"remaining", remaining)
	}
	cancelDrain()
	a.releasePausedTasks(ctx)
	a.teardown()
	return nil
}

func (a *Agent) pauseRequestIntake(ctx context.Context) {
	if a.stopRequestIntake != nil {
		a.stopRequestIntake()
	}
	if a.requestIntakeDone == nil {
		return
	}
	select {
	case <-a.requestIntakeDone:
	case <-ctx.Done():
	}
}

const pausedTaskReleaseTimeout = 2 * time.Second

func (a *Agent) releasePausedTasks(ctx context.Context) {
	releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), pausedTaskReleaseTimeout)
	defer cancel()
	a.flushPausedTasksOnShutdown(releaseCtx)
}

func (a *Agent) teardown() {
	a.subComponentsMu.Lock()
	a.subComponentsStopped = true
	subComponents := slices.Clone(a.subComponents)
	a.subComponentsMu.Unlock()
	for _, sc := range subComponents {
		sc.Stop()
	}
	if a.cancelFn != nil {
		a.cancelFn()
	}
	a.wg.Wait()

	brk := a.svc.Broker()
	if a.requestQueue != nil {
		if err := brk.DeleteQueue(a.requestQueue.Name()); err != nil {
			a.logger.Warn("delete request queue failed", "queue", a.requestQueue.Name(), "error", err)
		}
		a.requestQueue = nil
	}
	if a.queue != nil {
		if err := brk.DeleteQueue(a.queue.Name()); err != nil {
			a.logger.Warn("terminate peer-direct subscription failed", "handle", a.queue.Name(), "error", err)
		}
		a.queue = nil
	}
	a.stopPeerDiscovery()
}

func (a *Agent) controlDrainBudget() time.Duration {
	if a.drainTimeout <= 0 {
		return defaultDrainTimeout
	}
	return a.drainTimeout
}

func (a *Agent) Remove(ctx context.Context) error {
	requestQueueName := ""
	if a.requestQueue != nil {
		requestQueueName = a.requestQueue.Name()
	} else {
		requestQueueName = a2a.QueueName(a.cfg.Namespace, "a2a", a.cfg.Name)
	}

	a.removing.Store(true)
	stopErr := a.Stop(ctx)

	var deprovisionErr error
	if a.svc != nil {
		if brk := a.svc.Broker(); brk != nil && requestQueueName != "" {
			if err := brk.DeprovisionQueue(requestQueueName); err != nil {
				a.logger.Warn("deprovision request queue failed",
					"queue", requestQueueName, "error", err)
				deprovisionErr = fmt.Errorf("deprovision request queue %q: %w", requestQueueName, err)
			}
		}
	}
	return errors.Join(stopErr, deprovisionErr)
}

const defaultDrainTimeout = 20 * time.Second

func (a *Agent) drainActiveTasks(ctx context.Context) int {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		remaining := a.inFlightCount()
		if remaining == 0 {
			return 0
		}
		a.logger.Debug("draining in-flight tasks before shutdown", "remaining", remaining)
		select {
		case <-ctx.Done():
			return remaining
		case <-ticker.C:
		}
	}
}

func (a *Agent) inFlightCount() int {
	waiting := a.tasksWaitingOnAPerson()
	a.activeTasksMu.Lock()
	defer a.activeTasksMu.Unlock()
	running := map[string]bool{}
	for taskID := range a.activeTasks {
		running[taskID] = !waiting[taskID]
	}
	for taskID := range a.requestsInProcess {
		running[taskID] = !waiting[taskID]
	}
	n := len(a.controlEntries)
	for _, counted := range running {
		if counted {
			n++
		}
	}
	return n
}

func (a *Agent) beginRequest(taskID string) {
	a.activeTasksMu.Lock()
	a.requestsInProcess[taskID]++
	a.activeTasksMu.Unlock()
}

func (a *Agent) endRequest(taskID string) {
	a.activeTasksMu.Lock()
	defer a.activeTasksMu.Unlock()
	if a.requestsInProcess[taskID] <= 1 {
		delete(a.requestsInProcess, taskID)
		return
	}
	a.requestsInProcess[taskID]--
}

func (a *Agent) tasksWaitingOnAPerson() map[string]bool {
	a.pendingHILMu.Lock()
	defer a.pendingHILMu.Unlock()
	waiting := make(map[string]bool, len(a.pendingHIL))
	for _, p := range a.pendingHIL {
		if p.taskID != "" {
			waiting[p.taskID] = true
		}
	}
	return waiting
}

func (a *Agent) Health() error {
	if a.ctx == nil {
		return fmt.Errorf("agent %q not started", a.cfg.Name)
	}
	if err := a.ctx.Err(); err != nil {
		return fmt.Errorf("agent %q receive loop stopped: %w", a.cfg.Name, err)
	}
	if !a.stopping.Load() && a.requestQueueGone() {
		return fmt.Errorf("agent %q request receive loop exited: %w", a.cfg.Name, runtime.ErrRestartRequired)
	}
	return nil
}

func effectOnCard(c tool.EffectClass) string {
	if c.ReadOnly() {
		return string(tool.EffectReadOnly)
	}
	return string(tool.EffectSideEffecting)
}

const deploymentIDWireKey = "deployment_id"

func (a *Agent) BuildAgentCard() a2a.AgentCard {
	a.syncPeers()
	in := CardInputs{
		ToolRegistrations: a.toolRegistrationsExtension(),
		SessionControl:    a.extensions.SupportsURI(sessioncontrol.URI),
		PeerNames:         a.peerNames(),
		Health:            append(a.health.Snapshot(), a.peerDiscoveryProblems()...),
	}
	for _, td := range a.tools.ToLLMDefinitions() {
		effect, _ := a.tools.EffectClassFor(td.Name)
		in.Tools = append(in.Tools, CardTool{Name: td.Name, Description: td.Description, Effect: effect})
	}
	if a.skillManager != nil {
		in.LoadableSkills = a.skillManager.AvailableSkillsForCard()
	}
	return BuildCard(&a.cfg, in)
}

type CardInputs struct {
	Tools             []CardTool
	ToolRegistrations *a2a.AgentExtension
	SessionControl    bool
	PeerNames         []string
	LoadableSkills    []AvailableSkill
	Health            []health.Problem
}

type CardTool struct {
	Name           string
	Description    string
	RequiredScopes []string
	Effect         tool.EffectClass
}

func scopesOrEmpty(scopes []string) []string {
	if scopes == nil {
		return []string{}
	}
	return scopes
}

func BuildCard(cfg *AgentConfig, in CardInputs) a2a.AgentCard {
	requestTopic := a2a.AgentRequestTopic(cfg.Namespace, cfg.Name)

	version := cfg.Version
	if version == "" {
		version = "1.0.0"
	}

	var extensions []a2a.AgentExtension
	if len(in.Tools) > 0 {
		toolManifest := make([]any, len(in.Tools))
		for i, td := range in.Tools {
			effect := td.Effect
			if effect.Source == "" {
				effect = tool.DefaultEffectClass
			}
			toolManifest[i] = map[string]any{
				"id":              td.Name,
				"name":            td.Name,
				"description":     td.Description,
				"requiredScopes":  scopesOrEmpty(td.RequiredScopes),
				"required_scopes": scopesOrEmpty(td.RequiredScopes),
				"effect":          effectOnCard(effect),
				"effect_source":   string(effect.Source),
			}
		}
		extensions = append(extensions, a2a.AgentExtension{
			URI: "https://solace.com/a2a/extensions/sam/tools",
			Params: map[string]any{
				"tools": toolManifest,
			},
		})
	}

	if in.ToolRegistrations != nil {
		extensions = append(extensions, *in.ToolRegistrations)
	}

	agentType := cfg.AgentType
	if agentType == "" {
		agentType = "standard"
	}
	extensions = append(extensions, a2a.AgentExtension{
		URI: "https://solace.com/a2a/extensions/agent-type",
		Params: map[string]any{
			"type": agentType,
		},
	})

	if cfg.InputSchema != nil || cfg.OutputSchema != nil {
		schemaParams := map[string]any{}
		if cfg.InputSchema != nil {
			schemaParams["input_schema"] = cfg.InputSchema
		}
		if cfg.OutputSchema != nil {
			schemaParams["output_schema"] = cfg.OutputSchema
		}
		extensions = append(extensions, a2a.AgentExtension{
			URI:    a2a.ExtensionURISchemas,
			Params: schemaParams,
		})
	}

	if len(cfg.RequiredScopes) > 0 {
		extensions = append(extensions, a2a.AgentExtension{
			URI: a2a.ExtensionURIRequiredScopes,
			Params: map[string]any{
				"scopes": cfg.RequiredScopes,
			},
		})
	}

	if cfg.DisplayName != "" {
		extensions = append(extensions, a2a.AgentExtension{
			URI: ExtensionURIDisplayName,
			Params: map[string]any{
				"display_name": cfg.DisplayName,
			},
		})
	}

	if cfg.Internal {
		params := map[string]any{
			"internal": true,
		}
		if cfg.ShowSessions {
			params["show_sessions"] = true
		}
		extensions = append(extensions, a2a.AgentExtension{
			URI:    ExtensionURIInternal,
			Params: params,
		})
	}

	if cfg.Priority != 0 {
		extensions = append(extensions, a2a.AgentExtension{
			URI: ExtensionURIPriority,
			Params: map[string]any{
				"priority": cfg.Priority,
			},
		})
	}

	if cfg.Ephemeral {
		extensions = append(extensions, a2a.AgentExtension{
			URI: ExtensionURIEphemeral,
			Params: map[string]any{
				"ephemeral": true,
			},
		})
	}

	if in.SessionControl {
		extensions = append(extensions, a2a.AgentExtension{
			URI:         sessioncontrol.URI,
			Description: "Manually compact session history.",
			Required:    false,
		})
	}

	if cfg.DeploymentID != "" {
		extensions = append(extensions, a2a.AgentExtension{
			URI:         ExtensionURIDeployment,
			Description: "SAM deployment tracking for rolling updates",
			Params: map[string]any{
				deploymentIDWireKey: cfg.DeploymentID,
			},
		})
	}

	if len(in.PeerNames) > 0 {
		extensions = append(extensions, a2a.AgentExtension{
			URI:         ExtensionURIPeerAgentTopology,
			Description: "A list of peer agents this agent is configured to communicate with.",
			Params: map[string]any{
				"peer_agent_names": in.PeerNames,
			},
		})
	}

	if cfg.Welcome != nil {
		welcomeParams := map[string]any{}
		if cfg.Welcome.Message != "" {
			welcomeParams["welcome_message"] = cfg.Welcome.Message
		}
		if len(cfg.Welcome.Suggestions) > 0 {
			suggestions := make([]map[string]any, len(cfg.Welcome.Suggestions))
			for i, s := range cfg.Welcome.Suggestions {
				suggestions[i] = map[string]any{
					"label":     s.Label,
					"prompt":    s.Prompt,
					"auto_send": s.AutoSend,
				}
			}
			welcomeParams["suggestions"] = suggestions
		}
		if len(welcomeParams) > 0 {
			extensions = append(extensions, a2a.AgentExtension{
				URI:    ExtensionURIWelcome,
				Params: welcomeParams,
			})
		}
	}

	if avail := in.LoadableSkills; len(avail) > 0 {
		avail = slices.Clone(avail)
		slices.SortFunc(avail, func(x, y AvailableSkill) int {
			return cmp.Compare(x.Name, y.Name)
		})
		skillList := make([]any, len(avail))
		for i, s := range avail {
			skillList[i] = map[string]any{
				"name":        s.Name,
				"description": s.Description,
			}
		}
		extensions = append(extensions, a2a.AgentExtension{
			URI: skillsExtensionURI,
			Params: map[string]any{
				"skills": skillList,
			},
		})
	}

	if len(in.Health) > 0 {
		extensions = append(extensions, a2a.HealthProblemsExtension(in.Health))
	}

	inputModes := cfg.DefaultInputModes
	if len(inputModes) == 0 {
		inputModes = []string{"text"}
	}
	outputModes := cfg.DefaultOutputModes
	if len(outputModes) == 0 {
		outputModes = []string{"text"}
	}

	skills := cfg.Skills
	if skills == nil {
		skills = []a2a.AgentSkill{}
	}

	return a2a.AgentCard{
		Name:            cfg.Name,
		Description:     cfg.Description,
		URL:             "solace:" + requestTopic,
		Version:         version,
		ProtocolVersion: "0.3.0",
		Capabilities: a2a.AgentCapabilities{
			Streaming:              cfg.SupportsStreaming,
			PushNotifications:      false,
			StateTransitionHistory: false,
			Extensions:             extensions,
		},
		DefaultInputModes:  inputModes,
		DefaultOutputModes: outputModes,
		Skills:             skills,
		Provider:           cfg.Provider,
		DocumentationURL:   cfg.DocumentationURL,
	}
}

func (a *Agent) toolRegistrationsExtension() *a2a.AgentExtension {
	a.toolRegistrationsMu.RLock()
	defer a.toolRegistrationsMu.RUnlock()
	if len(a.toolRegistrations) == 0 {
		return nil
	}
	regEntries := make([]any, len(a.toolRegistrations))
	for i, reg := range a.toolRegistrations {
		entry := map[string]any{
			"tool_type":  reg.ToolType,
			"status":     reg.Status,
			"tool_count": reg.ToolCount,
		}
		if reg.ConnectorID != "" {
			entry["connector_id"] = reg.ConnectorID
		}
		if reg.Name != "" {
			entry["name"] = reg.Name
		}
		if len(reg.ToolNames) > 0 {
			names := make([]any, len(reg.ToolNames))
			for j, n := range reg.ToolNames {
				names[j] = n
			}
			entry["tool_names"] = names
		}
		if reg.Error != "" {
			entry["error"] = reg.Error
		}
		regEntries[i] = entry
	}
	return &a2a.AgentExtension{
		URI:         ExtensionURIToolRegistrations,
		Description: "Startup registration status of connector-sourced tool entries.",
		Params: map[string]any{
			"entries": regEntries,
		},
	}
}

func (a *Agent) publishAgentCard(ctx context.Context) error {
	a.cardMu.Lock()
	defer a.cardMu.Unlock()
	if a.cardRetired {
		return nil
	}
	card := a.BuildAgentCard()

	data, err := json.Marshal(card)
	if err != nil {
		return fmt.Errorf("marshal agent card: %w", err)
	}

	topic := a2a.DiscoveryAgentCardsTopic(a.cfg.Namespace)
	if pubErr := a.svc.Broker().Publish(ctx, topic, &broker.Message{
		Payload:        data,
		UserProperties: map[string]string{"instance_id": a.instanceID},
	}); pubErr != nil {
		return fmt.Errorf("publish agent card: %w", pubErr)
	}

	a.logger.Debug("published agent card", "name", card.Name, "topic", topic, "skills", len(card.Skills))
	return nil
}

func (a *Agent) publishRemovalCard(ctx context.Context) {
	a.cardMu.Lock()
	defer a.cardMu.Unlock()
	a.cardRetired = true
	card := a.BuildAgentCard()
	data, err := json.Marshal(card)
	if err != nil {
		a.logger.Warn("failed to marshal agent removal card", "error", err)
		return
	}
	topic := a2a.DiscoveryAgentCardsTopic(a.cfg.Namespace)
	if err := a.svc.Broker().Publish(ctx, topic, &broker.Message{
		Payload:        data,
		UserProperties: map[string]string{"removed": "true", "instance_id": a.instanceID},
	}); err != nil {
		a.logger.Warn("failed to publish agent removal card", "topic", topic, "instanceID", a.instanceID, "error", err)
	} else {
		a.logger.Info("published agent removal card", "topic", topic, "instanceID", a.instanceID)
	}
}

const cardPublishWarmupDuration = 3 * time.Minute
const cardPublishWarmupMaxInterval = 10 * time.Second

func (a *Agent) startCardPublisher(ctx context.Context) {
	a.cardMu.Lock()
	a.cardRetired = false
	a.cardMu.Unlock()
	if err := a.publishAgentCard(ctx); err != nil {
		a.logger.Warn("failed to publish initial agent card", "error", err)
	} else {
		a.logger.Info("published initial agent card", "instanceID", a.instanceID)
	}

	interval := a.cfg.CardPublishIntervalSeconds
	if interval <= 0 {
		return
	}

	normalInterval := time.Duration(interval) * time.Second
	warmupInterval := normalInterval
	if warmupInterval > cardPublishWarmupMaxInterval {
		warmupInterval = cardPublishWarmupMaxInterval
	}

	a.wg.Go(func() {
		warmupDeadline := time.Now().Add(cardPublishWarmupDuration)
		ticker := time.NewTicker(warmupInterval)
		defer ticker.Stop()
		inWarmup := warmupInterval != normalInterval
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if pubErr := a.publishAgentCard(ctx); pubErr != nil {
					a.logger.Warn("failed to publish agent card", "error", pubErr)
				}
				if inWarmup && time.Now().After(warmupDeadline) {
					ticker.Reset(normalInterval)
					inWarmup = false
				}
			}
		}
	})
}

func (a *Agent) Tools() *tool.Set { return a.tools }

func (a *Agent) SetSkillManager(mgr skillManager) {
	a.skillManager = mgr
}

func (a *Agent) SetSkillRehydrator(r SkillRehydrator) {
	a.skillRehydrator = r
}

func (a *Agent) SkillManager() skillManager {
	return a.skillManager
}

func (a *Agent) AddSubComponent(sc Startable) {
	a.subComponentsMu.Lock()
	a.subComponents = append(a.subComponents, sc)
	stopped := a.subComponentsStopped
	a.subComponentsMu.Unlock()
	if stopped {
		sc.Stop()
	}
}

func (a *Agent) subComponentsSnapshot() []Startable {
	a.subComponentsMu.Lock()
	defer a.subComponentsMu.Unlock()
	return slices.Clone(a.subComponents)
}

func (a *Agent) RegisterAuthHandler(handler tool.AuthResponseHandler) {
	a.authHandlersMu.Lock()
	defer a.authHandlersMu.Unlock()
	a.authHandlers[handler.CredentialKey()] = handler
}

func (a *Agent) DeregisterAuthHandler(credentialKey string) {
	if credentialKey == "" {
		return
	}
	a.authHandlersMu.Lock()
	defer a.authHandlersMu.Unlock()
	delete(a.authHandlers, credentialKey)
}

func (a *Agent) RegisterMCPAuthHandler(handler tool.AuthResponseHandler) {
	a.RegisterAuthHandler(handler)
}

func (a *Agent) receiveLoop(loopCtx, taskCtx context.Context, q broker.Queue, redeliverable bool) {
	broker.RunReceiveLoop(loopCtx, a.svc.Broker(), q, a.logger, broker.ReceiveLoopOptions{
		Health:   &a.health,
		Teardown: a.stopping.Load,
	}, func(msg *broker.Message) {
		a.routeMessageFrom(taskCtx, msg, redeliverable)
	})
}

func (a *Agent) requestQueueGone() bool {
	key := "queue:" + a2a.QueueName(a.cfg.Namespace, "a2a", a.cfg.Name)
	return slices.ContainsFunc(a.health.Snapshot(), func(p health.Problem) bool {
		return p.ID == key && p.Severity == health.Fatal
	})
}

func (a *Agent) routeMessage(ctx context.Context, msg *broker.Message) {
	a.routeMessageFrom(ctx, msg, false)
}

func (a *Agent) routeMessageFrom(ctx context.Context, msg *broker.Message, redeliverable bool) {
	if strings.HasPrefix(msg.Topic, strings.TrimRight(a.cfg.Namespace, "/")+"/sam/events/deep_research/") {
		a.dispatchDeepResearchEvent(msg)
		return
	}

	if strings.HasPrefix(msg.Topic, a2a.NotifyResponseTopicPrefix(a.cfg.Namespace, a.cfg.Name)) {
		if a.checkpoints != nil {
			go a.handleNotifyHILResponseFrom(msg, redeliverable)
			return
		}
		a.handleNotifyHILResponse(msg)
		return
	}

	if msg.Topic == a.forwardTopic() {
		go a.handleForwardedControl(ctx, msg)
		return
	}

	var req a2a.Request
	if err := json.Unmarshal(msg.Payload, &req); err != nil {
		msg.Ack()
		return
	}

	switch {
	case isPeerStatusUpdate(msg.Topic, a.cfg.Namespace, a.cfg.Name):
		msg.Ack()
		subTaskID := extractSubTaskIDFromTopic(msg.Topic)
		a.peerRouter.HandleStatus(subTaskID, msg)

	case isAgentResponseMsg(req.Method, req.ID):
		msg.Ack()
		a.peerRouter.HandleResponse(req.ID, msg)

	case req.Method == "message/send" || req.Method == "message/stream":
		if msg.Redelivered {
			props := a2a.DecodeUserProperties(msg.UserProperties)
			a.logger.Warn("redelivered task request received (possible poison-pill retry after a consumer crash)",
				"taskID", req.ID, "traceID", props.TraceID, "topic", msg.Topic, "method", req.Method)
		}
		a.beginRequest(req.ID)
		go func(r a2a.Request) {
			defer a.endRequest(r.ID)
			a.processMessage(ctx, msg, &r)
		}(req)

	case req.Method == "tasks/cancel":
		msg.Ack()
		a.handleCancelRequest(&req)

	case a.hasExtensionMethod(req.Method):
		msg.Ack()
		go a.handleExtensionRequest(ctx, msg, &req)

	default:
		msg.Ack()
	}
}

func (a *Agent) handleCancelRequest(req *a2a.Request) {
	m, ok := req.Params.(map[string]any)
	if !ok {
		a.logger.Error("invalid cancel request: params is not a map", "reqID", req.ID)
		return
	}
	taskID, _ := m["id"].(string)
	if taskID == "" {
		a.logger.Error("invalid cancel request: missing or empty id", "reqID", req.ID)
		return
	}

	a.activeTasksMu.Lock()
	entry, active := a.activeTasks[taskID]
	controls := a.controlEntriesForLocked(taskID)
	a.activeTasksMu.Unlock()

	for _, control := range controls {
		control.cancel()
	}

	if active {
		entry.cancel()
	}

	if state := a.pausedTasks.Load(taskID); state != nil {
		a.logger.Info("cancelling paused task", "taskID", taskID, "traceID", state.UserProps.TraceID)
		a.cancelPausedTask(state, cancelFromElsewhere)
		return
	}

	if active {
		a.logger.Info("cancelling active task", "taskID", taskID, "traceID", entry.userProps.TraceID)
		if entry.replyTo != "" && a.answersCancelOfRunningTurn(taskID, entry) {
			entry.publishOnce.Do(func() {
				resp := buildCancelledResponse(taskID, entry.contextID, a.cfg.Name, entry.usageSnapshot(), entry.usageFacts())
				a.publishTaskResponse(taskID, resp, entry.replyTo, entry.userProps)
			})
		}
		return
	}

	if a.checkpoints != nil {
		_ = a.cancelCheckpointedTask(taskID)
		return
	}

	a.logger.Debug("cancel request for unknown task", "taskID", taskID)
}

func (a *Agent) answersCancelOfRunningTurn(taskID string, entry *activeTaskEntry) bool {
	if a.checkpoints == nil || entry.checkpointed == nil || !entry.checkpointed.Load() {
		return true
	}
	if a.checkpoints.discardIfOwned(context.Background(), taskID) {
		a.logger.Info("a cancel reached a turn whose task was already answered elsewhere; not answering it again",
			"taskID", taskID, "traceID", entry.userProps.TraceID)
		return false
	}
	return true
}

func (a *Agent) deleteRunBasedSession(ctx context.Context, state *TaskExecutionState) error {
	dctx, cancel := detach(ctx)
	defer cancel()
	return a.svc.Sessions().Delete(dctx, a.cfg.Name, state.UserProps.UserID, state.EffectiveSessionID)
}

func (a *Agent) cancelCheckpointedTask(taskID string) bool {
	ctx, cancel := detach(context.Background())
	defer cancel()

	cp, err := a.checkpoints.store.GetCheckpoint(ctx, taskID)
	if err != nil {
		a.logger.Warn("failed to read the checkpoint for a cancelled task; a late reply could still resume it",
			"taskID", taskID, "error", err)
		return false
	}
	if cp == nil {
		a.logger.Debug("cancel request for a task with no checkpoint; another cancel or its own completion got there first",
			"taskID", taskID)
		return false
	}
	if cp.Final != nil {
		a.logger.Info("cancel request for a task already being answered; leaving its answer to go out",
			"traceID", cp.TraceID, "taskID", taskID)
		return false
	}

	state, err := unmarshalTaskState(cp.State)
	if err != nil {
		a.logger.Warn("cannot decode a cancelled task's checkpoint to answer the caller; leaving the row for a replica that can",
			"traceID", cp.TraceID, "taskID", taskID, "error", err)
		return false
	}

	a.applyCheckpointResults(state, cp.ToolResults)
	dispatchedAt := a.remoteCallDispatchTimes(ctx, state)

	taken, err := a.checkpoints.store.PopCheckpoint(ctx, taskID)
	if err != nil {
		a.logger.Warn("failed to take the checkpoint for a cancelled task; a late reply could still resume it",
			"taskID", taskID, "error", err)
		return false
	}
	if taken == nil {
		a.logger.Debug("another cancel or the task's answer took this task's checkpoint first; publishing nothing",
			"traceID", cp.TraceID, "taskID", taskID)
		return false
	}
	a.logger.Info("cancelled a task owned by another replica",
		"taskID", taskID, "traceID", state.UserProps.TraceID)
	a.endCancelledWaitsOfStoredTask(state, dispatchedAt)

	if state.UserProps.ReplyTo == "" {
		return true
	}
	resp := buildCancelledResponse(taskID, state.ContextID, a.cfg.Name, state.usageSnapshot(), &state.usageFacts)
	a.publishTaskResponse(taskID, resp, state.UserProps.ReplyTo, state.UserProps)
	return true
}

func (a *Agent) publishTaskResponse(taskID string, resp *a2a.Response, replyTo string, props a2a.UserProperties) {
	respBytes, marshalErr := json.Marshal(resp)
	if marshalErr != nil {
		a.logger.Error("failed to marshal response", "taskID", taskID, "traceID", props.TraceID, "error", marshalErr)
		return
	}
	respUserProps := ResponseUserProperties(a.cfg.Name, props)
	a.seqGen.Stamp(respUserProps, a.cfg.Name, taskID)

	pubCtx, pubCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer pubCancel()
	if pubErr := a.svc.Broker().PublishGuaranteed(pubCtx, replyTo, &broker.Message{
		Payload:        respBytes,
		UserProperties: respUserProps,
	}); pubErr != nil {
		a.logger.Error("failed to publish response", "taskID", taskID, "traceID", props.TraceID, "error", pubErr)
	}
}

func ResponseUserProperties(agentName string, props a2a.UserProperties) map[string]string {
	respUserProps := map[string]string{
		"delegating_agent_name": agentName,
		"timestamp":             strconv.FormatInt(time.Now().UnixMilli(), 10),
	}
	if props.ClientID != "" {
		respUserProps["clientId"] = props.ClientID
	}
	if props.UserID != "" {
		respUserProps["userId"] = props.UserID
	}
	if props.A2AUserConfig != "" {
		respUserProps["a2aUserConfig"] = props.A2AUserConfig
	}
	a2a.AddTraceID(respUserProps, props.TraceID)
	return respUserProps
}

func (a *Agent) processMessage(ctx context.Context, msg *broker.Message, req *a2a.Request) {
	leftForRedelivery := false
	defer func() {
		if !leftForRedelivery {
			msg.Ack()
		}
	}()

	taskCtx, endTurn, stopTask := cancellableTaskContext(ctx)
	defer endTurn()

	a2aMsg, err := extractA2AMessage(req.Params)
	if err != nil {
		props := a2a.DecodeUserProperties(msg.UserProperties)
		a.logger.Error("failed to extract message from params", "taskID", req.ID, "traceID", props.TraceID, "error", err)
		return
	}

	props := a2a.DecodeUserProperties(msg.UserProperties)

	if msg.Redelivered && a.checkpoints != nil && !a.isControlMessage(a2aMsg) && a.taskRunningHere(req.ID) {
		a.logger.Info("redelivered task is already running in this process; dropping the copy", "taskID", req.ID, "traceID", props.TraceID)
		return
	}

	entry := &activeTaskEntry{
		cancel:      stopTask,
		replyTo:     props.ReplyTo,
		contextID:   a2aMsg.ContextID,
		userProps:   props,
		publishOnce: &sync.Once{},
		kind:        agentTaskKind(a2aMsg),
	}
	if a.isControlMessage(a2aMsg) {
		a.trackControlEntry(req.ID, entry)
		defer a.releaseControlEntry(entry)
	} else {
		a.activeTasksMu.Lock()
		a.activeTasks[req.ID] = entry
		a.activeTasksMu.Unlock()
		defer a.releaseActiveTask(req.ID, entry)
	}

	resp, handleErr := a.handleTask(taskCtx, req.ID, a2aMsg, props, taskDelivery{redelivered: msg.Redelivered})
	if resp == nil && handleErr == nil && !a.isControlMessage(a2aMsg) && a.cutWhileHoldingUnsavedPause(ctx, req.ID) {
		leftForRedelivery = true
		a.logger.Info("stopped while a task waited that could not be checkpointed; left its request unacknowledged for redelivery",
			"taskID", req.ID, "traceID", props.TraceID)
		return
	}
	if a.cutByProcessExit(ctx) {
		leftForRedelivery = true
		a.seqGen.Drop(req.ID)
		a.logger.Info("task cut by a stop that hands it on; left unacknowledged for redelivery", "taskID", req.ID, "traceID", props.TraceID)
		return
	}
	if errors.Is(handleErr, errAnswerNotRecorded) {
		leftForRedelivery = true
		if msg.Redelivered {
			a.logger.Error("could not record a user's answer on its redelivery either", "taskID", req.ID, "traceID", props.TraceID)
		} else {
			a.logger.Warn("could not record a user's answer; asking the broker to deliver it again", "taskID", req.ID, "traceID", props.TraceID)
		}
		msg.Nack()
		return
	}
	if handleErr != nil {
		if taskCtx.Err() != nil {
			a.logger.Info("task cancelled, building cancelled response", "taskID", req.ID, "traceID", props.TraceID)
			resp = buildCancelledResponse(req.ID, a2aMsg.ContextID, a.cfg.Name, entry.usageSnapshot(), entry.usageFacts())
		} else {
			a.logger.Error("task handling failed", "taskID", req.ID, "traceID", props.TraceID, "error", handleErr)
			resp = a2a.NewErrorResponse(req.ID, -32603, "internal error")
		}
		a.seqGen.Drop(req.ID)
	}

	if resp != nil && props.ReplyTo != "" {
		entry.publishOnce.Do(func() {
			a.publishTaskResponse(req.ID, resp, props.ReplyTo, props)
		})
	}
}

func (a *Agent) cutWhileHoldingUnsavedPause(ctx context.Context, taskID string) bool {
	state := a.pausedTasks.Load(taskID)
	if state == nil {
		return false
	}
	held := state.heldRequest()
	if held == nil {
		return false
	}
	select {
	case <-held:
		return false
	case <-ctx.Done():
		return true
	}
}

func (a *Agent) cutByProcessExit(runCtx context.Context) bool {
	return a.exitingProcess.Load() && runCtx.Err() != nil
}

func extractA2AMessage(params any) (a2a.Message, error) {
	var msg a2a.Message
	m, ok := params.(map[string]any)
	if !ok {
		return msg, fmt.Errorf("params is %T, expected map[string]any", params)
	}
	raw, ok := m["message"]
	if !ok {
		return msg, fmt.Errorf("params has no \"message\" key")
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return msg, fmt.Errorf("marshal message sub-key: %w", err)
	}
	if err := json.Unmarshal(b, &msg); err != nil {
		return msg, fmt.Errorf("unmarshal message: %w", err)
	}
	return msg, nil
}

const taskPanicMessage = "internal error"

func (a *Agent) applyPanicResponse(source, taskID string, r any, state *TaskExecutionState, resp **a2a.Response, err *error) {
	a.logger.Error("panic recovered",
		"source", source,
		"taskID", taskID,
		"panic", r,
		"stack", string(debug.Stack()),
	)
	*resp = a2a.NewErrorResponse(taskID, -32603, taskPanicMessage)
	if state != nil {
		addFactsToErrorData(*resp, &state.usageFacts)
	}
	*err = fmt.Errorf("panic in %s: %v", source, r)
	signalTaskDone(state, *resp)
	a.seqGen.Drop(taskID)
}

func addFactsToErrorData(resp *a2a.Response, facts *taskUsageFacts) {
	if resp == nil || resp.Error == nil || facts == nil {
		return
	}
	data, ok := resp.Error.Data.(map[string]any)
	if !ok {
		data = make(map[string]any, 4)
	}
	facts.AddTo(data)
	if len(data) > 0 {
		resp.Error.Data = data
	}
}

func signalTaskDone(state *TaskExecutionState, resp *a2a.Response) {
	if state == nil || state.DoneCh == nil {
		return
	}
	select {
	case state.DoneCh <- resp:
	default:
	}
}

func (a *Agent) recoverResumePanic(state *TaskExecutionState) {
	r := recover()
	if r == nil {
		return
	}
	a.logger.Error("panic in resumeTaskExecution",
		"taskID", state.TaskID,
		"panic", r,
		"stack", string(debug.Stack()),
	)
	resp := a2a.NewErrorResponse(state.TaskID, -32603, taskPanicMessage)
	addFactsToErrorData(resp, &state.usageFacts)
	signalTaskDone(state, resp)

	publishCtx, publishCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer publishCancel()
	logger := a.logger.With("traceID", state.UserProps.TraceID, "taskID", state.TaskID)
	final := a.finalMessageFor(resp, state, logger)
	if !a.checkpoints.markAnswering(publishCtx, state, final) {
		logger.Warn("not publishing a panic response for a task taken over elsewhere")
		a.standDownLostTask(state)
		return
	}
	a.deliverAnswer(publishCtx, state, final, nil, logger)
	a.forgetPausedTask(state.TaskID)
}

func (a *Agent) HandleTask(ctx context.Context, taskID string, msg a2a.Message, userProps a2a.UserProperties) (resp *a2a.Response, err error) {
	return a.handleTask(ctx, taskID, msg, userProps, taskDelivery{})
}

func (a *Agent) handleTask(ctx context.Context, taskID string, msg a2a.Message, userProps a2a.UserProperties, delivery taskDelivery) (resp *a2a.Response, err error) {
	var state *TaskExecutionState
	defer func() {
		if r := recover(); r != nil {
			a.applyPanicResponse("HandleTask", taskID, r, state, &resp, &err)
		}
	}()
	defer func() {
		if state != nil && a.pausedTasks.Load(taskID) == nil {
			a.releaseRunningTask(taskID)
		}
	}()

	var (
		rc        *taskRunContext
		earlyResp *a2a.Response
		setupErr  error
	)
	state, rc, earlyResp, setupErr = a.setupTask(ctx, taskID, msg, userProps, delivery)
	if setupErr != nil || earlyResp != nil || state == nil {
		return earlyResp, setupErr
	}

	if rc.storedFinalAnswer != nil {
		resp = a.republishStoredFinal(ctx, state, rc)
	} else {
		resp, err = a.runFirstTurn(ctx, state, rc)
	}

	if resp != nil && (state.IsRunBased || rc.structuredCtx != nil) {
		if delErr := a.deleteRunBasedSession(ctx, state); delErr != nil {
			a.logger.Warn("failed to delete run-based session", "sessionID", state.EffectiveSessionID, "error", delErr)
		}
	}

	if resp != nil {
		select {
		case state.DoneCh <- resp:
		default:
		}
	}

	if resp != nil {
		a.seqGen.Drop(taskID)
	}

	return resp, err
}

func (a *Agent) HandleTaskToCompletion(ctx context.Context, taskID string, msg a2a.Message, userProps a2a.UserProperties) (resp *a2a.Response, err error) {
	var state *TaskExecutionState
	defer func() {
		if r := recover(); r != nil {
			a.applyPanicResponse("HandleTaskToCompletion", taskID, r, state, &resp, &err)
		}
	}()

	var (
		rc        *taskRunContext
		earlyResp *a2a.Response
		setupErr  error
	)
	defer func() {
		if state != nil && a.pausedTasks.Load(taskID) == nil {
			a.releaseRunningTask(taskID)
		}
	}()
	state, rc, earlyResp, setupErr = a.setupTask(ctx, taskID, msg, userProps, taskDelivery{})
	if setupErr != nil || earlyResp != nil || state == nil {
		return earlyResp, setupErr
	}

	resp, err = a.runFirstTurn(ctx, state, rc)

	if resp != nil && (state.IsRunBased || rc.structuredCtx != nil) {
		if delErr := a.deleteRunBasedSession(ctx, state); delErr != nil {
			a.logger.Warn("failed to delete run-based session", "sessionID", state.EffectiveSessionID, "error", delErr)
		}
	}

	if resp != nil {
		select {
		case state.DoneCh <- resp:
		default:
		}
		return resp, err
	}

	select {
	case finalResp := <-state.DoneCh:
		return finalResp, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

type streamCallInfo struct {
	UserID             string
	SessionID          string
	UserTimezone       string
	PrependTurnSpacing bool
	MaxContinuations   int

	IsStructuredInvocation bool

	IsWorkflowDispatch bool

	RenderTemplatesInline bool

	CallerDisplaysTemplates bool

	BlockValidationRetriesLeft int

	Logger *slog.Logger

	ContinuationsUsed int

	WithholdLeakedToolCall bool

	LeakedToolCall string
}

type artifactCreationRecord struct {
	Filename    string
	MIMEType    string
	Description string
	Version     int
	SizeBytes   int
	CallID      string
	Status      string
	ErrorMsg    string
	Tags        []string
}

func (r artifactCreationRecord) resolvedStatus() string {
	if r.Status == "" {
		return "success"
	}
	return r.Status
}

type blockValidationError struct {
	VisibleText string
	RawText     string
	ErrorMsg    string
	Filename    string
	BlockType   FencedBlockType
	ErrorKind   string
	Reasoning   string
}

func (e *blockValidationError) Error() string { return e.ErrorMsg }

func validateArtifactHeader(block *FencedBlockEvent) error {
	if !block.FilenameSet {
		return fmt.Errorf(
			"Artifact block is missing the 'filename' parameter. " +
				"Expected format: «««save_artifact: filename=\"name.ext\" mime_type=\"type\" description=\"desc\"\n" +
				"Parameters must use double quotes around values (e.g., filename=\"data.csv\", not filename=data.csv).")
	}
	if block.Filename == "" {
		return fmt.Errorf(
			"Artifact block has an empty filename. " +
				"Expected format: «««save_artifact: filename=\"name.ext\"")
	}
	return nil
}

func (a *Agent) streamLLMCall(ctx context.Context, sp *StatusPublisher, req *llm.ChatRequest, info *streamCallInfo) (*llm.ChatResponse, []artifactCreationRecord, error) {
	streamCtx, cancelStream := context.WithCancel(ctx)
	defer cancelStream()

	if req.Observability.ComponentName == "" {
		req.Observability.ComponentName = "agent-" + agentDisplayName(&a.cfg)
	}
	if req.Observability.OwnerID == "" && info != nil {
		req.Observability.OwnerID = info.UserID
	}

	stream, err := a.llmClient(streamCtx).ChatCompletionStream(streamCtx, req)
	if err != nil {
		return nil, nil, err
	}

	var contCount *int32
	if a.autoContinuationEnabled() {
		invoke := func(ctx context.Context, r *llm.ChatRequest) (<-chan llm.StreamChunk, error) {
			return a.llmClient(ctx).ChatCompletionStream(ctx, r)
		}
		stream, contCount = autoContinueStream(streamCtx, stream, req, invoke, a.logger, info.MaxContinuations)
	}
	defer func() {
		if contCount != nil {
			info.ContinuationsUsed = int(atomic.LoadInt32(contCount))
		}
	}()

	events := runStreamingPipeline(streamCtx, stream, a.cfg.StreamBatchingThresholdBytes, a.cfg.StreamBatchingFlushIntervalMs)

	var resolver *EmbedResolver
	if info != nil {
		resolver = NewEmbedResolver(a.svc.Artifacts(), a.cfg.Name, info.UserID, info.SessionID, info.UserTimezone, a.logger)
	}

	var visibleText strings.Builder
	var rawText strings.Builder
	var reasoning strings.Builder
	var createdArtifacts []artifactCreationRecord
	var toolCalls []llm.ToolCall
	var finishReason string
	var usage *llm.Usage
	var artBytesTransferred int
	turnSpacingPending := info != nil && info.PrependTurnSpacing
	thinkingActive := false
	templateEmitted := false
	hold := newTextHold(withheldTools(req.Tools, info))
	publishText := func(text string) {
		thinkingActive, turnSpacingPending = handleStreamTextEvent(ctx, sp, text, thinkingActive, turnSpacingPending, &visibleText, &rawText)
	}

	for ev := range events {
		ev = hold.embedAsText(ev)
		publishText(hold.releaseBefore(ev.Type))
		switch ev.Type {
		case EventText:
			publishText(hold.admit(ev.Text))

		case EventEmbed:
			if ev.Embed == nil {
				continue
			}
			a.handleEmbedEvent(ctx, sp, resolver, ev.Embed, &visibleText, &turnSpacingPending)
			rawText.WriteString(ev.Embed.Raw)

		case EventBlockStart:
			if ev.Block != nil && ev.Block.Type == FencedBlockArtifact {
				if bve := validateAndDrainOnHeaderError(ctx, sp, ev.Block, events, cancelStream, &visibleText, &rawText); bve != nil {
					bve.Reasoning = reasoning.String()
					return nil, nil, bve
				}
				artBytesTransferred = 0
				a.publishArtifactBlockStart(ctx, sp, ev.Block, &rawText)
			}

		case EventBlockContent:
			if ev.Block != nil && ev.Block.Type == FencedBlockArtifact {
				artBytesTransferred += len(ev.Text)
				rawText.WriteString(ev.Text)
				chunk := ev.Text
				var chunkMimeType *string
				if ev.Block.MIMEType != "" {
					chunkMimeType = &ev.Block.MIMEType
				}
				sp.PublishArtifactProgress(ctx, ev.Block.Filename, "in-progress", artBytesTransferred, &chunk, chunkMimeType, strPtr(ev.Block.Description), nil, nil, ev.Block.Tags)
			}

		case EventBlockEnd:
			if ev.Block == nil {
				continue
			}
			if bve := a.handleBlockEnd(ctx, sp, ev.Block, &streamPipelineState{
				visibleText:         &visibleText,
				rawText:             &rawText,
				resolver:            resolver,
				info:                info,
				artBytesTransferred: artBytesTransferred,
				createdArtifacts:    &createdArtifacts,
				templateEmitted:     &templateEmitted,
				thinkingActive:      &thinkingActive,
				turnSpacingPending:  &turnSpacingPending,
			}); bve != nil {
				bve.Reasoning = reasoning.String()
				return nil, nil, bve
			}

		case EventToolStart:
			if ev.ToolCall != nil {
				toolCalls = append(toolCalls, *ev.ToolCall)
			}

		case EventThinkingContent:
			thinkingActive = true
			reasoning.WriteString(ev.Text)
			sp.PublishGenericSignal(ctx, a2a.ThinkingContentData{
				Type:    string(a2a.SignalThinkingContent),
				Content: ev.Text,
			}, string(a2a.SignalThinkingContent))

		case EventDone:
			if thinkingActive {
				thinkingActive = false
				sp.PublishGenericSignal(ctx, a2a.ThinkingContentData{
					Type:       string(a2a.SignalThinkingContent),
					IsComplete: true,
				}, string(a2a.SignalThinkingContent))
			}
			finishReason = ev.FinishReason
			usage = ev.Usage

		case EventError:
			return nil, nil, ev.Err
		}
	}

	publishText(hold.finish(info, len(toolCalls) > 0 || len(createdArtifacts) > 0))

	if ctx.Err() != nil {
		return &llm.ChatResponse{
			Content:          visibleText.String(),
			RawContent:       rawContentWhenDiverged(&rawText, len(createdArtifacts) > 0, templateEmitted),
			ToolCalls:        toolCalls,
			ReasoningContent: reasoning.String(),
			FinishReason:     finishReason,
			Usage:            usage,
		}, createdArtifacts, ctx.Err()
	}

	return &llm.ChatResponse{
		Content:          visibleText.String(),
		RawContent:       rawContentWhenDiverged(&rawText, len(createdArtifacts) > 0, templateEmitted),
		ToolCalls:        toolCalls,
		ReasoningContent: reasoning.String(),
		FinishReason:     finishReason,
		Usage:            usage,
	}, createdArtifacts, nil
}

func rawContentWhenDiverged(rawText *strings.Builder, artifactsCreated, templateEmitted bool) string {
	if artifactsCreated || templateEmitted {
		return rawText.String()
	}
	return ""
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func isFilenameSafe(filename string) bool {
	return artifact.IsFilenameSafe(filename)
}

func (a *Agent) previewArtifactNotifications(artifacts []artifactCreationRecord) []LLMResponseToolCall {
	var calls []LLMResponseToolCall
	for _, art := range artifacts {
		calls = append(calls, LLMResponseToolCall{
			Name: "_notify_artifact_save",
			Args: map[string]any{
				"filename": art.Filename,
				"version":  art.Version,
				"status":   art.resolvedStatus(),
			},
			ID:       art.CallID,
			Injected: true,
		})
	}
	return calls
}

func handleStreamTextEvent(ctx context.Context, sp *StatusPublisher, text string, thinkingActive, turnSpacingPending bool, visibleText, rawText *strings.Builder) (bool, bool) {
	if text == "" {
		return thinkingActive, turnSpacingPending
	}
	publishAnswerText(ctx, sp, text, &thinkingActive, &turnSpacingPending, visibleText)
	rawText.WriteString(text)
	return thinkingActive, turnSpacingPending
}

func publishAnswerText(ctx context.Context, sp *StatusPublisher, text string, thinkingActive, turnSpacingPending *bool, visibleText *strings.Builder) {
	if *thinkingActive {
		*thinkingActive = false
		sp.PublishGenericSignal(ctx, a2a.ThinkingContentData{
			Type:       string(a2a.SignalThinkingContent),
			IsComplete: true,
		}, string(a2a.SignalThinkingContent))
	}
	if *turnSpacingPending {
		*turnSpacingPending = false
		sp.PublishText(ctx, turnSeparator+text)
	} else {
		sp.PublishText(ctx, text)
	}
	visibleText.WriteString(text)
}

const turnSeparator = "\n\n"

func publishTurnSpacing(ctx context.Context, sp *StatusPublisher, turnSpacingPending *bool) {
	if *turnSpacingPending {
		*turnSpacingPending = false
		sp.PublishText(ctx, turnSeparator)
	}
}

type streamPipelineState struct {
	visibleText         *strings.Builder
	rawText             *strings.Builder
	resolver            *EmbedResolver
	info                *streamCallInfo
	artBytesTransferred int
	createdArtifacts    *[]artifactCreationRecord
	templateEmitted     *bool
	thinkingActive      *bool
	turnSpacingPending  *bool
}

func (a *Agent) handleBlockEnd(ctx context.Context, sp *StatusPublisher, block *FencedBlockEvent, st *streamPipelineState) *blockValidationError {
	if errMsg := incompleteBlockErrorMessage(block); errMsg != "" {
		switch block.Type {
		case FencedBlockArtifact:
			sp.PublishArtifactProgressWithError(ctx, block.Filename, "cancelled",
				st.artBytesTransferred, nil, nil, strPtr(block.Description), nil, nil, &errMsg, block.Tags)
		case FencedBlockTemplate:
			st.rawText.WriteString(rawUnclosedTemplateBlock(block))
		}
		return &blockValidationError{
			VisibleText: st.visibleText.String(),
			RawText:     st.rawText.String(),
			ErrorMsg:    errMsg,
			Filename:    block.Filename,
			BlockType:   block.Type,
		}
	}

	switch block.Type {
	case FencedBlockArtifact:
		if record := a.finalizeArtifactBlock(ctx, sp, block, st); record != nil {
			*st.createdArtifacts = append(*st.createdArtifacts, *record)
		}
	case FencedBlockTemplate:
		st.rawText.WriteString(rawTemplateBlock(block))
		if errMsg := a.validateTemplateBlock(ctx, block, st.info); errMsg != "" {
			return &blockValidationError{
				VisibleText: st.visibleText.String(),
				RawText:     st.rawText.String(),
				ErrorMsg:    errMsg,
				BlockType:   FencedBlockTemplate,
			}
		}
		if st.info != nil && st.info.RenderTemplatesInline {
			return a.renderTemplateInline(ctx, sp, block, st)
		}
		publishTurnSpacing(ctx, sp, st.turnSpacingPending)
		sp.PublishTemplateBlock(ctx, block)
		*st.templateEmitted = sp.Enabled()
	}
	return nil
}

const templateInlineTruncatedFormat = "\n... [output truncated; the full data is in artifact '%s']"

const templateShownToUserNote = "\n\n[Note from SAM to the calling agent: the user already sees the output above, rendered directly in their chat. Do not reproduce it in your reply; refer to it or summarize it instead.]"

const templateRetryInstruction = " Emit only the corrected template block; do not repeat text you already wrote."

func (a *Agent) renderTemplateInline(ctx context.Context, sp *StatusPublisher, block *FencedBlockEvent, st *streamPipelineState) *blockValidationError {
	logger := cmp.Or(st.info.Logger, a.logger)
	renderer := NewTemplateRenderer(a.svc.Artifacts(), a.cfg.Name, st.info.UserID, st.info.SessionID, logger)
	rendered, err := renderer.RenderBlock(ctx, block)
	filename, _ := ParseArtifactSpec(block.DataSource)
	if err != nil {
		kind := templateRenderErrorKind(err)
		if msg := fixableTemplateRenderMessage(kind, filename); msg != "" && templateRetryAllowed(st) {
			return &blockValidationError{
				VisibleText: st.visibleText.String(),
				RawText:     st.rawText.String(),
				ErrorMsg:    msg,
				BlockType:   FencedBlockTemplate,
				ErrorKind:   string(kind),
			}
		}
		logger.Warn("inline template render failed",
			"dataArtifact", block.DataSource,
			"templateLen", len(block.Content),
			"errorKind", string(kind),
		)
		st.rawText.WriteString(rendered)
		publishAnswerText(ctx, sp, rendered, st.thinkingActive, st.turnSpacingPending, st.visibleText)
		*st.templateEmitted = true
		return nil
	}

	_, ceiling := a.toolOutputCaps()
	text := strutil.Truncate(rendered, ceiling, fmt.Sprintf(templateInlineTruncatedFormat, filename))
	if st.info.CallerDisplaysTemplates {
		sp.PublishTemplateBlock(ctx, block)
		st.visibleText.WriteString(text + templateShownToUserNote)
	} else {
		publishAnswerText(ctx, sp, text, st.thinkingActive, st.turnSpacingPending, st.visibleText)
	}
	*st.templateEmitted = true
	return nil
}

func templateRetryAllowed(st *streamPipelineState) bool {
	return len(*st.createdArtifacts) == 0 && st.info.BlockValidationRetriesLeft > 0
}

func fixableTemplateRenderMessage(kind templateErrorKind, filename string) string {
	var msg string
	switch kind {
	case templateErrorMissingData:
		msg = "Template block is missing the data parameter. Name the data artifact with data=\"<filename>\"."
	case templateErrorNotFound:
		msg = fmt.Sprintf("Template data artifact '%s' was not found. Save the data before the template block, or fix the data= name.", filename)
	case templateErrorJSONPath:
		msg = fmt.Sprintf("The jsonpath on template data artifact '%s' failed. Fix the jsonpath so it matches the data's structure, or remove it.", filename)
	case templateErrorRender:
		msg = fmt.Sprintf("Template rendering against '%s' failed. Check that filters match the field types.", filename)
	default:
		return ""
	}
	return msg + templateRetryInstruction
}

func drainStreamEvents(events <-chan OutputEvent) {
	for ev := range events {
		_ = ev
	}
}

func validateAndDrainOnHeaderError(ctx context.Context, sp *StatusPublisher, block *FencedBlockEvent, events <-chan OutputEvent, cancelStream context.CancelFunc, visibleText, rawText *strings.Builder) *blockValidationError {
	headerErr := validateArtifactHeader(block)
	if headerErr == nil {
		return nil
	}
	cancelStream()
	errMsg := headerErr.Error()
	sp.PublishArtifactProgressWithError(ctx, block.Filename, "cancelled",
		0, nil, nil, strPtr(block.Description), nil, nil, &errMsg, block.Tags)
	drainStreamEvents(events)
	filename := block.Filename
	if !block.FilenameSet {
		filename = "unknown_artifact"
	}
	return &blockValidationError{
		VisibleText: visibleText.String(),
		RawText:     rawText.String(),
		ErrorMsg:    errMsg,
		Filename:    filename,
		BlockType:   FencedBlockArtifact,
	}
}

func incompleteBlockErrorMessage(block *FencedBlockEvent) string {
	if !block.Incomplete {
		return ""
	}
	blockName := block.Filename
	if blockName == "" {
		blockName = "unknown"
	}
	return fmt.Sprintf(
		"Artifact block for '%s' was not properly closed. "+
			"The closing delimiter »»» is missing. "+
			"If the content intentionally contains »»», it must be escaped. "+
			"Please regenerate the complete artifact with proper opening and closing delimiters.",
		blockName)
}

func (a *Agent) finalizeArtifactBlock(ctx context.Context, sp *StatusPublisher, block *FencedBlockEvent, st *streamPipelineState) *artifactCreationRecord {
	visibleText := st.visibleText
	rawText := st.rawText
	resolver := st.resolver
	info := st.info
	artBytesTransferred := st.artBytesTransferred
	rawText.WriteString(BlockDelimiterClose + "\n")

	content := block.Content
	if resolver != nil {
		content = resolver.ResolveEmbedsInString(ctx, content)
	}
	mimeType := block.MIMEType
	if mimeType == "" {
		mimeType = "text/plain"
	}
	filename := block.Filename
	description := block.Description
	if resolver != nil {
		filename = resolver.ResolveEmbedsInString(ctx, filename)
		description = resolver.ResolveEmbedsInString(ctx, description)
	}

	autoTagForStructuredInvocation := info != nil && info.IsStructuredInvocation
	if autoTagForStructuredInvocation && info.IsWorkflowDispatch && !a2a.IsWorkflowHandoffArtifactName(filename) {
		autoTagForStructuredInvocation = false
	}
	shouldAutoTag := autoTagForStructuredInvocation || artifact.IsSAMConfigMIME(block.MIMEType)
	if shouldAutoTag && !slices.Contains(block.Tags, a2a.ArtifactTagWorking) {
		block.Tags = append(block.Tags, a2a.ArtifactTagWorking)
	}

	record := a.handleArtifactBlockSave(ctx, sp, resolvedArtifactBlock{
		Block:               block,
		Filename:            filename,
		Description:         description,
		MIMEType:            mimeType,
		Content:             content,
		ArtBytesTransferred: artBytesTransferred,
	}, info)

	if visibleText != nil && info != nil && info.IsStructuredInvocation && block.Status != "" &&
		record != nil && record.Status == "success" {
		synth := synthesizeResultEmbed(filename, block.Status, block.Message)
		visibleText.WriteString(synth)
		rawText.WriteString(synth)
	}

	return record
}

func synthesizeResultEmbed(filename, status, message string) string {
	var b strings.Builder
	b.WriteString("\u00abresult:artifact=")
	b.WriteString(filename)
	b.WriteString(" status=")
	b.WriteString(status)
	if message != "" {
		b.WriteString(` message="`)
		b.WriteString(strings.ReplaceAll(message, `"`, `\"`))
		b.WriteString(`"`)
	}
	b.WriteString("\u00bb")
	return b.String()
}

func (a *Agent) publishArtifactBlockStart(ctx context.Context, sp *StatusPublisher, block *FencedBlockEvent, rawText *strings.Builder) {
	statusText := fmt.Sprintf("Receiving artifact `%s`...", block.Filename)
	if block.Description != "" {
		statusText = fmt.Sprintf("Receiving artifact `%s`: %s", block.Filename, block.Description)
	}
	sp.PublishProgressUpdate(ctx, statusText)
	sp.PublishArtifactProgress(ctx, block.Filename, "in-progress", 0, nil, nil, strPtr(block.Description), nil, nil, block.Tags)
	rawText.WriteString(BlockPrefixSaveArtifact)
	if block.Filename != "" {
		fmt.Fprintf(rawText, " filename=%q", block.Filename)
	}
	if block.MIMEType != "" {
		fmt.Fprintf(rawText, " mime_type=%q", block.MIMEType)
	}
	if block.Description != "" {
		fmt.Fprintf(rawText, " description=%q", block.Description)
	}
	rawText.WriteString("\n")
}

func (a *Agent) handleEmbedEvent(ctx context.Context, sp *StatusPublisher, resolver *EmbedResolver, embed *EmbedRef, visibleText *strings.Builder, turnSpacingPending *bool) {
	if embed.Type == "result" {
		visibleText.WriteString(embed.Raw)
		return
	}
	if !embed.IsEarly || resolver == nil {
		visibleText.WriteString(embed.Raw)
		publishTurnSpacing(ctx, sp, turnSpacingPending)
		sp.PublishText(ctx, embed.Raw)
		return
	}
	resolved, resolveErr := resolver.Resolve(ctx, embed)
	switch {
	case resolveErr == ErrStatusUpdateSignal:
		sp.PublishProgressUpdateWithPartMeta(ctx, resolved, map[string]any{
			"source_embed_type": "status_update",
		})
	case resolveErr != nil:
		a.logger.Debug("embed resolution failed", "raw", embed.Raw, "error", resolveErr)
		visibleText.WriteString(embed.Raw)
		publishTurnSpacing(ctx, sp, turnSpacingPending)
		sp.PublishText(ctx, embed.Raw)
	default:
		visibleText.WriteString(resolved)
		publishTurnSpacing(ctx, sp, turnSpacingPending)
		sp.PublishText(ctx, resolved)
	}
}

func (a *Agent) validateTemplateBlock(ctx context.Context, block *FencedBlockEvent, info *streamCallInfo) string {
	if syntaxErr := validateTemplateSyntax(block.Content); syntaxErr != nil {
		return fmt.Sprintf(
			"Template block has invalid Liquid syntax: %s. "+
				"Please fix the template syntax and regenerate.",
			syntaxErr.Error())
	}
	if a.svc.Artifacts() != nil && info != nil {
		if boundsErr := validateTemplateIndexBounds(ctx, block, a.svc.Artifacts(), a.cfg.Name, info.UserID, info.SessionID); boundsErr != nil {
			return fmt.Sprintf(
				"Template index out of bounds: %s. "+
					"Please fix the array indices and regenerate the template.",
				boundsErr.Error())
		}
	}
	return ""
}

type resolvedArtifactBlock struct {
	Block               *FencedBlockEvent
	Filename            string
	Description         string
	MIMEType            string
	Content             string
	ArtBytesTransferred int
}

func newArtifactErrorRecord(filename, description, mimeType string, tags []string, errMsg string) *artifactCreationRecord {
	return &artifactCreationRecord{
		Filename:    filename,
		Description: description,
		MIMEType:    mimeType,
		Version:     0,
		CallID:      hostNotifyCallIDPrefix + idgen.New(),
		Status:      "error",
		ErrorMsg:    errMsg,
		Tags:        tags,
	}
}

func (a *Agent) handleArtifactBlockSave(
	ctx context.Context,
	sp *StatusPublisher,
	rb resolvedArtifactBlock,
	info *streamCallInfo,
) *artifactCreationRecord {
	block := rb.Block
	filename := rb.Filename
	description := rb.Description
	mimeType := rb.MIMEType
	content := rb.Content
	artBytesTransferred := rb.ArtBytesTransferred

	if !isFilenameSafe(filename) {
		displayName := filename
		if !block.FilenameSet {
			displayName = "unknown_artifact"
		}
		a.logger.Warn("invalid artifact filename", "filename", filename)
		sp.PublishArtifactProgress(ctx, displayName, "failed", 0, nil, nil, strPtr(description), nil, nil, block.Tags)
		return newArtifactErrorRecord(displayName, description, mimeType, block.Tags,
			fmt.Sprintf("Invalid filename: '%s'. Filename must not contain path separators or traversal sequences.", filename))
	}

	if !isInlineableTextMIME(mimeType) {
		a.logger.Warn("rejecting save_artifact for non-text MIME type",
			"filename", filename, "mimeType", mimeType)
		sp.PublishArtifactProgress(ctx, filename, "failed", 0, nil, &mimeType, strPtr(description), nil, nil, block.Tags)
		return newArtifactErrorRecord(filename, description, mimeType, block.Tags, fmt.Sprintf(
			"Cannot save_artifact for non-text MIME type %q. The fenced-block save_artifact only supports text-like content (text/*, JSON, YAML, XML, etc.). To deliver an existing binary artifact, use the «artifact_return:filename» embed. Binary outputs from tools (images, PDFs, archives) are saved by the tool itself.",
			mimeType))
	}

	if a.svc.Artifacts() == nil || info == nil {
		sp.PublishArtifactProgress(ctx, filename, "completed", artBytesTransferred, nil, &mimeType, strPtr(description), nil, nil, block.Tags)
		return nil
	}

	savingStatusText := fmt.Sprintf("Saving artifact `%s`...", filename)
	if description != "" {
		savingStatusText = fmt.Sprintf("Saving artifact `%s`: %s", filename, description)
	}
	sp.PublishProgressUpdate(ctx, savingStatusText)

	saveResult, saveErr := artifact.SaveWithMetadata(ctx, a.svc.Artifacts(),
		a.cfg.Name, info.UserID, info.SessionID, filename,
		[]byte(content), mimeType,
		artifact.MetadataOpts{
			Description:    description,
			SchemaMaxDepth: a.cfg.SchemaInferenceDepth,
			SchemaMaxKeys:  a.cfg.SchemaMaxKeys,
			Tags:           block.Tags,
		}, a.logger)
	var savedVersion int
	if saveResult != nil {
		savedVersion = saveResult.DataVersion
	}
	if saveErr != nil {
		a.logger.Warn("failed to save artifact", "filename", filename, "error", saveErr)
		sp.PublishArtifactProgress(ctx, filename, "failed", artBytesTransferred, nil, nil, strPtr(description), nil, nil, block.Tags)
		return newArtifactErrorRecord(filename, description, mimeType, block.Tags, saveErr.Error())
	}

	callID := hostNotifyCallIDPrefix + idgen.New()
	sp.PublishArtifactProgress(ctx, filename, "completed", artBytesTransferred, nil, &mimeType, strPtr(description), &savedVersion, &callID, block.Tags)
	sp.PublishArtifactSaved(ctx, filename, savedVersion, mimeType, int64(len(content)), strPtr(description), &callID, block.Tags)
	return &artifactCreationRecord{
		Filename:    filename,
		MIMEType:    mimeType,
		Description: description,
		Version:     savedVersion,
		SizeBytes:   len(content),
		CallID:      callID,
		Status:      "success",
		Tags:        block.Tags,
	}
}

func (a *Agent) injectArtifactNotifications(ctx context.Context, sp *StatusPublisher, rawContent string, artifacts []artifactCreationRecord, messages *[]llm.ChatMessage) {
	a.injectArtifactNotificationsWithGroupID(ctx, sp, rawContent, artifacts, messages, artifactBatchGroupID(artifacts))
}

func artifactBatchGroupID(artifacts []artifactCreationRecord) string {
	if len(artifacts) > 1 {
		return NewParallelGroupID()
	}
	return ""
}

func (a *Agent) publishArtifactNotificationSignals(ctx context.Context, sp *StatusPublisher, artifacts []artifactCreationRecord) {
	groupID := artifactBatchGroupID(artifacts)
	for _, art := range artifacts {
		status := art.Status
		if status == "" {
			status = "success"
		}
		args := map[string]any{
			"filename": art.Filename,
			"version":  art.Version,
			"status":   status,
		}
		resultData := map[string]any{
			"status":   status,
			"filename": art.Filename,
			"version":  art.Version,
		}
		if status == "success" {
			resultData["message"] = "Artifact has been created and provided to the requester"
		} else {
			resultData["message"] = art.ErrorMsg
			resultData["error"] = art.ErrorMsg
		}
		sp.PublishToolInvocationStart(ctx, "_notify_artifact_save", args, art.CallID, groupID)
		sp.PublishToolResult(ctx, "_notify_artifact_save", resultData, art.CallID)
	}
}

func (a *Agent) injectArtifactNotificationsWithGroupID(ctx context.Context, sp *StatusPublisher, rawContent string, artifacts []artifactCreationRecord, messages *[]llm.ChatMessage, parallelGroupID string) {
	assistantMsg := llm.ChatMessage{
		Role:    "assistant",
		Content: rawContent,
	}

	var toolResults []llm.ChatMessage

	for _, art := range artifacts {
		callID := art.CallID

		status := art.Status
		if status == "" {
			status = "success"
		}

		args := map[string]any{
			"filename": art.Filename,
			"version":  art.Version,
			"status":   status,
		}
		argsJSON, err := json.Marshal(args)
		if err != nil {
			a.logger.Debug("failed to marshal artifact notification args", "filename", art.Filename, "error", err)
			argsJSON = []byte("{}")
		}

		resultData := map[string]any{
			"status":   status,
			"filename": art.Filename,
			"version":  art.Version,
		}
		if status == "success" {
			resultData["message"] = "Artifact has been created and provided to the requester"
		} else {
			resultData["message"] = art.ErrorMsg
			resultData["error"] = art.ErrorMsg
		}

		llmResult := resultData
		if status == "success" {
			llmResult = map[string]any{
				"status":   status,
				"filename": art.Filename,
				"version":  art.Version,
				"message":  resultData["message"],
				"instructions": "The artifact is saved and already delivered to the user. " +
					"Refer to it by filename if needed. Do NOT repeat or re-summarize any text " +
					"you already wrote before this save. End your turn now unless you have a " +
					"genuinely new next step (e.g. another tool call or new information).",
			}
		}
		resultJSON, err := json.Marshal(llmResult)
		if err != nil {
			a.logger.Debug("failed to marshal artifact notification result", "filename", art.Filename, "error", err)
			resultJSON = []byte("{}")
		}

		sp.PublishToolInvocationStart(ctx, "_notify_artifact_save", args, callID, parallelGroupID)
		sp.PublishToolResult(ctx, "_notify_artifact_save", resultData, callID)

		assistantMsg.ToolCalls = append(assistantMsg.ToolCalls, llm.ToolCall{
			ID:        callID,
			Name:      "_notify_artifact_save",
			Arguments: string(argsJSON),
		})
		toolResults = append(toolResults, llm.ChatMessage{
			Role:       "tool",
			Content:    string(resultJSON),
			ToolCallID: callID,
			ToolName:   "_notify_artifact_save",
		})
	}

	*messages = append(*messages, assistantMsg)
	*messages = append(*messages, toolResults...)
}

func (a *Agent) injectBlockValidationFeedback(ctx context.Context, sp *StatusPublisher, bve *blockValidationError, messages *[]llm.ChatMessage) {
	callID := "block-validate-" + idgen.New()

	historyContent := bve.RawText
	if historyContent == "" {
		historyContent = bve.VisibleText
	}

	filename := bve.Filename

	args := map[string]any{
		"filename": filename,
		"version":  0,
		"status":   "error",
	}
	argsJSON, err := json.Marshal(args)
	if err != nil {
		a.logger.Debug("failed to marshal block validation args", "error", err)
		argsJSON = []byte("{}")
	}

	resultData := map[string]any{
		"filename": filename,
		"version":  0,
		"status":   "error",
		"message":  bve.ErrorMsg,
	}
	resultJSON, err := json.Marshal(resultData)
	if err != nil {
		a.logger.Debug("failed to marshal block validation result", "error", err)
		resultJSON = []byte("{}")
	}

	const toolName = "_notify_artifact_save"
	sp.PublishToolInvocationStart(ctx, toolName, args, callID, "")
	sp.PublishToolResult(ctx, toolName, resultData, callID)

	assistantMsg := llm.ChatMessage{
		Role:      "assistant",
		Content:   historyContent,
		Reasoning: bve.Reasoning,
		ToolCalls: []llm.ToolCall{{
			ID:        callID,
			Name:      toolName,
			Arguments: string(argsJSON),
		}},
	}

	*messages = append(*messages, assistantMsg)
	*messages = append(*messages, llm.ChatMessage{
		Role:       "tool",
		Content:    string(resultJSON),
		ToolCallID: callID,
		ToolName:   toolName,
	})
}

func applyCacheControl(req *llm.ChatRequest, strategy string) {
	cc := &llm.CacheControl{Type: "ephemeral"}
	if strategy == "1h" {
		cc.TTL = "1h"
	}

	for i := range req.Messages {
		req.Messages[i].CacheControl = nil
	}
	for i := range req.Tools {
		req.Tools[i].CacheControl = nil
	}

	for i := range req.Messages {
		if req.Messages[i].Role == "system" && !req.Messages[i].NoCacheBreakpoint {
			req.Messages[i].CacheControl = cc
			break
		}
	}

	if len(req.Tools) > 0 {
		req.Tools[len(req.Tools)-1].CacheControl = cc
	}

	marked := 0
	for i := len(req.Messages) - 1; i >= 0 && marked < 2; i-- {
		m := &req.Messages[i]
		if m.Role == "system" || m.CacheControl != nil || m.NoCacheBreakpoint {
			continue
		}
		if strings.TrimSpace(m.Content) == "" && len(m.ContentBlocks) == 0 {
			continue
		}
		m.CacheControl = cc
		marked++
	}
}

func filterNotifyArtifactCalls(toolCalls []llm.ToolCall, logger *slog.Logger) (filtered []llm.ToolCall, errorMsgs []llm.ChatMessage) {
	filtered = make([]llm.ToolCall, 0, len(toolCalls))
	for _, tc := range toolCalls {
		if tc.Name == "_notify_artifact_save" && !strings.HasPrefix(tc.ID, hostNotifyCallIDPrefix) {
			logger.Warn("dropping LLM-generated _notify_artifact_save call — synthesising error so the LLM retries", "id", tc.ID)
			errorMsgs = append(errorMsgs, llm.ChatMessage{
				Role:       "tool",
				ToolCallID: tc.ID,
				ToolName:   tc.Name,
				Content:    "_notify_artifact_save is an internal side-effect tool — you must not invoke it. The system fires it automatically when YOU create an artifact via a fenced ```yaml ... ``` block (or another artifact-producing path). Re-issue your previous intent without calling _notify_artifact_save; if you meant to save an artifact, emit the fenced block directly instead.",
			})
			continue
		}
		filtered = append(filtered, tc)
	}
	return filtered, errorMsgs
}

func directiveCorrection(name string) (string, bool) {
	switch {
	case isBlockType(name):
		params := "<parameters>"
		if name == "save_artifact" {
			params = `filename="name.ext" mime_type="text/plain" description="what it is"`
		}
		return name + " is not a tool. Write it as a fenced block in your response text: " +
			BlockDelimiterOpen + name + ": " + params + "\n<content>\n" +
			BlockDelimiterClose + "\nRe-issue your previous intent that way, without calling " + name + ".", true
	case earlyEmbedTypes[name]:
		return name + " is not a tool. Write it inline in your response text as " + EmbedOpen + name + ":..." + EmbedClose +
			", then continue with your work, without calling " + name + ".", true
	}
	return "", false
}

func redirectDirectiveCalls(toolCalls []llm.ToolCall, registered func(string) bool, logger *slog.Logger) (kept []llm.ToolCall, errorMsgs []llm.ChatMessage) {
	kept = make([]llm.ToolCall, 0, len(toolCalls))
	for _, tc := range toolCalls {
		correction, isDirective := directiveCorrection(tc.Name)
		if !isDirective || registered(tc.Name) {
			kept = append(kept, tc)
			continue
		}
		logger.Warn("answering a directive called as a tool with its text form", "id", tc.ID, "name", tc.Name)
		errorMsgs = append(errorMsgs, llm.ChatMessage{Role: "tool", ToolCallID: tc.ID, ToolName: tc.Name, Content: correction})
	}
	return kept, errorMsgs
}

var validToolName = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_-]*$`)

func sanitizeToolCalls(toolCalls []llm.ToolCall, logger *slog.Logger) (valid []llm.ToolCall, errorMsgs []llm.ChatMessage) {
	valid = make([]llm.ToolCall, 0, len(toolCalls))
	for _, tc := range toolCalls {
		if tc.Name == "" || strings.HasPrefix(tc.Name, "$") || !validToolName.MatchString(tc.Name) {
			logger.Warn("dropping tool call with invalid name", "id", tc.ID, "name", tc.Name)
			errText := fmt.Sprintf(
				`{"error": "Invalid tool name '%s'. Tool names must start with a letter or underscore and contain only letters, digits, underscores, and hyphens. Please retry with a valid tool name from your available tools."}`,
				tc.Name,
			)
			errorMsgs = append(errorMsgs, llm.ChatMessage{
				Role:       "tool",
				Content:    errText,
				ToolCallID: tc.ID,
				ToolName:   tc.Name,
			})
			continue
		}
		valid = append(valid, tc)
	}
	return valid, errorMsgs
}

const truncatedToolArgsError = `{"error": "The arguments for %s were cut off because your response reached the output token limit before the call was complete, so the tool was not run. Retry with smaller arguments: split the work across several calls, or save large content to an artifact first and pass its name."}`

const malformedToolArgsError = `{"error": "The arguments for %s were not a valid JSON object, so the tool was not run. Retry the call with its arguments as one JSON object."}`

const finishReasonContextWindowExceeded = "model_context_window_exceeded"

func isOutputLimitFinish(finishReason string) bool {
	switch finishReason {
	case "length", "max_tokens", finishReasonContextWindowExceeded:
		return true
	}
	return false
}

func isNoArgumentsCall(args string) bool {
	t := strings.TrimSpace(args)
	return t == "" || t == "null"
}

func rejectMalformedToolArgs(toolCalls []llm.ToolCall, finishReason string, logger *slog.Logger) (valid []llm.ToolCall, errorMsgs []llm.ChatMessage) {
	valid = make([]llm.ToolCall, 0, len(toolCalls))
	truncated := isOutputLimitFinish(finishReason)
	for _, tc := range toolCalls {
		if isNoArgumentsCall(tc.Arguments) || !llm.IsInvalidToolArgs(tc.Arguments) {
			valid = append(valid, tc)
			continue
		}
		logger.Warn("rejecting tool call with malformed arguments",
			"id", tc.ID, "tool", tc.Name, "argsLen", len(tc.Arguments), "finishReason", finishReason)
		errTemplate := malformedToolArgsError
		if truncated {
			errTemplate = truncatedToolArgsError
		}
		errorMsgs = append(errorMsgs, llm.ChatMessage{
			Role:       "tool",
			Content:    fmt.Sprintf(errTemplate, tc.Name),
			ToolCallID: tc.ID,
			ToolName:   tc.Name,
		})
	}
	return valid, errorMsgs
}

func clearMalformedToolArgs(toolCalls []llm.ToolCall) []llm.ToolCall {
	hasMalformed := func(tc llm.ToolCall) bool {
		return strings.TrimSpace(tc.Arguments) != "" && llm.IsInvalidToolArgs(tc.Arguments)
	}
	if !slices.ContainsFunc(toolCalls, hasMalformed) {
		return toolCalls
	}
	cleared := slices.Clone(toolCalls)
	for i := range cleared {
		if hasMalformed(cleared[i]) {
			cleared[i].Arguments = "{}"
		}
	}
	return cleared
}

func (a *Agent) resolveEmbedsInArgs(ctx context.Context, resolver *EmbedResolver, toolName string, args map[string]any) (map[string]any, string) {
	var rawArgs []string
	if t := a.tools.GetTool(toolName); t != nil {
		rawArgs = t.RawStringArgs()
	}
	args = resolver.ResolveToolArgs(ctx, args, rawArgs)
	resolvedJSON, err := json.Marshal(args)
	if err != nil {
		return args, ""
	}
	return args, string(resolvedJSON)
}

func (a *Agent) prepareToolCall(ctx context.Context, sp *StatusPublisher, toolName string, tCtx *tool.Context) *tool.Result {
	if refused := a.refuseUnloadedSkillTool(toolName, tCtx); refused != nil {
		return refused
	}

	if len(a.cfg.ToolVolumeBindings) > 0 {
		volumeBindings, resolveErr := a.resolveVolumeBindings(toolName, &volumeResolveCtx{
			ctx:             ctx,
			sp:              sp,
			userID:          tCtx.UserID,
			sessionID:       tCtx.SessionID,
			callerVolumeIDs: tCtx.CallerVolumeIDs,
			canPrompt:       tCtx.CanPromptUser(),
		})

		if resolveErr != nil {
			return tool.Error(fmt.Sprintf("volume resolution for %s: %v", toolName, resolveErr))
		}
		if len(volumeBindings) > 0 {
			tCtx.VolumeBindings = volumeBindings
		}
	}

	if len(a.cfg.ToolNetworkBindings) > 0 {
		networkBindings, resolveErr := a.resolveNetworkBindings(toolName)
		if resolveErr != nil {
			return tool.Error(fmt.Sprintf("network resolution for %s: %v", toolName, resolveErr))
		}
		if len(networkBindings) > 0 {
			tCtx.NetworkBindings = networkBindings
		}
	}
	return nil
}

func (a *Agent) dispatchToolCallsWithStatus(ctx context.Context, toolCalls []llm.ToolCall, toolCtx *tool.Context, sp *StatusPublisher, parallelGroupID string) []*tool.Result {
	calls := make([]tool.Call, len(toolCalls))
	for i, tc := range toolCalls {
		calls[i] = tool.Call{
			ID:        tc.ID,
			Name:      tc.Name,
			Arguments: tc.Arguments,
		}
	}

	results := make([]*tool.Result, len(toolCalls))

	var resolver *EmbedResolver
	if toolCtx != nil && toolCtx.ArtifactService != nil {
		resolver = NewEmbedResolver(toolCtx.ArtifactService, a.cfg.Name,
			toolCtx.UserID, toolCtx.SessionID, tool.GetUserTimezone(toolCtx), a.logger)
	}

	var wg sync.WaitGroup

	for i, tc := range toolCalls {
		var args map[string]any
		if err := json.Unmarshal([]byte(tc.Arguments), &args); err != nil {
			args = map[string]any{"raw": tc.Arguments}
		}

		if resolver != nil {
			var resolvedJSON string
			args, resolvedJSON = a.resolveEmbedsInArgs(ctx, resolver, tc.Name, args)
			if resolvedJSON != "" {
				calls[i].Arguments = resolvedJSON
			}
		}

		sp.PublishToolInvocationStart(ctx, tc.Name, args, tc.ID, parallelGroupID)

		callToolCtx := *toolCtx
		callToolCtx.PrepareCall(tc.ID, tc.Name)
		idx := i
		call := calls[i]
		toolName := tc.Name
		callID := tc.ID
		tCtx := &callToolCtx
		callArgs := args
		wg.Go(func() {
			defer func() {
				if r := recover(); r != nil {
					a.logger.Error("tool dispatch panic recovered", "tool", toolName, "panic", r)
					results[idx] = tool.Error(fmt.Sprintf("internal error in tool %s: panic: %v", toolName, r))
					panicCtx, panicCancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer panicCancel()
					a.publishToolResult(panicCtx, sp, toolName, results[idx], toolCtx, callID)
				}
			}()
			a.dispatchOneToolCall(ctx, sp, toolCtx, results, toolCallInFlight{
				call: call, toolName: toolName, callID: callID, tCtx: tCtx, callArgs: callArgs, idx: idx,
			})
		})
	}

	wg.Wait()
	return results
}

type toolCallInFlight struct {
	call     tool.Call
	toolName string
	callID   string
	tCtx     *tool.Context
	callArgs map[string]any
	idx      int
}

func (a *Agent) dispatchOneToolCall(ctx context.Context, sp *StatusPublisher, toolCtx *tool.Context, results []*tool.Result, c toolCallInFlight) {
	call, toolName, callID, tCtx, idx := c.call, c.toolName, c.callID, c.tCtx, c.idx
	if refused := a.prepareToolCall(ctx, sp, toolName, tCtx); refused != nil {
		results[idx] = refused
		a.publishToolResult(ctx, sp, toolName, refused, toolCtx, callID)
		return
	}

	if !a.approveGatedToolCall(ctx, sp, toolCtx, results, c) {
		return
	}

	result := a.executeToolCall(ctx, call, tCtx)
	results[idx] = result
	a.publishToolResult(ctx, sp, toolName, result, toolCtx, callID)
}

func (a *Agent) approveGatedToolCall(ctx context.Context, sp *StatusPublisher, toolCtx *tool.Context, results []*tool.Result, c toolCallInFlight) bool {
	toolName, callID, tCtx, callArgs, idx := c.toolName, c.callID, c.tCtx, c.callArgs, c.idx
	hilCfg, ok := a.cfg.HILToolConfigs[toolName]
	if !ok || !tool.ShouldGateCall(a.tools.GetTool(toolName), hilCfg, callArgs) {
		return true
	}
	if hilCfg.Approvers.Configured() && tCtx != nil && a2a.NormalizeTrigger(tCtx.Trigger) == a2a.TriggerEval {
		denied := tool.Error(fmt.Sprintf("Tool %q requires human approval, but this is an evaluation run and no human is "+
			"asked during one. Denied for safety.", toolName))
		results[idx] = denied
		a.auditApprovalRefusal(ctx, tCtx, toolName, tool.RefusalNoHumanInEvalRun)
		a.publishToolResult(ctx, sp, toolName, denied, toolCtx, callID)
		return false
	}
	if !tCtx.CanPromptUser() && !hilCfg.Approvers.Configured() {
		denied := tool.Error(fmt.Sprintf("Tool %q requires human approval, but this channel "+
			"can't show an approval prompt. Denied for safety — use the web UI to approve.", toolName))
		results[idx] = denied
		a.auditApprovalRefusal(ctx, tCtx, toolName, tool.RefusalNoApprovalRoute)
		a.publishToolResult(ctx, sp, toolName, denied, toolCtx, callID)
		return false
	}
	if a.pausesToolApprovals(tCtx) && !hilCfg.Approvers.Configured() {
		pending, refusal := a.pauseForToolApproval(ctx, sp, toolName, callArgs, hilCfg, tCtx)
		results[idx] = pending
		if refusal != "" {
			a.auditApprovalRefusal(ctx, tCtx, toolName, refusal)
			a.publishToolResult(ctx, sp, toolName, pending, toolCtx, callID)
		}
		return false
	}
	approvalResult, refusal := a.checkToolApproval(ctx, sp, toolName, callArgs, hilCfg, tCtx)
	if approvalResult != nil {
		results[idx] = approvalResult
		a.auditApprovalRefusal(ctx, tCtx, toolName, refusal)
		a.publishToolResult(ctx, sp, toolName, approvalResult, toolCtx, callID)
		return false
	}
	tCtx.ApprovalReason = tool.ApprovalHuman
	return true
}

func (a *Agent) executeToolCall(ctx context.Context, call tool.Call, tCtx *tool.Context) *tool.Result {
	toolName := call.Name
	var toolMon *observability.Monitor
	if _, isRemote := a.tools.GetTool(toolName).(*remote.Tool); isRemote {
		toolMon = observability.NewRemoteToolMonitor(a.svc.Metrics(), toolName)
	} else {
		toolMon = observability.NewToolMonitor(a.svc.Metrics(), toolName)
	}

	authorisedBy := tCtx.ApprovalReason
	start := time.Now()
	result, dispatchErr := a.tools.Dispatch(ctx, call, tCtx)
	toolDuration := time.Since(start)
	if dispatchErr == nil && isPausedRemoteToolCall(result) {
		return result
	}
	if dispatchErr != nil {
		result = tool.DispatchFailure(dispatchErr)
	}

	a.reportToolOutcome(ctx, toolOutcome{
		toolName:     toolName,
		caller:       tCtx.Caller(),
		sessionID:    tCtx.SessionID,
		traceID:      tCtx.TraceID,
		authorisedBy: authorisedBy,
		duration:     toolDuration,
		monitor:      toolMon,
	}, result, dispatchErr)
	return result
}

func isPausedRemoteToolCall(result *tool.Result) bool {
	return result != nil && result.Status == tool.StatusPending && classifyPendingKind(result) == PendingCallRemoteTool
}

type toolOutcome struct {
	toolName     string
	caller       auth.CallerIdentity
	sessionID    string
	traceID      string
	authorisedBy tool.ApprovalReason
	duration     time.Duration
	monitor      *observability.Monitor
}

func (a *Agent) reportToolOutcome(ctx context.Context, outcome toolOutcome, result *tool.Result, dispatchErr error) {
	toolName := outcome.toolName
	durationMs := float64(outcome.duration.Milliseconds())

	provenance, _ := a.ToolProvenanceFor(toolName)
	execSuccess := dispatchErr == nil && result != nil && !isFailedToolResult(result)
	execError := ""
	toolErrorCode, toolErrorSite, toolErrorClass := "", "", ""
	if dispatchErr != nil {
		execError = classifyDispatchError(dispatchErr)
		a.logger.Error("tool dispatch failed",
			"tool", toolName, "duration_ms", durationMs,
			"errorType", execError, "errorLen", len(dispatchErr.Error()))
	} else if isFailedToolResult(result) {
		execError = tool.AuditToolErrorCode
		toolErrorCode, toolErrorSite, toolErrorClass = builtinFailureContext(provenance.Category, result)
		a.logger.Warn("tool returned error",
			"tool", toolName, "duration_ms", durationMs,
			"messageLength", len(toolResultMessage(result)))
	}

	if result != nil && result.Status == tool.StatusPending {
		outcome.monitor.StopWithErrorType("paused")
	} else {
		outcome.monitor.StopWithErrorType(execError)
	}

	a.svc.AuditLogger().LogToolExecution(ctx, auth.ToolExecutionEvent{
		UserID:          outcome.caller.Sub,
		CredentialID:    outcome.caller.CredentialID,
		AgentName:       a.cfg.Name,
		ToolName:        toolName,
		ToolCategory:    provenance.Category,
		SessionID:       outcome.sessionID,
		TraceID:         outcome.traceID,
		DurationMs:      durationMs,
		Success:         execSuccess,
		Error:           execError,
		ToolErrorCode:   toolErrorCode,
		ToolErrorSite:   toolErrorSite,
		ToolErrorClass:  toolErrorClass,
		ApprovalReason:  string(outcome.authorisedBy),
		ApprovalRefusal: string(backstopRefusal(result)),
	})
}

func (a *Agent) reportDeliveredRemoteToolCall(ctx context.Context, toolName string, caller auth.CallerIdentity, sessionID, traceID string, startedAt time.Time, result *tool.Result) {
	a.reportToolOutcome(context.WithoutCancel(ctx), toolOutcome{
		toolName:  toolName,
		caller:    caller,
		sessionID: sessionID,
		traceID:   traceID,
		duration:  time.Since(startedAt),
		monitor:   observability.NewRemoteToolMonitorSince(a.svc.Metrics(), toolName, startedAt),
	}, result, nil)
}

func isFailedToolResult(result *tool.Result) bool {
	return result != nil && (result.Status == tool.StatusError || result.Status == tool.StatusFailed)
}

func builtinFailureContext(category string, result *tool.Result) (code, site, class string) {
	if category != taxonomy.ToolCategoryBuiltin {
		return "", "", ""
	}
	return toolResultErrorCode(result), result.ErrorSite, result.ErrorClass
}

func toolResultErrorCode(result *tool.Result) string {
	if result.FailedDetail != nil && result.FailedDetail.Code != "" {
		return result.FailedDetail.Code
	}
	return result.ErrorCode
}

func toolResultData(result *tool.Result, inlineCap, ceiling int, hasArtifactService bool) any {
	if result == nil {
		return nil
	}
	previews, omitted := inlineDataObjectPreviews(result.DataObjects, inlineCap, ceiling, hasArtifactService)
	if len(previews) > 0 {
		return dataObjectResultData(result, previews, omitted)
	}
	return tool.PublishedResultData(result)
}

func dataObjectResultData(result *tool.Result, previews []inlineOutput, omitted int) map[string]any {
	payload := map[string]any{}
	if result.Status == tool.StatusError || result.Status == tool.StatusFailed {
		payload["status"] = result.Status
	}
	if result.Message != "" {
		payload["message"] = result.Message
	}
	if result.ErrorCode != "" {
		payload["error_code"] = result.ErrorCode
	}
	for k, v := range result.Data {
		payload[k] = v
	}
	applyInlineOutputs(payload, previews, omitted > 0)
	if omitted > 0 {
		payload["omitted_outputs"] = omitted
	}
	return payload
}

func inlineDataObjectPreviews(objects []tool.DataObject, inlineCap, ceiling int, hasArtifactService bool) (previews []inlineOutput, omitted int) {
	remaining := maxStatusEventTextBytes
	for _, do := range objects {
		plan := planDataObject(do, effectiveInlineCap(do, inlineCap, ceiling), hasArtifactService)
		if !plan.inline {
			continue
		}
		if remaining < minStatusPreviewBytes && len(plan.text) > remaining {
			omitted++
			continue
		}
		capped, truncated := capStatusText(plan.text, remaining)
		remaining -= len(capped)
		previews = append(previews, inlineOutput{name: do.Name, content: capped, truncated: truncated})
	}
	return previews, omitted
}

func effectiveInlineCap(do tool.DataObject, inlineCap, ceiling int) int {
	if do.InlineMaxBytes > 0 {
		return min(do.InlineMaxBytes, ceiling)
	}
	return inlineCap
}

const (
	DefaultToolOutputSaveThresholdBytes = 8192
	DefaultToolOutputLLMReturnMaxBytes  = 65536
)

const (
	defaultToolOutputSaveThresholdBytes = 8192
	defaultToolOutputLLMReturnMaxBytes  = 102400
)

func (a *Agent) toolOutputCaps() (inlineCap, ceiling int) {
	inlineCap = a.cfg.ToolOutputSaveThresholdBytes
	if inlineCap <= 0 {
		inlineCap = defaultToolOutputSaveThresholdBytes
	}
	ceiling = a.cfg.ToolOutputLLMReturnMaxBytes
	if ceiling <= 0 {
		ceiling = defaultToolOutputLLMReturnMaxBytes
	}
	return inlineCap, ceiling
}

func (a *Agent) publishToolResult(ctx context.Context, sp *StatusPublisher, toolName string, result *tool.Result,
	toolCtx *tool.Context, callID string) {
	sp.PublishToolResultWithPartMeta(ctx, toolName, a.publishedToolResult(result, toolCtx), callID, toolResultPartMeta(result))
}

func toolResultPartMeta(result *tool.Result) map[string]any {
	if result == nil || result.LoadedSkillContentHash == "" {
		return nil
	}
	return map[string]any{a2a.PartMetaSkillContentHash: result.LoadedSkillContentHash}
}

func (a *Agent) publishedToolResult(result *tool.Result, toolCtx *tool.Context) any {
	if a.cfg.ToolResultProcessingEnabled != nil && !*a.cfg.ToolResultProcessingEnabled {
		return tool.PublishedResultData(result)
	}
	inlineCap, ceiling := a.toolOutputCaps()
	return toolResultData(result, inlineCap, ceiling, toolCtx.ArtifactService != nil)
}

func toolResultMessage(result *tool.Result) string {
	if result == nil {
		return ""
	}
	if result.Message != "" {
		return result.Message
	}
	if result.Data != nil {
		return fmt.Sprintf("%v", result.Data)
	}
	return ""
}

type toolDefinition struct {
	description string
	parameters  map[string]any
}

var conciseToolDefinitions = map[string]toolDefinition{
	"grep_skill_resources": {
		description: "Regex search over a loaded skill's resource files. Returns path, line and text for each match.",
		parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"skill_name":       map[string]any{"type": "string", "description": "The loaded skill's name."},
				"pattern":          map[string]any{"type": "string", "description": "RE2 regular expression."},
				"case_insensitive": map[string]any{"type": "boolean"},
				"context_lines":    map[string]any{"type": "integer"},
			},
			"required": []any{"pattern", "skill_name"},
		},
	},
	"read_skill_resource": {
		description: "Read a resource file of a loaded skill, optionally only start_line to end_line.",
		parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"skill_name": map[string]any{"type": "string", "description": "The loaded skill's name."},
				"path":       map[string]any{"type": "string", "description": "A file path from the load_skill result or a grep match, e.g. references/api.md."},
				"start_line": map[string]any{"type": "integer"},
				"end_line":   map[string]any{"type": "integer"},
			},
			"required": []any{"path", "skill_name"},
		},
	},
	"query_data_with_sql":             {description: "Run one read-only SELECT over data artifacts (CSV, JSON, YAML, SQLite), each loaded as a table in in-memory SQLite. input_files maps a table name to an artifact filename. Pre-aggregate each table in a CTE before a one-to-many join, or sums inflate."},
	"merge_structured_data":           {description: "Deep-merge a patch into a JSON or YAML artifact (RFC 7396): objects merge, arrays and scalars are replaced, null deletes a key. Saves a new version."},
	"reinterpret_artifact":            {description: "Re-save an artifact's bytes unchanged under a new filename or MIME type, e.g. so the data tools accept a YAML file saved as text."},
	"transform_data_with_jmespath":    {description: "Apply a JMESPath expression to a JSON, YAML or CSV artifact (CSV rows become objects) and save the result as JSON. The expression starts at the document's top level."},
	"create_chart_from_plotly_config": {description: "Render a Plotly config (a JSON or YAML string) to an image artifact, PNG by default."},
	"mermaid_diagram_generator":       {description: "Render Mermaid syntax to a PNG (default) or SVG artifact."},
}

func shapeTools(defs []llm.ToolDefinition) []llm.ToolDefinition {
	out := defs[:0]
	for _, d := range defs {
		if strings.HasPrefix(d.Name, peerToolPrefix) {
			d.Parameters = peerToolParameters
		}
		if concise, ok := conciseToolDefinitions[d.Name]; ok {
			d.Description = concise.description
			if concise.parameters != nil {
				d.Parameters = concise.parameters
			}
		}
		out = append(out, d)
	}
	return out
}

func (a *Agent) buildLLMTools(ctx context.Context, identity *auth.Identity, skillState tool.SkillStateProvider, projectID string) []llm.ToolDefinition {
	defs := a.tools.ToLLMDefinitions()

	if projectID == "" {
		filtered := make([]tool.LLMToolDefinition, 0, len(defs))
		for _, d := range defs {
			if d.Name == "index_search" {
				continue
			}
			filtered = append(filtered, d)
		}
		defs = filtered
	}

	if identity != nil {
		infos := make([]auth.ToolInfo, 0, len(defs))
		for _, d := range defs {
			var scopes []string
			if t := a.tools.GetTool(d.Name); t != nil {
				scopes = tool.RequiredScopesOf(t)
			}
			infos = append(infos, auth.ToolInfo{Name: d.Name, RequiredScopes: scopes})
		}
		allowed := a.svc.ToolAuthorizer().FilterTools(ctx, identity, infos)
		allowedSet := make(map[string]struct{}, len(allowed))
		for _, ti := range allowed {
			allowedSet[ti.Name] = struct{}{}
		}
		filtered := make([]tool.LLMToolDefinition, 0, len(allowed))
		for _, d := range defs {
			if _, ok := allowedSet[d.Name]; ok {
				filtered = append(filtered, d)
			}
		}
		defs = filtered
	}

	defs = a.filterUnauthorizedPeerTools(ctx, identity, defs)

	out := make([]llm.ToolDefinition, 0, len(defs)+1)
	for _, d := range defs {
		out = append(out, llm.ToolDefinition{
			Name:        d.Name,
			Description: d.Description,
			Parameters:  d.Parameters,
		})
	}

	out = append(out, llm.ToolDefinition{
		Name:        "_notify_artifact_save",
		Description: "CRITICAL: _notify_artifact_save is automatically invoked by the system as a side-effect when you create artifacts. You should NEVER call this tool yourself. The system will call it for you and provide the results in your next turn. If you manually invoke it, you are making an error.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"filename": map[string]any{
					"type":        "string",
					"description": "The name of the artifact that was saved.",
				},
				"version": map[string]any{
					"type":        "integer",
					"description": "The version number of the saved artifact.",
				},
				"status": map[string]any{
					"type":        "string",
					"description": "The status of the save operation.",
				},
			},
			"required": []string{"filename", "version", "status"},
		},
	})

	if a.autoContinuationEnabled() {
		out = append(out, llm.ToolDefinition{
			Name:        "_continue_generation",
			Description: "INTERNAL TOOL. This tool is used by the system to continue a response that was interrupted. You MUST NOT call this tool directly.",
			Parameters: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		})
	}

	if a.skillManager != nil && skillState != nil {
		filtered := out[:0]
		for _, d := range out {
			if _, hidden := a.skillToolHidden(d.Name, skillState); hidden {
				continue
			}
			if isResourceTool(d.Name) && !a.skillManager.HasAnyResources() {
				continue
			}
			if d.Name == "instantiate_template" || d.Name == "read_template" {
				if !a.skillManager.HasAnyAssets() && !skillState.IsLoaded("sam-templates") {
					continue
				}
			}
			if (d.Name == "verify_template_candidate" || d.Name == "package_template") && !skillState.IsLoaded("sam-templates") {
				continue
			}
			filtered = append(filtered, d)
		}
		out = filtered
	}

	out = shapeTools(out)
	a.markDeferredTools(out)
	return out
}

func llmToolsInclude(defs []llm.ToolDefinition, name string) bool {
	for _, d := range defs {
		if d.Name == name {
			return true
		}
	}
	return false
}

func (a *Agent) injectLastCallWarning(messages []llm.ChatMessage) []llm.ChatMessage {
	out := make([]llm.ChatMessage, len(messages), len(messages)+1)
	copy(out, messages)
	if len(out) == 0 {
		return out
	}

	return append(out, trailingReminder(lastCallWarningText))
}

func trailingReminder(text string) llm.ChatMessage {
	return llm.ChatMessage{
		Role:              "user",
		Content:           "<system-reminder>\n" + text + "\n</system-reminder>",
		NoCacheBreakpoint: true,
	}
}

func (a *Agent) appendPerRequestContext(messages []llm.ChatMessage, gc gatewayContext) []llm.ChatMessage {
	ctxMsgs := a.perRequestContextMessages(gc)
	if len(ctxMsgs) == 0 {
		return messages
	}
	out := make([]llm.ChatMessage, len(messages), len(messages)+len(ctxMsgs))
	copy(out, messages)
	return append(out, ctxMsgs...)
}

func (a *Agent) appendStructuredInstructions(messages []llm.ChatMessage, rc *taskRunContext) []llm.ChatMessage {
	if rc.structuredCtx == nil {
		return messages
	}
	out := make([]llm.ChatMessage, len(messages), len(messages)+1)
	copy(out, messages)
	sc := rc.structuredCtx
	text := workflowNodeReminder(sc.Request.NodeID, sc.Request.WorkflowName, sc.Request.SuggestedOutputFilename, sc.OutputSchema)
	return append(out, trailingReminder(text))
}

func (a *Agent) perRequestContextMessages(gc gatewayContext) []llm.ChatMessage {
	var sections []string
	if a.cfg.InjectUserProfile && strings.TrimSpace(gc.UserProfile) != "" {
		sections = append(sections, "## Inquiring User Profile\n\n"+gc.UserProfile)
	}
	if strings.TrimSpace(gc.ConversationContext) != "" {
		sections = append(sections, "## Conversation Context\n\n"+gc.ConversationContext)
	}
	if len(sections) == 0 {
		return nil
	}
	msg := trailingReminder(strings.Join(sections, "\n\n"))
	msg.OmitFromTrace = true
	return []llm.ChatMessage{msg}
}

func (a *Agent) reportModel() string {
	if a.cfg.OriginalModel != "" {
		return a.cfg.OriginalModel
	}
	if a.cfg.Model != "" {
		return a.cfg.Model
	}
	if v := a.resolvedModelName.Load(); v != nil {
		return *v
	}
	return ""
}

func (a *Agent) reportModelCtx(ctx context.Context) string {
	if a.cfg.OriginalModel != "" {
		return a.cfg.OriginalModel
	}
	if a.cfg.Model != "" {
		return a.cfg.Model
	}
	if a.cfg.ModelAlias == "" {
		if v := a.resolvedModelName.Load(); v != nil {
			return *v
		}
		return ""
	}
	if reg := a.svc.Bindings(); reg != nil {
		a.refreshResolvedModelName(ctx, reg)
	}
	if v := a.resolvedModelName.Load(); v != nil {
		return *v
	}
	return ""
}

func (a *Agent) autoContinuationEnabled() bool {
	if a.cfg.EnableAutoContinuation == nil {
		return true
	}
	return *a.cfg.EnableAutoContinuation
}

func (a *Agent) buildToolResultMessage(ctx context.Context, result *tool.Result, toolCtx *tool.Context, sp *StatusPublisher, tc llm.ToolCall) llm.ChatMessage {
	textContent, withheld := a.formatToolResult(ctx, result, toolCtx, sp)
	msg := llm.ChatMessage{
		Role:       "tool",
		Content:    textContent,
		ToolCallID: tc.ID,
		ToolName:   tc.Name,
	}
	if a.countsContextSavings {
		msg.WithheldBytes = withheld
	}
	if len(result.ImageBlocks) > 0 {
		blocks := []llm.ContentBlock{{Type: "text", Text: textContent}}
		for _, ib := range result.ImageBlocks {
			dataURL := visionimage.DataURL(ib.MIMEType, ib.Data)
			blocks = append(blocks, llm.ContentBlock{Type: "image_url", ImageURL: dataURL})
		}
		msg.ContentBlocks = blocks
	}
	return msg
}

func (a *Agent) formatToolResult(ctx context.Context, r *tool.Result, toolCtx *tool.Context, sp *StatusPublisher) (text string, withheldBytes int64) {
	if r == nil {
		return "no result", 0
	}

	if r.ValidationError != "" {
		data, err := json.Marshal(map[string]any{"error": r.ValidationError})
		if err != nil {
			return r.ValidationError, 0
		}
		return string(data), 0
	}

	if len(r.DataObjects) > 0 {
		return a.processDataObjects(ctx, r, toolCtx, sp)
	}

	combined := make(map[string]any)
	combined["status"] = r.Status
	if r.Message != "" {
		combined["message"] = r.Message
	}

	if (r.Status == tool.StatusFailed || r.Status == tool.StatusError) && r.FailedDetail != nil {
		d := r.FailedDetail
		combined["code"] = d.Code
		if d.StderrTail != "" {
			combined["stderr_tail"] = truncateWithMarker(d.StderrTail, 2048)
		}
		if d.StackTrace != "" {
			combined["stack_trace"] = truncateWithMarker(d.StackTrace, 4096)
		}
	}

	for k, v := range llmResultData(r) {
		combined[k] = v
	}
	for k, v := range r.LLMData {
		combined[k] = v
	}

	applyFailureClassification(combined, r)

	if outputFilename, ok := combined["output_filename"].(string); ok && outputFilename != "" && toolCtx != nil && toolCtx.ArtifactService != nil {
		var ver *int
		if v, ok := combined["output_version"]; ok {
			if vi, ok := v.(int); ok {
				ver = &vi
			} else if vf, ok := v.(float64); ok {
				vi := int(vf)
				ver = &vi
			}
		}
		meta := artifact.LoadCompanionMetadata(ctx, toolCtx.ArtifactService,
			toolCtx.AgentName, toolCtx.UserID, toolCtx.SessionID,
			outputFilename, ver)
		if meta != nil {
			if schema, ok := meta["schema"].(map[string]any); ok {
				combined["artifact_schema"] = schema
			}
		}
	}

	data, err := json.Marshal(combined)
	if err != nil {
		return r.Message, r.WithheldBytes
	}
	text, spilled := a.boundOversizedPayload(ctx, r, combined, data, toolCtx, sp)
	return text, r.WithheldBytes + spilled
}

func (a *Agent) processDataObjects(ctx context.Context, r *tool.Result, toolCtx *tool.Context, sp *StatusPublisher) (text string, withheldBytes int64) {
	if a.cfg.ToolResultProcessingEnabled != nil && !*a.cfg.ToolResultProcessingEnabled {
		return passthroughToolResult(r), 0
	}

	inlineCap, ceiling := a.toolOutputCaps()

	inlineOutputs, savedArtifacts, failedArtifacts := a.classifyDataObjects(ctx, r, toolCtx, sp, inlineCap, ceiling)

	result := map[string]any{
		"status":  r.Status,
		"message": r.Message,
	}
	if r.ErrorCode != "" {
		result["error_code"] = r.ErrorCode
	}

	for k, v := range r.Data {
		result[k] = v
	}

	applyFailureClassification(result, r)

	buildArtifactsCreated(ctx, result, savedArtifacts, toolCtx)
	applyInlineOutputs(result, inlineOutputs, false)
	applyFailedArtifacts(result, failedArtifacts)
	if a.savedOutOfReach(savedArtifacts) {
		result["note"] = outOfReachNote
	}

	withheldBytes = r.WithheldBytes + routedWithheldBytes(result, savedArtifacts)
	data, err := json.Marshal(result)
	if err != nil {
		return r.Message, withheldBytes
	}
	return string(data), withheldBytes
}

const outOfReachNote = "This result was too large to show and was saved as a file this agent has no tool to read. " +
	"Call again with a narrower request (fewer rows or columns, or an aggregate)."

var savedFileReaders = []string{loadArtifactToolName, "artifact_grep", "query_data_with_sql"}

const jmespathToolName = "transform_data_with_jmespath"

var jmespathMIMETypes = []string{mimeJSON, mimeYAML, "text/csv"}

func (a *Agent) savedOutOfReach(saved []savedArtifactInfo) bool {
	return slices.ContainsFunc(saved, func(s savedArtifactInfo) bool { return s.OverCap && !a.canReadSaved(s) })
}

func (a *Agent) canReadSaved(s savedArtifactInfo) bool {
	if a.tools == nil {
		return false
	}
	if slices.ContainsFunc(savedFileReaders, func(name string) bool { return a.tools.GetTool(name) != nil }) {
		return true
	}
	return slices.Contains(jmespathMIMETypes, s.MIMEType) && a.tools.GetTool(jmespathToolName) != nil
}

type inlineOutput struct {
	name      string
	content   string
	truncated bool
}

type dataObjectPlan struct {
	save    bool
	inline  bool
	text    string
	failure string
	overCap bool
}

func planDataObject(do tool.DataObject, effectiveCap int, hasArtifactService bool) dataObjectPlan {
	persisted := func() dataObjectPlan {
		if hasArtifactService {
			return dataObjectPlan{save: true}
		}
		return degradedDataObjectPlan(do, effectiveCap)
	}

	switch {
	case do.ArtifactRef != nil:
		if isTextMIME(do.MIMEType) && !savedOnPurpose(do) && do.ArtifactRef.SizeBytes > int64(effectiveCap) {
			return overCap(persisted())
		}
		return persisted()
	case !isTextMIME(do.MIMEType) || !utf8.Valid(do.Content):
		return persisted()
	case savedOnPurpose(do):
		return persisted()
	case do.Disposition == tool.DispositionInline:
		if len(do.Content) <= effectiveCap {
			return dataObjectPlan{inline: true, text: string(do.Content)}
		}
		return overCap(persisted())
	default:
		switch {
		case len(do.Content) == 0:
			return dataObjectPlan{}
		case len(do.Content) <= effectiveCap:
			return dataObjectPlan{save: hasArtifactService, inline: true, text: string(do.Content)}
		default:
			return overCap(persisted())
		}
	}
}

func savedOnPurpose(do tool.DataObject) bool {
	return do.Disposition == tool.DispositionArtifact || do.Disposition == tool.DispositionArtifactWithPreview
}

func overCap(p dataObjectPlan) dataObjectPlan {
	p.overCap = p.save
	return p
}

func degradedDataObjectPlan(do tool.DataObject, effectiveCap int) dataObjectPlan {
	switch {
	case do.ArtifactRef != nil:
		return dataObjectPlan{save: true}
	case len(do.Content) == 0:
		return dataObjectPlan{inline: true,
			text: fmt.Sprintf("[artifact %q: no artifact service available]", do.Name)}
	case !isTextMIME(do.MIMEType) || !utf8.Valid(do.Content):
		return dataObjectPlan{inline: true,
			text:    fmt.Sprintf("[binary content %q (%d bytes) omitted: no artifact service available]", do.Name, len(do.Content)),
			failure: "no artifact service configured; binary content could not be delivered"}
	case len(do.Content) <= effectiveCap:
		return dataObjectPlan{inline: true, text: string(do.Content)}
	default:
		return dataObjectPlan{inline: true,
			text:    strutil.Truncate(string(do.Content), effectiveCap, "... [truncated, no artifact service available]"),
			failure: fmt.Sprintf("no artifact service configured; result truncated to %d of %d bytes", effectiveCap, len(do.Content))}
	}
}

func (a *Agent) classifyDataObjects(ctx context.Context, r *tool.Result, toolCtx *tool.Context, sp *StatusPublisher, inlineCap, ceiling int) ([]inlineOutput, []savedArtifactInfo, []failedArtifactInfo) {
	var inlineOutputs []inlineOutput
	var savedArtifacts []savedArtifactInfo
	var failedArtifacts []failedArtifactInfo

	hasArtifactService := toolCtx.ArtifactService != nil

	for _, do := range r.DataObjects {
		plan := planDataObject(do, effectiveInlineCap(do, inlineCap, ceiling), hasArtifactService)

		if plan.save {
			if hasArtifactService {
				_, info, failed := a.saveDataObjectAsArtifact(ctx, do, toolCtx, sp)
				if info != nil {
					info.WithheldBytes = withheldTextBytes(do, plan, info)
					info.OverCap = plan.overCap
					savedArtifacts = append(savedArtifacts, *info)
				}
				if failed != nil {
					failedArtifacts = append(failedArtifacts, *failed)
				}
			} else if do.ArtifactRef != nil {
				info := savedArtifactInfo{
					Filename:  do.ArtifactRef.Filename,
					Version:   do.ArtifactRef.Version,
					SizeBytes: do.ArtifactRef.SizeBytes,
					MIMEType:  do.MIMEType,
					Tags:      do.Tags,
				}
				info.WithheldBytes = withheldTextBytes(do, plan, &info)
				savedArtifacts = append(savedArtifacts, info)
			}
		}
		if plan.inline {
			inlineOutputs = append(inlineOutputs, inlineOutput{name: do.Name, content: plan.text})
		}
		if plan.failure != "" {
			failedArtifacts = append(failedArtifacts, failedArtifactInfo{Filename: do.Name, Error: plan.failure})
		}
	}
	return inlineOutputs, savedArtifacts, failedArtifacts
}

const artifactsCreatedKey = "artifacts_created"

func buildArtifactsCreated(ctx context.Context, result map[string]any, savedArtifacts []savedArtifactInfo, toolCtx *tool.Context) {
	if len(savedArtifacts) == 0 {
		return
	}
	artList := make([]map[string]any, 0, len(savedArtifacts))
	for _, saved := range savedArtifacts {
		artEntry := map[string]any{"filename": saved.Filename}
		if saved.Version >= 0 {
			artEntry["version"] = saved.Version
		}
		if saved.SizeBytes > 0 {
			artEntry["size_bytes"] = saved.SizeBytes
		}
		if saved.MIMEType != "" {
			artEntry["mime_type"] = saved.MIMEType
		}
		if saved.Description != "" {
			artEntry["description"] = saved.Description
		}
		if saved.Preview != "" {
			artEntry["preview"] = saved.Preview
		}
		artList = append(artList, artEntry)
	}
	result[artifactsCreatedKey] = artList

	if _, hasVersion := result["version"]; hasVersion && len(savedArtifacts) == 1 {
		result["version"] = savedArtifacts[0].Version
	}

	if toolCtx.ArtifactService == nil {
		return
	}
	for _, saved := range savedArtifacts {
		ver := saved.Version
		meta := artifact.LoadCompanionMetadata(ctx, toolCtx.ArtifactService,
			toolCtx.AgentName, toolCtx.UserID, toolCtx.SessionID, saved.Filename, &ver)
		if meta != nil {
			mergeArtifactMetadata(artList, saved.Filename, meta)
		}
	}
}

func mergeArtifactMetadata(artList []map[string]any, filename string, meta map[string]any) {
	for i := range artList {
		if artList[i]["filename"] != filename {
			continue
		}
		for k, v := range meta {
			if _, exists := artList[i][k]; !exists {
				artList[i][k] = v
			}
		}
		return
	}
}

func applyInlineOutputs(result map[string]any, inlineOutputs []inlineOutput, forceList bool) {
	if len(inlineOutputs) == 0 {
		return
	}
	if len(inlineOutputs) == 1 && !forceList {
		result["content"] = inlineOutputs[0].content
		if inlineOutputs[0].truncated {
			result["content_truncated"] = true
		}
		return
	}
	list := make([]map[string]any, 0, len(inlineOutputs))
	for _, io := range inlineOutputs {
		entry := map[string]any{
			"name":    io.name,
			"content": io.content,
		}
		if io.truncated {
			entry["truncated"] = true
		}
		list = append(list, entry)
	}
	result["inline_outputs"] = list
}

func applyFailedArtifacts(result map[string]any, failedArtifacts []failedArtifactInfo) {
	if len(failedArtifacts) == 0 {
		return
	}
	if s, _ := result["status"].(string); s == "" || s == tool.StatusSuccess {
		result["status"] = tool.StatusPartial
	}
	failList := make([]map[string]any, 0, len(failedArtifacts))
	notes := make([]string, 0, len(failedArtifacts))
	for _, f := range failedArtifacts {
		failList = append(failList, map[string]any{
			"filename": f.Filename,
			"error":    f.Error,
		})
		notes = append(notes, fmt.Sprintf("failed to save artifact %q: %s", f.Filename, f.Error))
	}
	result["failed_artifacts"] = failList
	note := "Artifact save failure(s): " + strings.Join(notes, "; ")
	if msg, _ := result["message"].(string); msg != "" {
		result["message"] = msg + " | " + note
	} else {
		result["message"] = note
	}
}

func applyFailureClassification(out map[string]any, r *tool.Result) {
	if r.Status == tool.StatusFailed {
		out["retryable"] = false
	}
	if r.FailedDetail != nil && r.FailedDetail.Guidance != "" {
		out["guidance"] = r.FailedDetail.Guidance
	}
}

func passthroughToolResult(r *tool.Result) string {
	out := map[string]any{"status": r.Status}
	if r.Message != "" {
		out["message"] = r.Message
	}
	for k, v := range r.Data {
		out[k] = v
	}
	applyFailureClassification(out, r)
	b, err := json.Marshal(out)
	if err != nil {
		return r.Message
	}
	return string(b)
}

type savedArtifactInfo struct {
	Filename      string
	Version       int
	SizeBytes     int64
	MIMEType      string
	Description   string
	Preview       string
	Tags          []string
	WithheldBytes int64
	OverCap       bool
}

type failedArtifactInfo struct {
	Filename string
	Error    string
}

func (a *Agent) saveDataObjectAsArtifact(ctx context.Context, do tool.DataObject, toolCtx *tool.Context, sp *StatusPublisher) (string, *savedArtifactInfo, *failedArtifactInfo) {
	if do.ArtifactRef != nil {
		if sp != nil && toolCtx.NotifyArtifactSaved == nil {
			callID := hostNotifyCallIDPrefix + idgen.New()
			version := do.ArtifactRef.Version
			sp.PublishArtifactProgress(ctx, do.ArtifactRef.Filename, "completed",
				int(do.ArtifactRef.SizeBytes), nil, &do.MIMEType, &do.Description,
				&version, &callID, do.Tags)
			sp.PublishArtifactSaved(ctx, do.ArtifactRef.Filename, do.ArtifactRef.Version,
				do.MIMEType, do.ArtifactRef.SizeBytes, &do.Description, &callID, do.Tags)
		}
		return fmt.Sprintf("[Artifact saved: %s (version %d, %d bytes)]",
				do.ArtifactRef.Filename, do.ArtifactRef.Version, do.ArtifactRef.SizeBytes),
			&savedArtifactInfo{
				Filename:  do.ArtifactRef.Filename,
				Version:   do.ArtifactRef.Version,
				SizeBytes: do.ArtifactRef.SizeBytes,
				MIMEType:  do.MIMEType,
				Tags:      do.Tags,
			}, nil
	}

	if len(do.Content) == 0 {
		name := cmp.Or(do.Name, "(unnamed)")
		a.logger.Warn("refusing to save empty artifact content; upstream save may have failed",
			"filename", name, "mimeType", do.MIMEType)
		return fmt.Sprintf("[failed to save artifact %q: no content (upstream save may have failed)]", name), nil,
			&failedArtifactInfo{Filename: name, Error: "artifact content was empty (upstream save may have failed)"}
	}

	filename := do.Name
	if filename == "" {
		filename = generateArtifactFilename(do.MIMEType)
		a.logger.Debug("auto-generated artifact filename", "filename", filename, "mimeType", do.MIMEType)
	}

	if !isFilenameSafe(filename) {
		a.logger.Warn("rejected unsafe artifact filename", "filename", filename)
		return fmt.Sprintf("[failed to save artifact %q: invalid filename]", filename), nil,
			&failedArtifactInfo{Filename: filename, Error: "invalid filename"}
	}

	saveResult, err := artifact.SaveWithMetadata(ctx, toolCtx.ArtifactService,
		toolCtx.AgentName, toolCtx.UserID, toolCtx.SessionID, filename,
		do.Content, do.MIMEType,
		artifact.MetadataOpts{
			Description:    do.Description,
			Custom:         do.Metadata,
			SchemaMaxDepth: a.cfg.SchemaInferenceDepth,
			SchemaMaxKeys:  a.cfg.SchemaMaxKeys,
			Tags:           do.Tags,
		}, a.logger)
	if err != nil {
		a.logger.Error("failed to save tool output artifact", "filename", filename, "error", err)
		return fmt.Sprintf("[failed to save artifact %q: %v]", filename, err), nil,
			&failedArtifactInfo{Filename: filename, Error: err.Error()}
	}
	version := saveResult.DataVersion

	a.logger.Debug("saved tool output as artifact", "filename", filename, "version", version, "size", len(do.Content))

	if sp != nil {
		callID := hostNotifyCallIDPrefix + idgen.New()
		description := fmt.Sprintf("MCP tool output: %s", filename)
		sp.PublishArtifactProgress(ctx, filename, "completed", len(do.Content), nil, &do.MIMEType, &description, &version, &callID, do.Tags)
		sp.PublishArtifactSaved(ctx, filename, version, do.MIMEType, int64(len(do.Content)), &description, &callID, do.Tags)
	}

	preview := a.generatePreview(do)

	ref := fmt.Sprintf("[Artifact saved: %s (version %d, %d bytes, %s)]",
		filename, version, len(do.Content), do.MIMEType)
	if preview != "" {
		ref += "\nPreview: " + preview
	}

	return ref, &savedArtifactInfo{
		Filename:    filename,
		Version:     version,
		SizeBytes:   int64(len(do.Content)),
		MIMEType:    do.MIMEType,
		Description: do.Description,
		Preview:     preview,
		Tags:        do.Tags,
	}, nil
}

func (a *Agent) dataObjectInlineText(do tool.DataObject, maxBytes int) string {
	if len(do.Content) == 0 {
		return ""
	}
	text := string(do.Content)
	return strutil.Truncate(text, maxBytes, fmt.Sprintf("... [truncated, %d total bytes]", len(do.Content)))
}

func generateArtifactFilename(mimeType string) string {
	return artifact.FreshName(mimeType)
}

const defaultToolResultPreviewLengthChars = 500

func (a *Agent) generatePreview(do tool.DataObject) string {
	if do.Preview != "" {
		return do.Preview
	}
	if !utf8.Valid(do.Content) {
		return fmt.Sprintf("[Binary content: %d bytes, mime_type: %s]", len(do.Content), do.MIMEType)
	}
	if len(do.Content) == 0 {
		return "[Empty content]"
	}
	limit := a.cfg.ToolResultPreviewLengthChars
	if limit <= 0 {
		limit = defaultToolResultPreviewLengthChars
	}
	text := string(do.Content)
	if len(text) <= limit {
		return text
	}
	return strutil.Truncate(text, limit, "...")
}

func isTextMIME(mimeType string) bool {
	if strings.HasPrefix(mimeType, "text/") {
		return true
	}
	switch mimeType {
	case mimeJSON, mimeYAML, mimeXML,
		"application/javascript", "application/typescript":
		return true
	}
	return false
}

func (a *Agent) buildMessages(ctx context.Context, identity *auth.Identity, sess *session.Session, userText string, gc gatewayContext, userImageBlocks ...llm.ContentBlock) (messages []llm.ChatMessage, historyOffset int) {
	return a.buildMessagesForTask(ctx, identity, sess, historyTurns{}, userText, gc, userImageBlocks...)
}

func (a *Agent) buildMessagesForTask(ctx context.Context, identity *auth.Identity, sess *session.Session, turns historyTurns, userText string, gc gatewayContext, userImageBlocks ...llm.ContentBlock) (messages []llm.ChatMessage, historyOffset int) {
	systemPrompt := a.buildSystemPrompt(ctx, identity, gc, promptSkillState{}, false)
	if systemPrompt != "" {
		messages = append(messages, llm.ChatMessage{
			Role:    "system",
			Content: systemPrompt,
		})
	}

	sessionMessages := sess.Messages
	if compaction := getCompactionMetadata(sess); compaction != nil {
		sessionMessages = filterCompactedMessages(sess.Messages, compaction)
	}
	sessionMessages = arrangeSessionTurns(sessionMessages, turns)
	var ownRows []session.Message
	var storedQuestion *session.Message
	if turns.ownRows != ownRowsInHistory {
		sessionMessages, storedQuestion, ownRows = splitOwnRows(sessionMessages, turns.currentTaskID)
	}
	messages = append(messages, stubToolResults(sessionMessagesToChat(sessionMessages), turns.stubBatchBytes)...)

	historyOffset = len(messages)

	if (a.cfg.InjectCurrentTime == nil || *a.cfg.InjectCurrentTime) && (userText != "" || len(userImageBlocks) > 0) {
		stamp, tzFallback := currentTimeStamp(time.Now(), gc.UserTimezone)
		if tzFallback {
			a.logger.Debug("unresolvable user timezone on turn stamp, using UTC",
				"sessionID", sess.SessionID, "timezone", gc.UserTimezone)
		}
		userText = currentTimePrefix + stamp + "]\n" + stripCurrentTimeStamp(userText)
	}

	userMsg := llm.ChatMessage{
		Role:    "user",
		Content: userText,
	}
	if len(userImageBlocks) > 0 {
		blocks := make([]llm.ContentBlock, 0, len(userImageBlocks)+1)
		if userText != "" {
			blocks = append(blocks, llm.ContentBlock{Type: "text", Text: userText})
		}
		blocks = append(blocks, userImageBlocks...)
		userMsg.ContentBlocks = blocks
	}
	if turns.question != nil {
		userMsg = *turns.question
	} else if turns.ownRows == ownRowsAfterQuestionStubbed && storedQuestion != nil {
		userMsg = sessionMessagesToChat([]session.Message{*storedQuestion})[0]
	}
	if userMsg.Content == "" && len(userMsg.ContentBlocks) == 0 {
		a.logger.Warn("building LLM request with an empty user turn — likely a misconfigured/empty input", "agentName", a.cfg.Name)
	}
	messages = append(messages, userMsg)
	switch turns.ownRows {
	case ownRowsAfterQuestion:
		messages = append(messages, sessionMessagesToChat(ownRows)...)
	case ownRowsAfterQuestionStubbed:
		messages = append(messages, stubToolResults(sessionMessagesToChat(ownRows), turns.stubBatchBytes)...)
	}

	for i := range messages {
		if messages[i].Role == "user" && len(messages[i].ContentBlocks) == 0 && strings.TrimSpace(messages[i].Content) == "" {
			messages[i].Content = " "
		}
	}

	return
}

func sessionMessagesToChat(rows []session.Message) []llm.ChatMessage {
	out := make([]llm.ChatMessage, 0, len(rows))
	for _, m := range rows {
		cm := llm.ChatMessage{
			Role:          m.Role,
			Content:       m.Content,
			WithheldBytes: m.WithheldBytes,
			ToolCallID:    m.ToolCallID,
		}
		if len(m.ToolCalls) > 0 {
			cm.ToolCalls = make([]llm.ToolCall, len(m.ToolCalls))
			for i, tc := range m.ToolCalls {
				cm.ToolCalls[i] = llm.ToolCall{ID: tc.ID, Name: tc.Name, Arguments: tc.Arguments}
			}
			cm.Reasoning = m.Reasoning
		}
		out = append(out, cm)
	}
	return out
}

type gatewayContext struct {
	SystemPurpose       string
	ResponseFormat      string
	UserProfile         string
	ProjectID           string
	WebUIRedirect       bool
	ConversationContext string
	UserTimezone        string
	DisplaysTemplates   bool
}

const (
	metaSystemPurpose       = "system_purpose"
	metaResponseFormat      = "response_format"
	metaProjectID           = "project_id"
	metaConversationContext = "conversation_context"
)

var gatewayMetadataKeys = []string{metaSystemPurpose, metaResponseFormat, metaProjectID, metaConversationContext}

func gatewayMetadataOf(msg a2a.Message) map[string]string {
	var meta map[string]string
	for _, key := range gatewayMetadataKeys {
		v, ok := msg.Metadata[key].(string)
		if !ok {
			continue
		}
		if meta == nil {
			meta = make(map[string]string, len(gatewayMetadataKeys))
		}
		meta[key] = v
	}
	return meta
}

type RequestContext struct {
	SystemPurpose       string
	ResponseFormat      string
	ProjectID           string
	ConversationContext string
	UserProfile         string
	UserTimezone        string
}

func RequestContextOf(msg a2a.Message, userProps a2a.UserProperties) RequestContext {
	gc := gatewayContextFrom(gatewayMetadataOf(msg), userProps, false)
	return RequestContext{
		SystemPurpose:       gc.SystemPurpose,
		ResponseFormat:      gc.ResponseFormat,
		ProjectID:           gc.ProjectID,
		ConversationContext: gc.ConversationContext,
		UserProfile:         gc.UserProfile,
		UserTimezone:        gc.UserTimezone,
	}
}

func gatewayContextFrom(meta map[string]string, userProps a2a.UserProperties, displaysTemplates bool) gatewayContext {
	gc := gatewayContext{
		SystemPurpose:       meta[metaSystemPurpose],
		ResponseFormat:      meta[metaResponseFormat],
		ProjectID:           meta[metaProjectID],
		ConversationContext: meta[metaConversationContext],
		DisplaysTemplates:   displaysTemplates,
	}

	gc.WebUIRedirect = userProps.GatewayCapabilities["web_ui_redirect"]

	if userProps.A2AUserConfig != "" {
		var cfg map[string]any
		if err := json.Unmarshal([]byte(userProps.A2AUserConfig), &cfg); err == nil {
			switch v := cfg["user_profile"].(type) {
			case string:
				gc.UserProfile = v
			case map[string]any:
				if b, err := json.MarshalIndent(v, "", "  "); err == nil {
					gc.UserProfile = string(b)
				}
			}
			if v, ok := cfg["user_timezone"].(string); ok {
				gc.UserTimezone = v
			}
		}
	}
	return gc
}

func resolveRenderSections(cfg *AgentConfig) (templatesEnabled, embedsEnabled, chatRendered bool) {
	headless := cfg.Headless != nil && *cfg.Headless

	templatesEnabled = !headless
	if cfg.EnableTemplateInstructions != nil {
		templatesEnabled = *cfg.EnableTemplateInstructions
	}

	embedsEnabled = cfg.EmbedResolutionEnabled()
	chatRendered = embedsEnabled && !headless
	return templatesEnabled, embedsEnabled, chatRendered
}

func withheldPromptSections(cfg *AgentConfig) []string {
	templates, embeds, chatRendered := resolveRenderSections(cfg)
	var withheld []string
	if !templates {
		withheld = append(withheld, "templateInstructions")
	}
	if !embeds {
		withheld = append(withheld, "embedInstructions")
	}
	if !chatRendered {
		withheld = append(withheld, "peerEmbedRelay", "conversationFlow")
	}
	return withheld
}

const projectDocumentSearchText = "This conversation is associated with a project that has indexed documents. " +
	"You MUST call the index_search tool to consult the project's uploaded files BEFORE answering " +
	"any question that could be covered by those documents — do not rely on prior knowledge or answer " +
	"directly without searching first. " +
	"Always cite results using [[cite:idxTrN]] format where T is the search turn and N is the result index. " +
	"Use the SOURCE FILE name when referencing documents, not the converted filename."

const entrypointRedirectText = "This entrypoint cannot render web UI redirect buttons (links to the Solace Agent Mesh Builder page). " +
	"If the user asks to build or automate something that would normally open the Builder, do NOT write " +
	"an \"I'll take you to the … page\" preview and do NOT describe what they'll be able to do there — no button will appear. " +
	"Instead, reply with a single short message telling the user that this action is only available in the Solace Agent Mesh web UI's Builder."

func (a *Agent) buildPeerInfo(ctx context.Context, identity *auth.Identity) []PeerInfo {
	cards := a.peerCards()
	if len(cards) == 0 {
		return nil
	}
	allowed := a.allowedPeerSet(ctx, identity, cards)
	peers := make([]PeerInfo, 0, len(cards))
	for _, card := range cards {
		if _, ok := allowed[card.Name]; !ok {
			continue
		}
		agentType, _ := agentCardExtensions(card)
		peers = append(peers, PeerInfo{
			Name:        card.Name,
			DisplayName: displayNameFromCard(card),
			IsWorkflow:  agentType == "workflow",
		})
	}
	return peers
}

func (a *Agent) filterUnauthorizedPeerTools(ctx context.Context, identity *auth.Identity, defs []tool.LLMToolDefinition) []tool.LLMToolDefinition {
	cards := a.peerCards()
	if len(cards) == 0 {
		return defs
	}
	allowed := a.allowedPeerSet(ctx, identity, cards)
	deny := make(map[string]struct{})
	for _, c := range cards {
		if _, ok := allowed[c.Name]; !ok {
			deny[peerToolName(c)] = struct{}{}
		}
	}
	if len(deny) == 0 {
		return defs
	}
	out := make([]tool.LLMToolDefinition, 0, len(defs))
	for _, d := range defs {
		if _, blocked := deny[d.Name]; blocked {
			continue
		}
		out = append(out, d)
	}
	return out
}

func (a *Agent) allowedPeerSet(ctx context.Context, identity *auth.Identity, cards []a2a.AgentCard) map[string]struct{} {
	agentNames := make([]string, 0, len(cards))
	workflowNames := make([]string, 0, len(cards))
	for _, c := range cards {
		if agentType, _ := agentCardExtensions(c); agentType == "workflow" {
			workflowNames = append(workflowNames, c.Name)
		} else {
			agentNames = append(agentNames, c.Name)
		}
	}
	authz := a.svc.AgentAuthorizer()
	allowedNames := authz.FilterAgents(ctx, identity, agentNames)
	allowedNames = append(allowedNames, authz.FilterWorkflows(ctx, identity, workflowNames)...)
	allowed := make(map[string]struct{}, len(allowedNames))
	for _, n := range allowedNames {
		allowed[n] = struct{}{}
	}
	if identity == nil {
		return allowed
	}
	for _, card := range cards {
		if _, permitted := allowed[card.Name]; !permitted {
			continue
		}
		if !auth.HasAllScopes(identity.Scopes, a2a.RequiredScopesFromCard(card)) {
			delete(allowed, card.Name)
		}
	}
	return allowed
}

const anonymousSessionUserID = "anonymous"

func sessionUserID(userID string) string {
	if userID == "" {
		return anonymousSessionUserID
	}
	return userID
}

func (a *Agent) getOrCreateSession(ctx context.Context, userID, contextID, taskID string) (*session.Session, error) {
	if contextID == "" {
		return nil, fmt.Errorf("getOrCreateSession: contextID is required")
	}
	if a.svc == nil || a.svc.Sessions() == nil {
		return nil, fmt.Errorf("getOrCreateSession: no session store configured")
	}
	userID = sessionUserID(userID)

	if taskID == "" {
		sess, err := a.svc.Sessions().Get(ctx, a.cfg.Name, userID, contextID)
		if err != nil {
			return nil, err
		}
		if sess != nil {
			return sess, nil
		}
		now := time.Now()
		sess = &session.Session{
			AppName:   a.cfg.Name,
			UserID:    userID,
			SessionID: contextID,
			ContextID: contextID,
			CreatedAt: now,
			UpdatedAt: now,
		}
		if err := a.svc.Sessions().Create(ctx, sess); err != nil {
			if !errors.Is(err, session.ErrSessionExists) {
				return nil, err
			}
			return a.requireSession(a.svc.Sessions().Get(ctx, a.cfg.Name, userID, contextID))
		}
		return sess, nil
	}

	sess, err := a.svc.Sessions().GetByContextAndTask(ctx, a.cfg.Name, userID, contextID, taskID)
	if err != nil {
		return nil, err
	}
	if sess != nil {
		return sess, nil
	}
	tID := taskID
	now := time.Now()
	sess = &session.Session{
		AppName:   a.cfg.Name,
		UserID:    userID,
		SessionID: idgen.New(),
		ContextID: contextID,
		TaskID:    &tID,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := a.svc.Sessions().Create(ctx, sess); err != nil {
		if !errors.Is(err, session.ErrSessionExists) {
			return nil, err
		}
		return a.requireSession(a.svc.Sessions().GetByContextAndTask(ctx, a.cfg.Name, userID, contextID, taskID))
	}
	return sess, nil
}

func (a *Agent) requireSession(sess *session.Session, err error) (*session.Session, error) {
	if err != nil {
		return nil, fmt.Errorf("re-read session after concurrent create: %w", err)
	}
	if sess == nil {
		return nil, errors.New("session vanished after concurrent create")
	}
	return sess, nil
}

const danglingToolResultContent = `{"status":"error","message":"This tool call from a previous turn did not receive a response. It has been automatically repaired. Do NOT retry this call — instead, proceed with the current user request."}`

func repairDanglingToolCalls(messages []llm.ChatMessage, historyEnd int, logger *slog.Logger, incomingMessages []llm.ChatMessage) ([]llm.ChatMessage, int) {
	if historyEnd <= 1 {
		return messages, historyEnd
	}

	hasCalls := false
	for i := range historyEnd {
		if (messages[i].Role == "assistant" && len(messages[i].ToolCalls) > 0) ||
			(messages[i].Role == "tool" && messages[i].ToolCallID != "") {
			hasCalls = true
			break
		}
	}
	if !hasCalls {
		return messages, historyEnd
	}

	incomingResponseIDs := make(map[string]struct{})
	for _, m := range incomingMessages {
		if m.Role == "tool" && m.ToolCallID != "" {
			incomingResponseIDs[m.ToolCallID] = struct{}{}
		}
	}

	messages, historyEnd, duplicateTurns := dropDuplicateToolCallTurns(messages, historyEnd)
	if duplicateTurns > 0 {
		logger.Warn("dropped duplicate tool call turns in history", "count", duplicateTurns)
	}
	messages, renamed := renameRedeclaredToolCallIDs(messages, historyEnd, incomingResponseIDs)
	if renamed > 0 {
		logger.Warn("renamed re-declared tool call ids in history", "count", renamed)
	}
	declaredIDs := declaredToolCallIDs(messages[:historyEnd])

	repaired := make([]llm.ChatMessage, 0, len(messages)+4)
	inserted := 0
	dropped := 0
	undeclared := 0
	resolvedIDs := make(map[string]struct{})
	for i := range historyEnd {
		if messages[i].Role == "tool" && messages[i].ToolCallID != "" {
			if _, declared := declaredIDs[messages[i].ToolCallID]; !declared {
				undeclared++
				dropped++
				continue
			}
			if _, already := resolvedIDs[messages[i].ToolCallID]; already {
				logger.Warn("dropping orphan duplicate tool response in history",
					"toolCallID", messages[i].ToolCallID,
					"toolName", messages[i].ToolName,
				)
				dropped++
				continue
			}
			resolvedIDs[messages[i].ToolCallID] = struct{}{}
			repaired = append(repaired, messages[i])
			continue
		}

		repaired = append(repaired, messages[i])
		if messages[i].Role != "assistant" || len(messages[i].ToolCalls) == 0 {
			continue
		}
		respondedHere := make(map[string]struct{})
		for j := i + 1; j < historyEnd && messages[j].Role == "tool"; j++ {
			if messages[j].ToolCallID != "" {
				respondedHere[messages[j].ToolCallID] = struct{}{}
			}
		}
		names := make([]string, 0, len(messages[i].ToolCalls))
		for _, tc := range messages[i].ToolCalls {
			if _, paired := respondedHere[tc.ID]; paired {
				continue
			}
			if _, ok := incomingResponseIDs[tc.ID]; ok {
				logger.Debug("skipping repair for tool call with incoming response",
					"toolCallID", tc.ID, "toolName", tc.Name)
				continue
			}
			names = append(names, tc.Name)
			repaired = append(repaired, llm.ChatMessage{
				Role:       "tool",
				ToolCallID: tc.ID,
				ToolName:   tc.Name,
				Content:    danglingToolResultContent,
			})
			resolvedIDs[tc.ID] = struct{}{}
			inserted++
		}
		if len(names) > 0 {
			logger.Warn("repaired dangling tool calls in history", "tools", names)
		}
	}
	if undeclared > 0 {
		logger.Warn("dropped tool responses with no tool call in history", "count", undeclared)
	}

	if inserted == 0 && dropped == 0 {
		return messages, historyEnd
	}

	repaired = append(repaired, messages[historyEnd:]...)
	return repaired, historyEnd + inserted - dropped
}

const cancellationMarkerContent = "[The previous request was cancelled or interrupted before completion. Do NOT re-execute it — proceed with the current user request.]"

const cancelledToolResultContent = `{"status":"cancelled","message":"This tool call was cancelled by the user before completion. Do not retry."}`

func appendCancellationMarker(state *TaskExecutionState) {
	appendTerminalMarker(state, cancellationMarkerContent)
}

func appendTerminalMarker(state *TaskExecutionState, content string) {
	if state == nil || state.Fenced.Load() || state.answered.Load() {
		return
	}
	if n := len(state.Messages); n > 0 {
		last := state.Messages[n-1]
		if last.Role == "assistant" && last.Content == content {
			return
		}
	}
	state.Messages = append(state.Messages, llm.ChatMessage{
		Role:    "assistant",
		Content: content,
	})
}

const failedTaskMarkerContent = "[The previous request failed before a reply was produced. Do NOT re-execute it; proceed with the current user request.]"

func repairOrphanUserMessages(messages []llm.ChatMessage, historyEnd int, logger *slog.Logger) ([]llm.ChatMessage, int) {
	if historyEnd <= 1 {
		return messages, historyEnd
	}

	hasOrphan := false
	for i := range historyEnd {
		if messages[i].Role != "user" {
			continue
		}
		if i+1 < historyEnd && messages[i+1].Role == "user" {
			hasOrphan = true
			break
		}
		if i == historyEnd-1 {
			hasOrphan = true
			break
		}
	}
	if !hasOrphan {
		return messages, historyEnd
	}

	repaired := make([]llm.ChatMessage, 0, len(messages)+4)
	inserted := 0
	for i := range historyEnd {
		repaired = append(repaired, messages[i])
		if messages[i].Role != "user" {
			continue
		}
		nextIsUser := i+1 < historyEnd && messages[i+1].Role == "user"
		isLastHistory := i == historyEnd-1
		if !nextIsUser && !isLastHistory {
			continue
		}
		repaired = append(repaired, llm.ChatMessage{
			Role:    "assistant",
			Content: cancellationMarkerContent,
		})
		inserted++
	}
	if inserted > 0 {
		logger.Warn("repaired orphan user messages in history", "count", inserted)
	}

	if inserted == 0 {
		return messages, historyEnd
	}

	repaired = append(repaired, messages[historyEnd:]...)
	return repaired, historyEnd + inserted
}

func llmMessagesToSessionMessages(msgs []llm.ChatMessage, taskID string) []session.Message {
	out := make([]session.Message, 0, len(msgs))
	now := time.Now()
	for _, m := range msgs {
		sm := session.Message{
			Role:          m.Role,
			Content:       m.Content,
			Timestamp:     now,
			WithheldBytes: m.WithheldBytes,
			TaskID:        taskID,
		}
		if len(m.ToolCalls) > 0 {
			sm.ToolCalls = make([]session.ToolCall, len(m.ToolCalls))
			for i, tc := range m.ToolCalls {
				sm.ToolCalls[i] = session.ToolCall{
					ID:        tc.ID,
					Name:      tc.Name,
					Arguments: tc.Arguments,
				}
			}
			sm.Reasoning = m.Reasoning
		}
		if m.ToolCallID != "" {
			sm.ToolCallResult = session.ToolCallResult{
				ToolCallID: m.ToolCallID,
				ToolName:   m.ToolName,
			}
		}
		out = append(out, sm)
	}
	return out
}

type inlineMediaTracker struct {
	ImagesInlined   int
	BytesInlined    int
	PDFsProcessed   int
	PDFsInlined     int
	PDFBytesInlined int
	withheldBytes   int64
}

func isImageArtifactMIME(filename, mimeType string) bool {
	if mimeType != "" {
		normalized := strings.ToLower(strings.TrimSpace(strings.SplitN(mimeType, ";", 2)[0]))
		if normalized == "image/svg+xml" {
			return false
		}
		if strings.HasPrefix(normalized, "image/") {
			return true
		}
		if normalized != mimeOctetStream {
			return false
		}
	}
	if filename != "" {
		ext := strings.ToLower(filepathExt(filename))
		switch ext {
		case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp":
			return true
		}
	}
	return false
}

func isPDFArtifact(filename, mimeType string) bool {
	if mimeType != "" {
		normalized := strings.ToLower(strings.TrimSpace(strings.SplitN(mimeType, ";", 2)[0]))
		if normalized == mimePDF {
			return true
		}
		if normalized != mimeOctetStream {
			return false
		}
	}
	return strings.EqualFold(filepathExt(filename), ".pdf")
}

func filepathExt(name string) string {
	for i := len(name) - 1; i >= 0 && name[i] != '/'; i-- {
		if name[i] == '.' {
			return name[i:]
		}
	}
	return ""
}

const structuredInputInlineMaxBytes = 16 << 10

func (a *Agent) processFileParts(ctx context.Context, msg a2a.Message, userProps a2a.UserProperties, originalSessionID, userText string, structuredInputPart int) (string, []llm.ContentBlock, int64) {
	if a.svc.Artifacts() == nil {
		return userText, nil, 0
	}

	agentSessionID := originalSessionID

	tracker := &inlineMediaTracker{}
	var imageBlocks []llm.ContentBlock

	for i, p := range msg.Parts {
		if p.Kind != "file" || p.File == nil {
			continue
		}
		inlineMaxBytes := a.cfg.MaxInlineArtifactBytes
		if i == structuredInputPart && inlineMaxBytes > 0 {
			inlineMaxBytes = max(inlineMaxBytes, structuredInputInlineMaxBytes)
		}
		userText, imageBlocks = a.handleFilePart(ctx, p, inlineMaxBytes, userProps.UserID, agentSessionID, userText, imageBlocks, tracker)
	}

	return userText, imageBlocks, tracker.withheldBytes
}

func (a *Agent) handleFilePart(
	ctx context.Context,
	p a2a.Part,
	inlineMaxBytes int,
	agentUserID string,
	agentSessionID string,
	userText string,
	imageBlocks []llm.ContentBlock,
	tracker *inlineMediaTracker,
) (string, []llm.ContentBlock) {
	if p.File.URI == "" && p.File.Bytes != "" {
		if canInlineFilePartBytesAsText(p, inlineMaxBytes) {
			return maybeInlineFilePartBytes(p, userText, inlineMaxBytes), imageBlocks
		}
		if newPart, ok := a.saveInlineFilePartAsArtifact(ctx, p, agentUserID, agentSessionID); ok {
			p = newPart
		} else {
			return maybeInlineFilePartBytes(p, userText, inlineMaxBytes), imageBlocks
		}
	}
	if p.File.URI == "" {
		return userText, imageBlocks
	}

	ref, errMsg, ok := parseArtifactFilePartURI(p.File.URI)
	if !ok {
		if errMsg != "" {
			a.logger.Warn("artifact file part URI could not be read", "uriLength", len(p.File.URI))
			return userText + errMsg, imageBlocks
		}
		return userText, imageBlocks
	}

	ref = a.maybeCopyForeignArtifact(ctx, ref, p.File.URI, agentUserID, agentSessionID)

	loc, mimeType, companionMeta, fallbackMeta, errMsg := a.loadFilePartArtifactMeta(ctx, ref)
	if errMsg != "" {
		return userText + errMsg, imageBlocks
	}

	if a.cfg.EnableInlineVision && isImageArtifactMIME(ref.filename, mimeType) {
		if block, ok := a.tryInlineVisionImage(ctx, loc, ref, mimeType, tracker); ok {
			return userText, append(imageBlocks, block)
		}
	}

	if a.cfg.EnableInlinePDF && isPDFArtifact(ref.filename, mimeType) {
		if block, ok := a.tryInlinePDFDocument(ctx, loc, ref, companionMeta, fallbackMeta, tracker); ok {
			return userText, append(imageBlocks, block)
		}
	}

	inlined, sizeBytes, outcome := a.tryInlineArtifactContent(ctx, loc, ref, mimeType, companionMeta, fallbackMeta)
	if outcome == inlineContentInlined {
		return userText + inlined, imageBlocks
	}

	summary := a.formatArtifactMetadataSummary(ref.filename, mimeType, companionMeta, fallbackMeta)
	if outcome == inlineContentWithheld {
		tracker.withheldBytes += uploadWithheldBytes(mimeType, sizeBytes, summary)
	}
	return userText + summary, imageBlocks
}

type artifactFilePartRef struct {
	appName   string
	filename  string
	userID    string
	sessionID string
	version   *int
}

func parseArtifactFilePartURI(uri string) (artifactFilePartRef, string, bool) {
	parsed, err := url.Parse(uri)
	if err != nil || parsed.Scheme != "artifact" {
		return artifactFilePartRef{}, "", false
	}
	loc, version, ok := artifact.ParseURI(uri)
	if !ok {
		return artifactFilePartRef{}, fmt.Sprintf("\n[System Note: The file '%s' could not be processed. Error: Invalid artifact URI format: %s]", uri, uri), false
	}
	return artifactFilePartRef{
		appName:   loc.AppName,
		filename:  loc.Filename,
		userID:    loc.UserID,
		sessionID: loc.SessionID,
		version:   version,
	}, "", true
}

func (a *Agent) maybeCopyForeignArtifact(
	ctx context.Context,
	ref artifactFilePartRef,
	uri string,
	agentUserID, agentSessionID string,
) artifactFilePartRef {
	if a.svc.Artifacts() == nil {
		return ref
	}
	destLoc := a.svc.Artifacts().Resolve(a.cfg.Name, agentUserID, agentSessionID, ref.filename)
	sourceLoc := a.svc.Artifacts().Resolve(ref.appName, ref.userID, ref.sessionID, ref.filename)

	if sourceLoc.AppName == destLoc.AppName &&
		sourceLoc.UserID == destLoc.UserID &&
		sourceLoc.SessionID == destLoc.SessionID {
		ref.appName = destLoc.AppName
		return ref
	}

	content, mimeType, loadErr := a.svc.Artifacts().Inner.Load(ctx, sourceLoc, ref.version)
	if loadErr != nil || len(content) == 0 {
		if loadErr != nil {
			a.logger.Warn("cross-namespace artifact load failed; falling back to source coords",
				"filename", ref.filename, "sourceApp", sourceLoc.AppName, "error", loadErr)
		}
		return ref
	}

	saveResult, saveErr := artifact.SaveWithMetadata(
		ctx, a.svc.Artifacts(),
		a.cfg.Name, agentUserID, agentSessionID,
		ref.filename, content, mimeType,
		artifact.MetadataOpts{
			Custom: map[string]any{
				"source":     "a2a_filepart_uri_reference",
				"source_uri": uri,
			},
		},
		a.logger,
	)
	if saveErr != nil {
		a.logger.Warn("cross-namespace artifact resave failed; using source coords",
			"filename", ref.filename, "error", saveErr)
		return ref
	}

	a.logger.Info("resolved cross-namespace artifact reference",
		"filename", ref.filename,
		"sourceApp", sourceLoc.AppName,
		"sourceUser", ref.userID,
		"sourceSession", ref.sessionID,
		"newVersion", saveResult.DataVersion,
	)

	v := saveResult.DataVersion
	return artifactFilePartRef{
		appName:   destLoc.AppName,
		filename:  ref.filename,
		userID:    agentUserID,
		sessionID: agentSessionID,
		version:   &v,
	}
}

func (a *Agent) loadFilePartArtifactMeta(ctx context.Context, ref artifactFilePartRef) (
	artifact.Locator, string, map[string]any, *artifact.VersionMetadata, string,
) {
	loc := a.svc.Artifacts().Resolve(a.cfg.Name, ref.userID, ref.sessionID, ref.filename)
	companionMeta := artifact.LoadCompanionMetadata(ctx, a.svc.Artifacts(), a.cfg.Name, ref.userID, ref.sessionID, ref.filename, ref.version)
	if ref.appName != "" && ref.appName != loc.AppName {
		loc = artifact.Locator{
			AppName:   ref.appName,
			UserID:    ref.userID,
			SessionID: ref.sessionID,
			Filename:  ref.filename,
		}
		companionMeta = artifact.LoadCompanionMetadataAtLocator(ctx, a.svc.Artifacts(), loc, ref.version)
	}
	var mimeType string
	var fallbackMeta *artifact.VersionMetadata
	if companionMeta != nil {
		if mt, ok := companionMeta["mime_type"].(string); ok {
			mimeType = mt
		}
		return loc, mimeType, companionMeta, nil, ""
	}
	meta, metaErr := a.svc.Artifacts().Inner.GetVersionMetadata(ctx, loc, ref.version)
	if metaErr != nil || meta == nil {
		errMsg := "not found"
		if metaErr != nil {
			errMsg = metaErr.Error()
		}
		return loc, "", nil, nil, fmt.Sprintf("\n[System Note: The file '%s' could not be processed. Error: Failed to load metadata: %s]", ref.filename, errMsg)
	}
	fallbackMeta = meta
	mimeType = meta.MIMEType
	return loc, mimeType, nil, fallbackMeta, ""
}

func (a *Agent) tryInlineVisionImage(
	ctx context.Context,
	loc artifact.Locator,
	ref artifactFilePartRef,
	mimeType string,
	tracker *inlineMediaTracker,
) (llm.ContentBlock, bool) {
	maxImages := a.cfg.MaxInlineVisionImages
	if maxImages <= 0 {
		maxImages = visionimage.DefaultMaxImages
	}
	maxBytes := a.cfg.MaxInlineVisionBytes
	if maxBytes <= 0 {
		maxBytes = visionimage.DefaultMaxBytes
	}
	if tracker.ImagesInlined >= maxImages {
		a.logger.Debug("inline vision image cap reached, falling back to text metadata",
			"filename", ref.filename,
			"imagesInlined", tracker.ImagesInlined,
			"maxImages", maxImages,
		)
		return llm.ContentBlock{}, false
	}
	if tracker.BytesInlined >= maxBytes {
		a.logger.Debug("inline vision byte cap reached, falling back to text metadata",
			"filename", ref.filename,
			"bytesInlined", tracker.BytesInlined,
			"max_bytes", maxBytes,
		)
		return llm.ContentBlock{}, false
	}
	content, _, loadErr := a.svc.Artifacts().Inner.Load(ctx, loc, ref.version)
	if loadErr != nil || len(content) == 0 {
		if loadErr != nil {
			a.logger.Warn("failed to load image bytes for inline vision, falling back to text metadata",
				"filename", ref.filename,
				"error", loadErr,
			)
		}
		return llm.ContentBlock{}, false
	}
	fitted, fittedMIME, ok := visionimage.Clamp(content, mimeType)
	if !ok {
		a.logger.Debug("inline vision: image cannot be fitted to provider limits, falling back to text metadata",
			"filename", ref.filename, "mimeType", mimeType, "bytes", len(content))
		return llm.ContentBlock{}, false
	}
	dataURL := visionimage.DataURL(fittedMIME, fitted)
	tracker.ImagesInlined++
	tracker.BytesInlined += len(fitted)
	a.logger.Debug("inline vision: passing image directly to LLM",
		"filename", ref.filename,
		"bytes", len(fitted),
		"mimeType", fittedMIME,
	)
	return llm.ContentBlock{Type: "image_url", ImageURL: dataURL}, true
}

func (a *Agent) tryInlinePDFDocument(
	ctx context.Context,
	loc artifact.Locator,
	ref artifactFilePartRef,
	companionMeta map[string]any,
	fallbackMeta *artifact.VersionMetadata,
	tracker *inlineMediaTracker,
) (llm.ContentBlock, bool) {
	maxBytes := a.cfg.MaxInlinePDFBytes
	if maxBytes <= 0 {
		maxBytes = 20 * 1024 * 1024
	}
	maxPages := a.cfg.MaxInlinePDFPages
	if maxPages <= 0 {
		maxPages = 100
	}
	maxCount := a.cfg.MaxInlinePDFCount
	if maxCount <= 0 {
		maxCount = 5
	}
	if tracker.PDFsProcessed >= maxCount {
		a.logger.Debug("inline PDF: per-message count cap reached, falling back to text metadata",
			"filename", ref.filename, "pdfsProcessed", tracker.PDFsProcessed, "maxCount", maxCount)
		return llm.ContentBlock{}, false
	}
	if hint := metaSizeHint(companionMeta, fallbackMeta); hint > int64(maxBytes) {
		a.logger.Debug("inline PDF: size hint exceeds byte cap, falling back to text metadata",
			"filename", ref.filename, "size_bytes", hint, "max_bytes", maxBytes)
		return llm.ContentBlock{}, false
	}
	content, _, loadErr := a.svc.Artifacts().Inner.Load(ctx, loc, ref.version)
	if loadErr != nil || len(content) == 0 {
		if loadErr != nil {
			a.logger.Warn("failed to load PDF bytes for inline document, falling back to text metadata",
				"filename", ref.filename,
				"error", loadErr,
			)
		}
		return llm.ContentBlock{}, false
	}
	if tracker.PDFBytesInlined+len(content) > maxBytes {
		a.logger.Debug("inline PDF: byte budget exceeded, falling back to text metadata",
			"filename", ref.filename,
			"pdfBytesInlined", tracker.PDFBytesInlined,
			"bytes", len(content),
			"max_bytes", maxBytes,
		)
		return llm.ContentBlock{}, false
	}
	tracker.PDFsProcessed++
	structure, probeErr := probePDFStructure(ctx, content)
	if probeErr != nil {
		a.logger.Warn("inline PDF: could not parse PDF structure, falling back to text metadata",
			"filename", ref.filename, "error", probeErr)
		return llm.ContentBlock{}, false
	}
	if structure.pageCount > maxPages {
		a.logger.Debug("inline PDF: page count exceeds cap, falling back to text metadata",
			"filename", ref.filename, "pages", structure.pageCount, "maxPages", maxPages)
		return llm.ContentBlock{}, false
	}
	imageBased := structure.substantial
	if !imageBased {
		sparse, textErr := pdfHasSparseTextPage(ctx, content, structure.pageCount)
		if textErr != nil {
			a.logger.Warn("inline PDF: could not classify text density, falling back to text metadata",
				"filename", ref.filename, "pages", structure.pageCount, "error", textErr)
			return llm.ContentBlock{}, false
		}
		imageBased = sparse
	}
	if !imageBased {
		a.logger.Debug("inline PDF: text-rich PDF, leaving to tool-based conversion",
			"filename", ref.filename, "pages", structure.pageCount)
		return llm.ContentBlock{}, false
	}
	dataURL := visionimage.DataURL(mimePDF, content)
	tracker.PDFsInlined++
	tracker.PDFBytesInlined += len(content)
	a.logger.Debug("inline PDF: passing image-based PDF directly to LLM",
		"filename", ref.filename,
		"bytes", len(content),
		"pages", structure.pageCount,
	)
	return llm.ContentBlock{Type: "file", FileData: dataURL, FileName: ref.filename, FileType: mimePDF}, true
}

type inlineContentOutcome int

const (
	inlineContentInlined inlineContentOutcome = iota
	inlineContentWithheld
	inlineContentUnavailable
)

func (a *Agent) tryInlineArtifactContent(
	ctx context.Context,
	loc artifact.Locator,
	ref artifactFilePartRef,
	mimeType string,
	companionMeta map[string]any,
	fallbackMeta *artifact.VersionMetadata,
) (inlined string, sizeBytes int64, outcome inlineContentOutcome) {
	sizeBytes = metaSizeHint(companionMeta, fallbackMeta)
	threshold := a.cfg.MaxInlineArtifactBytes
	if threshold <= 0 || !isInlineableTextMIME(mimeType) {
		return "", sizeBytes, inlineContentWithheld
	}
	if sizeBytes >= 0 && sizeBytes > int64(threshold) {
		return "", sizeBytes, inlineContentWithheld
	}
	content, _, loadErr := a.svc.Artifacts().Inner.Load(ctx, loc, ref.version)
	if loadErr != nil {
		a.logger.Debug("inline artifact load failed, falling back to metadata",
			"filename", ref.filename, "error", loadErr)
		return "", sizeBytes, inlineContentUnavailable
	}
	sizeBytes = int64(len(content))
	if len(content) > threshold {
		return "", sizeBytes, inlineContentWithheld
	}
	return fmt.Sprintf(
		userFileContextPreamble+
			"The full content is inlined below for convenience; the artifact `%s` "+
			"(version %s) is also available to your tools.%s\n\n"+
			"--- Begin content of `%s` (%s, %d bytes) ---\n%s\n--- End content of `%s` ---",
		ref.filename, formatVersionLabel(ref.version), a.artifactToolHint(ref.filename), ref.filename, mimeType, len(content), string(content), ref.filename), sizeBytes, inlineContentInlined
}

func metaSizeHint(companionMeta map[string]any, fallbackMeta *artifact.VersionMetadata) int64 {
	if companionMeta != nil {
		switch sz := companionMeta["size_bytes"].(type) {
		case int64:
			return sz
		case float64:
			return int64(sz)
		case int:
			return int64(sz)
		}
		return -1
	}
	if fallbackMeta != nil {
		return fallbackMeta.SizeBytes
	}
	return -1
}

func (a *Agent) formatArtifactMetadataSummary(
	filename, mimeType string,
	companionMeta map[string]any,
	fallbackMeta *artifact.VersionMetadata,
) string {
	if companionMeta != nil {
		out := userFileContextPreamble +
			"Use the information contained within its metadata to complete your objective. " +
			"You can access the full content using your tools if necessary." + a.artifactToolHint(filename) + "\n\n" +
			artifact.FormatMetadataForLLM(companionMeta)
		out += a.imageViewHint(filename, mimeType)
		return out
	}
	if fallbackMeta == nil {
		return ""
	}
	out := fmt.Sprintf(userFileContextPreamble+
		"Use the information contained within its metadata to complete your objective. "+
		"You can access the full content using your tools if necessary.%s\n\n"+
		"--- Metadata for artifact '%s' ---\n"+
		"**Artifact Metadata:**\n"+
		"*   **Type:** %s\n"+
		"*   **Size:** %d bytes\n"+
		"--- End Metadata ---", a.artifactToolHint(filename), filename, fallbackMeta.MIMEType, fallbackMeta.SizeBytes)
	out += a.imageViewHint(filename, fallbackMeta.MIMEType)
	return out
}

func formatVersionLabel(v *int) string {
	if v == nil {
		return "latest"
	}
	return strconv.Itoa(*v)
}

func maybeInlineFilePartBytes(p a2a.Part, userText string, inlineMaxBytes int) string {
	filename := p.File.Name
	if filename == "" {
		filename = "input"
	}
	mimeType := filePartMIMEType(p)

	if content, ok := inlineableFilePartContent(p, inlineMaxBytes); ok {
		return userText + fmt.Sprintf(
			"\nThe caller has provided the following input data (named `%s`, %s, %d bytes). "+
				"The full content is inlined below for your immediate use; no tool call is needed to read it.\n\n"+
				"--- Begin content of `%s` ---\n%s\n--- End content of `%s` ---",
			filename, mimeType, len(content), filename, string(content), filename)
	}

	return userText + fmt.Sprintf(
		"\n[Input file '%s' (%s) is attached but exceeds the inline threshold (%d bytes); "+
			"it is consumed by the runtime for input validation and is not directly readable here.]",
		filename, mimeType, inlineMaxBytes)
}

func isInlineableTextMIME(mimeType string) bool {
	if mimeType == "" {
		return false
	}
	mt := strings.ToLower(strings.TrimSpace(mimeType))
	if i := strings.Index(mt, ";"); i >= 0 {
		mt = strings.TrimSpace(mt[:i])
	}
	if strings.HasPrefix(mt, "text/") {
		return true
	}
	switch mt {
	case mimeJSON,
		"application/x-ndjson",
		mimeYAML,
		"application/x-yaml",
		mimeXML,
		"application/javascript",
		"application/x-sh",
		"application/toml":
		return true
	}
	return strings.HasSuffix(mt, "+json") || strings.HasSuffix(mt, "+xml") || strings.HasSuffix(mt, "+yaml")
}

func canInlineFilePartBytesAsText(p a2a.Part, inlineMaxBytes int) bool {
	_, ok := inlineableFilePartContent(p, inlineMaxBytes)
	return ok
}

func inlineableFilePartContent(p a2a.Part, inlineMaxBytes int) ([]byte, bool) {
	if p.File == nil || inlineMaxBytes <= 0 || !isInlineableTextMIME(filePartMIMEType(p)) {
		return nil, false
	}
	if len(p.File.Bytes) > base64.StdEncoding.EncodedLen(inlineMaxBytes) {
		return nil, false
	}
	content := structured.DecodeFileBytes(p.File.Bytes)
	return content, len(content) <= inlineMaxBytes
}

func filePartMIMEType(p a2a.Part) string {
	if p.File.MIMEType == "" {
		return mimeOctetStream
	}
	return p.File.MIMEType
}

func (a *Agent) saveInlineFilePartAsArtifact(
	ctx context.Context,
	p a2a.Part,
	agentUserID, agentSessionID string,
) (a2a.Part, bool) {
	if a.svc.Artifacts() == nil {
		return p, false
	}
	if p.File == nil || p.File.Bytes == "" {
		return p, false
	}

	content, err := base64.StdEncoding.DecodeString(p.File.Bytes)
	if err != nil {
		a.logger.Warn("inline file-bytes decode failed; not saved as artifact",
			"filename", p.File.Name, "error", err)
		return p, false
	}
	if len(content) == 0 {
		return p, false
	}

	filename := p.File.Name
	if filename == "" {
		filename = "input"
	}
	if !isFilenameSafe(filename) || artifact.IsInternalArtifact(filename) {
		a.logger.Warn("refusing to save inline file part with unsafe or reserved filename",
			"filename", filename)
		return p, false
	}
	mimeType := p.File.MIMEType
	if mimeType == "" {
		mimeType = mimeOctetStream
	}

	res, err := artifact.SaveWithMetadata(
		ctx, a.svc.Artifacts(),
		a.cfg.Name, agentUserID, agentSessionID,
		filename, content, mimeType,
		artifact.MetadataOpts{
			Source: "user_upload",
			Tags:   []string{a2a.ArtifactTagUserUploaded},
		},
		a.logger,
	)
	if err != nil {
		a.logger.Warn("failed to save user-uploaded inline file as artifact",
			"filename", filename, "error", err)
		return p, false
	}

	loc := a.svc.Artifacts().Resolve(a.cfg.Name, agentUserID, agentSessionID, filename)
	uri := fmt.Sprintf("artifact://%s/%s/%s/%s?version=%d",
		url.PathEscape(loc.AppName),
		url.PathEscape(agentUserID),
		url.PathEscape(agentSessionID),
		url.PathEscape(filename),
		res.DataVersion,
	)

	a.logger.Info("saved user-uploaded file as artifact",
		"filename", filename, "mime", mimeType, "size", len(content),
		"session", agentSessionID, "version", res.DataVersion)

	return a2a.Part{
		Kind: "file",
		File: &a2a.FileContent{
			Name:     filename,
			MIMEType: mimeType,
			URI:      uri,
		},
	}, true
}

func (a *Agent) processInvokedArtifacts(ctx context.Context, msg a2a.Message, userProps a2a.UserProperties, userText string) string {
	if a.svc.Artifacts() == nil || msg.Metadata == nil {
		return userText
	}
	rawList, ok := msg.Metadata["invoked_with_artifacts"].([]any)
	if !ok || len(rawList) == 0 {
		return userText
	}

	userID := userProps.UserID
	sessionID := msg.ContextID

	const header = "\nThe user has provided the following artifacts as context for your task. " +
		"Use the information contained within their metadata to complete your objective."
	var summaries []string

	for _, raw := range rawList {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		filename, _ := entry["filename"].(string)
		if filename == "" {
			continue
		}
		var versionPtr *int
		switch v := entry["version"].(type) {
		case int:
			versionPtr = &v
		case int64:
			vv := int(v)
			versionPtr = &vv
		case float64:
			vv := int(v)
			versionPtr = &vv
		case string:
			if v == "" || v == "latest" {
				versionPtr = nil
			} else if parsed, err := strconv.Atoi(v); err == nil {
				versionPtr = &parsed
			}
		}

		companionMeta := artifact.LoadCompanionMetadata(ctx, a.svc.Artifacts(), a.cfg.Name, userID, sessionID, filename, versionPtr)
		if companionMeta == nil {
			loc := a.svc.Artifacts().Resolve(a.cfg.Name, userID, sessionID, filename)
			if vm, err := a.svc.Artifacts().Inner.GetVersionMetadata(ctx, loc, versionPtr); err == nil && vm != nil {
				synth := map[string]any{"filename": filename}
				if vm.MIMEType != "" {
					synth["mime_type"] = vm.MIMEType
				}
				if vm.SizeBytes > 0 {
					synth["size_bytes"] = vm.SizeBytes
				}
				if vm.Description != "" {
					synth["description"] = vm.Description
				}
				resolvedVersion := 0
				if versionPtr != nil {
					resolvedVersion = *versionPtr
				}
				synth["version"] = resolvedVersion
				for k, v := range vm.Custom {
					if _, exists := synth[k]; !exists {
						synth[k] = v
					}
				}
				companionMeta = synth
			}
		}
		if companionMeta == nil {
			a.logger.Debug("invoked_with_artifacts entry could not be resolved",
				"filename", filename)
			continue
		}
		if _, ok := companionMeta["filename"]; !ok {
			companionMeta["filename"] = filename
		}
		if _, ok := companionMeta["version"]; !ok && versionPtr != nil {
			companionMeta["version"] = *versionPtr
		}
		summaries = append(summaries, artifact.FormatMetadataForLLM(companionMeta))
	}

	if len(summaries) == 0 {
		return userText
	}
	userText += header + "\n\n" + strings.Join(summaries, "\n\n")
	return userText
}

func extractText(msg a2a.Message) string {
	var text string
	for _, p := range msg.Parts {
		if p.Kind == "text" {
			if text != "" {
				text += "\n"
			}
			text += p.Text
		}
	}
	return text
}

type artifactLoaderAdapter struct {
	svc *artifact.ScopedService
	ctx context.Context
}

func (a *artifactLoaderAdapter) LoadContent(agentName, userID, sessionID, filename string, version *int) ([]byte, int, error) {
	loc := a.svc.Resolve(agentName, userID, sessionID, filename)
	actualVersion := 0
	if version != nil {
		actualVersion = *version
	} else {
		versions, err := a.svc.Inner.ListVersions(a.ctx, loc)
		if err == nil && len(versions) > 0 {
			actualVersion = versions[len(versions)-1]
		}
	}
	content, _, err := a.svc.Inner.Load(a.ctx, loc, version)
	if err != nil {
		return nil, 0, err
	}
	if content == nil {
		return nil, 0, fmt.Errorf("artifact %q not found", filename)
	}
	return content, actualVersion, nil
}

func (a *Agent) validateStructuredOutput(
	ctx context.Context,
	state *TaskExecutionState,
	rc *taskRunContext,
	llmOutput string,
) *structured.RetryFeedback {
	if state.HadToolFailure {
		return nil
	}
	var loader structured.ArtifactLoader
	if a.svc.Artifacts() != nil {
		loader = &artifactLoaderAdapter{svc: a.svc.Artifacts(), ctx: ctx}
	}

	_, feedback, err := a.structuredHandler.ParseAndValidateOutput(
		ctx, llmOutput, rc.structuredCtx, loader,
		a.cfg.Name, state.UserProps.UserID, rc.originalSessionID,
	)
	if err != nil || feedback == nil {
		return nil
	}

	rc.structuredCtx.RetryCount++

	rc.logger.Info("structured invocation retry (main loop)",
		"nodeID", rc.structuredCtx.Request.NodeID,
		"retry", rc.structuredCtx.RetryCount,
		"feedbackLength", len(feedback.Message),
	)

	return feedback
}

func (a *Agent) finalizeStructuredInvocation(
	ctx context.Context,
	state *TaskExecutionState,
	rc *taskRunContext,
	llmOutput string,
) (*a2a.Response, error) {
	sp := rc.sp
	structuredCtx := rc.structuredCtx
	messages := state.Messages
	totalUsage := state.usageSnapshot()

	var loader structured.ArtifactLoader
	if a.svc.Artifacts() != nil {
		loader = &artifactLoaderAdapter{svc: a.svc.Artifacts(), ctx: ctx}
	}

	sendPendingRetry := structuredCtx.RetryPending && !state.HadToolFailure
	for {
		if !sendPendingRetry {
			resp, retry, err := a.judgeStructuredOutput(ctx, state, rc, loader, llmOutput, totalUsage, &messages)
			if !retry {
				return resp, err
			}
		}
		sendPendingRetry = false

		llmTools := a.buildLLMTools(ctx, rc.toolCtx.Identity, rc.toolCtx.SkillState, "")

		reqMessages := a.appendStructuredInstructions(a.appendPerRequestContext(messages, rc.gc), rc)
		chatReq := a.agentTurnRequest(ctx, state, reqMessages, llmTools)

		sp.PublishLLMInvocation(ctx, chatReq, a.callReportModel(ctx, state))

		streamResp, createdArtifacts, streamErr := a.streamLLMCall(ctx, sp, chatReq, structuredStreamCallInfo(state, rc))
		if streamErr != nil {
			return a.failStructuredStream(ctx, state, rc, totalUsage, streamErr)
		}

		totalUsage.add(streamResp.Usage)

		streamResp.ToolCalls, _ = filterNotifyArtifactCalls(streamResp.ToolCalls, rc.logger)

		if len(streamResp.ToolCalls) > 0 {
			rc.logger.Warn("ignoring tool calls during structured invocation retry",
				"toolCount", len(streamResp.ToolCalls),
				"retry", structuredCtx.RetryCount,
			)
		}

		for len(createdArtifacts) > 0 && len(streamResp.ToolCalls) == 0 {
			historyContent := streamResp.RawContent
			if historyContent == "" {
				historyContent = streamResp.Content
			}
			injectedCalls := a.previewArtifactNotifications(createdArtifacts)
			sp.PublishLLMResponse(ctx, a.callReportModel(ctx, state), historyContent, streamResp.Usage, injectedCalls...)
			a.injectArtifactNotifications(ctx, sp, historyContent, createdArtifacts, &messages)
			setLastAssistantReasoning(messages, streamResp.ReasoningContent)

			followUp := *chatReq
			followUp.Messages = a.appendStructuredInstructions(a.appendPerRequestContext(messages, rc.gc), rc)
			chatReq = &followUp
			a.applyReasoningAndCache(ctx, chatReq, state)
			a.guardAgainstFabricatedTurn(ctx, state, chatReq, messages)
			sp.PublishLLMInvocation(ctx, chatReq, a.callReportModel(ctx, state))

			var followUpArtifacts []artifactCreationRecord
			streamResp, followUpArtifacts, streamErr = a.streamLLMCall(ctx, sp, chatReq, structuredStreamCallInfo(state, rc))
			if streamErr != nil {
				return a.failStructuredStream(ctx, state, rc, totalUsage, streamErr)
			}
			totalUsage.add(streamResp.Usage)
			streamResp.ToolCalls, _ = filterNotifyArtifactCalls(streamResp.ToolCalls, rc.logger)
			createdArtifacts = followUpArtifacts
		}

		if len(streamResp.ToolCalls) == 0 {
			if streamResp.Content != "" {
				sp.PublishLLMResponse(ctx, a.callReportModel(ctx, state), streamResp.Content, streamResp.Usage)
			}
		}

		llmOutput = streamResp.Content
	}
}

func (a *Agent) judgeStructuredOutput(
	ctx context.Context,
	state *TaskExecutionState,
	rc *taskRunContext,
	loader structured.ArtifactLoader,
	llmOutput string,
	totalUsage taskUsageSnapshot,
	messages *[]llm.ChatMessage,
) (resp *a2a.Response, retry bool, err error) {
	structuredCtx := rc.structuredCtx
	if state.HadToolFailure {
		resp, err = a.buildStructuredResponse(state.TaskID, rc.msg,
			a.structuredToolFailureResult(state, rc), totalUsage, &state.usageFacts)
		return resp, false, err
	}
	result, feedback, err := a.structuredHandler.ParseAndValidateOutput(
		ctx, llmOutput, structuredCtx, loader,
		a.cfg.Name, state.UserProps.UserID, rc.originalSessionID,
	)
	if err != nil {
		resp, err = a.buildStructuredResponse(state.TaskID, rc.msg,
			a.structuredHandler.BuildErrorResult(err.Error(), nil, structuredCtx.RetryCount),
			totalUsage, &state.usageFacts)
		return resp, false, err
	}
	if feedback == nil {
		resp, err = a.buildStructuredResponse(state.TaskID, rc.msg, result, totalUsage, &state.usageFacts)
		return resp, false, err
	}

	structuredCtx.RetryCount++

	rc.logger.Info("structured invocation retry",
		"nodeID", structuredCtx.Request.NodeID,
		"retry", structuredCtx.RetryCount,
		"feedbackLength", len(feedback.Message),
	)

	*messages = append(*messages,
		llm.ChatMessage{Role: "assistant", Content: structuredRetryContent(llmOutput)},
		llm.ChatMessage{Role: "user", Content: feedback.Message},
	)
	return nil, true, nil
}

func structuredRetryContent(llmOutput string) string {
	if strings.TrimSpace(llmOutput) == "" {
		return "(The agent produced the artifact but did not include a text response.)"
	}
	return llmOutput
}

func structuredStreamCallInfo(state *TaskExecutionState, rc *taskRunContext) *streamCallInfo {
	return &streamCallInfo{
		UserID:                 state.UserProps.UserID,
		SessionID:              rc.originalSessionID,
		UserTimezone:           rc.gc.UserTimezone,
		MaxContinuations:       -1,
		IsStructuredInvocation: true,
		IsWorkflowDispatch:     state.kind == a2a.TaskKindWorkflowAgent,
	}
}

func (a *Agent) failStructuredStream(ctx context.Context, state *TaskExecutionState, rc *taskRunContext, totalUsage taskUsageSnapshot, streamErr error) (*a2a.Response, error) {
	retry := rc.structuredCtx.RetryCount
	if exhausted, ok := errors.AsType[*budget.ExhaustedError](streamErr); ok {
		rc.logger.Info("structured invocation stopped: a budget is spent", "retry", retry, "budgetTier", exhausted.Tier)
		return a.buildStructuredResponse(state.TaskID, rc.msg,
			a.structuredHandler.BuildErrorResult(budgetStoppedMessage(state, exhausted), nil, retry),
			totalUsage, &state.usageFacts)
	}
	if ctx.Err() == nil {
		logger := rc.logger.With("retry", retry)
		if bve, ok := errors.AsType[*blockValidationError](streamErr); ok {
			logger.Warn("block validation error, structured invocation not retried",
				"error", bve.ErrorMsg,
				"filename", bve.Filename,
			)
		} else {
			logLLMCallFailure(logger, state.LLMCallCount, streamErr)
		}
	}
	return a.buildStructuredResponse(state.TaskID, rc.msg,
		a.structuredHandler.BuildErrorResult(llm.UserFacingErrorMessage(streamErr), nil, retry),
		totalUsage, &state.usageFacts)
}

func (a *Agent) structuredToolFailureResult(state *TaskExecutionState, rc *taskRunContext) *a2a.StructuredInvocationResult {
	rc.logger.Info("structured invocation failed by unrecoverable tool failure",
		"nodeID", rc.structuredCtx.Request.NodeID,
		"code", toolFailureCode(state.ToolFailureDetail),
	)
	return a.structuredHandler.BuildErrorResult(
		toolFailureMessage(state.ToolFailureDetail), nil, rc.structuredCtx.RetryCount)
}

func toolFailureCode(d *tool.ToolFailedDetail) string {
	if d == nil {
		return ""
	}
	return d.Code
}

func toolFailureMessage(d *tool.ToolFailedDetail) string {
	if d == nil {
		return taxonomy.ToolExecutionFailedMessage
	}
	msg := strings.TrimSpace(d.Message)
	switch {
	case d.Code != "" && msg != "":
		return fmt.Sprintf("%s (%s): %s", taxonomy.ToolExecutionFailedMessage, d.Code, msg)
	case msg != "":
		return taxonomy.ToolExecutionFailedMessage + ": " + msg
	case d.Code != "":
		return fmt.Sprintf("%s (%s)", taxonomy.ToolExecutionFailedMessage, d.Code)
	}
	return taxonomy.ToolExecutionFailedMessage
}

func (a *Agent) buildStructuredResponse(
	taskID string,
	msg a2a.Message,
	result *a2a.StructuredInvocationResult,
	snap taskUsageSnapshot,
	facts *taskUsageFacts,
) (*a2a.Response, error) {

	resultData := map[string]any{
		"type":        result.Type,
		"status":      result.Status,
		"retry_count": result.RetryCount,
	}
	if result.OutputArtifactRef != nil {
		resultData["output_artifact_ref"] = map[string]any{
			"name":    result.OutputArtifactRef.Name,
			"version": result.OutputArtifactRef.Version,
		}
	}
	if result.ErrorMessage != "" {
		resultData["error_message"] = result.ErrorMessage
	}
	if len(result.ValidationErrors) > 0 {
		resultData["validation_errors"] = result.ValidationErrors
	}

	responseParts := []a2a.Part{
		a2a.NewDataPart(resultData),
	}

	state := a2a.TaskStateCompleted
	if result.Status == "error" {
		state = a2a.TaskStateFailed
	}

	responseMsg := a2a.Message{
		Kind:      "message",
		MessageID: fmt.Sprintf("msg-%s-resp", taskID),
		Role:      a2a.RoleAgent,
		Parts:     responseParts,
	}

	taskMetadata := map[string]any{
		"agent_name": a.cfg.Name,
	}
	if UsageRecorded(snap.usage) {
		tokenUsage := TokenUsageTotals(snap.usage)
		tokenUsage["last_input_tokens"] = snap.lastInputTokens
		taskMetadata["token_usage"] = tokenUsage
	}
	if facts != nil {
		facts.AddTo(taskMetadata)
	}

	task := a2a.Task{
		ID:        taskID,
		ContextID: msg.ContextID,
		Kind:      "task",
		Status: a2a.TaskStatus{
			State:     state,
			Timestamp: time.Now().UTC().Format("2006-01-02T15:04:05.000000+00:00"),
			Message:   &responseMsg,
		},
		Metadata: taskMetadata,
	}

	return a2a.NewResponse(taskID, task), nil
}

func (a *Agent) extractAuthResponse(msg a2a.Message) *a2a.AuthResponseData {
	return AuthResponseFromMessage(msg, a.logger)
}

func AuthResponseFromMessage(msg a2a.Message, logger *slog.Logger) *a2a.AuthResponseData {
	for _, p := range msg.Parts {
		if p.Kind != "data" || p.Data == nil {
			continue
		}
		dataMap, ok := p.Data.(map[string]any)
		if !ok {
			continue
		}
		if dataMap["type"] != "auth_response" {
			continue
		}
		raw, err := json.Marshal(dataMap)
		if err != nil {
			logger.Warn("failed to marshal auth_response data", "error", err)
			continue
		}
		var resp a2a.AuthResponseData
		if err := json.Unmarshal(raw, &resp); err != nil {
			logger.Warn("failed to unmarshal auth_response data", "error", err)
			continue
		}
		return &resp
	}
	return nil
}

func (a *Agent) handleAuthResponse(ctx context.Context, taskID string, resp *a2a.AuthResponseData, userProps a2a.UserProperties) (*a2a.Response, error) {
	a.logger.Info("handling MCP auth_response",
		"credentialKey", resp.CredentialKey,
		"targetAgent", resp.TargetAgent,
		"functionCallID", resp.FunctionCallID,
		"targetTaskID", resp.TargetAgentTaskID,
		"isMCPCredentialSave", resp.IsCredentialSave,
	)

	state := a.pausedTasks.Load(resp.TargetAgentTaskID)
	if sender := hilSenderUser(userProps.UserID); state != nil && !sender.mayAnswer(state.UserProps.UserID) {
		warnControlResponseRefused(a.logger.With("taskID", state.TaskID, "functionCallID", resp.FunctionCallID), a2a.SignalAuthResponse, sender)
		return nil, nil
	}

	dedupKey := authResponseDedupKey(resp.FunctionCallID, resp.Code)
	if dedupKey != "" && a.markAuthResponseProcessed(dedupKey) {
		a.logger.Info("auth_response already processed, skipping duplicate",
			"functionCallID", resp.FunctionCallID,
			"credentialKey", resp.CredentialKey,
		)
		responseMsg := a2a.Message{
			Role: "agent",
			Parts: []a2a.Part{
				a2a.NewTextPart("Authentication already processed."),
			},
		}
		task := a2a.Task{
			ID:   taskID,
			Kind: "task",
			Status: a2a.TaskStatus{
				State:     a2a.TaskStateCompleted,
				Timestamp: time.Now().UTC().Format("2006-01-02T15:04:05.000000+00:00"),
				Message:   &responseMsg,
			},
			Metadata: map[string]any{
				"agent_name":             a.cfg.Name,
				"is_mcp_credential_save": true,
				"auth_response_deduped":  true,
			},
		}
		return a2a.NewResponse(taskID, task), nil
	}

	a.authHandlersMu.RLock()
	handler, ok := a.authHandlers[resp.CredentialKey]
	a.authHandlersMu.RUnlock()
	if !ok {
		a.logger.Error("no MCP auth handler for credential key", "credentialKey", resp.CredentialKey, "taskID", taskID, "traceID", userProps.TraceID)
		return a2a.NewErrorResponse(taskID, -32600, fmt.Sprintf("no auth handler for credential key %q", resp.CredentialKey)), nil
	}

	toolCtx := &tool.Context{
		UserID:            userProps.UserID,
		AgentName:         a.cfg.Name,
		CredentialService: a.svc.CredentialService(),
	}

	authMuUnlocked := false
	if state != nil {
		state.authMu.Lock()
		defer func() {
			if !authMuUnlocked {
				state.authMu.Unlock()
			}
		}()
	}

	exchange := func() error { return handler.HandleAuthResponse(ctx, resp, toolCtx) }
	var answersPrompt bool
	if a.checkpoints != nil {
		matches, known := answersPendingPrompt(ctx, handler, resp, toolCtx)
		if known && !matches {
			a.logger.Info("ignoring an answer to an earlier sign-in prompt",
				"taskID", resp.TargetAgentTaskID, "traceID", userProps.TraceID, "functionCallID", resp.FunctionCallID)
			return nil, nil
		}
		answersPrompt = known
	}
	if state == nil && a.answerLoginForAbsentTask(ctx, resp, userProps, exchange) {
		return nil, nil
	}
	if state != nil && a.checkpoints != nil && signedInElsewhere(ctx, a.claimHeldLoginAnswer(ctx, state, resp), answersPrompt, handler, toolCtx) {
		a.logger.Info("login answer already exchanged by another AWE; running the call with the saved credential",
			"taskID", state.TaskID, "traceID", state.UserProps.TraceID, "functionCallID", resp.FunctionCallID)
		exchange = nil
	}

	var exchangeErr error
	if exchange != nil {
		exchangeErr = exchange()
		if exchangeErr == nil {
			a.logger.Info("MCP credential saved successfully", "credentialKey", resp.CredentialKey)
		}
	}
	if exchangeErr == nil {
		clearUserID, clearTraceID := userProps.UserID, userProps.TraceID
		if state != nil {
			clearUserID, clearTraceID = state.UserProps.UserID, state.UserProps.TraceID
		}
		a.clearAuthFailures(clearUserID, clearTraceID, resp.CredentialKey)
	}

	if state == nil {
		a.endAuthFlow(userProps.UserID, userProps.TraceID, resp.CredentialKey)
		a.resumeAuthFollowers(ctx, userProps.UserID, userProps.TraceID, resp.CredentialKey, "", resp.FunctionCallID)
		if exchangeErr != nil {
			if code := toolauth.ClassifyOAuth2Error(exchangeErr); code != "" {
				a.logger.Error("MCP auth response handling failed", "oauthErrorCode", code, "credentialKey", resp.CredentialKey)
			} else {
				a.logger.Error("MCP auth response handling failed", "error", exchangeErr, "credentialKey", resp.CredentialKey)
			}
			return a2a.NewErrorResponse(taskID, -32603, "Authorization failed: the authorization code could not be exchanged for a token ("+exchangeErr.Error()+"). Please try the operation again to restart the authorization process."), nil
		}
		return a.legacyAuthSavedResponse(taskID), nil
	}
	state.mu.Lock()
	pc, ok := state.PendingCalls[resp.FunctionCallID]
	if !ok || pc.Kind != PendingCallAuth {
		state.mu.Unlock()
		if exchangeErr != nil {
			a.logger.Info("auth_response for already-claimed pending call; treating as duplicate delivery",
				"credentialKey", resp.CredentialKey,
				"targetTaskID", resp.TargetAgentTaskID,
				"functionCallID", resp.FunctionCallID,
			)
		}
		return a.legacyAuthSavedResponse(taskID), nil
	}
	if pc.TimeoutTimer != nil {
		pc.TimeoutTimer.Stop()
	}
	delete(state.PendingCalls, pc.ToolCallID)
	pcSnapshot := *pc
	state.mu.Unlock()

	a.completeLoginCall(ctx, state, &pcSnapshot, resp, userProps, exchangeErr, &authMuUnlocked)
	return nil, nil
}

func (a *Agent) completeLoginCall(ctx context.Context, state *TaskExecutionState, pc *PendingToolCall, resp *a2a.AuthResponseData, userProps a2a.UserProperties, exchangeErr error, authMuUnlocked *bool) {
	pcSnapshot := *pc
	var result *tool.Result
	if exchangeErr != nil {
		result = tool.Error("Authorization failed: the authorization code could not be exchanged for a token (" + exchangeErr.Error() + "). Please try the operation again to restart the authorization process.")
		a.logger.Error("MCP auth response handling failed",
			"error", exchangeErr,
			"credentialKey", resp.CredentialKey,
			"taskID", state.TaskID,
			"toolCallID", pcSnapshot.ToolCallID,
		)
	} else {
		a.endAuthFlow(userProps.UserID, state.UserProps.TraceID, resp.CredentialKey)
		result, _ = a.dispatchToolSingle(ctx, state, &pcSnapshot)
	}

	authFailed := exchangeErr != nil
	if toolauth.IsPendingAuthResult(result) {
		if a.rearmPendingAuth(state, &pcSnapshot) {
			return
		}
		result = authRetryExhaustedError(result)
		authFailed = true
	}

	state.mu.Lock()
	state.SyncResults[pcSnapshot.Index] = result
	state.setPendingAuthOutcome(&authOutcome{
		success:         !authFailed,
		credentialKey:   resp.CredentialKey,
		functionCallID:  resp.FunctionCallID,
		toolDisplayName: pcSnapshot.ToolName,
	})
	state.mu.Unlock()
	a.checkpoints.recordResult(ctx, state.TaskID, &pcSnapshot, result)

	a.logger.Info("auth flow resumed",
		"taskID", resp.TargetAgentTaskID,
		"toolCallID", resp.FunctionCallID,
		"toolName", pcSnapshot.ToolName,
		"resultStatus", result.Status,
	)

	if state.DecrementPending() == 0 {
		a.ResumeTask(state.TaskID)
	}

	a.endAuthFlow(userProps.UserID, state.UserProps.TraceID, resp.CredentialKey)

	a.resumeFollowersInTask(ctx, state, resp.CredentialKey, resp.FunctionCallID, true)

	state.authMu.Unlock()
	*authMuUnlocked = true
	a.resumeAuthFollowers(ctx, userProps.UserID, state.UserProps.TraceID, resp.CredentialKey, state.TaskID, resp.FunctionCallID)
}

func authRetryExhaustedError(pending *tool.Result) *tool.Result {
	const base = "Authorization could not be completed after retry."
	if msg := toolauth.PendingAuthToolMessage(pending); msg != "" {
		return tool.Error(base + " " + msg + ". Please verify credentials and try the operation again.")
	}
	return tool.Error(base + " Please verify credentials and try the operation again.")
}

func (a *Agent) rearmPendingAuth(state *TaskExecutionState, pc *PendingToolCall) bool {
	pc.AuthRetries++
	if pc.AuthRetries >= 2 {
		a.logger.Warn("auth retry exhausted",
			"taskID", state.TaskID,
			"toolCallID", pc.ToolCallID,
			"toolName", pc.ToolName,
			"attempts", pc.AuthRetries,
		)
		return false
	}
	taskID := state.TaskID
	callID := pc.ToolCallID
	authTimeout := a.authPendingTimeout()
	newPC := &PendingToolCall{
		ToolCallID:     pc.ToolCallID,
		ToolName:       pc.ToolName,
		Kind:           PendingCallAuth,
		Index:          pc.Index,
		OriginalArgs:   pc.OriginalArgs,
		AuthRetries:    pc.AuthRetries,
		Timeout:        authTimeout,
		CredentialKey:  pc.CredentialKey,
		IsAuthFollower: pc.IsAuthFollower,
	}
	state.mu.Lock()
	state.PendingCalls[pc.ToolCallID] = newPC
	newPC.TimeoutTimer = time.AfterFunc(authTimeout, func() {
		a.handlePendingAuthTimeout(taskID, callID)
	})
	saveErr := a.checkpoints.save(context.Background(), state)
	state.mu.Unlock()
	if errors.Is(saveErr, errTaskLost) {
		a.logger.Warn("another process holds this task; standing down instead of prompting for sign-in again",
			"taskID", taskID, "traceID", state.UserProps.TraceID)
		a.standDownLostTask(state)
		return true
	}
	if saveErr != nil {
		a.logger.Warn("could not record the new sign-in prompt on the checkpoint; its answer is applied only by this AWE",
			slog.String("taskID", taskID), checkpointFailureAttr(saveErr))
	}
	a.logger.Info("auth flow re-pending after retry",
		"taskID", taskID,
		"toolCallID", callID,
		"retries", pc.AuthRetries,
	)
	return true
}

func (a *Agent) resumeFollowersInTask(ctx context.Context, state *TaskExecutionState, credentialKey, excludeFuncCallID string, holdsAuthMu bool) {
	if !holdsAuthMu {
		state.authMu.Lock()
		defer state.authMu.Unlock()
	}

	state.mu.Lock()
	var snapshots []PendingToolCall
	for _, pc := range state.PendingCalls {
		if pc.Kind != PendingCallAuth || pc.CredentialKey != credentialKey || pc.ToolCallID == excludeFuncCallID {
			continue
		}
		if pc.TimeoutTimer != nil {
			pc.TimeoutTimer.Stop()
		}
		delete(state.PendingCalls, pc.ToolCallID)
		snapshots = append(snapshots, *pc)
	}
	state.mu.Unlock()

	for i := range snapshots {
		pcSnap := snapshots[i]
		result, dispatchErr := a.dispatchToolSingle(ctx, state, &pcSnap)
		if dispatchErr != nil {
			a.logger.Error("auth follower re-dispatch failed",
				"taskID", state.TaskID,
				"toolCallID", pcSnap.ToolCallID,
				"toolName", pcSnap.ToolName,
				"credentialKey", credentialKey,
				"error", dispatchErr,
			)
			result = tool.Error("auth follower re-dispatch failed: " + dispatchErr.Error())
		}
		authFailed := dispatchErr != nil
		if toolauth.IsPendingAuthResult(result) {
			if a.rearmPendingAuth(state, &pcSnap) {
				continue
			}
			a.endAuthFlow(state.UserProps.UserID, state.UserProps.TraceID, credentialKey)
			result = authRetryExhaustedError(result)
			authFailed = true
		}
		state.mu.Lock()
		state.SyncResults[pcSnap.Index] = result
		state.setPendingAuthOutcome(&authOutcome{
			success:         !authFailed,
			credentialKey:   credentialKey,
			functionCallID:  pcSnap.ToolCallID,
			toolDisplayName: pcSnap.ToolName,
		})
		state.mu.Unlock()
		a.checkpoints.recordResult(ctx, state.TaskID, &pcSnap, result)
		a.logger.Info("auth follower resumed via leader sweep",
			"taskID", state.TaskID,
			"toolCallID", pcSnap.ToolCallID,
			"toolName", pcSnap.ToolName,
			"credentialKey", credentialKey,
			"resultStatus", result.Status,
		)
		if state.DecrementPending() == 0 {
			a.ResumeTask(state.TaskID)
		}
	}
}

func (a *Agent) resumeAuthFollowers(ctx context.Context, userID, traceID, credentialKey, excludeTaskID, excludeFuncCallID string) {
	for _, state := range a.pausedTasks.Snapshot() {
		if ctx.Err() != nil {
			return
		}
		if state.UserProps.UserID != userID {
			continue
		}
		if state.UserProps.TraceID != traceID {
			continue
		}
		if state.TaskID == excludeTaskID {
			continue
		}
		a.resumeFollowersInTask(ctx, state, credentialKey, excludeFuncCallID, false)
	}
}

func (a *Agent) legacyAuthSavedResponse(taskID string) *a2a.Response {
	return CredentialSavedResponse(taskID, a.cfg.Name)
}

func CredentialSavedResponse(taskID, component string) *a2a.Response {
	responseMsg := a2a.Message{
		Role: "agent",
		Parts: []a2a.Part{
			a2a.NewTextPart("Authentication successful. Please retry your request."),
		},
	}
	task := a2a.Task{
		ID:        taskID,
		ContextID: "",
		Kind:      "task",
		Status: a2a.TaskStatus{
			State:     a2a.TaskStateCompleted,
			Timestamp: time.Now().UTC().Format("2006-01-02T15:04:05.000000+00:00"),
			Message:   &responseMsg,
		},
		Metadata: map[string]any{
			"agent_name":             component,
			"is_mcp_credential_save": true,
		},
	}
	return a2a.NewResponse(taskID, task)
}

func cancelledTaskMetadata(agentName string, snap taskUsageSnapshot, facts *taskUsageFacts) map[string]any {
	metadata := map[string]any{"agent_name": agentName}
	if UsageRecorded(snap.usage) {
		tokenUsage := TokenUsageTotals(snap.usage)
		tokenUsage["last_input_tokens"] = snap.lastInputTokens
		metadata["token_usage"] = tokenUsage
	}
	if facts != nil {
		facts.AddTo(metadata)
	}
	return metadata
}

func buildCancelledResponse(taskID, contextID, agentName string, snap taskUsageSnapshot, facts *taskUsageFacts) *a2a.Response {
	return CancelledResponse(taskID, contextID, cancelledTaskMetadata(agentName, snap, facts))
}

func buildToolFailedResponse(taskID, contextID, agentName string, detail *tool.ToolFailedDetail) *a2a.Response {
	var parts []a2a.Part
	if detail != nil {
		parts = append(parts, a2a.NewDataPart(map[string]any{
			"a2a_signal_type": "tool_execution_error",
			"code":            detail.Code,
			"message":         detail.Message,
			"stderr_tail":     detail.StderrTail,
			"stack_trace":     detail.StackTrace,
		}))
	}
	parts = append(parts, a2a.NewTextPart("Tool execution failed."))
	responseMsg := a2a.Message{
		Kind:      "message",
		MessageID: fmt.Sprintf("msg-%s-tool-failed", taskID),
		Role:      a2a.RoleAgent,
		Parts:     parts,
	}
	task := a2a.Task{
		ID:        taskID,
		ContextID: contextID,
		Kind:      "task",
		Status: a2a.TaskStatus{
			State:     a2a.TaskStateFailed,
			Timestamp: time.Now().UTC().Format("2006-01-02T15:04:05.000000+00:00"),
			Message:   &responseMsg,
		},
		Metadata: map[string]any{
			"agent_name": agentName,
		},
	}
	return a2a.NewResponse(taskID, task)
}

func truncateWithMarker(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	return s[:maxBytes] + "[... truncated]"
}

type pendingHILRequest struct {
	ch          chan *a2a.UserInputResponseData
	ownerUserID string
	taskID      string
	approverSet bool
}

func (a *Agent) registerHILRequest(requestID, ownerUserID, taskID string) <-chan *a2a.UserInputResponseData {
	return a.registerPendingHIL(requestID, pendingHILRequest{ownerUserID: ownerUserID, taskID: taskID})
}

func (a *Agent) registerPendingHIL(requestID string, p pendingHILRequest) <-chan *a2a.UserInputResponseData {
	p.ch = make(chan *a2a.UserInputResponseData, 1)
	a.pendingHILMu.Lock()
	a.pendingHIL[requestID] = p
	a.pendingHILMu.Unlock()
	return p.ch
}

func (a *Agent) unregisterHILRequest(requestID string) {
	a.pendingHILMu.Lock()
	delete(a.pendingHIL, requestID)
	a.pendingHILMu.Unlock()
}

func (a *Agent) notifyHILEnabled() bool {
	return a.svc != nil && a.svc.FeatureRegistry() != nil && a.svc.FeatureRegistry().IsEnabled("notify_service")
}

func suppressCrossChannelHIL(caps map[string]bool) bool {
	return caps[tool.CapLiveWebSurface]
}

type hilRequest struct {
	requestID       string
	source          string
	recipientUserID string
	traceID         string
	sessionID       string
	surface         json.RawMessage
	webInboxOnly    bool
}

func (a *Agent) publishHILNotifyRequest(ctx context.Context, hr hilRequest, expiresAt string) {
	metadata := map[string]any{"source": hr.source}
	if hr.traceID != "" {
		metadata["trace_id"] = hr.traceID
	}
	if hr.sessionID != "" {
		metadata["session_id"] = hr.sessionID
	}
	var channels []string
	if hr.webInboxOnly {
		channels = []string{"web"}
	}
	req := a2a.NotifyRequest{
		RequestID:       hr.requestID,
		RecipientUserID: hr.recipientUserID,
		Mode:            a2a.NotifyModeInput,
		SenderUserID:    hr.recipientUserID,
		SenderAgent:     a.cfg.Name,
		Category:        a2a.CategoryHIL,
		RequesterAddr:   a2a.NotifyResponseTopic(a.cfg.Namespace, a.cfg.Name, hr.requestID),
		Channels:        channels,
		Title:           "Your input is requested",
		Surface:         hr.surface,
		ExpiresAt:       expiresAt,
		Metadata:        metadata,
	}
	if err := a.publishNotification(ctx, req); err != nil {
		a.logger.Warn("notify: failed to route HIL request via notification service (live-task path is the primary surface)",
			"requestID", hr.requestID, "source", hr.source, "error", err)
	}
}

func (a *Agent) handleNotifyHILResponse(msg *broker.Message) {
	a.handleNotifyHILResponseFrom(msg, false)
}

func (a *Agent) handleNotifyHILResponseFrom(msg *broker.Message, redeliverable bool) {
	ackedUpFront := a.checkpoints == nil
	if ackedUpFront {
		msg.Ack()
	}
	settle := func(recorded bool) {
		switch {
		case ackedUpFront:
		case recorded || !redeliverable:
			msg.Ack()
		default:
			msg.Nack()
		}
	}
	var resp a2a.UserInputResponseData
	if err := json.Unmarshal(msg.Payload, &resp); err != nil {
		a.logger.Warn("notify: malformed HIL response on notify topic; dropping", "topic", msg.Topic, "error", err)
		settle(true)
		return
	}
	if resp.RequestID == "" {
		a.logger.Warn("notify: HIL response missing request_id; dropping", "topic", msg.Topic)
		settle(true)
		return
	}
	if a.deliverHILResponse(&resp, hilSenderNotifyService) != hilUnmatched {
		settle(true)
		return
	}
	if !usableHILRequestID(resp.RequestID) {
		a.logger.Debug("dropping an answer whose request id is not usable", "requestIDBytes", len(resp.RequestID))
		settle(true)
		return
	}
	if a.checkpoints == nil {
		a.logger.Info("notify: HIL response with no matching pending request; ignoring",
			"requestID", resp.RequestID, "responseStatus", resp.Status)
		return
	}
	if a.recordAnswerForTaskHeldElsewhereThen(&resp, hilSenderNotifyService, settle) {
		return
	}
	traceID := a2a.DecodeUserProperties(msg.UserProperties).TraceID
	if redeliverable {
		a.logger.Warn("notify: could not queue a HIL response for recording; asking the broker to deliver it again",
			"requestID", resp.RequestID, "traceID", traceID, "responseStatus", resp.Status)
	} else {
		a.logger.Warn("notify: dropped a HIL response that could not be queued for recording; the task's holder may still apply it, otherwise the task waits out its input timeout",
			"requestID", resp.RequestID, "traceID", traceID, "responseStatus", resp.Status)
	}
	settle(false)
}

func (a *Agent) deliverHILResponse(resp *a2a.UserInputResponseData, sender hilSender) hilDelivery {
	if outcome := a.deliverHILResponseToBlockingTool(resp, sender); outcome != hilUnmatched {
		return outcome
	}
	if outcome := a.deliverHILResponseToPausedTask(resp, sender); outcome != hilUnmatched {
		return outcome
	}
	if a.checkpoints == nil {
		return hilUnmatched
	}
	if outcome := a.stashEarlyAnswer(resp, sender); outcome != hilUnmatched {
		return outcome
	}
	return a.deliverHILResponseToPausedTask(resp, sender)
}

func (a *Agent) deliverHILResponseToBlockingTool(resp *a2a.UserInputResponseData, sender hilSender) hilDelivery {
	a.pendingHILMu.Lock()
	pending, ok := a.pendingHIL[resp.RequestID]
	a.pendingHILMu.Unlock()
	if !ok {
		return hilUnmatched
	}
	if !sender.mayAnswerRequest(pending) {
		warnControlResponseRefused(a.logger.With("requestID", resp.RequestID), a2a.SignalUserInputResponse, sender)
		return hilRefused
	}
	select {
	case pending.ch <- resp:
	default:
	}
	return hilDelivered
}

func (a *Agent) deliverHILResponseToPausedTask(resp *a2a.UserInputResponseData, sender hilSender) hilDelivery {
	state, pc := a.pausedTasks.FindByHILRequestID(resp.RequestID)
	return a.deliverHILResponseToWait(state, pc, resp, sender)
}

func (a *Agent) deliverHILResponseToWaitOfTask(taskID string, resp *a2a.UserInputResponseData, sender hilSender) hilDelivery {
	state, pc := a.pausedTasks.FindHILWaitOfTask(taskID, resp.RequestID)
	return a.deliverHILResponseToWait(state, pc, resp, sender)
}

func (a *Agent) deliverHILResponseToWait(state *TaskExecutionState, pc *PendingToolCall, resp *a2a.UserInputResponseData, sender hilSender) hilDelivery {
	if state == nil {
		return hilUnmatched
	}
	if !sender.mayAnswer(state.UserProps.UserID) {
		warnControlResponseRefused(a.logger.With("requestID", resp.RequestID, "taskID", state.TaskID), a2a.SignalUserInputResponse, sender)
		return hilRefused
	}

	toolCallID := pc.ToolCallID
	idx := pc.Index
	result := hilResultFor(a.logger.With("taskID", state.TaskID, "traceID", state.UserProps.TraceID), pc, resp)

	if !state.claimPendingCall(toolCallID, idx, result) {
		return hilUnmatched
	}
	a.bankAnswer(state, pc, result)

	if pc.TimeoutTimer != nil {
		pc.TimeoutTimer.Stop()
	}

	a.logger.Info("HIL approval response delivered",
		"taskID", state.TaskID,
		"toolCallID", toolCallID,
		"requestID", resp.RequestID,
		"responseStatus", resp.Status,
	)

	if state.DecrementPending() == 0 {
		a.ResumeTask(state.TaskID)
	}
	return hilDelivered
}

func hilResult(logger *slog.Logger, data map[string]any) *tool.Result {
	j, err := json.Marshal(data)
	if err != nil {
		logger.Error("failed to marshal HIL result", "error", err)
		return tool.Error("internal error building HIL result")
	}
	return tool.OK(string(j), tool.WithData(data))
}

func buildApprovalResultFromHILResponse(logger *slog.Logger, resp *a2a.UserInputResponseData, timeout time.Duration) *tool.Result {
	switch resp.Status {
	case "answered":
		decision, _ := resp.Data["decision"].(string)
		decision = cmp.Or(decision, "unknown")
		approver, _ := resp.Data["approver"].(string)
		return hilResult(logger, map[string]any{"status": decision, "approver": approver})
	case "timed_out":
		msg := fmt.Sprintf("No response received within %d minutes.", int(timeout.Minutes()))
		return hilResult(logger, map[string]any{"status": "timed_out", "message": msg,
			"error_code": tool.DispatchErrorCode(taxonomy.DispatchClassUserInputUnanswered)})
	case "cancelled":
		return hilResult(logger, map[string]any{"status": "cancelled", "message": "Request was cancelled."})
	default:
		return hilResult(logger, map[string]any{"status": resp.Status, "message": "Unexpected response status."})
	}
}

func (a *Agent) extractUserInputResponse(msg a2a.Message) *a2a.UserInputResponseData {
	return UserInputResponseFromMessage(msg, a.logger)
}

func UserInputResponseFromMessage(msg a2a.Message, logger *slog.Logger) *a2a.UserInputResponseData {
	for _, p := range msg.Parts {
		if p.Kind != "data" || p.Data == nil {
			continue
		}
		dataMap, ok := p.Data.(map[string]any)
		if !ok {
			continue
		}
		if dataMap["type"] != "user_input_response" {
			continue
		}
		raw, err := json.Marshal(dataMap)
		if err != nil {
			logger.Warn("failed to marshal user_input_response data", "error", err)
			continue
		}
		var resp a2a.UserInputResponseData
		if err := json.Unmarshal(raw, &resp); err != nil {
			logger.Warn("failed to unmarshal user_input_response data", "error", err)
			continue
		}
		return &resp
	}
	return nil
}

func (a *Agent) waitForUserInput(ctx context.Context, sp *StatusPublisher, hr hilRequest, timeout time.Duration) *a2a.UserInputResponseData {
	ch := a.registerHILRequest(hr.requestID, hr.recipientUserID, sp.TaskID())
	defer a.unregisterHILRequest(hr.requestID)

	a.publishUserInputRequest(ctx, sp, hr, timeout)
	return awaitHILResponse(ctx, ch, hr.requestID, timeout)
}

func (a *Agent) publishPrompt(turn context.Context, sp *StatusPublisher, state *TaskExecutionState, hr hilRequest, timeout time.Duration) {
	promptCtx, release, ok := publishContext(turn, true, state)
	if !ok {
		a.logger.Debug("prompt dropped: the user cancelled the task",
			"taskID", sp.TaskID(), "traceID", hr.traceID, "requestIDBytes", len(hr.requestID))
		return
	}
	defer release()
	a.publishUserInputRequest(promptCtx, sp, hr, timeout)
}

func (a *Agent) publishUserInputRequest(ctx context.Context, sp *StatusPublisher, hr hilRequest, timeout time.Duration) {
	expiresAt := time.Now().Add(timeout).UTC().Format(time.RFC3339)

	sp.PublishUserInputRequest(ctx, a2a.UserInputRequestData{
		Type:      "user_input_request",
		RequestID: hr.requestID,
		ExpiresAt: expiresAt,
		Source:    hr.source,
		Surface:   hr.surface,
	})

	if a.notifyHILEnabled() {
		if hr.recipientUserID != "" {
			a.publishHILNotifyRequest(ctx, hr, expiresAt)
		} else {
			a.logger.Debug("notify: HIL not routed via notification service (task has no recipient user id)",
				"requestID", hr.requestID, "source", hr.source)
		}
	}
}

func awaitHILResponse(ctx context.Context, ch <-chan *a2a.UserInputResponseData, requestID string, timeout time.Duration) *a2a.UserInputResponseData {
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case resp := <-ch:
		return resp
	case <-timer.C:
		return &a2a.UserInputResponseData{
			Type:      "user_input_response",
			RequestID: requestID,
			Status:    "timed_out",
		}
	case <-ctx.Done():
		return &a2a.UserInputResponseData{
			Type:      "user_input_response",
			RequestID: requestID,
			Status:    "cancelled",
		}
	}
}

func backstopRefusal(result *tool.Result) tool.ApprovalRefusal {
	if result == nil {
		return ""
	}
	return result.ApprovalRefusal
}

func (a *Agent) auditApprovalRefusal(ctx context.Context, tCtx *tool.Context, toolName string, refusal tool.ApprovalRefusal) {
	event := auth.ToolExecutionEvent{
		AgentName:       a.cfg.Name,
		ToolName:        toolName,
		Success:         false,
		Error:           tool.AuditToolErrorCode,
		ApprovalRefusal: string(refusal),
	}
	provenance, _ := a.ToolProvenanceFor(toolName)
	event.ToolCategory = provenance.Category
	if tCtx != nil {
		caller := tCtx.Caller()
		event.UserID, event.SessionID, event.TraceID = caller.Sub, tCtx.SessionID, tCtx.TraceID
		event.CredentialID = caller.CredentialID
	}
	a.svc.AuditLogger().LogToolExecution(ctx, event)
}

func (a *Agent) checkToolApproval(ctx context.Context, sp *StatusPublisher, toolName string, toolArgs map[string]any, hilCfg HILToolConfig, tCtx *tool.Context) (*tool.Result, tool.ApprovalRefusal) {
	hr, timeout, err := a.toolApprovalRequest(toolName, toolArgs, hilCfg, tCtx)
	if err != nil {
		a.logger.Error("failed to generate approval surface", "tool", toolName, "error", err)
		return tool.Error("Internal error generating approval prompt."), tool.RefusalPromptFailed
	}
	resp, refused, refusal := a.askForApproval(ctx, sp, hr, toolName, hilCfg, timeout)
	if refused != nil {
		return refused, refusal
	}
	if hilCfg.Approvers.Configured() {
		if message, refusedByService := approverRefusalMessage(toolName, resp); refusedByService {
			return tool.Error(message), tool.RefusalNoApprovalRoute
		}
	}
	return toolApprovalOutcome(resp, timeout)
}

func (a *Agent) toolApprovalTimeout(hilCfg HILToolConfig) time.Duration {
	return cmp.Or(hilCfg.Timeout, a.cfg.HILDefaultTimeout, 45*time.Minute)
}

func (a *Agent) toolApprovalRequest(toolName string, toolArgs map[string]any, hilCfg HILToolConfig, tCtx *tool.Context) (hilRequest, time.Duration, error) {
	requestID := "hil-approval-" + idgen.New()
	timeout := a.toolApprovalTimeout(hilCfg)

	var schema *tool.JSONSchema
	if t := a.tools.GetTool(toolName); t != nil {
		schema = t.Parameters()
	}
	surface, err := generateApprovalSurface(requestID, toolName, agentDisplayName(&a.cfg), toolArgs, schema, hilCfg.ApprovalMessage, hilCfg.ShowArgs, timeout)
	if err != nil {
		return hilRequest{}, 0, err
	}

	approvalUserID, approvalTraceID, approvalSessionID := "", "", ""
	if tCtx != nil {
		approvalUserID, approvalTraceID, approvalSessionID = tCtx.UserID, tCtx.TraceID, tCtx.SessionID
	}
	return hilRequest{
		requestID:       requestID,
		source:          "tool_approval",
		recipientUserID: approvalUserID,
		traceID:         approvalTraceID,
		sessionID:       approvalSessionID,
		surface:         surface,
		webInboxOnly:    false,
	}, timeout, nil
}

func toolApprovalOutcome(resp *a2a.UserInputResponseData, timeout time.Duration) (*tool.Result, tool.ApprovalRefusal) {
	switch resp.Status {
	case "answered":
		approved, readable := resp.Data["approved"].(bool)
		switch {
		case readable && approved:
			return nil, ""
		case !readable:
			return tool.Error("Tool call denied by user."), tool.RefusalUnreadable
		}
		return tool.Error("Tool call denied by user."), tool.RefusalDenied
	case "cancelled":
		return tool.Error("Tool approval cancelled by user."), tool.RefusalCancelled
	case "timed_out":
		return tool.Error(fmt.Sprintf("Tool approval timed out after %d minutes.", int(timeout.Minutes()))), tool.RefusalTimedOut
	default:
		return tool.Error("Unexpected approval response."), tool.RefusalUnexpected
	}
}

func (a *Agent) taskRunningHere(taskID string) bool {
	a.activeTasksMu.Lock()
	defer a.activeTasksMu.Unlock()
	_, running := a.activeTasks[taskID]
	return running
}
