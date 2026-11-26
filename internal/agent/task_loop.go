// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/structured"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth/toolauth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/observability"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool/remote"
)

const defaultAuthPendingTimeout = 10 * time.Minute

const defaultAuthMaxAttempts = 2

var hilApprovalDefaultTimeout = 45 * time.Minute

var resumeTaskHook func(*Agent, *TaskExecutionState)

type taskRunContext struct {
	sp                *StatusPublisher
	toolCtx           *tool.Context
	structuredCtx     *structured.Context
	gc                gatewayContext
	sess              *session.Session
	originalSessionID string
	msg               a2a.Message
	skillState        tool.SkillStateProvider
	logger            *slog.Logger
	sessMetaFP        string
	unstoredMessages  int
	storedFinalAnswer *string
}

func (rc *taskRunContext) structuredRetryAnswered() {
	if rc.structuredCtx != nil {
		rc.structuredCtx.RetryPending = false
	}
}

const alwaysDirtyFingerprint = ""

func metadataFingerprint(m map[string]any) string {
	b, err := json.Marshal(m)
	if err != nil {
		return alwaysDirtyFingerprint
	}
	return string(b)
}

func (a *Agent) authenticateAndAuthorize(ctx context.Context, taskID string, userProps a2a.UserProperties, logger *slog.Logger) (*auth.Identity, *a2a.Response) {
	var identity *auth.Identity
	if userProps.AuthToken == "" {
		if _, allowsEmpty := a.svc.Authenticator().(auth.EmptyTokenAuthenticator); !allowsEmpty {
			logger.Warn("rejecting task with no auth token",
				"taskID", taskID,
				"userID", userProps.UserID,
				"isPeerCall", userProps.IsPeerCall(),
			)
			a.svc.AuditLogger().LogAuth(ctx, auth.AuthEvent{
				UserID:  userProps.UserID,
				TaskID:  taskID,
				Success: false,
				Reason:  "missing auth token",
			})
			return nil, a2a.NewErrorResponse(taskID, -32600, "Authentication failed")
		}
	}
	if userProps.AuthToken != "" {
		var authErr error

		isPeerCall := userProps.IsPeerCall()
		if isPeerCall {
			if pa, ok := a.svc.Authenticator().(auth.PeerAuthenticator); ok {
				identity, authErr = pa.AuthenticateWithSoftTaskBinding(ctx, userProps.AuthToken, "")
			} else {
				identity, authErr = a.svc.Authenticator().Authenticate(ctx, userProps.AuthToken, taskID)
			}
		} else {
			identity, authErr = a.svc.Authenticator().Authenticate(ctx, userProps.AuthToken, taskID)
		}

		if authErr != nil {
			logger.Warn("task authentication failed",
				"taskID", taskID,
				"userID", userProps.UserID,
				"isPeerCall", isPeerCall,
				"error", authErr,
			)
			a.svc.AuditLogger().LogAuth(ctx, auth.AuthEvent{
				UserID: userProps.UserID, TaskID: taskID, Success: false, Reason: authErr.Error(),
			})
			return nil, a2a.NewErrorResponse(taskID, -32600, "Authentication failed")
		}
		if identity != nil {
			a.svc.AuditLogger().LogAuth(ctx, auth.AuthEvent{
				UserID: identity.UserID, CredentialID: identity.CredentialID, TaskID: taskID, Success: true,
			})
		}
	}

	if a.cfg.MaxCallDepth > 0 && userProps.CallDepth > a.cfg.MaxCallDepth {
		logger.Warn("call depth exceeded",
			"callDepth", userProps.CallDepth,
			"maxCallDepth", a.cfg.MaxCallDepth,
		)
		a.svc.AuditLogger().LogAuth(ctx, auth.AuthEvent{
			UserID:  userProps.UserID,
			TaskID:  taskID,
			Success: false,
			Reason:  fmt.Sprintf("call depth %d exceeds maximum %d", userProps.CallDepth, a.cfg.MaxCallDepth),
		})
		return nil, a2a.NewErrorResponse(taskID, -32600,
			fmt.Sprintf(taxonomy.CallDepthExceededFormat, userProps.CallDepth, a.cfg.MaxCallDepth))
	}

	applySoftSubsetAssert(
		identity,
		userProps.A2AUserConfig,
		taskID,
		userProps.DelegatingAgentName,
		userProps.UserID,
		logger,
	)

	if len(a.cfg.RequiredScopes) > 0 {
		if err := a.svc.AgentAuthorizer().AuthorizeAgentScopes(ctx, identity, a.cfg.RequiredScopes); err != nil {
			logger.Warn("agent required scopes check failed",
				"userID", userProps.UserID,
				"taskID", taskID,
				"requiredScopes", a.cfg.RequiredScopes,
			)
			return nil, a2a.NewErrorResponse(taskID, -32600, "Access denied: insufficient scopes")
		}
	}

	return identity, nil
}

func (a *Agent) interceptControlMessage(ctx context.Context, taskID string, msg a2a.Message, userProps a2a.UserProperties, logger *slog.Logger) (bool, *a2a.Response, error) {
	if resp := a.extractAuthResponse(msg); resp != nil {
		earlyResp, err := a.handleAuthResponse(ctx, taskID, resp, userProps)
		return true, earlyResp, err
	}

	if resp := a.extractUserInputResponse(msg); resp != nil {
		passOn := func() { a.forwardControlMessage(ctx, taskID, msg, userProps) }
		switch a.routeUserInputResponseThenPassOn(ctx, resp, hilSenderUser(userProps.UserID), passOn) {
		case hilUnmatched:
			logger.Warn("received user_input_response with no matching pending request",
				hilRequestIDAttr(resp.RequestID),
				slog.String("responseStatus", resp.Status),
			)
			a.forwardControlMessage(ctx, taskID, msg, userProps)
		case hilUnchecked:
			a.forwardControlMessage(ctx, taskID, msg, userProps)
		case hilNotRecorded:
			a.forwardControlMessage(ctx, taskID, msg, userProps)
			return true, nil, errAnswerNotRecorded
		}
		return true, nil, nil
	}

	return false, nil, nil
}

var errAnswerNotRecorded = errors.New("a user's answer could not be recorded")

func (a *Agent) isControlMessage(msg a2a.Message) bool {
	return a.extractAuthResponse(msg) != nil || a.extractUserInputResponse(msg) != nil
}

func (a *Agent) routeUserInputResponse(ctx context.Context, resp *a2a.UserInputResponseData, sender hilSender) hilDelivery {
	return a.routeUserInputResponseThenPassOn(ctx, resp, sender, nil)
}

func (a *Agent) routeUserInputResponseThenPassOn(ctx context.Context, resp *a2a.UserInputResponseData, sender hilSender, passOn func()) hilDelivery {
	if outcome := a.deliverHILResponse(resp, sender); outcome != hilUnmatched {
		return outcome
	}
	if a.peerRouter != nil {
		if outcome := a.peerRouter.ForwardUserInputResponse(ctx, a.cfg.Namespace, resp, sender); outcome != hilUnmatched {
			return outcome
		}
	}
	return a.relayUserInputResponse(ctx, resp, sender, passOn)
}

func structuredInputPart(msg a2a.Message, sc *structured.Context) int {
	if sc == nil {
		return -1
	}
	return structured.InputFilePartIndex(msg, sc.InputSchema)
}

func (a *Agent) detectStructuredInvocation(taskID string, msg a2a.Message, logger *slog.Logger) (*structured.Context, *a2a.Response, error) {
	if a.structuredHandler == nil {
		return nil, nil, nil
	}
	req := a.structuredHandler.ExtractRequest(msg)
	if req == nil {
		return nil, nil, nil
	}
	structuredCtx := a.structuredHandler.BuildContext(req)
	logger.Debug("structured invocation detected",
		"workflow", req.WorkflowName,
		"nodeID", req.NodeID,
	)
	if errs := a.structuredHandler.ValidateInput(msg, structuredCtx); errs != nil {
		earlyResp, err := a.buildStructuredResponse(taskID, msg,
			a.structuredHandler.BuildErrorResult(
				structured.InputValidationFailedPrefix+strings.Join(errs, "; "), errs, 0),
			taskUsageSnapshot{}, nil)
		return nil, earlyResp, err
	}
	return structuredCtx, nil, nil
}

func resolveInvocationModifiers(msg a2a.Message, isStructured bool) (isRunBased bool, callerVolumeIDs []string) {
	if !isStructured {
		if sb, ok := msg.Metadata["sessionBehavior"].(string); ok && sb == "RUN_BASED" {
			isRunBased = true
		}
	}
	if vcRaw, ok := msg.Metadata["volume_context"].([]any); ok {
		for _, v := range vcRaw {
			if s, ok := v.(string); ok {
				callerVolumeIDs = append(callerVolumeIDs, s)
			}
		}
	}
	return isRunBased, callerVolumeIDs
}

func displaysTemplateSignals(msg a2a.Message) bool {
	displays, _ := msg.Metadata[a2a.MetadataDisplaysTemplateSignals].(bool)
	return displays
}

func callerDisplaysTemplates(msg a2a.Message, isRunBased, rootDisplays bool, arts *artifact.ScopedService) bool {
	parentTaskID, _ := msg.Metadata[metaKeyParentTaskID].(string)
	return isRunBased && parentTaskID != "" && rootDisplays && arts != nil && arts.Scope == artifact.ScopeNamespace
}

func schedulerHistoryMessages(metadata map[string]any, logger *slog.Logger) []session.Message {
	raw, ok := metadata["schedulerConversationHistory"]
	if !ok {
		return nil
	}
	entries, ok := raw.([]any)
	if !ok {
		logger.Warn("schedulerConversationHistory has unexpected type", "type", fmt.Sprintf("%T", raw))
		return nil
	}
	var msgs []session.Message
	for _, e := range entries {
		m, ok := e.(map[string]any)
		if !ok {
			continue
		}
		role, _ := m["role"].(string)
		content, _ := m["content"].(string)
		if content == "" || (role != "user" && role != "assistant") {
			continue
		}
		msgs = append(msgs, session.Message{Role: role, Content: content})
	}
	return msgs
}

func reseedSchedulerHistoryOnlyIntoEmptySession(sess *session.Session, metadata map[string]any, logger *slog.Logger) int {
	if len(sess.Messages) != 0 {
		return 0
	}
	injected := schedulerHistoryMessages(metadata, logger)
	if len(injected) == 0 {
		return 0
	}
	sess.Messages = injected
	logger.Info("injected scheduler conversation history", "count", len(injected))
	return len(injected)
}

func (a *Agent) setupTask(
	ctx context.Context,
	taskID string,
	msg a2a.Message,
	userProps a2a.UserProperties,
	delivery taskDelivery,
) (*TaskExecutionState, *taskRunContext, *a2a.Response, error) {

	taskLogger := a.logger.With(
		"traceID", userProps.TraceID,
		"taskID", taskID)

	identity, earlyResp := a.authenticateAndAuthorize(ctx, taskID, userProps, taskLogger)
	if earlyResp != nil {
		return nil, nil, earlyResp, nil
	}

	if handled, earlyResp, err := a.interceptControlMessage(ctx, taskID, msg, userProps, taskLogger); handled {
		return nil, nil, earlyResp, err
	}

	userProps = a.withRoot(userProps)
	userProps.EntrypointName = userProps.ResolveEntrypointName()
	spendOrigin := SpendOriginOf(msg, userProps)

	taskModelOverride, overrideErrReason := a.resolveTaskModelOverride(ctx, msg.Metadata, taskLogger)
	if overrideErrReason != "" {
		return nil, nil, a2a.NewErrorResponse(taskID, -32600,
			"Model override resolution failed: "+overrideErrReason), nil
	}

	originalSessionID := msg.ContextID
	if psid, ok := msg.Metadata["parentSessionID"].(string); ok && psid != "" {
		originalSessionID = psid
	}

	effectiveContextID := msg.ContextID
	if effectiveContextID == "" {
		effectiveContextID = idgen.New()
	}

	structuredCtx, structErrResp, structErr := a.detectStructuredInvocation(taskID, msg, taskLogger)
	if structErrResp != nil || structErr != nil {
		return nil, nil, structErrResp, structErr
	}

	isRunBased, callerVolumeIDs := resolveInvocationModifiers(msg, structuredCtx != nil)

	userText := extractText(msg)

	userText, userImageBlocks, uploadsWithheld := a.processFileParts(ctx, msg, userProps, originalSessionID, userText, structuredInputPart(msg, structuredCtx))

	userText = a.processInvokedArtifacts(ctx, msg, userProps, userText)

	lookupTaskID := ""
	if isRunBased || structuredCtx != nil {
		lookupTaskID = taskID
	}
	var claims siblingClaims
	if lookupTaskID == "" {
		claims = a.readSiblingClaims(ctx, a.cfg.Name, sessionUserID(userProps.UserID), effectiveContextID, taskLogger)
	}
	sess, err := a.getOrCreateSession(ctx, userProps.UserID, effectiveContextID, lookupTaskID)
	if err != nil {
		taskLogger.Error("session create or load failed", "error", err)
		return nil, nil, a2a.NewErrorResponse(taskID, -32603, "session error: "+err.Error()), nil
	}
	effectiveSessionID := sess.SessionID

	unstoredMessages := reseedSchedulerHistoryOnlyIntoEmptySession(sess, msg.Metadata, taskLogger)

	taskLogger = taskLogger.With(
		"sessionID", effectiveSessionID,
		"contextID", msg.ContextID,
	)

	takeUp, storedRows, err := a.decideContinuation(ctx, delivery, taskID, sess, taskLogger)
	if err != nil {
		taskLogger.Error("checkpoint read for a redelivered task failed", "error", err)
		return nil, nil, a2a.NewErrorResponse(taskID, -32603, "checkpoint error: "+err.Error()), nil
	}
	if takeUp == dropRedelivery {
		return nil, nil, nil, nil
	}

	sp := NewStatusPublisher(a.svc.Broker(), StatusPublisherConfig{
		StatusTopic: userProps.A2AStatusTopic,
		TaskID:      taskID,
		ContextID:   msg.ContextID,
		AgentName:   a.cfg.Name,
		ClientID:    userProps.ClientID,
		UserID:      userProps.UserID,
		UserConfig:  userProps.A2AUserConfig,
		TraceID:     userProps.TraceID,
		SeqGen:      a.seqGen,
	}, a.logger)

	gatewayMetadata := gatewayMetadataOf(msg)
	gc := gatewayContextFrom(gatewayMetadata, userProps, displaysTemplateSignals(msg))

	a.claimRunningTask(ctx, session.RunningTask{
		TaskID: taskID, AppName: sess.AppName, UserID: sess.UserID, SessionID: effectiveSessionID,
	}, taskLogger)
	claimHandedToCaller := false
	defer func() {
		if !claimHandedToCaller {
			a.releaseRunningTask(taskID)
		}
	}()
	turns := a.historyTurnsFor(ctx, sess, taskID, a.overrideStubBatchBytes(ctx, taskModelOverride), claims, taskLogger)
	if takeUp == rerunFailedTask {
		turns.ownRows = ownRowsOmitted
		takeUp = startAfresh
	}
	if takeUp != startAfresh {
		turns.ownRows = ownRowsAfterQuestion
	}
	messages, historyOffset := a.buildMessagesForTask(ctx, identity, sess, turns, userText, gc, userImageBlocks...)
	a.stampUserTurnWithheld(messages, historyOffset, uploadsWithheld)
	question := messages[historyOffset]

	if takeUp != startAfresh {
		messages = repairContinuedPrompt(messages, historyOffset, taskLogger)
		historyOffset = len(messages)
	} else {
		messages, historyOffset = repairDanglingToolCalls(messages, historyOffset, taskLogger, messages[historyOffset:])

		messages, historyOffset = repairOrphanUserMessages(messages, historyOffset, taskLogger)
	}

	maxCalls := a.cfg.MaxLLMCalls
	if maxCalls <= 0 {
		maxCalls = 20
	}

	subTaskBudget := new(atomic.Int64)
	searchTurns := &tool.SearchTurns{}
	rootDisplays := displaysTemplateSignals(msg)

	toolCtx := &tool.Context{
		Identity:                identity,
		UserID:                  userProps.UserID,
		AgentName:               a.cfg.Name,
		SessionID:               originalSessionID,
		ArtifactService:         a.svc.Artifacts(),
		CredentialService:       a.svc.CredentialService(),
		Namespace:               a.cfg.Namespace,
		A2AStatusTopic:          userProps.A2AStatusTopic,
		A2AUserConfig:           userProps.A2AUserConfig,
		ParentTaskID:            taskID,
		ClientID:                userProps.ClientID,
		TraceID:                 userProps.TraceID,
		Trigger:                 userProps.Trigger,
		RootKind:                userProps.RootKind,
		RootID:                  userProps.RootID,
		RootNodeID:              userProps.RootNodeID,
		Origin:                  userProps.Origin,
		DisplaysTemplateSignals: rootDisplays,
		CallDepth:               userProps.CallDepth,
		IncomingInstruction:     extractText(msg),
		MaxSubTaskFanout:        a.cfg.MaxSubTaskFanout,
		SubTaskCount:            subTaskBudget,
		SearchTurns:             searchTurns,
		ProjectID:               gc.ProjectID,
		PauseResume:             true,
		ToolConfig:              a.buildToolConfig(),
	}
	if len(callerVolumeIDs) > 0 {
		toolCtx.CallerVolumeIDs = callerVolumeIDs
	}
	toolCtx.PauseRemoteTools = a.checkpoints != nil
	if len(userProps.GatewayCapabilities) > 0 {
		toolCtx.GatewayCapabilities = userProps.GatewayCapabilities
	}
	a.wireTaskHooks(ctx, toolCtx, sp)

	var skillState tool.SkillStateProvider
	if a.skillManager != nil && SkillStateFactory != nil {
		skillState = SkillStateFactory(sess)
		toolCtx.SkillState = skillState
		a.rehydrateSkills(ctx, toolCtx)
	}

	state := &TaskExecutionState{
		TaskID:                  taskID,
		ContextID:               msg.ContextID,
		OriginalSessionID:       originalSessionID,
		EffectiveSessionID:      effectiveSessionID,
		ReplyTo:                 userProps.ReplyTo,
		UserProps:               userProps,
		SpendOrigin:             spendOrigin,
		Identity:                identity,
		IsRunBased:              isRunBased,
		CallerDisplaysTemplates: callerDisplaysTemplates(msg, isRunBased, rootDisplays, a.svc.Artifacts()),
		DisplaysTemplateSignals: rootDisplays,
		QuietEndAllowed:         quietEndAllowed(msg, userProps),
		GatewayMetadata:         gatewayMetadata,
		kind:                    agentTaskKind(msg),
		Messages:                messages,
		HistoryOffset:           historyOffset,
		Question:                question,
		MaxCalls:                maxCalls,
		CreatedAt:               time.Now(),
		PendingCalls:            make(map[string]*PendingToolCall),
		SyncResults:             make(map[int]*tool.Result),
		DoneCh:                  make(chan *a2a.Response, 1),
		StructuredCtx:           structuredCtx,
		subTaskCount:            subTaskBudget,
		searchTurns:             searchTurns,
	}
	state.setModelOverride(taskModelOverride)
	if takeUp != startAfresh {
		state.seedFromOwnRows(storedRows)
	}
	state.usageFacts.SetLogger(a.logger)
	a.registerActiveTaskFacts(state.TaskID, &state.usageFacts)

	a.wireTaskStateHooks(ctx, toolCtx, sp, state)

	rc := &taskRunContext{
		sp:                sp,
		toolCtx:           toolCtx,
		structuredCtx:     structuredCtx,
		gc:                gc,
		sess:              sess,
		originalSessionID: originalSessionID,
		msg:               msg,
		skillState:        skillState,
		logger:            taskLogger,
		sessMetaFP:        metadataFingerprint(sess.Metadata),
		unstoredMessages:  unstoredMessages,
	}
	if takeUp == republishFinal {
		rc.storedFinalAnswer = storedFinalAnswer(storedRows)
		state.endedQuietly = storedFinalQuiet(storedRows)
	}

	claimHandedToCaller = true
	return state, rc, nil, nil
}

func markFailedTaskUnanswered(ctx context.Context, state *TaskExecutionState, resp **a2a.Response) {
	failed := *resp != nil && (*resp).Error != nil
	if !failed || ctx.Err() != nil || len(state.Messages) == 0 {
		return
	}
	if state.Messages[len(state.Messages)-1].Role != "assistant" {
		appendTerminalMarker(state, failedTaskMarkerContent)
	}
}

func (a *Agent) runFirstTurn(ctx context.Context, state *TaskExecutionState, rc *taskRunContext) (*a2a.Response, error) {
	release, _ := markResuming(state)
	defer a.endResumeClaim(state, release)
	return a.continueTaskLoop(ctx, state, rc)
}

func (a *Agent) continueTaskLoop(
	ctx context.Context,
	state *TaskExecutionState,
	rc *taskRunContext,
) (resp *a2a.Response, err error) {
	defer observability.NewAgentMonitor(a.svc.Metrics(), agentDisplayName(&a.cfg)).Stop(&err)

	defer a.flushSessionMessagesFinal(ctx, state, rc)
	defer markFailedTaskUnanswered(ctx, state, &resp)

	a.flushSessionMessagesMidTask(ctx, state, rc)

	var cache promptToolCache

	var allText replyText

	var leakedRetry leakedCallRetry

	for ; state.LoopIteration < state.MaxCalls && !state.finishAfterArtifacts; state.LoopIteration++ {
		llmTools := a.withholdRepeatFailingTool(state, rc, a.refreshToolsAndPrompt(ctx, state, rc, &cache))

		reqMessages := a.appendStructuredInstructions(a.appendPerRequestContext(state.Messages, rc.gc), rc)

		lastAllowed := state.LoopIteration == state.MaxCalls-1 && state.LoopIteration > 0
		if lastAllowed {
			reqMessages = a.injectLastCallWarning(reqMessages)
		}
		reqMessages = leakedRetry.remind(reqMessages)
		reqMessages = state.repeatFailures.remind(reqMessages)

		chatReq := a.agentTurnRequest(ctx, state, reqMessages, llmTools)

		reportModel := a.callReportModel(ctx, state)
		rc.sp.ClassifyModel(state.usageFacts.recordModel(callModel(state, reportModel, a.reportedModelAPIBase())))
		if a.guardAgainstFabricatedTurn(ctx, state, chatReq, state.Messages) {
			rc.logger.Debug("stop sequences set on the turn after an artifact notification", "call", state.LoopIteration)
		}

		rc.sp.PublishLLMInvocationDelta(ctx, chatReq, reportModel, lastAllowed)

		prependSpacing := state.LoopIteration > 0 && state.HadVisibleText && !state.IsContinuation
		state.IsContinuation = false
		remainingCalls := state.MaxCalls - state.LoopIteration - 1
		if remainingCalls < 0 {
			remainingCalls = 0
		}
		sci := &streamCallInfo{
			UserID:                     state.UserProps.UserID,
			SessionID:                  rc.originalSessionID,
			UserTimezone:               rc.gc.UserTimezone,
			PrependTurnSpacing:         prependSpacing,
			MaxContinuations:           remainingCalls,
			IsStructuredInvocation:     rc.structuredCtx != nil,
			WithholdLeakedToolCall:     true,
			IsWorkflowDispatch:         state.kind == a2a.TaskKindWorkflowAgent,
			RenderTemplatesInline:      state.IsRunBased,
			CallerDisplaysTemplates:    state.CallerDisplaysTemplates,
			BlockValidationRetriesLeft: maxBlockValidationRetries - state.BlockValidationRetries,
			Logger:                     rc.logger,
		}
		rc.logger.Debug("LLM request starting",
			"call", state.LoopIteration,
			"messages", len(reqMessages),
			"tools", len(llmTools),
		)

		streamResp, createdArtifacts, err := a.streamLLMCall(ctx, rc.sp, chatReq, sci)
		state.LoopIteration += sci.ContinuationsUsed
		if err != nil {
			resp, outcome, retErr := a.handleStreamCallError(ctx, state, rc, streamResp, err, &allText)
			if outcome == loopContinue {
				continue
			}
			return resp, retErr
		}
		state.LLMCallCount++
		leakedRetry.delivered()
		rc.structuredRetryAnswered()
		state.LastFinishReason = streamResp.FinishReason
		state.LastContent = streamResp.Content
		if streamResp.Content != "" {
			state.HadVisibleText = true
		}

		logLLMResponse(rc.logger, state.LoopIteration, streamResp, len(createdArtifacts))

		state.recordCreatedArtifacts(createdArtifacts)

		a.foldToolLLMUsage(state)
		if streamResp.Usage != nil {
			state.recordCallUsage(reportModel, *streamResp.Usage)
			state.LastInputTokens = streamResp.Usage.PromptTokens
			a.recordActiveTaskUsage(state.TaskID, state.usageSnapshot())
		}

		streamResp.ToolCalls = uniquifyToolCallIDs(streamResp.ToolCalls, state.Messages)

		allToolCalls := streamResp.ToolCalls

		var notifyErrors []llm.ChatMessage
		streamResp.ToolCalls, notifyErrors = filterNotifyArtifactCalls(streamResp.ToolCalls, rc.logger)
		var directiveErrors []llm.ChatMessage
		streamResp.ToolCalls, directiveErrors = redirectDirectiveCalls(streamResp.ToolCalls,
			func(name string) bool { return a.tools.GetTool(name) != nil }, rc.logger)
		notifyErrors = append(notifyErrors, directiveErrors...)

		var sanitizeErrors []llm.ChatMessage
		streamResp.ToolCalls, sanitizeErrors = sanitizeToolCalls(streamResp.ToolCalls, rc.logger)

		var argErrors []llm.ChatMessage
		streamResp.ToolCalls, argErrors = rejectMalformedToolArgs(streamResp.ToolCalls, streamResp.FinishReason, rc.logger)
		allToolCalls = clearMalformedToolArgs(allToolCalls)

		var continueResults []llm.ChatMessage
		if a.autoContinuationEnabled() {
			turnHasOtherWork := len(notifyErrors)+len(sanitizeErrors)+len(argErrors) > 0 || len(createdArtifacts) > 0
			streamResp.ToolCalls, continueResults = answerImplicitContinueCalls(streamResp.ToolCalls, turnHasOtherWork, rc.logger)
		}
		combinedErrors := slices.Concat(notifyErrors, sanitizeErrors, argErrors, continueResults)

		if len(combinedErrors) > 0 && len(streamResp.ToolCalls) == 0 && len(createdArtifacts) == 0 {
			state.Messages = append(state.Messages, assistantToolCallTurn(streamResp, allToolCalls))
			state.Messages = append(state.Messages, combinedErrors...)
			continue
		}

		rejected := rejectedToolCalls{calls: allToolCalls, results: slices.Concat(notifyErrors, sanitizeErrors, argErrors)}
		llmResponseAlreadyPublished, artifactsDone, artifactResp := a.handleCreatedArtifacts(ctx, state, rc, streamResp, createdArtifacts, rejected, &allText)
		if artifactsDone {
			if artifactResp != nil {
				return artifactResp, nil
			}
			continue
		}

		implicit := implicitNone
		if a.autoContinuationEnabled() && onlyImplicitContinueCalls(streamResp.ToolCalls) {
			streamResp.ToolCalls = nil
			capLimit := implicitContinuationCapBelowInjectGuard(state.MaxCalls)
			if state.ImplicitContinuations < capLimit {
				state.ImplicitContinuations++
				implicit = implicitHonored
				rc.logger.Warn("LLM emitted internal continuation call; auto-continuing",
					"implicitContinuations", state.ImplicitContinuations, "limit", capLimit)
			} else {
				implicit = implicitCapCut
				rc.logger.Warn("implicit continuation limit reached; treating response as final",
					"implicitContinuations", state.ImplicitContinuations, "limit", capLimit)
			}
		}

		if sci.LeakedToolCall != "" {
			if resp := leakedRetry.discard(state, rc.logger, sci.LeakedToolCall); resp != nil {
				return resp, nil
			}
			continue
		}

		if len(streamResp.ToolCalls) == 0 {
			resp, outcome, retErr := a.handleNoToolCallsTermination(
				ctx, state, rc, streamResp, &allText, llmResponseAlreadyPublished, implicit)
			switch outcome {
			case loopContinue:
				continue
			case loopBreak:
				goto loopExit
			default:
				return resp, retErr
			}
		}

		if outcome, resp := a.executeToolTurn(ctx, state, rc, streamResp, allToolCalls, createdArtifacts, combinedErrors); outcome == loopReturn {
			return resp, nil
		}
	}
loopExit:

	_ = state.LastFinishReason

	rc.logger.Debug("task completed",
		"taskID", state.TaskID,
		"llmCalls", state.LLMCallCount,
		"promptTokens", state.TotalUsage.PromptTokens,
		"completionTokens", state.TotalUsage.CompletionTokens,
		"cachedTokens", state.TotalUsage.CachedTokens,
		"cacheWriteTokens", state.TotalUsage.CacheWriteTokens,
		"lastInputTokens", state.LastInputTokens,
	)

	finalContent, historyContent := allText.final(state.ContinuationParts, state.LastContent)

	if rc.structuredCtx != nil {
		return a.finalizeStructuredInvocation(ctx, state, rc, finalContent)
	}

	returns := namedFileReturns(finalContent, a.returnableFiles(state))
	return a.finalizeResponse(ctx, state, rc, finalContent+returns, historyContent+returns)
}

type replyText struct {
	visible strings.Builder
	history strings.Builder
}

func (r *replyText) add(visible, history string) {
	appendResponseText(&r.visible, visible)
	appendResponseText(&r.history, history)
}

func (r *replyText) String() string { return r.visible.String() }

func (r *replyText) final(continuationParts []string, lastContent string) (visible, history string) {
	if r.visible.Len() == 0 && r.history.Len() == 0 && len(continuationParts) == 0 {
		return lastContent, lastContent
	}
	prefix := strings.Join(continuationParts, "")
	return prefix + r.visible.String(), prefix + r.history.String()
}

func (r *replyText) reset() {
	r.visible.Reset()
	r.history.Reset()
}

func appendResponseText(reply *strings.Builder, text string) {
	if text == "" {
		return
	}
	if reply.Len() > 0 {
		switch prev := reply.String(); {
		case strings.HasSuffix(prev, "\n\n"):
		case strings.HasSuffix(prev, "\n"):
			reply.WriteString("\n")
		default:
			reply.WriteString("\n\n")
		}
	}
	reply.WriteString(text)
}

func (a *Agent) finalizeResponse(ctx context.Context, state *TaskExecutionState, rc *taskRunContext, finalContent, historyContent string) (*a2a.Response, error) {
	finalContent = structured.StripResultEmbeds(finalContent)
	historyContent = structured.StripResultEmbeds(historyContent)

	if state.CompactionSummary != "" {
		notice := "\n\n---\n\n" + compactionNotice
		finalContent += notice
		historyContent += notice
		rc.sp.PublishText(ctx, notice)
	}

	if state.IsRunBased {
		rc.sp.PublishFinal(ctx, finalContent)
	}

	state.Messages = append(state.Messages, llm.ChatMessage{
		Role:    "assistant",
		Content: historyContent,
	})
	state.answered.Store(true)
	state.runFacts.markFinal(finalContent, historyContent)

	a.flushSessionMessagesMidTask(ctx, state, rc)
	return a.buildFinalResponse(ctx, state, finalContent), nil
}

func (a *Agent) buildFinalResponse(ctx context.Context, state *TaskExecutionState, finalContent string) *a2a.Response {
	var responseParts []a2a.Part
	for _, art := range state.CreatedArtifacts {
		status := art.resolvedStatus()
		partData := map[string]any{
			"filename": art.Filename,
			"version":  art.Version,
			"status":   status,
		}
		if status == "success" {
			partData["message"] = "Artifact has been created and provided to the requester"
		} else {
			partData["message"] = art.ErrorMsg
			partData["error"] = art.ErrorMsg
		}
		responseParts = append(responseParts, a2a.NewDataPartWithMetadata(
			partData,
			map[string]any{"tool_name": "_notify_artifact_save"},
		))
	}
	responseParts = append(responseParts, a2a.NewTextPart(finalContent))

	taskMetadata := map[string]any{
		"agent_name": a.cfg.Name,
	}
	state.usageFacts.AddTo(taskMetadata)
	if state.endedQuietly {
		taskMetadata[a2a.MetadataRunOutcome] = a2a.RunOutcomeQuiet
	}
	if UsageRecorded(state.TotalUsage) {
		tokenUsage := TokenUsageTotals(state.TotalUsage)
		tokenUsage["last_input_tokens"] = state.LastInputTokens
		tokenUsage["by_model"] = a.buildByModelUsage(ctx, state)
		taskMetadata["token_usage"] = tokenUsage
	}

	var successArtifacts []artifactCreationRecord
	for _, art := range state.CreatedArtifacts {
		if art.resolvedStatus() == "success" {
			successArtifacts = append(successArtifacts, art)
		}
	}
	for _, art := range state.toolSavesSnapshot() {
		listed := slices.ContainsFunc(successArtifacts, func(r artifactCreationRecord) bool { return r.Filename == art.Filename })
		if !listed && !slices.Contains(art.Tags, a2a.ArtifactTagWorking) {
			successArtifacts = append(successArtifacts, art)
		}
	}
	artifacts := make([]map[string]any, 0, len(successArtifacts))
	listed := make([]string, 0, len(successArtifacts))
	for _, sart := range successArtifacts {
		listed = append(listed, sart.Filename)
		artifacts = append(artifacts, map[string]any{
			"filename": sart.Filename,
			"version":  sart.Version,
		})
	}
	if state.endedQuietly {
		for _, name := range a.returnableFiles(state) {
			if !slices.Contains(listed, name) {
				artifacts = append(artifacts, map[string]any{"filename": name})
			}
		}
	}
	if len(artifacts) > 0 {
		taskMetadata["produced_artifacts"] = artifacts
	}

	finalState := a2a.TaskStateCompleted
	if state.HadToolFailure {
		finalState = a2a.TaskStateFailed
		if state.ToolFailureDetail != nil {
			errorPart := a2a.NewDataPart(map[string]any{
				"a2a_signal_type": "tool_execution_error",
				"code":            state.ToolFailureDetail.Code,
				"message":         state.ToolFailureDetail.Message,
				"stderr_tail":     state.ToolFailureDetail.StderrTail,
				"stack_trace":     state.ToolFailureDetail.StackTrace,
			})
			responseParts = append([]a2a.Part{errorPart}, responseParts...)
		}
	}

	return FinalResponse(state.TaskID, state.ContextID, finalState, responseParts, taskMetadata)
}

type promptToolCache struct {
	peerVersion  uint64
	skillVersion int64
	promptSkills promptSkillState
	systemPrompt string
	llmTools     []llm.ToolDefinition
}

func (a *Agent) withholdRepeatFailingTool(state *TaskExecutionState, rc *taskRunContext, tools []llm.ToolDefinition) []llm.ToolDefinition {
	tools = state.repeatFailures.startTurn(tools)
	if withheld := state.repeatFailures.withheld; withheld != "" {
		rc.logger.Warn("withholding a tool after repeated identical failures", "tool", withheld, "call", state.LoopIteration)
	}
	return tools
}

func (a *Agent) refreshToolsAndPrompt(ctx context.Context, state *TaskExecutionState, rc *taskRunContext, cache *promptToolCache) []llm.ToolDefinition {
	peerVersion := a.peerVersion()
	skillVersion := int64(0)
	if rc.skillState != nil {
		skillVersion = rc.skillState.Version()
	}
	toolsStale := cache.llmTools == nil || peerVersion != cache.peerVersion || skillVersion != cache.skillVersion
	if toolsStale {
		cache.llmTools = a.buildLLMTools(ctx, state.Identity, rc.skillState, rc.gc.ProjectID)
		if state.QuietEndAllowed {
			cache.llmTools = append(cache.llmTools, quietEndToolDefinition())
		}
		cache.peerVersion = peerVersion
		cache.skillVersion = skillVersion
	}
	promptSkills := a.promptSkills(rc.skillState, state.Messages)
	if toolsStale || !promptSkills.equal(cache.promptSkills) {
		cache.systemPrompt = a.buildSystemPrompt(ctx, state.Identity, rc.gc, promptSkills, llmToolsInclude(cache.llmTools, "index_search"))
		cache.promptSkills = promptSkills
	}
	if len(state.Messages) > 0 && state.Messages[0].Role == "system" {
		state.Messages[0].Content = cache.systemPrompt
	}
	return cache.llmTools
}

func logLLMResponse(logger *slog.Logger, loopIteration int, streamResp *llm.ChatResponse, artifactsCreated int) {
	toolNames := make([]string, 0, len(streamResp.ToolCalls))
	for _, tc := range streamResp.ToolCalls {
		toolNames = append(toolNames, tc.Name)
	}
	promptTok, completionTok := 0, 0
	if streamResp.Usage != nil {
		promptTok = streamResp.Usage.PromptTokens
		completionTok = streamResp.Usage.CompletionTokens
	}
	logger.Debug("LLM response received",
		"call", loopIteration,
		"finishReason", streamResp.FinishReason,
		"contentLen", len(streamResp.Content),
		"toolCalls", len(streamResp.ToolCalls),
		"toolNames", toolNames,
		"artifactsCreated", artifactsCreated,
		"promptTokens", promptTok,
		"completionTokens", completionTok,
	)
}

type rejectedToolCalls struct {
	calls   []llm.ToolCall
	results []llm.ChatMessage
}

func (r rejectedToolCalls) record(msgs []llm.ChatMessage) []llm.ChatMessage {
	if len(r.results) == 0 {
		return msgs
	}
	answeredIDs := syntheticErrorCallIDs(r.results)
	answered := slices.DeleteFunc(slices.Clone(r.calls), func(tc llm.ToolCall) bool {
		return !answeredIDs[tc.ID]
	})
	appendToolCallsToLastAssistant(msgs, answered)
	return append(msgs, r.results...)
}

func assistantToolCallTurn(resp *llm.ChatResponse, calls []llm.ToolCall) llm.ChatMessage {
	return llm.ChatMessage{
		Role:      "assistant",
		Content:   rawHistoryContent(resp),
		ToolCalls: calls,
		Reasoning: resp.ReasoningContent,
	}
}

func setLastAssistantReasoning(msgs []llm.ChatMessage, reasoning string) {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "assistant" {
			msgs[i].Reasoning = reasoning
			return
		}
	}
}

func appendToolCallsToLastAssistant(msgs []llm.ChatMessage, calls []llm.ToolCall) {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "assistant" {
			msgs[i].ToolCalls = append(msgs[i].ToolCalls, calls...)
			return
		}
	}
}

func (a *Agent) handleCreatedArtifacts(
	ctx context.Context,
	state *TaskExecutionState,
	rc *taskRunContext,
	streamResp *llm.ChatResponse,
	createdArtifacts []artifactCreationRecord,
	rejected rejectedToolCalls,
	allText *replyText,
) (published bool, done bool, resp *a2a.Response) {
	if len(createdArtifacts) == 0 {
		return false, false, nil
	}
	hasInlineResultEmbed := rc.structuredCtx != nil &&
		len(streamResp.ToolCalls) == 0 &&
		structured.ParseResultEmbed(streamResp.Content) != nil

	historyContent := rawHistoryContent(streamResp)
	injectedCalls := a.previewArtifactNotifications(createdArtifacts)

	if hasInlineResultEmbed {
		rc.sp.PublishLLMResponse(ctx, a.callReportModel(ctx, state), historyContent, streamResp.Usage, injectedCalls...)
		a.publishArtifactNotificationSignals(ctx, rc.sp, createdArtifacts)
		return true, false, nil
	}

	if len(streamResp.ToolCalls) == 0 && len(rejected.calls) == 0 && rc.structuredCtx == nil &&
		strings.TrimSpace(streamResp.Content) != "" && allArtifactsSaved(createdArtifacts) &&
		state.repeatFailures.withheld == "" {
		rc.sp.PublishLLMResponse(ctx, a.callReportModel(ctx, state), historyContent, streamResp.Usage, injectedCalls...)
		a.publishArtifactNotificationSignals(ctx, rc.sp, createdArtifacts)
		returns := missingFileReturns(streamResp.Content, createdArtifacts)
		allText.add(streamResp.Content+returns, historyContent+returns)
		state.finishAfterArtifacts = true
		return true, true, nil
	}

	if len(streamResp.ToolCalls) == 0 {
		rc.sp.PublishLLMResponse(ctx, a.callReportModel(ctx, state), historyContent, streamResp.Usage, injectedCalls...)
		a.injectArtifactNotifications(ctx, rc.sp, historyContent, createdArtifacts, &state.Messages)
		setLastAssistantReasoning(state.Messages, streamResp.ReasoningContent)
		state.Messages = rejected.record(state.Messages)
		allText.add(streamResp.Content, streamResp.Content)
		if ctx.Err() != nil {
			return false, true, buildCancelledResponse(state.TaskID, state.ContextID, a.cfg.Name, state.usageSnapshot(), &state.usageFacts)
		}
		rc.logger.Debug("artifact notification injected, continuing loop", "artifacts", len(createdArtifacts), "call", state.LoopIteration)
		return false, true, nil
	}
	return false, false, nil
}

const artifactReturnEmbedPrefix = EmbedOpen + "artifact_return:"

func missingFileReturns(content string, records []artifactCreationRecord) string {
	var b strings.Builder
	for _, r := range records {
		if slices.Contains(r.Tags, a2a.ArtifactTagWorking) {
			continue
		}
		embed := artifactReturnEmbedPrefix + r.Filename
		if strings.Contains(content, embed) {
			continue
		}
		b.WriteString("\n\n" + embed + EmbedClose)
	}
	return b.String()
}

func namedFileReturns(content string, files []string) string {
	var b strings.Builder
	for _, name := range files {
		if mentionsFileName(content, name) && !hasReturnEmbed(content, name) {
			b.WriteString("\n\n" + artifactReturnEmbedPrefix + name + EmbedClose)
		}
	}
	return b.String()
}

const nameJoiners = "._-"

func mentionsFileName(content, name string) bool {
	for from := 0; ; {
		i := strings.Index(content[from:], name)
		if i < 0 {
			return false
		}
		start := from + i
		end := start + len(name)
		if !extendsNameBackward(content[:start]) && !extendsNameForward(content[end:]) {
			return true
		}
		from = start + 1
	}
}

func extendsNameBackward(before string) bool {
	if strings.HasSuffix(before, "/") || strings.HasSuffix(before, `\`) {
		return true
	}
	trimmed := strings.TrimRight(before, nameJoiners)
	for {
		r, size := utf8.DecodeLastRuneInString(trimmed)
		if !unicode.IsMark(r) {
			return isNameRune(r)
		}
		trimmed = trimmed[:len(trimmed)-size]
	}
}

func extendsNameForward(after string) bool {
	if strings.HasPrefix(after, "/") || strings.HasPrefix(after, `\`) {
		return true
	}
	first, _ := utf8.DecodeRuneInString(after)
	if unicode.IsMark(first) {
		return true
	}
	trimmed := strings.TrimLeft(after, nameJoiners)
	r, _ := utf8.DecodeRuneInString(trimmed)
	return isNameRune(r)
}

var wordSpacedScripts = []*unicode.RangeTable{
	unicode.Latin, unicode.Greek, unicode.Cyrillic, unicode.Coptic,
	unicode.Armenian, unicode.Georgian,
	unicode.Hebrew, unicode.Arabic, unicode.Syriac, unicode.Thaana, unicode.Nko,
	unicode.Ethiopic, unicode.Tifinagh, unicode.Cherokee, unicode.Canadian_Aboriginal, unicode.Mongolian,
	unicode.Devanagari, unicode.Bengali, unicode.Gurmukhi, unicode.Gujarati, unicode.Oriya,
	unicode.Tamil, unicode.Telugu, unicode.Kannada, unicode.Malayalam, unicode.Sinhala,
}

const (
	halfAndFullWidthFormsFirst = 0xFF00
	halfAndFullWidthFormsLast  = 0xFFEF
)

func isNameRune(r rune) bool {
	if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
		return false
	}
	if r < utf8.RuneSelf {
		return true
	}
	if r >= halfAndFullWidthFormsFirst && r <= halfAndFullWidthFormsLast {
		return false
	}
	return unicode.In(r, wordSpacedScripts...)
}

func hasReturnEmbed(content, name string) bool {
	embed := artifactReturnEmbedPrefix + name
	for from := 0; ; {
		i := strings.Index(content[from:], embed)
		if i < 0 {
			return false
		}
		next := content[from+i+len(embed):]
		if strings.HasPrefix(next, EmbedClose) || strings.HasPrefix(next, ":") {
			return true
		}
		from += i + len(embed)
	}
}

func producedFiles(state *TaskExecutionState) []string {
	return appendNewNames(savedFiles(state), state.ReportedFiles)
}

func savedFiles(state *TaskExecutionState) []string {
	var names []string
	for _, r := range state.toolSavesSnapshot() {
		if r.resolvedStatus() == "success" && !slices.Contains(r.Tags, a2a.ArtifactTagWorking) {
			names = appendNewNames(names, []string{r.Filename})
		}
	}
	return names
}

func appendNewNames(names, more []string) []string {
	for _, name := range more {
		if name != "" && !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	return names
}

func (a *Agent) returnableFiles(state *TaskExecutionState) []string {
	if arts := a.svc.Artifacts(); arts != nil && arts.Scope != artifact.ScopeNamespace {
		return savedFiles(state)
	}
	return producedFiles(state)
}

func allArtifactsSaved(records []artifactCreationRecord) bool {
	for _, r := range records {
		if r.Status != "success" {
			return false
		}
	}
	return true
}

func (a *Agent) executeToolTurn(
	ctx context.Context,
	state *TaskExecutionState,
	rc *taskRunContext,
	streamResp *llm.ChatResponse,
	allToolCalls []llm.ToolCall,
	createdArtifacts []artifactCreationRecord,
	combinedErrors []llm.ChatMessage,
) (loopOutcome, *a2a.Response) {
	remaining, quietResults, quietCall := takeQuietEnd(streamResp.ToolCalls, state.QuietEndAllowed,
		len(combinedErrors) > 0 || len(createdArtifacts) > 0)
	if quietCall != nil {
		return loopReturn, a.endQuietly(ctx, state, rc, streamResp, *quietCall)
	}
	streamResp.ToolCalls = remaining
	combinedErrors = append(combinedErrors, quietResults...)

	var allResponseCalls []LLMResponseToolCall
	if len(createdArtifacts) > 0 {
		allResponseCalls = append(allResponseCalls, a.previewArtifactNotifications(createdArtifacts)...)
	}
	for _, tc := range allToolCalls {
		var args map[string]any
		if err := json.Unmarshal([]byte(tc.Arguments), &args); err != nil {
			args = map[string]any{"raw": tc.Arguments}
		}
		allResponseCalls = append(allResponseCalls, LLMResponseToolCall{
			Name: tc.Name,
			Args: args,
			ID:   tc.ID,
		})
	}

	rc.sp.PublishLLMResponse(ctx, a.callReportModel(ctx, state), rawHistoryContent(streamResp), streamResp.Usage, allResponseCalls...)

	totalToolCalls := len(allToolCalls) + len(createdArtifacts)
	parallelGroupID := ""
	if totalToolCalls > 1 {
		parallelGroupID = NewParallelGroupID()
	}

	if len(createdArtifacts) > 0 {
		a.injectArtifactNotificationsWithGroupID(ctx, rc.sp, rawHistoryContent(streamResp), createdArtifacts, &state.Messages, parallelGroupID)
		appendToolCallsToLastAssistant(state.Messages, allToolCalls)
		setLastAssistantReasoning(state.Messages, streamResp.ReasoningContent)
	} else {
		state.Messages = append(state.Messages, assistantToolCallTurn(streamResp, allToolCalls))
	}

	toolResults, paused := a.dispatchToolCallsMayPause(ctx, state, streamResp.ToolCalls, rc.toolCtx, rc.sp, parallelGroupID, combinedErrors, rc.logger)
	if paused {
		return loopReturn, nil
	}

	for _, tr := range toolResults {
		recordToolFailure(state, tr, rc.logger)
	}
	state.repeatFailures.record(streamResp.ToolCalls, toolResults)

	for i, tc := range streamResp.ToolCalls {
		msg := a.buildToolResultMessage(ctx, toolResults[i], rc.toolCtx, rc.sp, tc)
		state.recordReportedFiles(msg)
		state.Messages = append(state.Messages, msg)
	}

	state.Messages = append(state.Messages, combinedErrors...)

	a.flushSessionMessagesMidTask(ctx, state, rc)

	if ctx.Err() != nil {
		return loopReturn, buildCancelledResponse(state.TaskID, state.ContextID, a.cfg.Name, state.usageSnapshot(), &state.usageFacts)
	}
	return loopContinue, nil
}

type loopOutcome int

const (
	loopContinue loopOutcome = iota
	loopBreak
	loopReturn
)

const maxBlockValidationRetries = 3

func (a *Agent) handleStreamCallError(
	ctx context.Context,
	state *TaskExecutionState,
	rc *taskRunContext,
	streamResp *llm.ChatResponse,
	err error,
	allText *replyText,
) (*a2a.Response, loopOutcome, error) {
	if bve, ok := errors.AsType[*blockValidationError](err); ok {
		state.BlockValidationRetries++
		if state.BlockValidationRetries > maxBlockValidationRetries {
			return errorResponseWithUsage(state, -32603,
				"Repeated block formatting errors: "+bve.ErrorMsg), loopReturn, nil
		}
		a.injectBlockValidationFeedback(ctx, rc.sp, bve, &state.Messages)
		rc.structuredRetryAnswered()
		state.HadVisibleText = state.HadVisibleText || bve.VisibleText != ""
		allText.add(bve.VisibleText, bve.VisibleText)
		logger := rc.logger
		if bve.ErrorKind != "" {
			logger = logger.With("errorKind", bve.ErrorKind)
		}
		logger.Warn("block validation error, retrying",
			"error", bve.ErrorMsg,
			"retry", state.BlockValidationRetries,
			"filename", bve.Filename,
		)
		return nil, loopContinue, nil
	}

	llmErr, isLLMErr := errors.AsType[*llm.Error](err)

	if isLLMErr && llmErr.IsContextLimitError() &&
		a.cfg.AutoSummarization != nil && a.cfg.AutoSummarization.Enabled {
		return a.handleContextLimitRetry(ctx, state, rc)
	}

	if ctx.Err() != nil {
		if streamResp != nil && streamResp.Content != "" && len(streamResp.ToolCalls) == 0 {
			state.Messages = append(state.Messages, llm.ChatMessage{
				Role:    "assistant",
				Content: streamResp.Content,
			})
			allText.add(streamResp.Content, streamResp.Content)
			rc.logger.Info("persisted partial assistant text on cancel",
				"contentLen", len(streamResp.Content),
			)
		}
		return buildCancelledResponse(state.TaskID, state.ContextID, a.cfg.Name, state.usageSnapshot(), &state.usageFacts), loopReturn, nil
	}
	if exhausted, ok := errors.AsType[*budget.ExhaustedError](err); ok {
		return budgetStoppedResponse(state, rc, exhausted), loopReturn, nil
	}
	logLLMCallFailure(rc.logger, state.LLMCallCount, err)
	resp := errorResponseWithUsage(state, -32603, llm.UserFacingErrorMessage(err))
	if isLLMErr {
		attachLLMFailureClass(resp, llmErr)
	}
	return resp, loopReturn, nil
}

func budgetStoppedResponse(state *TaskExecutionState, rc *taskRunContext, exhausted *budget.ExhaustedError) *a2a.Response {
	rc.logger.Info("task stopped: a budget is spent", "llmCall", state.LLMCallCount, "budgetTier", exhausted.Tier)
	resp := errorResponseWithUsage(state, -32603, budgetStoppedMessage(state, exhausted))
	AttachFailureCategory(resp, taxonomy.ErrorCategoryBudgetExhausted, "")
	return resp
}

func budgetStoppedMessage(state *TaskExecutionState, exhausted *budget.ExhaustedError) string {
	exhausted = exhausted.ForPrincipal(spend.SpendPrincipal(state.UserProps, state.Identity))
	return "This request was stopped because " + budget.SpentText(exhausted) + ". " + budget.ContactAdminText
}

func logLLMCallFailure(logger *slog.Logger, llmCall int, err error) {
	logArgs := []any{
		"llmCall", llmCall,
		"error", llm.SanitizeErrorText(err),
	}
	if llmErr, ok := errors.AsType[*llm.Error](err); ok {
		logArgs = append(logArgs,
			"upstreamStatus", llmErr.StatusCode,
			"provider", llmErr.Provider,
		)
		if llmErr.Type != "" {
			logArgs = append(logArgs, "errorType", llmErr.Type)
		}
		if llmErr.Code != "" {
			logArgs = append(logArgs, "errorCode", llmErr.Code)
		}
		if llmErr.Attempts > 0 {
			logArgs = append(logArgs,
				"attempts", llmErr.Attempts,
				"elapsed_ms", llmErr.Elapsed.Milliseconds(),
			)
		}
	}
	logger.Error("LLM call failed", logArgs...)
}

func attachLLMFailureClass(resp *a2a.Response, llmErr *llm.Error) {
	if llmErr == nil {
		return
	}
	AttachFailureCategory(resp, llmErr.Category(), llmErr.Provider)
}

func attachCompactionFailureClass(resp *a2a.Response, compErr error) {
	if llmErr, ok := errors.AsType[*llm.Error](compErr); ok {
		attachLLMFailureClass(resp, llmErr)
		return
	}
	AttachFailureCategory(resp, llm.ErrorCategoryContextOverflow, "")
}

func AttachFailureCategory(resp *a2a.Response, category, provider string) {
	if resp == nil || resp.Error == nil {
		return
	}
	data, ok := resp.Error.Data.(map[string]any)
	if !ok {
		data = make(map[string]any, 2)
		resp.Error.Data = data
	}
	data[a2a.ErrorDataLLMErrorCategory] = category
	if label := llm.ProviderLabel(provider); label != "" {
		data[a2a.ErrorDataLLMProvider] = label
	}
}

func errorResponseWithUsage(state *TaskExecutionState, code int, message string) *a2a.Response {
	resp := a2a.NewErrorResponse(state.TaskID, code, message)
	data := make(map[string]any, 4)
	if snap := state.usageSnapshot(); UsageRecorded(snap.usage) {
		tokenUsage := TokenUsageTotals(snap.usage)
		tokenUsage["last_input_tokens"] = snap.lastInputTokens
		data["token_usage"] = tokenUsage
	}
	state.usageFacts.AddTo(data)
	if len(data) > 0 {
		resp.Error.Data = data
	}
	return resp
}

func (a *Agent) handleContextLimitRetry(
	ctx context.Context,
	state *TaskExecutionState,
	rc *taskRunContext,
) (*a2a.Response, loopOutcome, error) {
	state.CompactionRetries++
	if state.CompactionRetries > maxCompactionRetries {
		return errorResponseWithUsage(state, -32603,
			msgSummarizationExhausted), loopReturn, nil
	}
	a.flushSessionMessagesMidTask(ctx, state, rc)

	origSessionLen := len(rc.sess.Messages)
	newMsgs := llmMessagesToSessionMessages(state.Messages[state.HistoryOffset:], state.TaskID)
	rc.sess.Messages = append(rc.sess.Messages, newMsgs...)

	cm, compErr := a.performCompaction(ctx, rc.sess, *a.cfg.AutoSummarization,
		a.onAgentDefaultModel(ctx, a.taskSpendAttribution(ctx, state, spend.SurfaceCompaction)))
	if compErr != nil {
		rc.sess.Messages = rc.sess.Messages[:origSessionLen]

		if errors.Is(compErr, ErrConversationTooShortToCompact) && state.CompactionRetries <= 1 {
			rc.logger.Warn("context too large to compact — single oversized turn",
				"error", llm.SanitizeErrorText(compErr))
			return errorResponseWithUsage(state, -32603,
				taxonomy.OversizedTurnMessage), loopReturn, nil
		}

		if exhausted, ok := errors.AsType[*budget.ExhaustedError](compErr); ok {
			return budgetStoppedResponse(state, rc, exhausted), loopReturn, nil
		}
		rc.logger.Warn("context compaction failed; terminating task",
			"error", llm.SanitizeErrorText(compErr))
		resp := errorResponseWithUsage(state, -32603, msgSummarizationFailed)
		attachCompactionFailureClass(resp, compErr)
		return resp, loopReturn, nil
	}

	if cm.SummarizerUsage != nil {
		state.recordUnattributedUsage(*cm.SummarizerUsage)
	}

	rc.sess.Messages = rc.sess.Messages[:origSessionLen]

	if cm.Metadata.CoveredThrough > len(rc.sess.Messages) {
		rc.logger.Error("compaction cutoff past persisted history; discarding summary",
			"coveredThrough", cm.Metadata.CoveredThrough,
			"persistedMessages", len(rc.sess.Messages))
		if rc.sess.Metadata != nil {
			delete(rc.sess.Metadata, compactionMetadataKey)
		}
		return errorResponseWithUsage(state, -32603,
			msgSummarizationFailed), loopReturn, nil
	}

	if saved, reloaded, saveErr := a.saveSessionWithRetry(ctx, rc.sess, func(s *session.Session) {
		setCompactionMetadata(s, cm.Metadata)
	}); saveErr != nil {
		rc.logger.Error("failed to persist compaction metadata; continuing with in-memory state", "error", saveErr)
	} else {
		rc.sess = saved
		rc.sessMetaFP = metadataFingerprint(saved.Metadata)
		if reloaded {
			rc.unstoredMessages = 0
			if state.unconfirmedAppend != nil {
				state.unconfirmedAppend.sess = nil
			}
		}
	}

	userText := extractLastUserText(state.Messages)
	claims := a.readSiblingClaims(ctx, rc.sess.AppName, rc.sess.UserID, rc.sess.SessionID, rc.logger)
	a.reloadSessionForRebuild(ctx, state, rc)
	unsaved := unsavedTurns(state, rc.sess)
	turns := a.historyTurnsFor(ctx, rc.sess, state.TaskID, a.taskStubBatchBytes(ctx, state), claims, rc.logger)
	turns.ownRows = ownRowsAfterQuestionStubbed
	if state.Question.Role != "" {
		turns.question = &state.Question
	}
	messages, questionAt := a.buildMessagesForTask(ctx, state.Identity, rc.sess, turns, userText, rc.gc)
	state.Messages = repairContinuedPrompt(messages, questionAt, rc.logger)
	if questionIsUnsaved(state.Messages, unsaved) {
		state.Messages = state.Messages[:len(state.Messages)-1]
	}
	state.HistoryOffset = len(state.Messages)
	state.Messages = append(state.Messages, unsaved...)
	state.CompactionSummary = cm.Metadata.Summary

	rc.logger.Info("context compaction succeeded, retrying LLM call",
		"retry", state.CompactionRetries,
		"summaryLength", len(cm.Metadata.Summary),
		"coveredThrough", cm.Metadata.CoveredThrough,
	)
	return nil, loopContinue, nil
}

func implicitContinuationCapBelowInjectGuard(maxCalls int) int {
	return min(maxAutoContinuations, maxCalls-2)
}

func (a *Agent) handleNoToolCallsTermination(
	ctx context.Context,
	state *TaskExecutionState,
	rc *taskRunContext,
	streamResp *llm.ChatResponse,
	allText *replyText,
	llmResponseAlreadyPublished bool,
	implicit implicitOutcome,
) (*a2a.Response, loopOutcome, error) {
	truncatedPassthrough := (streamResp.FinishReason == "max_tokens" || streamResp.FinishReason == "length") &&
		a.autoContinuationEnabled()
	isInterrupted := (implicit == implicitHonored || truncatedPassthrough) && implicit != implicitCapCut

	if streamResp.Content != "" && !llmResponseAlreadyPublished {
		rc.sp.PublishLLMResponse(ctx, a.callReportModel(ctx, state), rawHistoryContent(streamResp), streamResp.Usage)
	}

	if isInterrupted && a.autoContinuationEnabled() {
		return a.injectAutoContinuation(ctx, state, rc, streamResp, implicit == implicitHonored)
	}

	allText.add(streamResp.Content, rawHistoryContent(streamResp))

	if rc.structuredCtx != nil {
		finalContent := allText.String()
		if len(state.ContinuationParts) > 0 {
			finalContent = strings.Join(state.ContinuationParts, "") + finalContent
		}
		feedback := a.validateStructuredOutput(ctx, state, rc, finalContent)
		if feedback != nil {
			state.Messages = append(state.Messages,
				llm.ChatMessage{Role: "assistant", Content: structuredRetryContent(finalContent)},
				llm.ChatMessage{Role: "user", Content: feedback.Message},
			)
			rc.structuredCtx.RetryPending = true
			allText.reset()
			state.ContinuationParts = nil
			return nil, loopContinue, nil
		}
	}

	return nil, loopBreak, nil
}

func (a *Agent) injectAutoContinuation(
	ctx context.Context,
	state *TaskExecutionState,
	rc *taskRunContext,
	streamResp *llm.ChatResponse,
	implicitContinue bool,
) (*a2a.Response, loopOutcome, error) {
	if state.LoopIteration >= state.MaxCalls-1 {
		limitMsg := "This interaction has reached its processing limit. If you'd like to continue this conversation, please type 'continue'. Otherwise, you can start a new topic."
		return errorResponseWithUsage(state, -32603, limitMsg), loopReturn, nil
	}
	state.ContinuationParts = append(state.ContinuationParts, streamResp.Content)
	continueCallID := fmt.Sprintf("host-continue-%s", idgen.New())
	content := rawHistoryContent(streamResp)
	if content == "" {
		content = " "
	}
	state.Messages = append(state.Messages, llm.ChatMessage{
		Role:    "assistant",
		Content: content,
		ToolCalls: []llm.ToolCall{{
			ID:        continueCallID,
			Name:      "_continue_generation",
			Arguments: "{}",
		}},
		Reasoning: streamResp.ReasoningContent,
	})
	state.Messages = append(state.Messages, llm.ChatMessage{
		Role:       "tool",
		ToolCallID: continueCallID,
		ToolName:   "_continue_generation",
		Content:    continueGenerationResponse,
	})
	if ctx.Err() != nil {
		return buildCancelledResponse(state.TaskID, state.ContextID, a.cfg.Name, state.usageSnapshot(), &state.usageFacts), loopReturn, nil
	}
	state.IsContinuation = true
	rc.logger.Debug("auto-continuation triggered", "call", state.LoopIteration, "implicit", implicitContinue)
	return nil, loopContinue, nil
}

func (a *Agent) abandonUnresumableTask(ctx context.Context, state *TaskExecutionState, logger *slog.Logger, why string) {
	resp := a2a.NewErrorResponse(state.TaskID, -32603, "This task could not be resumed: "+why+".")
	addFactsToErrorData(resp, &state.usageFacts)
	final := a.finalMessageFor(resp, state, logger)
	if !a.checkpoints.markAnswering(ctx, state, final) {
		logger.Warn("this task was taken over while it failed to resume; leaving the answer to its new owner",
			"taskID", state.TaskID)
		a.standDownLostTask(state)
		return
	}
	signalTaskDone(state, resp)
	a.deliverAnswer(ctx, state, final, nil, logger)
	a.forgetPausedTask(state.TaskID)
}

func recordPausedBatch(state *TaskExecutionState) {
	for i := len(state.Messages) - 1; i >= 0; i-- {
		if state.Messages[i].Role == "assistant" {
			state.PausedAssistantMsg = state.Messages[i]
			break
		}
	}
	state.pauseReplayed.Store(false)
}

func (a *Agent) dispatchToolCallsMayPause(
	ctx context.Context,
	state *TaskExecutionState,
	toolCalls []llm.ToolCall,
	toolCtx *tool.Context,
	sp *StatusPublisher,
	parallelGroupID string,
	syntheticToolErrors []llm.ChatMessage,
	logger *slog.Logger,
) ([]*tool.Result, bool) {
	a.recordToolUsage(state, toolCalls)

	batchToolCtx, authPrompts := holdAuthPrompts(toolCtx, logger)
	dispatchedAt := time.Now()
	results := a.dispatchToolCallsWithStatus(ctx, toolCalls, batchToolCtx, sp, parallelGroupID)
	a.foldToolLLMUsage(state)

	var pendingIndices []int
	claimedRequestIDs := make(map[string]bool)
	for i, r := range results {
		if r == nil || r.Status != tool.StatusPending {
			continue
		}
		if refusal := a.refuseUnanswerablePersonRequest(state, toolCalls[i].ID, toolCalls[i].Name, r, claimedRequestIDs); refusal != nil {
			results[i] = refusal
			continue
		}
		pendingIndices = append(pendingIndices, i)
	}

	if len(pendingIndices) == 0 {
		a.forgetEarlyAnswers(state.TaskID)
		authPrompts.release()
		return results, false
	}

	syncResults := make(map[int]*tool.Result)
	for i, r := range results {
		if r != nil && r.Status != tool.StatusPending {
			syncResults[i] = r
		}
	}

	pendingCalls := make(map[string]*PendingToolCall)
	var pausedApprovals map[string]pausedApprovalCall

	state.SyntheticToolErrors = syntheticToolErrors
	state.ParallelGroupID = parallelGroupID
	state.PausedAt = time.Now()

	armed := make([]armedPendingCall, 0, len(pendingIndices))
	for _, idx := range pendingIndices {
		tc := toolCalls[idx]
		r := results[idx]

		subTaskID, _ := r.Data["sub_task_id"].(string)

		pc := &PendingToolCall{
			ToolCallID: tc.ID,
			ToolName:   tc.Name,
			Kind:       classifyPendingKind(r),
			Index:      idx,
			SubTaskID:  subTaskID,
		}
		pendingCalls[tc.ID] = pc
		if pc.Kind == PendingCallAuth {
			pc.OriginalArgs = toolauth.PendingAuthOriginalArgs(r)
			pc.CredentialKey = toolauth.PendingAuthCredentialKey(r)
			pc.IsAuthFollower = toolauth.IsPendingAuthFollower(r)
			pc.Timeout = a.authPendingTimeout()
		}
		if pc.Kind.waitsOnPerson() {
			pc.HILRequestID, pc.Timeout = hilWaitOf(r)
		}
		if pc.Kind == PendingCallHILAsk {
			pc.OriginalArgs = map[string]any{"questions": r.Data["questions"]}
		}
		if pc.Kind == PendingCallToolApproval {
			pc.OriginalArgs, _ = r.Data[toolApprovalArgsKey].(map[string]any)
			if pausedApprovals == nil {
				pausedApprovals = make(map[string]pausedApprovalCall)
			}
			pausedApprovals[tc.ID] = pausedApprovalCallOf(pc)
		}
		if pc.Kind == PendingCallRemoteTool {
			pc.Timeout = remoteToolWaitWindow(r)
			pc.reportUndelivered = a.remoteCallUndeliveredReporter(ctx, pc.ToolName, toolCtx, dispatchedAt)
		}
		armed = append(armed, armedPendingCall{pc: pc, result: r})
	}

	state.mu.Lock()
	state.SyncResults = syncResults
	state.PendingCalls = pendingCalls
	state.PausedApprovals = pausedApprovals
	atomic.StoreInt32(&state.TotalPending, int32(len(pendingIndices)))
	state.armPublishHandoff()
	recordPausedBatch(state)
	state.mu.Unlock()

	durableBefore := state.checkpointed.Load()
	err := a.saveRefusingTakenWaits(ctx, state, func(refused *PendingToolCall, refusal *tool.Result) (saveAgain bool) {
		results[refused.Index] = refusal
		armed = slices.DeleteFunc(armed, func(ap armedPendingCall) bool { return ap.pc == refused })
		return len(armed) > 0 || state.lostItsCheckpoint(durableBefore)
	})
	if errors.Is(err, errTaskLost) {
		logger.Warn("another process holds this task; standing down at the pause", "taskID", state.TaskID)
		authPrompts.discard()
		a.forgetEarlyAnswers(state.TaskID)
		a.standDownLostTask(state)
		for _, ap := range armed {
			if ap.pc.Kind == PendingCallRemoteTool && ap.pc.SubTaskID != "" {
				a.cancelRemoteToolCall(ap.pc.ToolName, ap.pc.SubTaskID)
			}
			if ap.pc.reportUndelivered != nil {
				ap.pc.reportUndelivered()
			}
		}
		return nil, true
	} else if err != nil {
		logger.Error("failed to checkpoint paused task; it will not survive a restart of this process",
			checkpointFailureAttr(err))
		if !state.resumedFromPause.Load() && holdsABlockingWait(armed) {
			logger.Warn("keeping the request unacknowledged until this task ends, so the broker redelivers it if this process is lost")
			state.holdRequest()
		}
	}
	if len(armed) == 0 {
		a.forgetEarlyAnswers(state.TaskID)
		authPrompts.release()
		return results, false
	}

	for _, ap := range armed {
		pc := ap.pc

		switch pc.Kind {
		case PendingCallAuth:
			taskID := state.TaskID
			callID := pc.ToolCallID
			pc.TimeoutTimer = time.AfterFunc(pc.Timeout, func() {
				a.handlePendingAuthTimeout(taskID, callID)
			})
		case PendingCallHILApproval, PendingCallHILAsk, PendingCallToolApproval:
			pc.TimeoutTimer = a.armHILTimeout(state.TaskID, pc)
		}
	}

	for _, ap := range armed {
		if ap.result.OnDelivery != nil {
			a.wireDelivery(ctx, state, ap, toolCtx, dispatchedAt, logger)
		}
	}

	a.pausedTasks.Store(state)

	if ctx.Err() != nil {
		a.forgetEarlyAnswers(state.TaskID)
		a.cancelPausedTask(state, cancelFromOwningTurn)
		authPrompts.discard()
		return nil, true
	}

	authPrompts.release()
	a.applyEarlyAnswers(state.TaskID, armedRequestIDs(armed))
	if state.passPublishHandoff() {
		a.ResumeTask(state.TaskID)
	}
	return nil, true
}

func (a *Agent) settleAfterHandoff(ctx context.Context, d deliveredCall, logger *slog.Logger) {
	state, pc := d.state, d.pc
	if pc.Kind != PendingCallRemoteTool {
		logger.Debug("dropping OnDelivery for a lost task",
			"taskID", state.TaskID,
			"toolCallID", pc.ToolCallID,
			"toolName", pc.ToolName,
		)
		return
	}
	logger.Debug("recording a reply that arrived after this task was handed off, for the replica that adopts it",
		"taskID", state.TaskID,
		"toolCallID", pc.ToolCallID,
		"toolName", pc.ToolName,
	)
	if !a.checkpoints.recordResult(ctx, state.TaskID, pc, d.result) {
		a.reportDeliveredCall(ctx, d)
	}
}

func holdsABlockingWait(armed []armedPendingCall) bool {
	return slices.ContainsFunc(armed, func(ap armedPendingCall) bool {
		switch ap.pc.Kind {
		case PendingCallRemoteTool, PendingCallHILAsk, PendingCallToolApproval:
			return true
		}
		return false
	})
}

func armedRequestIDs(armed []armedPendingCall) []string {
	ids := make([]string, 0, len(armed))
	for _, ap := range armed {
		if ap.pc.HILRequestID != "" {
			ids = append(ids, ap.pc.HILRequestID)
		}
	}
	return ids
}

func (a *Agent) wireDelivery(ctx context.Context, state *TaskExecutionState, ap armedPendingCall, toolCtx *tool.Context, dispatchedAt time.Time, logger *slog.Logger) {
	pc := ap.pc
	taskID := state.TaskID
	var settled atomic.Bool
	ap.result.OnDelivery(func(finalResult *tool.Result) {
		if state.Cancelled.Load() {
			logger.Debug("dropping OnDelivery for a cancelled task",
				"taskID", taskID,
				"toolCallID", pc.ToolCallID,
				"toolName", pc.ToolName,
			)
			return
		}
		delivered := deliveredCall{
			state:        state,
			pc:           pc,
			toolCtx:      toolCtx,
			dispatchedAt: dispatchedAt,
			result:       finalResult,
		}
		if state.Fenced.Load() {
			a.settleAfterHandoff(ctx, delivered, logger)
			return
		}
		state.mu.Lock()
		if state.Cancelled.Load() || !settled.CompareAndSwap(false, true) {
			state.mu.Unlock()
			return
		}
		a.expectDeliveredPersonAnswer(state, finalResult)
		pc.Result = finalResult
		state.durableWrites.Add(1)
		delivered.remaining = atomic.AddInt32(&state.TotalPending, -1)
		state.mu.Unlock()

		a.settleDeliveredCall(ctx, delivered)
	})
}

func hilWaitOf(r *tool.Result) (requestID string, timeout time.Duration) {
	requestID, _ = r.Data["hil_request_id"].(string)
	timeout = hilApprovalDefaultTimeout
	if ns, _ := r.Data["hil_timeout_ns"].(float64); ns > 0 {
		timeout = time.Duration(min(ns, float64(hilApprovalMaxTimeout)))
	}
	return requestID, timeout
}

func (a *Agent) armHILTimeout(taskID string, pc *PendingToolCall) *time.Timer {
	callID := pc.ToolCallID
	return time.AfterFunc(pc.Timeout, func() {
		a.handlePendingHILTimeout(taskID, callID)
	})
}

func remoteToolWaitWindow(r *tool.Result) time.Duration {
	var window time.Duration
	switch ms := r.Data["timeout_ms"].(type) {
	case int64:
		window = time.Duration(ms) * time.Millisecond
	case float64:
		window = time.Duration(ms) * time.Millisecond
	}
	if window <= 0 {
		return remote.DefaultWaitWindow
	}
	return window
}

func classifyPendingKind(r *tool.Result) PendingCallKind {
	if toolauth.IsPendingAuthResult(r) {
		return PendingCallAuth
	}
	switch r.TrustedPending {
	case tool.TrustedPendingToolApproval:
		return PendingCallToolApproval
	case tool.TrustedPendingHILAsk:
		return PendingCallHILAsk
	}
	switch kind, _ := r.Data["pending_kind"].(string); kind {
	case "hil_approval":
		return PendingCallHILApproval
	case "workflow":
		return PendingCallWorkflow
	case "remote_tool":
		if r.OnDelivery != nil {
			return PendingCallRemoteTool
		}
	}
	return PendingCallPeer
}

func (a *Agent) releaseTaskOnExit(ctx context.Context, state *TaskExecutionState) {
	if !a.exitingProcess.Load() {
		return
	}
	a.pausedTasks.Delete(state.TaskID)
	releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), pausedTaskReleaseTimeout)
	defer cancel()
	a.checkpoints.releaseOwnership(releaseCtx, state.TaskID)
}

func (a *Agent) ResumeTask(taskID string) {
	state := a.pausedTasks.Load(taskID)
	if state == nil {
		a.logger.Warn("resume for unknown paused task", "taskID", taskID)
		return
	}
	taskLogger := a.logger.With(
		"traceID", state.UserProps.TraceID,
		"taskID", taskID, "sessionID", state.EffectiveSessionID,
		"contextID", state.ContextID,
	)
	if state.Cancelled.Load() {
		taskLogger.Debug("skipping resume for cancelled task", "taskID", taskID)
		return
	}
	if a.exitingProcess.Load() && state.checkpointed.Load() {
		taskLogger.Info("process exiting; leaving the answered task to the process that takes it over")
		return
	}

	if resumeTaskHook != nil {
		resumeTaskHook(a, state)
		return
	}

	state.resumeRequested.Store(true)
	release, claimed := markResuming(state)
	if !claimed {
		taskLogger.Debug("task is mid-turn; resuming it when that turn ends")
		return
	}
	state.resumedFromPause.Store(true)
	state.resumeRequested.Store(false)
	taskLogger.Info("resuming paused task",
		"pauseDuration", time.Since(state.PausedAt).Round(time.Millisecond),
	)
	go func() {
		defer a.endResumeClaim(state, release)
		a.resumeTaskExecution(state)
	}()
}

func (a *Agent) endResumeClaim(state *TaskExecutionState, release func()) {
	release()
	resumeWanted := state.resumeRequested.Swap(false)
	taskLogger := a.logger.With("traceID", state.UserProps.TraceID, "taskID", state.TaskID)
	if a.pausedTasks.Load(state.TaskID) != state {
		if resumeWanted {
			taskLogger.Debug("dropping a resume request for a task that is no longer paused")
		}
		return
	}
	switch {
	case state.Cancelled.Load():
		taskLogger.Info("performing the teardown for a cancel recorded while a turn held this task")
		a.tearDownCancelledTask(state)
	case a.handingOffPausedTasks.Load():
		handoffCtx, cancel := context.WithTimeout(context.Background(), pausedTaskReleaseTimeout)
		a.handOffPausedTask(handoffCtx, state)
		cancel()
	case !resumeWanted:
	case atomic.LoadInt32(&state.TotalPending) != 0:
		taskLogger.Debug("dropping a resume request; the task paused again and waits on new results",
			"pending", atomic.LoadInt32(&state.TotalPending))
	default:
		a.ResumeTask(state.TaskID)
	}
}

func (a *Agent) handlePendingAuthTimeout(taskID, toolCallID string) {
	state := a.pausedTasks.Load(taskID)
	if state == nil {
		return
	}
	state.mu.Lock()
	pc, ok := state.PendingCalls[toolCallID]
	if !ok || pc.Kind != PendingCallAuth {
		state.mu.Unlock()
		return
	}
	delete(state.PendingCalls, toolCallID)
	timeoutResult := tool.Error(fmt.Sprintf(taxonomy.AuthorizationTimedOutFormat, pc.Timeout),
		tool.WithErrorCode(tool.DispatchErrorCode(taxonomy.DispatchClassUserInputUnanswered)))
	state.SyncResults[pc.Index] = timeoutResult
	state.setPendingAuthOutcome(&authOutcome{
		success:         false,
		credentialKey:   pc.CredentialKey,
		functionCallID:  toolCallID,
		toolDisplayName: pc.ToolName,
	})
	state.durableWrites.Add(1)
	state.mu.Unlock()

	resolvedElsewhere := a.checkpoints.recordResult(context.Background(), taskID, pc, timeoutResult)
	if resolvedElsewhere {
		if recorded := a.useRecordedResult(context.Background(), state, pc); recorded != nil {
			state.mu.Lock()
			state.setPendingAuthOutcome(&authOutcome{
				success:         !resultIsError(recorded),
				credentialKey:   pc.CredentialKey,
				functionCallID:  toolCallID,
				toolDisplayName: pc.ToolName,
			})
			state.mu.Unlock()
		}
	}
	state.durableWrites.Done()

	taskLogger := a.logger.With(
		"traceID", state.UserProps.TraceID,
		"taskID", taskID)
	taskLogger.Info("auth flow timed out",
		"toolCallID", toolCallID,
		"toolName", pc.ToolName,
		"timeout", pc.Timeout.String(),
	)

	if state.DecrementPending() == 0 {
		a.ResumeTask(taskID)
	}

	if pc.Kind == PendingCallAuth && pc.CredentialKey != "" && !pc.IsAuthFollower {
		if !resolvedElsewhere {
			attempts := a.recordAuthFailure(state.UserProps.UserID, state.UserProps.TraceID, pc.CredentialKey)
			if attempts >= a.authMaxAttempts() {
				taskLogger.Warn("auth flow giving up after repeated timeouts",
					"toolName", pc.ToolName,
					"credentialKey", pc.CredentialKey,
					"attempts", attempts,
				)
			}
		}
		sweepCtx, sweepCancel := context.WithTimeout(context.Background(), 30*time.Second)
		a.endAuthFlow(state.UserProps.UserID, state.UserProps.TraceID, pc.CredentialKey)
		a.resumeAuthFollowers(sweepCtx, state.UserProps.UserID, state.UserProps.TraceID, pc.CredentialKey, "", pc.ToolCallID)
		sweepCancel()
	}
}

func (a *Agent) handlePendingHILTimeout(taskID, toolCallID string) {
	state := a.pausedTasks.Load(taskID)
	if state == nil {
		return
	}
	state.mu.Lock()
	pc, ok := state.PendingCalls[toolCallID]
	if !ok || !pc.Kind.waitsOnPerson() {
		state.mu.Unlock()
		return
	}
	delete(state.PendingCalls, toolCallID)
	timeoutResult := hilResultFor(a.logger, pc, &a2a.UserInputResponseData{
		RequestID: pc.HILRequestID,
		Status:    "timed_out",
		Data:      map[string]any{},
	})
	state.SyncResults[pc.Index] = timeoutResult
	state.durableWrites.Add(1)
	state.mu.Unlock()

	if a.checkpoints.recordResult(context.Background(), taskID, pc, timeoutResult) {
		a.useRecordedResult(context.Background(), state, pc)
	}
	state.durableWrites.Done()

	taskLogger := a.logger.With(
		"traceID", state.UserProps.TraceID,
		"taskID", taskID)
	taskLogger.Info("HIL approval timed out",
		"toolCallID", toolCallID,
		"toolName", pc.ToolName,
		"timeout", pc.Timeout.String(),
	)

	if state.DecrementPending() == 0 {
		a.ResumeTask(taskID)
	}
}

func releaseNothing() {
}

func markResuming(state *TaskExecutionState) (release func(), claimed bool) {
	if !state.resuming.CompareAndSwap(false, true) {
		return releaseNothing, false
	}
	return func() { state.resuming.Store(false) }, true
}

func claimForHandoff(state *TaskExecutionState) (release func(), claimed bool) {
	return markResuming(state)
}

func recordToolFailure(state *TaskExecutionState, result *tool.Result, logger *slog.Logger) {
	if result == nil || result.Status != tool.StatusFailed || state.HadToolFailure {
		return
	}
	state.HadToolFailure = true
	state.ToolFailureDetail = result.FailedDetail
	state.runFacts.setFailure(result.FailedDetail)
	logger.Warn("tool failure — allowing LLM to explain",
		"code", result.ErrorCode,
		"messageLen", len(result.Message),
	)
}

func (a *Agent) resumeTaskExecution(state *TaskExecutionState) {
	defer a.recoverResumePanic(state)

	state.durableWrites.Wait()

	taskLogger := a.logger.With(
		"traceID", state.UserProps.TraceID,
		"taskID", state.TaskID, "sessionID", state.EffectiveSessionID,
		"contextID", state.ContextID,
	)

	ctx, endTurn, stopTask := cancellableTaskContext(a.runCtx)
	defer endTurn()

	resumeEntry := &activeTaskEntry{
		cancel:       stopTask,
		replyTo:      state.UserProps.ReplyTo,
		contextID:    state.ContextID,
		userProps:    state.UserProps,
		publishOnce:  &sync.Once{},
		kind:         state.kind,
		checkpointed: &state.checkpointed,
	}
	resumeEntry.recordUsage(state.usageSnapshot())
	resumeEntry.recordFacts(&state.usageFacts)
	a.activeTasksMu.Lock()
	a.activeTasks[state.TaskID] = resumeEntry
	a.activeTasksMu.Unlock()
	defer a.releaseActiveTask(state.TaskID, resumeEntry)

	if state.Cancelled.Load() {
		taskLogger.Info("this task was cancelled before its turn could register; tearing down here")
		stopTask()
		a.tearDownCancelledTask(state)
		return
	}

	sp := a.statusPublisherFor(state)

	if outcome := state.takePendingAuthOutcome(); outcome != nil {
		a.publishAuthOutcome(ctx, sp, state.TaskID, outcome)
	}

	toolCtx := a.buildToolCtxFromState(ctx, state, sp)

	if a.runCtx != nil && a.runCtx.Err() != nil && state.checkpointed.Load() {
		taskLogger.Info("stopping before the resumed turn; leaving this task to the process that takes it over")
		a.releaseTaskOnExit(ctx, state)
		return
	}

	sess, err := a.svc.Sessions().Get(ctx, a.cfg.Name, state.UserProps.UserID, state.EffectiveSessionID)
	if err != nil {
		taskLogger.Error("failed to load session for resumed task", "taskID", state.TaskID, "error", err)
		a.abandonUnresumableTask(ctx, state, taskLogger, "its session could not be loaded")
		return
	}
	if sess == nil {
		taskLogger.Error("session row missing for resumed task", "taskID", state.TaskID,
			"sessionID", state.EffectiveSessionID)
		a.abandonUnresumableTask(ctx, state, taskLogger, "its session row is missing")
		return
	}

	gc := gatewayContextFrom(state.GatewayMetadata, state.UserProps, state.DisplaysTemplateSignals)
	rc := &taskRunContext{
		sp:                sp,
		toolCtx:           toolCtx,
		structuredCtx:     state.StructuredCtx,
		gc:                gc,
		sess:              sess,
		originalSessionID: state.OriginalSessionID,
		msg:               a2a.Message{ContextID: state.ContextID},
		logger:            taskLogger,
		sessMetaFP:        metadataFingerprint(sess.Metadata),
	}

	if a.skillManager != nil && SkillStateFactory != nil {
		rc.skillState = SkillStateFactory(sess)
		rc.toolCtx.SkillState = rc.skillState
		a.rehydrateSkills(ctx, rc.toolCtx)
	}

	if state.rebuildFromRows {
		a.rebuildAdoptedPrompt(ctx, state, rc)
	}

	entries := a.pausedBatchResults(state, taskLogger)
	if a.repauseOnPersonRequests(ctx, state, entries, taskLogger) {
		return
	}

	state.mu.Lock()
	pausedApprovals := maps.Clone(state.PausedApprovals)
	state.mu.Unlock()
	replayed := make([]llm.ChatMessage, 0, len(entries))
	replayedResults := make([]*tool.Result, 0, len(entries))
	for _, entry := range entries {
		tc, result := entry.call, entry.result
		if call, paused := pausedApprovals[tc.ID]; paused {
			result = a.settlePausedApproval(ctx, state, sp, toolCtx, pausedToolCall{toolCall: tc, approval: call}, result, taskLogger)
		}

		a.publishToolResult(ctx, sp, tc.Name, result, toolCtx, tc.ID)
		replayed = append(replayed, a.buildToolResultMessage(ctx, result, toolCtx, sp, tc))
		replayedResults = append(replayedResults, result)
	}

	if !state.pauseReplayed.Load() {
		replayed = append(replayed, state.SyntheticToolErrors...)
	}

	state.mu.Lock()
	state.Messages = append(state.Messages, replayed...)
	state.PausedApprovals = nil
	for _, m := range replayed {
		state.recordReportedFiles(m)
	}
	for _, result := range replayedResults {
		recordToolFailure(state, result, taskLogger)
	}
	state.pauseReplayed.Store(true)
	state.LoopIteration++
	state.mu.Unlock()

	var resp *a2a.Response
	var loopErr error
	if rc.storedFinalAnswer != nil {
		resp = a.republishStoredFinal(ctx, state, rc)
	} else {
		resp, loopErr = a.continueTaskLoop(ctx, state, rc)
	}

	if state.Cancelled.Load() {
		taskLogger.Info("performing the teardown for a cancel delegated to this turn")
		a.tearDownCancelledTask(state)
		return
	}

	if resp == nil && loopErr == nil {
		return
	}

	if a.runCtx != nil && a.runCtx.Err() != nil && state.checkpointed.Load() {
		taskLogger.Info("stopping mid-turn; leaving this task to the process that takes it over")
		a.releaseTaskOnExit(ctx, state)
		return
	}

	a.forgetPausedTask(state.TaskID)
	final := a.finalMessageFor(resp, state, taskLogger)
	if !a.checkpoints.markAnswering(ctx, state, final) {
		taskLogger.Warn("another process holds this task; standing down without publishing")
		a.standDownLostTask(state)
		return
	}

	if state.IsRunBased {
		if delErr := a.svc.Sessions().Delete(ctx, a.cfg.Name, state.UserProps.UserID, state.EffectiveSessionID); delErr != nil {
			taskLogger.Warn("failed to delete run-based session after resume", "sessionID", state.EffectiveSessionID, "error", delErr)
		}
	}

	if resp != nil && state.DoneCh != nil {
		select {
		case state.DoneCh <- resp:
		default:
		}
	}

	a.deliverAnswer(ctx, state, final, resumeEntry.publishOnce, taskLogger)
	if final == nil && loopErr != nil {
		taskLogger.Error("resumed task loop failed", "taskID", state.TaskID, "error", loopErr)
	}
}

func (a *Agent) finalMessageFor(resp *a2a.Response, state *TaskExecutionState, logger *slog.Logger) *broker.Message {
	if resp == nil || state.UserProps.ReplyTo == "" || state.Fenced.Load() {
		return nil
	}
	msg, err := a.resumedResponseMessage(resp, state)
	if err != nil {
		logger.Error("failed to marshal resumed task response", "error", err)
		return nil
	}
	return msg
}

func (a *Agent) deliverAnswer(ctx context.Context, state *TaskExecutionState, final *broker.Message, once *sync.Once, logger *slog.Logger) {
	defer a.seqGen.Drop(state.TaskID)
	if final == nil {
		return
	}
	ran, failed := false, false
	publish := func() {
		ran = true
		failed = a.publishFinalMessage(ctx, state.UserProps.ReplyTo, final, logger) != nil
	}
	if once != nil {
		once.Do(publish)
	} else {
		publish()
	}
	if ran && failed {
		return
	}
	if state.checkpointed.Load() {
		a.checkpoints.discardIfOwned(ctx, state.TaskID)
	}
}

func (a *Agent) buildToolCtxFromState(ctx context.Context, state *TaskExecutionState, sp *StatusPublisher) *tool.Context {
	toolCtx := &tool.Context{
		Identity:                state.Identity,
		UserID:                  state.UserProps.UserID,
		AgentName:               a.cfg.Name,
		SessionID:               state.OriginalSessionID,
		ArtifactService:         a.svc.Artifacts(),
		CredentialService:       a.svc.CredentialService(),
		Namespace:               a.cfg.Namespace,
		A2AStatusTopic:          state.UserProps.A2AStatusTopic,
		A2AUserConfig:           state.UserProps.A2AUserConfig,
		ParentTaskID:            state.TaskID,
		ClientID:                state.UserProps.ClientID,
		TraceID:                 state.UserProps.TraceID,
		Trigger:                 state.UserProps.Trigger,
		RootKind:                state.UserProps.RootKind,
		RootID:                  state.UserProps.RootID,
		RootNodeID:              state.UserProps.RootNodeID,
		Origin:                  state.UserProps.Origin,
		DisplaysTemplateSignals: state.DisplaysTemplateSignals,
		CallDepth:               state.UserProps.CallDepth,
		MaxSubTaskFanout:        a.cfg.MaxSubTaskFanout,
		SubTaskCount:            state.subTaskCount,
		SearchTurns:             state.searchTurns,
		ProjectID:               state.GatewayMetadata[metaProjectID],
		PauseResume:             true,
		PauseRemoteTools:        a.checkpoints != nil,
		ToolConfig:              a.buildToolConfig(),
	}
	if len(state.UserProps.GatewayCapabilities) > 0 {
		toolCtx.GatewayCapabilities = state.UserProps.GatewayCapabilities
	}

	a.wireTaskHooks(ctx, toolCtx, sp)
	a.wireTaskStateHooks(ctx, toolCtx, sp, state)

	return toolCtx
}

func (a *Agent) wireTaskHooks(ctx context.Context, toolCtx *tool.Context, sp *StatusPublisher) {
	toolCtx.BeginAuthFlow = a.beginAuthFlow
	toolCtx.EndAuthFlow = a.endAuthFlow
	toolCtx.AuthGaveUp = a.authGaveUp
	toolCtx.AwaitDeepResearchPlanResponse = a.awaitDeepResearchPlanResponse
	a.wireBrokerHooks(toolCtx)
}

func (a *Agent) wirePublishHooks(ctx context.Context, toolCtx *tool.Context, sp *StatusPublisher, state *TaskExecutionState) {
	toolCtx.SendSignal = func(signal any) error {
		signalCtx, release, ok := publishContext(ctx, toolCtx.PauseRemoteTools, state)
		if !ok {
			sp.logDroppedForCancelledTask("signal")
			return nil
		}
		defer release()
		if authData, ok := signal.(*a2a.AuthenticationRequiredData); ok {
			sp.PublishAuthenticationRequired(signalCtx, *authData)
			return nil
		}
		if typed, ok := signal.(a2a.SignalTyper); ok {
			sp.PublishGenericSignal(signalCtx, signal, typed.GetSignalType())
			return nil
		}
		return nil
	}
	a.wireStatusStreamHooks(ctx, toolCtx, sp, state)
}

const controlQueryTimeout = 10 * time.Second

const detachedPublishTimeout = 5 * time.Second

func taskCancelledByUser(turn context.Context, state *TaskExecutionState) bool {
	return taskWasCancelled(turn) || (state != nil && state.Cancelled.Load())
}

func publishContext(turn context.Context, outlivesTurn bool, state *TaskExecutionState) (context.Context, context.CancelFunc, bool) {
	if !outlivesTurn {
		bounded, release := context.WithCancel(turn)
		return bounded, release, true
	}
	if taskCancelledByUser(turn, state) {
		abandoned, release := context.WithCancel(turn)
		release()
		return abandoned, release, false
	}
	detached, release := context.WithTimeout(context.WithoutCancel(turn), detachedPublishTimeout)
	return detached, release, true
}

func (a *Agent) wireBrokerHooks(toolCtx *tool.Context) {
	if a.svc == nil || a.svc.Broker() == nil {
		return
	}
	controlOpts := []control.ClientOption{control.WithTimeout(controlQueryTimeout)}
	if id := toolCtx.Identity; id != nil && id.AuthToken != "" {
		controlOpts = append(controlOpts, control.WithUserProperties(map[string]string{
			a2a.PropAuthToken: id.AuthToken,
		}))
	}
	toolCtx.ControlQuerier = control.NewClient(a.svc.Broker(), a.cfg.Namespace, controlOpts...)
	toolCtx.PublishNotification = a.publishNotification
	toolCtx.ScheduleTask = a.publishScheduleRequest
}

func (a *Agent) wireStatusStreamHooks(ctx context.Context, toolCtx *tool.Context, sp *StatusPublisher, state *TaskExecutionState) {
	if !sp.Enabled() {
		return
	}
	toolCtx.SendStatus = func(message string) error {
		statusCtx, release, ok := publishContext(ctx, toolCtx.PauseRemoteTools, state)
		if !ok {
			sp.logDroppedForCancelledTask("status")
			return nil
		}
		defer release()
		sp.PublishProgressUpdate(statusCtx, message)
		return nil
	}
	toolCtx.PublishUserInputRequest = sp.PublishUserInputRequest
	a.wireUserInput(toolCtx, sp, state, suppressCrossChannelHIL(toolCtx.GatewayCapabilities))
}

func (a *Agent) awaitDeepResearchPlanResponse(ctx context.Context, planID, userID string) (*tool.PlanResponse, error) {
	ch := a.planWaiters.Register(planID, userID)
	defer a.planWaiters.Drop(planID)
	select {
	case resp, ok := <-ch:
		if !ok || resp == nil {
			return nil, fmt.Errorf("plan waiter channel closed without response")
		}
		return &tool.PlanResponse{Action: resp.Action, Steps: resp.Steps}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (a *Agent) wireTaskStateHooks(ctx context.Context, toolCtx *tool.Context, sp *StatusPublisher, state *TaskExecutionState) {
	a.wirePublishHooks(ctx, toolCtx, sp, state)
	toolCtx.NotifyArtifactSaved = artifactSavedHook(sp, state)
	a.wireInternalLLMCallbacks(toolCtx, sp, state, a.taskSpendAttribution(ctx, state, spend.SurfaceTool))
}

func (a *Agent) dispatchToolSingle(ctx context.Context, state *TaskExecutionState, pc *PendingToolCall) (*tool.Result, error) {
	resolvedTool := a.tools.GetTool(pc.ToolName)
	if resolvedTool == nil {
		return tool.Error(fmt.Sprintf("tool %q no longer registered", pc.ToolName)), nil
	}
	executable, ok := resolvedTool.(tool.BuiltinTool)
	if !ok {
		return tool.Error(fmt.Sprintf("tool %q is not directly executable for auth replay", pc.ToolName)), nil
	}

	sp := a.statusPublisherFor(state)

	toolCtx := a.buildToolCtxFromState(ctx, state, sp)
	toolCtx.PrepareCall(pc.ToolCallID, pc.ToolName)

	return a.tools.ExecuteBuiltin(ctx, pc.ToolName, executable, pc.OriginalArgs, toolCtx)
}

func syntheticErrorCallIDs(errs []llm.ChatMessage) map[string]bool {
	ids := make(map[string]bool, len(errs))
	for _, m := range errs {
		ids[m.ToolCallID] = true
	}
	return ids
}

func (a *Agent) synthesizePendingCancelledResults(state *TaskExecutionState) {
	if state == nil || len(state.PausedAssistantMsg.ToolCalls) == 0 {
		return
	}
	if state.pauseReplayed.Load() {
		return
	}
	syncCount := 0
	cancelCount := 0
	syntheticIDs := syntheticErrorCallIDs(state.SyntheticToolErrors)
	dispatchIdx := 0
	for _, tc := range state.PausedAssistantMsg.ToolCalls {
		if strings.HasPrefix(tc.ID, hostNotifyCallIDPrefix) {
			continue
		}
		var content string
		if syntheticIDs[tc.ID] {
			content = cancelledToolResultContent
			cancelCount++
		} else {
			if syncResult, ok := state.SyncResults[dispatchIdx]; ok {
				content = formatSyncResultForCancelFlush(syncResult)
				syncCount++
			} else {
				content = cancelledToolResultContent
				cancelCount++
			}
			dispatchIdx++
		}
		state.Messages = append(state.Messages, llm.ChatMessage{
			Role:       "tool",
			ToolCallID: tc.ID,
			ToolName:   tc.Name,
			Content:    content,
		})
	}
	a.logger.With(
		"traceID", state.UserProps.TraceID,
		"taskID", state.TaskID).Debug("synthesized cancel-time tool results",
		"syncResults", syncCount,
		"cancelledStubs", cancelCount,
	)
}

func formatSyncResultForCancelFlush(r *tool.Result) string {
	if r == nil {
		return cancelledToolResultContent
	}
	combined := map[string]any{"status": r.Status}
	if r.Message != "" {
		combined["message"] = r.Message
	}
	for k, v := range llmResultData(r) {
		combined[k] = v
	}
	data, err := json.Marshal(combined)
	if err != nil {
		return `{"status":"error","message":"failed to format sync result on cancel"}`
	}
	return string(data)
}

func (a *Agent) cancelActiveEntry(taskID string) {
	a.activeTasksMu.Lock()
	entry, ok := a.activeTasks[taskID]
	a.activeTasksMu.Unlock()
	if ok {
		entry.cancel()
	}
}

type cancelCaller bool

const (
	cancelFromOwningTurn cancelCaller = true
	cancelFromElsewhere  cancelCaller = false
)

func (a *Agent) cancelPausedTask(state *TaskExecutionState, caller cancelCaller) {
	state.Cancelled.Store(true)

	if caller == cancelFromElsewhere && state.resuming.Load() {
		a.logger.Info("cancel recorded for a task mid-turn; its own loop will tear it down",
			"traceID", state.UserProps.TraceID, "taskID", state.TaskID)
		a.cancelActiveEntry(state.TaskID)
		return
	}

	a.tearDownCancelledTask(state)
}

func (a *Agent) tearDownCancelledTask(state *TaskExecutionState) {
	if !state.teardownStarted.CompareAndSwap(false, true) {
		return
	}
	state.mu.Lock()
	a.synthesizePendingCancelledResults(state)
	var authCredKeys []string
	cancelledApprovals := pausedApprovalToolNames(state)
	var undeliveredRemoteCalls []func()
	for _, pc := range state.PendingCalls {
		if pc.TimeoutTimer != nil {
			pc.TimeoutTimer.Stop()
		}
		if (pc.Kind == PendingCallPeer || pc.Kind == PendingCallWorkflow) && pc.SubTaskID != "" {
			a.peerRouter.CancelWithPropagation(pc.SubTaskID, a.cfg.Namespace)
		}
		if pc.Kind == PendingCallRemoteTool && pc.SubTaskID != "" {
			a.cancelRemoteToolCall(pc.ToolName, pc.SubTaskID)
		}
		if pc.Kind == PendingCallRemoteTool && pc.Result == nil && pc.reportUndelivered != nil {
			undeliveredRemoteCalls = append(undeliveredRemoteCalls, pc.reportUndelivered)
		}
		if pc.Kind == PendingCallAuth && pc.CredentialKey != "" {
			if !pc.IsAuthFollower {
				authCredKeys = append(authCredKeys, pc.CredentialKey)
			}
			delete(state.PendingCalls, pc.ToolCallID)
		}
	}
	state.PendingCalls = make(map[string]*PendingToolCall)
	state.mu.Unlock()

	if len(authCredKeys) > 0 {
		sweepCtx, sweepCancel := context.WithTimeout(context.Background(), 30*time.Second)
		seen := make(map[string]struct{}, len(authCredKeys))
		for _, credKey := range authCredKeys {
			if _, dup := seen[credKey]; dup {
				continue
			}
			seen[credKey] = struct{}{}
			a.endAuthFlow(state.UserProps.UserID, state.UserProps.TraceID, credKey)
			a.resumeAuthFollowers(sweepCtx, state.UserProps.UserID, state.UserProps.TraceID, credKey, "", "")
		}
		sweepCancel()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	answers := a.checkpoints.removeCancelledRow(ctx, state)
	if !answers {
		state.Fenced.Store(true)
	}
	if answers {
		for _, report := range undeliveredRemoteCalls {
			report()
		}
		a.endCancelledApprovals(state, cancelledApprovals)
	}

	a.flushPausedTaskSessionMessages(state)
	a.forgetPausedTask(state.TaskID)

	if !answers {
		a.logger.Info("this task is held by another process; cancelling it there",
			"traceID", state.UserProps.TraceID, "taskID", state.TaskID)
		a.peerRouter.ReleaseTask(state.TaskID)
		a.seqGen.Drop(state.TaskID)
		if a.cancelCheckpointedTask(state.TaskID) && state.IsRunBased {
			a.deleteCancelledRunSession(ctx, state)
		}
		return
	}

	if state.UserProps.ReplyTo != "" {
		cancelledResp := a2a.NewResponse(state.TaskID, a2a.Task{
			ID:        state.TaskID,
			ContextID: state.ContextID,
			Kind:      "task",
			Status: a2a.TaskStatus{
				State:     a2a.TaskStateCancelled,
				Timestamp: time.Now().UTC().Format("2006-01-02T15:04:05.000000+00:00"),
				Message:   &a2a.Message{Kind: "message", Role: a2a.RoleAgent, Parts: []a2a.Part{a2a.NewTextPart("Task cancelled")}},
			},
			Metadata: cancelledTaskMetadata(a.cfg.Name, a.pausedTaskUsage(state), &state.usageFacts),
		})
		a.publishResumedResponse(ctx, cancelledResp, state)
	}

	if state.IsRunBased {
		a.deleteCancelledRunSession(ctx, state)
	}
}

func (a *Agent) deleteCancelledRunSession(ctx context.Context, state *TaskExecutionState) {
	if delErr := a.svc.Sessions().Delete(ctx, a.cfg.Name, state.UserProps.UserID, state.EffectiveSessionID); delErr != nil {
		a.logger.Warn("failed to delete run-based session after cancel",
			"traceID", state.UserProps.TraceID, "taskID", state.TaskID,
			"sessionID", state.EffectiveSessionID, "error", delErr)
	}
}

func (a *Agent) publishResumedResponse(ctx context.Context, resp *a2a.Response, state *TaskExecutionState) {
	taskLogger := a.logger.With(
		"traceID", state.UserProps.TraceID,
		"taskID", state.TaskID)
	if state.Fenced.Load() {
		taskLogger.Info("suppressing resumed response: another replica owns this task")
		return
	}
	msg, err := a.resumedResponseMessage(resp, state)
	if err != nil {
		taskLogger.Error("failed to marshal resumed task response", "error", err)
		return
	}
	defer a.seqGen.Drop(state.TaskID)
	_ = a.publishFinalMessage(ctx, state.UserProps.ReplyTo, msg, taskLogger)
}

func (a *Agent) resumedResponseMessage(resp *a2a.Response, state *TaskExecutionState) (*broker.Message, error) {
	respBytes, err := json.Marshal(resp)
	if err != nil {
		return nil, err
	}
	userProps := map[string]string{}
	if state.UserProps.ClientID != "" {
		userProps["clientId"] = state.UserProps.ClientID
	}
	if state.UserProps.UserID != "" {
		userProps["userId"] = state.UserProps.UserID
	}
	a2a.AddTraceID(userProps, state.UserProps.TraceID)
	a.seqGen.Stamp(userProps, a.cfg.Name, state.TaskID)
	return &broker.Message{Payload: respBytes, UserProperties: userProps}, nil
}

func (a *Agent) publishFinalMessage(ctx context.Context, topic string, msg *broker.Message, logger *slog.Logger) error {
	const maxAttempts = 2
	var pubErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		pubCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		pubErr = a.svc.Broker().PublishGuaranteed(pubCtx, topic, msg)
		cancel()
		if pubErr == nil {
			return nil
		}
		if attempt < maxAttempts {
			logger.Warn("publish resumed task response failed, retrying",
				"attempt", attempt,
				"error", pubErr,
			)
			time.Sleep(1 * time.Second)
		}
	}
	logger.Error("failed to publish resumed task response after retries",
		"replyTo", topic,
		"error", pubErr,
	)
	return pubErr
}

func rawHistoryContent(resp *llm.ChatResponse) string {
	if resp.RawContent != "" {
		return resp.RawContent
	}
	return resp.Content
}

const sessionSaveTimeout = 5 * time.Second

func (a *Agent) flushSessionMessages(ctx context.Context, state *TaskExecutionState, rc *taskRunContext) error {
	if rc == nil || rc.sess == nil || state == nil {
		return nil
	}
	if state.Fenced.Load() {
		return nil
	}
	if a.runCtx != nil && a.cutByProcessExit(a.runCtx) {
		return nil
	}
	if ctx.Err() != nil {
		appendCancellationMarker(state)
	}
	if state.HistoryOffset >= len(state.Messages) {
		return nil
	}
	newMsgs := llmMessagesToSessionMessages(state.Messages[state.HistoryOffset:], state.TaskID)
	targetOffset := len(state.Messages)

	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sessionSaveTimeout)
	defer cancel()
	landed, err := a.landedUnconfirmedAppend(saveCtx, state, rc.sess, newMsgs)
	if err != nil {
		return err
	}
	if landed > 0 {
		state.HistoryOffset += landed
		if rc.sess == state.unconfirmedAppend.sess {
			rc.sess.Messages = append(rc.sess.Messages, newMsgs[:landed]...)
		}
		newMsgs = newMsgs[landed:]
	} else if state.unconfirmedAppend != nil {
		state.runFacts.giveBack(state.unconfirmedAppend.facts)
	}
	state.unconfirmedAppend = nil
	facts := attachPendingRunFacts(newMsgs, &state.runFacts)
	if err := a.appendSessionMessages(saveCtx, rc.sess, newMsgs); err != nil {
		state.unconfirmedAppend = &unconfirmedSessionAppend{
			sess:         rc.sess,
			storedBefore: max(0, len(rc.sess.Messages)-rc.unstoredMessages),
			msgs:         newMsgs,
			facts:        facts,
		}
		return err
	}
	state.HistoryOffset = targetOffset

	if fp := metadataFingerprint(rc.sess.Metadata); fp != rc.sessMetaFP {
		saved, reloaded, err := a.saveSessionWithRetry(saveCtx, rc.sess, func(s *session.Session) {
			s.Metadata = rc.sess.Metadata
		})
		if err != nil {
			rc.logger.Warn("failed to persist session metadata; retrying on next flush",
				"taskID", state.TaskID,
				"sessionID", state.EffectiveSessionID,
				"error", err,
			)
		} else {
			rc.sess = saved
			rc.sessMetaFP = fp
			if reloaded {
				rc.unstoredMessages = 0
			}
		}
	}
	return nil
}

type unconfirmedSessionAppend struct {
	sess         *session.Session
	storedBefore int
	msgs         []session.Message
	facts        *session.RunFacts
}

func (u *unconfirmedSessionAppend) heads(newMsgs []session.Message) bool {
	return u != nil && len(newMsgs) >= len(u.msgs) && session.SameMessages(newMsgs[:len(u.msgs)], u.msgs)
}

func (a *Agent) landedUnconfirmedAppend(ctx context.Context, state *TaskExecutionState, sess *session.Session, newMsgs []session.Message) (int, error) {
	u := state.unconfirmedAppend
	if !u.heads(newMsgs) {
		return 0, nil
	}
	stored, err := a.svc.Sessions().Get(ctx, sess.AppName, sess.UserID, sess.SessionID)
	if err != nil {
		return 0, fmt.Errorf("check unconfirmed session append: %w", err)
	}
	if stored == nil || !session.AppendLanded(stored.Messages, u.storedBefore, u.msgs) {
		return 0, nil
	}
	return len(u.msgs), nil
}

func unsavedTurns(state *TaskExecutionState, sess *session.Session) []llm.ChatMessage {
	if state.HistoryOffset >= len(state.Messages) {
		return nil
	}
	tail := slices.Clone(state.Messages[state.HistoryOffset:])
	u := state.unconfirmedAppend
	if u == nil || !u.heads(llmMessagesToSessionMessages(tail, state.TaskID)) {
		return tail
	}
	if session.AppendLanded(sess.Messages, u.storedBefore, u.msgs) {
		state.unconfirmedAppend = nil
		return tail[len(u.msgs):]
	}
	return tail
}

func questionIsUnsaved(rebuilt, unsaved []llm.ChatMessage) bool {
	if len(rebuilt) == 0 || len(unsaved) == 0 {
		return false
	}
	last := rebuilt[len(rebuilt)-1]
	return last.Role == "user" && unsaved[0].Role == "user" &&
		stripCurrentTimeStamp(last.Content) == stripCurrentTimeStamp(unsaved[0].Content)
}

func (a *Agent) flushSessionMessagesMidTask(ctx context.Context, state *TaskExecutionState, rc *taskRunContext) {
	err := a.flushSessionMessages(ctx, state, rc)
	if err == nil {
		return
	}
	logger := rc.logger
	if logger == nil {
		logger = a.logger
	}
	logger.Warn("failed to save session mid-task; retrying on next flush", "error", err)
}

var finalFlushRetryBackoffs = []time.Duration{500 * time.Millisecond, 2 * time.Second}

var finalFlushBudget = 2*sessionSaveTimeout + time.Second

func (a *Agent) flushSessionMessagesFinal(ctx context.Context, state *TaskExecutionState, rc *taskRunContext) {
	started := time.Now()
	err := a.flushSessionMessages(ctx, state, rc)
	if err == nil {
		return
	}
	logger := rc.logger
	if logger == nil {
		logger = a.logger
	}
	for attempt := 0; err != nil && attempt < len(finalFlushRetryBackoffs); attempt++ {
		backoff := finalFlushRetryBackoffs[attempt]
		if time.Since(started)+backoff+sessionSaveTimeout > finalFlushBudget {
			break
		}
		logger.Warn("failed to save session; retrying terminal flush",
			"taskID", state.TaskID,
			"sessionID", state.EffectiveSessionID,
			"attempt", attempt+1,
			"error", err,
		)
		if !a.waitUnlessShuttingDown(backoff) {
			break
		}
		err = a.flushSessionMessages(ctx, state, rc)
	}
	if err == nil {
		return
	}
	a.recordSessionSaveLost("flush")
	logger.Error("failed to save session",
		"taskID", state.TaskID,
		"sessionID", state.EffectiveSessionID,
		"error", err,
	)
}

func (a *Agent) waitUnlessShuttingDown(d time.Duration) bool {
	var shutdown <-chan struct{}
	if a.runCtx != nil {
		shutdown = a.runCtx.Done()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-shutdown:
		return false
	}
}

const sessionSaveMaxRetries = 5

const saveSessionErrFmt = "save session: %w"

const metricSessionSaveLost = "session.save.lost"

func (a *Agent) recordSessionSaveLost(path string) {
	a.svc.Metrics().Counter(metricSessionSaveLost, 1, "component.name", agentDisplayName(&a.cfg), "path", path)
}

func (a *Agent) appendSessionMessages(ctx context.Context, sess *session.Session, newMsgs []session.Message) error {
	if sess == nil {
		return fmt.Errorf("append session messages: nil session")
	}
	if len(newMsgs) == 0 {
		return nil
	}
	if err := a.svc.Sessions().AppendMessages(ctx, sess.AppName, sess.UserID, sess.SessionID, newMsgs); err != nil {
		return fmt.Errorf("append session messages: %w", err)
	}
	sess.Messages = append(sess.Messages, newMsgs...)
	return nil
}

func (a *Agent) saveSessionWithRetry(ctx context.Context, sess *session.Session, apply func(*session.Session)) (saved *session.Session, reloaded bool, err error) {
	if sess == nil {
		return nil, false, fmt.Errorf("save session: nil session")
	}
	working := *sess
	apply(&working)
	err = a.svc.Sessions().Save(ctx, &working)
	if err == nil {
		*sess = working
		return sess, false, nil
	}
	if !errors.Is(err, session.ErrOptimisticLock) {
		return sess, false, fmt.Errorf(saveSessionErrFmt, err)
	}
	lastErr := err
	for attempt := 1; attempt <= sessionSaveMaxRetries; attempt++ {
		if ctx.Err() != nil {
			return sess, false, fmt.Errorf(saveSessionErrFmt, ctx.Err())
		}
		fresh, getErr := a.svc.Sessions().Get(ctx, sess.AppName, sess.UserID, sess.SessionID)
		if getErr != nil {
			return sess, false, fmt.Errorf("reload session for save retry: %w", getErr)
		}
		if fresh == nil {
			return sess, false, fmt.Errorf("session row missing during save retry")
		}
		apply(fresh)
		saveErr := a.svc.Sessions().Save(ctx, fresh)
		if saveErr == nil {
			*sess = *fresh
			return sess, true, nil
		}
		if !errors.Is(saveErr, session.ErrOptimisticLock) {
			return sess, false, fmt.Errorf(saveSessionErrFmt, saveErr)
		}
		lastErr = saveErr
	}
	return sess, false, fmt.Errorf("save session after %d retries: %w", sessionSaveMaxRetries, lastErr)
}

const pausedTaskHandoffTimeout = 30 * time.Second

func (a *Agent) letGoOfPausedTask(state *TaskExecutionState) {
	state.Fenced.Store(true)
	a.pausedTasks.Delete(state.TaskID)
	if !a.removing.Load() {
		a.peerRouter.ReleaseTask(state.TaskID)
	}
}

func (a *Agent) flushPausedTasksOnShutdown(ctx context.Context) {
	if a.pausedTasks == nil {
		return
	}
	states := a.pausedTasks.Snapshot()
	if len(states) == 0 {
		return
	}

	if a.checkpoints != nil {
		a.handingOffPausedTasks.Store(true)
		a.logger.Info("handing off paused-task checkpoints on shutdown", "count", len(states))
		handoffCtx, cancel := context.WithTimeout(ctx, pausedTaskHandoffTimeout)
		defer cancel()
		for i, state := range states {
			if handoffCtx.Err() != nil {
				a.logger.Warn("shutdown deadline reached during checkpoint handoff; remaining paused tasks resume after their lease lapses",
					"remaining", len(states)-i)
				for _, remaining := range states[i:] {
					if _, claimed := claimForHandoff(remaining); claimed {
						a.letGoOfPausedTask(remaining)
					}
				}
				return
			}
			a.handOffPausedTask(handoffCtx, state)
		}
		return
	}

	a.logger.Info("flushing paused-task sessions on shutdown", "count", len(states))
	for _, state := range states {
		a.flushPausedTaskSessionMessages(state)
		a.releaseRunningTask(state.TaskID)
	}
}

func (a *Agent) handOffPausedTask(ctx context.Context, state *TaskExecutionState) {
	if _, claimed := claimForHandoff(state); !claimed {
		a.logger.Debug("skipping checkpoint handoff for a task mid-turn; the turn hands it off when it ends",
			"traceID", state.UserProps.TraceID, "taskID", state.TaskID)
		return
	}
	a.letGoOfPausedTask(state)
	a.checkpoints.releaseOwnership(ctx, state.TaskID)
}

func (a *Agent) flushPausedTaskSessionMessages(state *TaskExecutionState) {
	if state == nil || state.IsRunBased {
		return
	}
	taskLogger := a.logger.With(
		"traceID", state.UserProps.TraceID,
		"taskID", state.TaskID)
	if state.Fenced.Load() {
		taskLogger.Debug("skipping paused-task flush: another replica owns this task")
		return
	}
	appendCancellationMarker(state)
	if state.HistoryOffset >= len(state.Messages) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), sessionSaveTimeout)
	defer cancel()
	sess, err := a.svc.Sessions().Get(ctx, a.cfg.Name, state.UserProps.UserID, state.EffectiveSessionID)
	if err != nil {
		taskLogger.Warn("failed to load session for paused-task flush",
			"taskID", state.TaskID,
			"sessionID", state.EffectiveSessionID,
			"error", err,
		)
		return
	}
	if sess == nil {
		taskLogger.Warn("session row missing for paused-task flush",
			"taskID", state.TaskID,
			"sessionID", state.EffectiveSessionID,
		)
		return
	}
	newMsgs := llmMessagesToSessionMessages(state.Messages[state.HistoryOffset:], state.TaskID)
	state.HistoryOffset = len(state.Messages)
	if u := state.unconfirmedAppend; u.heads(newMsgs) && session.AppendLanded(sess.Messages, u.storedBefore, u.msgs) {
		newMsgs = newMsgs[len(u.msgs):]
	}
	state.unconfirmedAppend = nil
	if err := a.appendSessionMessages(ctx, sess, newMsgs); err != nil {
		a.recordSessionSaveLost("paused_flush")
		taskLogger.Error("failed to save session for paused-task flush",
			"taskID", state.TaskID,
			"sessionID", state.EffectiveSessionID,
			"error", err,
		)
	}
}
