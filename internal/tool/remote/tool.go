// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package remote

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"regexp"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth/toolauth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const retryDelay = 1 * time.Second

type AuthHandlerChangeFunc func(oldCredentialKey string, newHandler tool.AuthResponseHandler)

type Tool struct {
	cfg           *ToolConfig
	executor      *Executor
	logger        *slog.Logger
	rawStringArgs []string

	authHandler atomic.Pointer[toolauth.AuthHandler]

	authNotify AuthHandlerChangeFunc

	initUnavailable atomic.Bool

	advertisedTimeoutSeconds atomic.Int64

	initMu                   sync.RWMutex
	initSchema               *tool.JSONSchema
	initInstructions         string
	initDescription          string
	initArtifacts            map[string]tool.ArtifactParamInfo
	initFingerprint          string
	initFromRequest          bool
	initBroadcastFingerprint string
	initAuthFingerprint      string
	initEffect               string
	broadcastGen             uint64
	lastBroadcastEffect      string
	appliedRequestGen        uint64
	pods                     podEffects
	clock                    func() time.Time
}

func (t *Tool) mergePodEffects(params *remotetool.InitParams, reply, ignoredReply bool) *remotetool.InitParams {
	if t.pods == nil {
		t.pods = podEffects{}
	}
	now := t.timeNow()
	t.pods.expire(now)
	switch {
	case ignoredReply:
	case reply:
		t.pods.recordReply(params.InstanceID, params.Effect, now)
	default:
		t.pods.recordBroadcast(params.InstanceID, computeInitFingerprint(params), params.Effect, now)
	}
	merged, any := t.pods.merged()
	if !any || merged == params.Effect {
		return params
	}
	withMerged := *params
	withMerged.Effect = merged
	return &withMerged
}

func (t *Tool) timeNow() time.Time {
	if t.clock != nil {
		return t.clock()
	}
	return time.Now()
}

var _ tool.BuiltinTool = (*Tool)(nil)

var _ tool.EffectClassifier = (*Tool)(nil)

func (t *Tool) EffectClass() tool.EffectClass {
	t.initMu.RLock()
	effect := t.initEffect
	t.initMu.RUnlock()
	if !remotetool.ValidEffect(effect) {
		return tool.DefaultEffectClass
	}
	return tool.DeclaredEffect(tool.Effect(effect))
}

func (t *Tool) markInitUnavailable() { t.initUnavailable.Store(true) }

func (t *Tool) Available() bool { return !t.initUnavailable.Load() }

func (t *Tool) unavailableMessage() string {
	msg := fmt.Sprintf("tool %q is unavailable: no STR worker answered its init_request", t.cfg.ToolName)
	if bin := t.cfg.STRBinary(); bin != "" {
		msg += fmt.Sprintf(" (backing binary %q not found in any worker manifest)", bin)
	}
	return msg + "; ensure the tool's STR binary is deployed (container/tools dir), or run a sam build with the tool compiled in for embedded mode"
}

func (t *Tool) failureText(err error) string {
	return fmt.Sprintf("remote tool %q: %v", t.cfg.ToolName, err)
}

func (t *Tool) staleConnectorRegistration(err *InvokeError) bool {
	return err != nil && err.Code == remotetool.ErrToolNotFound && t.cfg.STRBinary() != ""
}

func (t *Tool) reinitTimeout(ctx context.Context) time.Duration {
	d := t.executor.InitTimeout()
	if dl, ok := ctx.Deadline(); ok {
		if rem := time.Until(dl); rem < d {
			d = rem
		}
	}
	return d
}

func executorLogger(executor *Executor) *slog.Logger {
	if executor != nil && executor.logger != nil {
		return executor.logger
	}
	return slog.Default().With("logger_name", "remote-tool") //nolint:sloglint
}

func NewTool(cfg *ToolConfig, executor *Executor) *Tool {
	t := &Tool{
		cfg:      cfg,
		executor: executor,
		logger:   executorLogger(executor).With("tool", cfg.ToolName, "type", "remote"),
	}
	if cfg.Auth != nil {
		t.authHandler.Store(toolauth.NewAuthHandler(cfg.Auth, t.logger))
	}
	return t
}

func NewToolWithOptions(cfg *ToolConfig, executor *Executor, rawStringArgs []string) *Tool {
	t := &Tool{
		cfg:           cfg,
		executor:      executor,
		logger:        executorLogger(executor).With("tool", cfg.ToolName, "type", "remote"),
		rawStringArgs: rawStringArgs,
	}
	if cfg.Auth != nil {
		t.authHandler.Store(toolauth.NewAuthHandler(cfg.Auth, t.logger))
	}
	return t
}

func (t *Tool) SetAuthHandlerNotifier(fn AuthHandlerChangeFunc) {
	t.initMu.Lock()
	defer t.initMu.Unlock()
	t.authNotify = fn
}

func (t *Tool) Name() string { return t.cfg.ToolName }

func (t *Tool) Description() string {
	if t.cfg.Description != "" {
		return t.cfg.Description
	}
	t.initMu.RLock()
	defer t.initMu.RUnlock()
	return t.initDescription
}

func (t *Tool) ArgumentErrorHint(args map[string]any) string {
	if t.cfg.ArgumentErrorHint == nil {
		return ""
	}
	return t.cfg.ArgumentErrorHint(args)
}

func (t *Tool) ToolPassthroughConfig() map[string]any {
	return t.cfg.ToolPassthroughConfig
}

func (t *Tool) TimeoutSeconds() int {
	return t.cfg.TimeoutSeconds
}

const maxAdvertisedTimeoutSeconds = 24 * 60 * 60

func clampAdvertisedTimeout(seconds int) int {
	return max(0, min(seconds, maxAdvertisedTimeoutSeconds))
}

func (t *Tool) Parameters() *tool.JSONSchema {
	t.initMu.RLock()
	defer t.initMu.RUnlock()
	if t.initSchema != nil {
		return t.initSchema
	}
	return t.cfg.BuildJSONSchema()
}

func (t *Tool) Instructions() string {
	t.initMu.RLock()
	defer t.initMu.RUnlock()
	return t.initInstructions
}

func (t *Tool) IsLongRunning() bool { return true }

func (t *Tool) RequiredScopes() []string { return t.cfg.RequiredScopes }

func (t *Tool) RawStringArgs() []string { return t.rawStringArgs }

func (t *Tool) ArtifactParams() map[string]tool.ArtifactParamInfo {
	return nil
}

func (t *Tool) ApplyInitParams(params *remotetool.InitParams) {
	t.applyInit(params, false)
}

func (t *Tool) ApplyInitParamsKeepingEffect(params *remotetool.InitParams) {
	t.applyInitWith(params, initApplication{keepEffect: true})
}

func (t *Tool) broadcastGeneration() uint64 {
	t.initMu.RLock()
	defer t.initMu.RUnlock()
	return t.broadcastGen
}

func (t *Tool) applyInit(params *remotetool.InitParams, fromRequest bool) (refreshNeeded bool) {
	return t.applyInitWith(params, initApplication{fromRequest: fromRequest, requestedAt: t.broadcastGeneration()})
}

func (t *Tool) applyRequestedInit(params *remotetool.InitParams, requestedAt uint64) (refreshNeeded bool) {
	return t.applyInitWith(params, initApplication{fromRequest: true, requestedAt: requestedAt})
}

type initApplication struct {
	fromRequest bool
	keepEffect  bool
	requestedAt uint64
}

const maxStaleRequestRetries = 3

func (t *Tool) applyInitWith(params *remotetool.InitParams, how initApplication) (refreshNeeded bool) {
	if t.initNamesAnotherTool(params, how.fromRequest) {
		return false
	}

	t.initMu.Lock()
	defer t.initMu.Unlock()

	staleReply := false
	defer func() {
		refreshNeeded = refreshNeeded || staleReply
	}()
	if how.fromRequest && !t.claimRequestGenLocked(how.requestedAt) {
		return false
	}
	params, staleReply = t.effectiveInitLocked(params, how)
	newFingerprint := computeInitFingerprint(params)

	t.initUnavailable.Store(false)

	t.advertisedTimeoutSeconds.Store(int64(clampAdvertisedTimeout(params.TimeoutSeconds)))

	if !how.fromRequest && !t.recordChangedBroadcastLocked(newFingerprint, how.keepEffect) {
		return false
	}

	if t.initFingerprint == newFingerprint {
		t.logger.Debug("init schema unchanged, skipping", "tool", t.cfg.ToolName)
		return false
	}

	if !how.fromRequest && t.initFromRequest {
		t.holdConfigAwareSchemaLocked(params)
		return true
	}

	t.storeInitLocked(params)
	t.initFingerprint = newFingerprint
	if how.fromRequest {
		t.initFromRequest = true
	}

	t.logger.Debug("tool schema updated from init",
		"hasInstructions", params.Instructions != "",
		"hasParameters", params.Parameters != nil,
		"artifactParams", len(params.ArtifactParams),
		"hasAuth", params.Auth != nil)
	return false
}

func (t *Tool) initNamesAnotherTool(params *remotetool.InitParams, fromRequest bool) bool {
	if params.ToolName == "" || params.ToolName == t.cfg.ToolName {
		return false
	}
	t.logger.Warn("rejecting init params for wrong tool",
		"expected", t.cfg.ToolName,
		"received", params.ToolName,
		"fromRequest", fromRequest)
	return true
}

func (t *Tool) claimRequestGenLocked(requestedAt uint64) bool {
	if requestedAt < t.appliedRequestGen {
		t.logger.Debug("ignoring an init reply older than the one already applied")
		return false
	}
	t.appliedRequestGen = requestedAt
	return true
}

func (t *Tool) effectiveInitLocked(params *remotetool.InitParams, how initApplication) (effective *remotetool.InitParams, staleReply bool) {
	if !how.fromRequest && !how.keepEffect {
		t.lastBroadcastEffect = params.Effect
	}
	if how.keepEffect {
		kept := t.withCachedEffectLocked(params)
		return kept, t.replyOlderThanStricterBroadcastLocked(kept, how)
	}
	staleReply = t.replyOlderThanStricterBroadcastLocked(params, how)
	return t.mergePodEffects(params, how.fromRequest && !staleReply, how.fromRequest && staleReply), staleReply
}

func (t *Tool) withCachedEffectLocked(params *remotetool.InitParams) *remotetool.InitParams {
	kept := *params
	kept.Effect = t.initEffect
	return &kept
}

func (t *Tool) replyOlderThanStricterBroadcastLocked(params *remotetool.InitParams, how initApplication) bool {
	stale := how.fromRequest && how.requestedAt != t.broadcastGen && params.Effect == remotetool.EffectReadOnly &&
		t.lastBroadcastEffect != remotetool.EffectReadOnly
	if stale {
		t.logger.Warn("init reply to a request made before a broadcast declared a stricter effect; keeping the broadcast's effect and asking again")
	}
	return stale
}

func (t *Tool) recordChangedBroadcastLocked(newFingerprint string, keepEffect bool) bool {
	unchanged := t.initBroadcastFingerprint == newFingerprint
	t.initBroadcastFingerprint = newFingerprint
	if unchanged {
		t.logger.Debug("broadcast init unchanged, skipping", "tool", t.cfg.ToolName)
		return false
	}
	if !keepEffect {
		t.broadcastGen++
	}
	return true
}

func (t *Tool) holdConfigAwareSchemaLocked(params *remotetool.InitParams) {
	if t.initEffect == remotetool.EffectReadOnly && params.Effect != remotetool.EffectReadOnly {
		t.initEffect = params.Effect
		t.initFingerprint = ""
	}
	t.logger.Debug("broadcast init differs from cached config-aware schema; requesting refresh",
		"tool", t.cfg.ToolName)
}

func (t *Tool) storeInitLocked(params *remotetool.InitParams) {
	if params.Instructions != "" {
		t.initInstructions = params.Instructions
	}
	if params.Description != "" {
		t.initDescription = params.Description
	}
	t.initEffect = params.Effect

	if params.Parameters != nil {
		t.initSchema = initSchemaFromParameters(params.Parameters)
	}
	if len(params.ArtifactParams) > 0 {
		t.storeArtifactParamsLocked(params.ArtifactParams)
	}
	if len(params.VolumeParams) > 0 && len(t.cfg.VolumeParams) == 0 {
		t.cfg.VolumeParams = volumeParamConfigs(params.VolumeParams)
	}

	t.applyInitAuthLocked(params.Auth)
}

func initSchemaFromParameters(parameters map[string]any) *tool.JSONSchema {
	schema := &tool.JSONSchema{
		Type: "object",
	}
	if props, ok := parameters["properties"].(map[string]any); ok {
		schema.Properties = props
	}
	if req, ok := parameters["required"].([]any); ok {
		for _, r := range req {
			if s, ok := r.(string); ok {
				schema.Required = append(schema.Required, s)
			}
		}
		slices.Sort(schema.Required)
	}
	return schema
}

func (t *Tool) storeArtifactParamsLocked(artifactParams map[string]remotetool.ArtifactParamInfo) {
	artInfo := make(map[string]tool.ArtifactParamInfo, len(artifactParams))
	for name, info := range artifactParams {
		artInfo[name] = tool.ArtifactParamInfo{
			IsList:      info.IsList,
			IsOptional:  info.IsOptional,
			FilenameKey: info.FilenameKey,
		}
		t.declareArtifactParamLocked(name, info)
	}
	t.initArtifacts = artInfo
}

func (t *Tool) declareArtifactParamLocked(name string, info remotetool.ArtifactParamInfo) {
	if t.cfg.Parameters == nil {
		t.cfg.Parameters = make(map[string]ParamConfig)
	}
	if _, exists := t.cfg.Parameters[name]; exists {
		return
	}
	t.cfg.Parameters[name] = ParamConfig{
		Type:        artifactParamType(info),
		Required:    !info.IsOptional,
		FilenameKey: info.FilenameKey,
	}
}

func artifactParamType(info remotetool.ArtifactParamInfo) string {
	switch {
	case info.FilenameKey != "":
		return "artifact_struct_list"
	case info.IsList:
		return "artifact_list"
	default:
		return "artifact"
	}
}

func volumeParamConfigs(params []remotetool.VolumeParam) []VolumeParamConfig {
	vps := make([]VolumeParamConfig, len(params))
	for i, vp := range params {
		vps[i] = VolumeParamConfig{
			Name:        vp.Name,
			Description: vp.Description,
			Mode:        vp.Mode,
			MountPath:   vp.MountPath,
		}
	}
	return vps
}

func (t *Tool) applyInitAuthLocked(wireAuth *remotetool.AuthDeclaration) {
	newAuthFP := authWireFingerprint(wireAuth)
	if newAuthFP == t.initAuthFingerprint {
		return
	}

	merged := toolauth.MergeAuthConfigs(authDeclarationToConfig(wireAuth), t.cfg.Auth)
	var newHandler *toolauth.AuthHandler
	if merged != nil {
		newHandler = toolauth.NewAuthHandler(merged, t.logger)
	}

	var oldKey string
	if oldHandler := t.authHandler.Load(); oldHandler != nil {
		oldKey = oldHandler.CredentialKey()
	}
	t.authHandler.Store(newHandler)
	t.initAuthFingerprint = newAuthFP

	if t.authNotify != nil {
		t.authNotify(oldKey, authHandlerAsResponseHandler(newHandler))
	}

	newKey := ""
	if newHandler != nil {
		newKey = newHandler.CredentialKey()
	}
	t.logger.Info("tool auth handler updated from init",
		"tool", t.cfg.ToolName,
		"oldCredentialKey", oldKey,
		"newCredentialKey", newKey,
		"hasHandler", newHandler != nil)
}

func authHandlerAsResponseHandler(h *toolauth.AuthHandler) tool.AuthResponseHandler {
	if h == nil {
		return nil
	}
	return h
}

func authWireFingerprint(a *remotetool.AuthDeclaration) string {
	if a == nil {
		return ""
	}
	data, _ := json.Marshal(a)
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum)
}

func authDeclarationToConfig(a *remotetool.AuthDeclaration) *toolauth.AuthConfig {
	if a == nil || a.Type == "" {
		return nil
	}
	ac := &toolauth.AuthConfig{
		Type: a.Type,
	}
	if a.Scheme != nil {
		ac.Scheme = &toolauth.OAuthScheme{
			AuthorizationURL:        a.Scheme.AuthorizationURL,
			TokenURL:                a.Scheme.TokenURL,
			Scopes:                  a.Scheme.Scopes,
			TokenEndpointAuthMethod: a.Scheme.TokenEndpointAuthMethod,
		}
	}
	return ac
}

func computeInitFingerprint(params *remotetool.InitParams) string {
	h := sha256.New()
	h.Write([]byte(params.Description))
	h.Write([]byte(params.Instructions))
	h.Write([]byte{0})
	if params.Parameters != nil {
		data, _ := json.Marshal(params.Parameters)
		h.Write(data)
	}
	h.Write([]byte{0})
	if params.ArtifactParams != nil {
		data, _ := json.Marshal(params.ArtifactParams)
		h.Write(data)
	}
	h.Write([]byte{0})
	if params.Auth != nil {
		data, _ := json.Marshal(params.Auth)
		h.Write(data)
	}
	h.Write([]byte{0})
	if len(params.VolumeParams) > 0 {
		data, _ := json.Marshal(params.VolumeParams)
		h.Write(data)
	}
	h.Write([]byte{0})
	h.Write([]byte(params.Effect))
	return fmt.Sprintf("%x", h.Sum(nil))
}

func (t *Tool) RequestSchema(ctx context.Context) error {
	timeout := t.executor.InitTimeout()
	for attempt := 0; ; attempt++ {
		requestedAt := t.broadcastGeneration()
		params, err := t.executor.RequestToolSchema(ctx, t.cfg.ToolName, t.cfg.ToolPassthroughConfig, timeout)
		if err != nil {
			return err
		}
		if !t.applyRequestedInit(params, requestedAt) || attempt >= maxStaleRequestRetries {
			return nil
		}
	}
}

func (t *Tool) ContextParam() string { return "" }

func (t *Tool) VolumeParams() []tool.VolumeParamInfo {
	t.initMu.Lock()
	defer t.initMu.Unlock()
	if len(t.cfg.VolumeParams) == 0 {
		return nil
	}
	result := make([]tool.VolumeParamInfo, len(t.cfg.VolumeParams))
	for i, vp := range t.cfg.VolumeParams {
		result[i] = tool.VolumeParamInfo{
			Name:      vp.Name,
			Mode:      vp.Mode,
			MountPath: vp.MountPath,
		}
	}
	return result
}

func (t *Tool) AuthHandler() *toolauth.AuthHandler {
	return t.authHandler.Load()
}

func headerToMap(h http.Header) map[string]string {
	if len(h) == 0 {
		return nil
	}
	out := make(map[string]string, len(h))
	for k := range h {
		if v := h.Get(k); v != "" {
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (t *Tool) resolveToolAuth(ctx context.Context, args map[string]any, toolCtx *tool.Context) (oauthToken string, authHeaders map[string]string, early *tool.Result, earlyErr error) {
	ah := t.authHandler.Load()
	if ah == nil {
		return "", nil, nil, nil
	}
	switch ah.Config().Type {
	case "oauth2":
		token, err := ah.GetOAuthToken(ctx, toolCtx)
		if errors.Is(err, toolauth.ErrAuthRequired) {
			early, earlyErr = t.triggerAuthFlow(ctx, toolCtx, args, "")
			return "", nil, early, earlyErr
		}
		if errors.Is(err, toolauth.ErrTransientRefresh) {
			return "", nil, toolauth.NewSignInProviderUnavailableResult(t.Name(), err), nil
		}
		if err != nil {
			return "", nil, tool.Error(fmt.Sprintf("auth error: %v", err)), nil
		}
		if token != nil {
			oauthToken = token.AccessToken
		}
	case "bearer", "basic":
		headers, err := ah.GetAuthHeaders(ctx, toolCtx)
		if err != nil {
			t.logger.Warn("static tool auth configured but header could not be built",
				"authType", ah.Config().Type, "error", err)
		} else {
			authHeaders = headerToMap(headers)
		}
		if ah.Config().Type == "bearer" {
			if cred := ah.Config().Credential; cred != nil && cred.Token != "" {
				oauthToken = cred.Token
			}
		}
	}
	return oauthToken, authHeaders, nil, nil
}

func (t *Tool) Execute(ctx context.Context, args map[string]any, toolCtx *tool.Context) (*tool.Result, error) {
	if refused := t.refuseBeforeInvoke(args); refused != nil {
		return refused, nil
	}

	oauthToken, authHeaders, early, earlyErr := t.resolveToolAuth(ctx, args, toolCtx)
	if early != nil || earlyErr != nil {
		return early, earlyErr
	}

	originalArgs := args
	args = t.resolveLateEmbeds(ctx, args, toolCtx)

	if missing := t.refuseMissingArtifacts(ctx, args, toolCtx); missing != nil {
		return missing, nil
	}

	searchTurn := t.drawSearchTurn(toolCtx)
	req := t.invokeRequest(args, toolCtx, oauthToken, authHeaders, searchTurn)

	statusCb := t.statusForwarder(toolCtx)
	call, err := t.executor.Begin(ctx, generateCorrID(), req, statusCb)
	if err == nil && t.pausable(toolCtx) {
		return t.pendingResult(ctx, toolCtx, call, req, statusCb, originalArgs, searchTurn), nil
	}
	return t.awaitCall(ctx, toolCtx, call, err, invocation{req: req, statusCb: statusCb, originalArgs: originalArgs, searchTurn: searchTurn})
}

const pendingKindRemoteTool = "remote_tool"

func (t *Tool) pausable(toolCtx *tool.Context) bool {
	return toolCtx.PauseResume && toolCtx.PauseRemoteTools && t.authHandler.Load() == nil
}

func (t *Tool) pendingResult(ctx context.Context, toolCtx *tool.Context, call *Call, req *InvokeRequest, statusCb StatusCallback, originalArgs map[string]any, searchTurn *int) *tool.Result {
	result := tool.Pending(fmt.Sprintf("Waiting for remote tool %q", t.Name()), tool.WithData(map[string]any{
		"pending_kind": pendingKindRemoteTool,
		"sub_task_id":  call.CorrID(),
		"timeout_ms":   call.Timeout().Milliseconds(),
	}))
	call.MarkPaused()
	attempts, cancelAttempts := context.WithCancelCause(context.WithoutCancel(ctx))
	t.executor.trackPausedCall(call.CorrID(), cancelAttempts)
	result.OnDelivery = func(deliver func(*tool.Result)) {
		go t.completePausedCall(toolCtx, call, deliver, func() *tool.Result {
			defer t.executor.untrackPausedCall(call.CorrID())
			return t.awaitPausedCall(attempts, toolCtx, call, req, statusCb, originalArgs, searchTurn)
		})
	}
	return result
}

func (t *Tool) awaitPausedCall(ctx context.Context, toolCtx *tool.Context, call *Call, req *InvokeRequest, statusCb StatusCallback, originalArgs map[string]any, searchTurn *int) *tool.Result {
	finished, err := t.awaitCall(ctx, toolCtx, call, nil, invocation{req: req, statusCb: statusCb, originalArgs: originalArgs, searchTurn: searchTurn})
	if errors.Is(err, errCallAbandoned) {
		return nil
	}
	if err != nil {
		t.logger.Error("a paused remote tool call could not be completed; delivering an error result",
			"traceID", toolCtx.TraceID, "taskID", toolCtx.ParentTaskID, "corrID", call.CorrID(), "tool", t.cfg.ToolName, "error", err)
		return tool.Error(t.failureText(err))
	}
	return finished
}

func (t *Tool) completePausedCall(toolCtx *tool.Context, call *Call, deliver func(*tool.Result), finish func() *tool.Result) {
	defer func() {
		recovered := recover()
		if recovered == nil {
			return
		}
		t.logger.Error("a paused remote tool call's completion panicked",
			"traceID", toolCtx.TraceID, "taskID", toolCtx.ParentTaskID, "corrID", call.CorrID(), "tool", t.cfg.ToolName,
			"panic", fmt.Sprint(recovered), "stack", string(debug.Stack()))
		t.deliverRecoveryError(toolCtx, call, deliver)
	}()
	if result := finish(); result != nil {
		deliver(result)
	}
}

func (t *Tool) deliverRecoveryError(toolCtx *tool.Context, call *Call, deliver func(*tool.Result)) {
	defer func() {
		recovered := recover()
		if recovered == nil {
			return
		}
		t.logger.Error("a paused remote tool call's recovery delivery panicked",
			"traceID", toolCtx.TraceID, "taskID", toolCtx.ParentTaskID, "corrID", call.CorrID(), "tool", t.cfg.ToolName,
			"panic", fmt.Sprint(recovered), "stack", string(debug.Stack()))
	}()
	deliver(tool.Error(fmt.Sprintf("remote tool %q failed while completing its call", t.Name())))
}

func (t *Tool) Executor() *Executor { return t.executor }

func (t *Tool) CancelCall(corrID string) {
	t.executor.CancelRemote(corrID)
}

func (t *Tool) ReplyIsRetryable(payload []byte) bool {
	roe := parseResponse(payload)
	if roe.err == nil {
		return false
	}
	failure := &InvokeError{Code: roe.err.Code, Message: roe.err.Message}
	return failure.Retryable() || t.staleConnectorRegistration(failure)
}

func (t *Tool) ResultFromReply(payload []byte) *tool.Result {
	roe := parseResponse(payload)
	if roe.err != nil {
		return t.invokeFailure(&InvokeError{
			Code:       roe.err.Code,
			Message:    roe.err.Message,
			StderrTail: roe.err.StderrTail,
			StackTrace: roe.err.StackTrace,
		})
	}
	return t.buildResult(&InvokeResponse{Result: *roe.result})
}

type invocation struct {
	req          *InvokeRequest
	statusCb     StatusCallback
	originalArgs map[string]any
	searchTurn   *int
}

func (t *Tool) awaitCall(ctx context.Context, toolCtx *tool.Context, call *Call, beginErr error, inv invocation) (*tool.Result, error) {
	req, statusCb, originalArgs, searchTurn := inv.req, inv.statusCb, inv.originalArgs, inv.searchTurn
	var resp *InvokeResponse
	err := beginErr
	if err == nil {
		resp, err = call.Wait(ctx)
	}
	if errors.Is(err, errCallAbandoned) {
		return nil, err
	}
	resp, failed := t.retryOnce(ctx, req, toolCtx, statusCb, resp, err)
	if failed != nil {
		if errors.Is(context.Cause(ctx), errCallAbandoned) {
			return nil, errCallAbandoned
		}
		return failed, nil
	}
	if t.rejectedTokenWasReplaced(ctx, toolCtx, resp, req.ToolOAuthToken) {
		oauthToken, authHeaders, early, earlyErr := t.resolveToolAuth(ctx, originalArgs, toolCtx)
		if early != nil || earlyErr != nil {
			return early, earlyErr
		}
		req = t.invokeRequest(req.Args, toolCtx, oauthToken, authHeaders, searchTurn)
		resp, err = t.executor.Invoke(ctx, generateCorrID(), req, statusCb)
		if resp, failed = t.retryOnce(ctx, req, toolCtx, statusCb, resp, err); failed != nil {
			if errors.Is(context.Cause(ctx), errCallAbandoned) {
				return nil, errCallAbandoned
			}
			return failed, nil
		}
	}
	return t.finishResult(ctx, toolCtx, resp, originalArgs, searchTurn, req.ToolOAuthToken)
}

func (t *Tool) rejectedTokenWasReplaced(ctx context.Context, toolCtx *tool.Context, resp *InvokeResponse, sentAccessToken string) bool {
	ah := t.authHandler.Load()
	if ah == nil || t.buildResult(resp).ErrorCode != tool.ErrorCodeAuthRequired || !ah.StoredTokenReplaces(ctx, toolCtx, sentAccessToken) {
		return false
	}
	t.logger.Debug("tool rejected a token another writer has replaced; retrying with the stored token",
		"tool", t.cfg.ToolName, "traceID", toolCtx.TraceID, "taskID", toolCtx.ParentTaskID)
	return true
}

func (t *Tool) refuseBeforeInvoke(args map[string]any) *tool.Result {
	if t.cfg.ArgGuard != nil {
		if err := t.cfg.ArgGuard(args); err != nil {
			return tool.Error(err.Error())
		}
	}

	if t.initUnavailable.Load() {
		unavailable := &InvokeError{Code: remotetool.ErrToolNotAvailable, Message: t.unavailableMessage()}
		return tool.Error(t.failureText(unavailable))
	}
	return nil
}

func (t *Tool) resolveLateEmbeds(ctx context.Context, args map[string]any, toolCtx *tool.Context) map[string]any {
	if toolCtx.ArtifactService == nil {
		return args
	}
	resolved := t.resolveArtifactContentEmbeds(ctx, args, toolCtx)
	t.autoResolveInputArtifacts(ctx, resolved, toolCtx)
	return resolved
}

func (t *Tool) invokeRequest(args map[string]any, toolCtx *tool.Context, oauthToken string, authHeaders map[string]string, searchTurn *int) *InvokeRequest {
	req := &InvokeRequest{
		ToolName:           t.cfg.ToolName,
		Args:               args,
		ToolConfig:         t.cfg.ToolPassthroughConfig,
		AppName:            toolCtx.AgentName,
		UserID:             toolCtx.UserID,
		SessionID:          toolCtx.SessionID,
		ArtifactReferences: t.buildArtifactReferences(args),
		TimeoutSeconds:     t.cfg.TimeoutSeconds,
		AdvertisedTimeout:  int(t.advertisedTimeoutSeconds.Load()),
		SandboxProfile:     t.cfg.SandboxProfile,
		ToolOAuthToken:     oauthToken,
		ToolAuthHeaders:    authHeaders,
		ParentTaskID:       toolCtx.ParentTaskID,
		A2AStatusTopic:     toolCtx.A2AStatusTopic,
		ContextID:          toolCtx.SessionID,
		TraceID:            toolCtx.TraceID,
		Spend:              toolCtx.RemoteSpend,
		SearchTurn:         searchTurn,
		VolumeBindings:     wireVolumeBindings(toolCtx.VolumeBindings),
		NetworkBindings:    wireNetworkBindings(toolCtx.NetworkBindings),
		A2AUserConfig:      toolCtx.A2AUserConfig,
	}
	if toolCtx.Identity != nil {
		req.AuthToken = toolCtx.Identity.AuthToken
	}
	return req
}

func wireVolumeBindings(bindings []tool.VolumeBinding) []remotetool.VolumeBinding {
	if len(bindings) == 0 {
		return nil
	}
	wire := make([]remotetool.VolumeBinding, len(bindings))
	for i, b := range bindings {
		wire[i] = remotetool.VolumeBinding{
			VolumeID:  b.VolumeID,
			MountPath: b.MountPath,
			ReadOnly:  b.ReadOnly,
			AllowExec: b.AllowExec,
		}
	}
	return wire
}

func wireNetworkBindings(bindings []tool.NetworkBinding) []remotetool.NetworkBinding {
	if len(bindings) == 0 {
		return nil
	}
	wire := make([]remotetool.NetworkBinding, len(bindings))
	for i, b := range bindings {
		allowlist := make([]string, len(b.Allowlist))
		copy(allowlist, b.Allowlist)
		wire[i] = remotetool.NetworkBinding{
			ParamName:  b.ParamName,
			PolicyName: b.PolicyName,
			Allowlist:  allowlist,
		}
	}
	return wire
}

func (t *Tool) statusForwarder(toolCtx *tool.Context) StatusCallback {
	if toolCtx.SendStatus == nil {
		return nil
	}
	return func(_, statusText string) {
		if err := toolCtx.SendStatus(statusText); err != nil {
			t.logger.Debug("status forward failed", "error", err)
		}
	}
}

func (t *Tool) retryOnce(ctx context.Context, req *InvokeRequest, toolCtx *tool.Context, statusCb StatusCallback, resp *InvokeResponse, err error) (*InvokeResponse, *tool.Result) {
	if err == nil {
		return resp, nil
	}
	if invokeErr, ok := errors.AsType[*InvokeError](err); ok && t.shouldRetry(ctx, invokeErr, toolCtx) {
		select {
		case <-time.After(retryDelay):
		case <-ctx.Done():
			return nil, tool.Error("cancelled during retry wait")
		}
		resp, err = t.executor.Invoke(ctx, generateCorrID(), req, statusCb)
	}
	if err != nil {
		return nil, t.invokeFailure(err)
	}
	return resp, nil
}

func (t *Tool) shouldRetry(ctx context.Context, invokeErr *InvokeError, toolCtx *tool.Context) bool {
	stale := t.staleConnectorRegistration(invokeErr)
	if !invokeErr.Retryable() && !stale {
		return false
	}
	if !stale {
		t.logger.Warn("retrying after TOOL_NOT_AVAILABLE", "tool", t.cfg.ToolName,
			"traceID", toolCtx.TraceID, "taskID", toolCtx.ParentTaskID)
		return true
	}
	initParams, reinitErr := t.executor.RequestToolSchema(ctx, t.cfg.ToolName, t.ToolPassthroughConfig(), t.reinitTimeout(ctx))
	if reinitErr != nil || initParams == nil {
		t.logger.Warn("connector tool re-init did not re-register; surfacing not-found without retry",
			"tool", t.cfg.ToolName, "strBinary", t.cfg.STRBinary(), "error", reinitErr,
			"traceID", toolCtx.TraceID, "taskID", toolCtx.ParentTaskID)
		return false
	}
	t.logger.Warn("connector tool re-registered via init_request; retrying",
		"tool", t.cfg.ToolName, "strBinary", t.cfg.STRBinary(),
		"traceID", toolCtx.TraceID, "taskID", toolCtx.ParentTaskID)
	return true
}

func (t *Tool) invokeFailure(err error) *tool.Result {
	invokeErr, ok := errors.AsType[*InvokeError](err)
	if !ok {
		return tool.Error(t.failureText(err))
	}
	detail := &tool.ToolFailedDetail{
		Code:       string(invokeErr.Code),
		Message:    invokeErr.Message,
		StderrTail: invokeErr.StderrTail,
		StackTrace: invokeErr.StackTrace,
		Guidance:   guidanceForCode(invokeErr.Code),
	}
	if isToolProcessFailure(invokeErr.Code) {
		return tool.Failed(t.failureText(err), detail)
	}
	r := tool.Error(t.failureText(err))
	r.FailedDetail = detail
	return r
}

func (t *Tool) finishResult(ctx context.Context, toolCtx *tool.Context, resp *InvokeResponse, originalArgs map[string]any, searchTurn *int, sentAccessToken string) (*tool.Result, error) {
	signals := takeResultSignals(resp.Result.ToolResult)
	result := t.buildResult(resp)
	t.notifyCreatedArtifacts(ctx, toolCtx, result)

	if t.authHandler.Load() != nil && result.ErrorCode == tool.ErrorCodeAuthRequired {
		pending, err := t.startAuthFlow(ctx, toolCtx, originalArgs, result.Message, sentAccessToken)
		holdSearchTurn(toolCtx, searchTurn, pending)
		return pending, err
	}

	if result.Status == tool.StatusSuccess || result.Status == tool.StatusPartial {
		t.publishResultSignals(toolCtx, signals, searchTurn)
	} else if signals != nil {
		t.logger.Debug("tool result signals not published: the call did not succeed",
			"resultStatus", result.Status, "traceID", toolCtx.TraceID, "taskID", toolCtx.ParentTaskID)
	}
	return result, nil
}

func (t *Tool) notifyCreatedArtifacts(ctx context.Context, toolCtx *tool.Context, result *tool.Result) {
	if toolCtx == nil || toolCtx.NotifyArtifactSaved == nil {
		return
	}
	for i := range result.DataObjects {
		do := result.DataObjects[i]
		if do.ArtifactRef == nil {
			continue
		}
		toolCtx.NotifyArtifactSaved(ctx, tool.ArtifactSavedInfo{
			Filename:    do.ArtifactRef.Filename,
			Version:     do.ArtifactRef.Version,
			MIMEType:    do.MIMEType,
			SizeBytes:   do.ArtifactRef.SizeBytes,
			Description: do.Description,
			Tags:        do.Tags,
			ToolCallID:  toolCtx.ToolCallID,
		})
	}
}

func (t *Tool) triggerAuthFlow(ctx context.Context, toolCtx *tool.Context, args map[string]any, toolMessage string) (*tool.Result, error) {
	return t.startAuthFlow(ctx, toolCtx, args, toolMessage, "")
}

func (t *Tool) startAuthFlow(ctx context.Context, toolCtx *tool.Context, args map[string]any, toolMessage, rejectedAccessToken string) (*tool.Result, error) {
	if r := toolauth.PreAuthShortCircuit(toolCtx, t.Name(), t.logger); r != nil {
		return r, nil
	}
	ah := t.authHandler.Load()
	if ah == nil {
		return tool.Error("auth required but no auth handler configured"), nil
	}
	credKey := ah.CredentialKey()

	if toolCtx.AuthGaveUp != nil && toolCtx.AuthGaveUp(toolCtx.UserID, toolCtx.TraceID, credKey) {
		t.logger.Warn("auth flow given up — failing tool without re-prompting",
			"toolName", t.cfg.ToolName,
			"credentialKey", credKey,
			"toolCallID", toolCtx.ToolCallID,
			"traceID", toolCtx.TraceID,
		)
		return toolauth.NewAuthGaveUpResult(t.cfg.ToolName, toolMessage), nil
	}

	if toolCtx.BeginAuthFlow != nil && !toolCtx.BeginAuthFlow(toolCtx.UserID, toolCtx.TraceID, credKey) {
		t.logger.Debug("auth flow coalesced (follower)",
			"toolName", t.cfg.ToolName,
			"credentialKey", credKey,
			"toolCallID", toolCtx.ToolCallID,
		)
		return toolauth.WithToolMessage(toolauth.NewPendingAuthFollowerResult(toolCtx.ToolCallID, args, credKey), toolMessage), nil
	}

	leaderActive := true
	defer func() {
		if leaderActive && toolCtx.EndAuthFlow != nil {
			toolCtx.EndAuthFlow(toolCtx.UserID, toolCtx.TraceID, credKey)
		}
	}()

	if clearErr := ah.ClearCredentialForReauth(ctx, toolCtx, rejectedAccessToken); clearErr != nil {
		t.logger.Debug("failed to clear credential before auth flow", "error", clearErr)
	}

	signal, err := ah.BuildAuthSignal(ctx, toolCtx)
	if err != nil {
		return tool.Error(fmt.Sprintf("failed to build auth signal: %v", err)), nil
	}

	if toolCtx.SendSignal == nil {
		t.logger.Warn("no SendSignal available; cannot publish authentication_required",
			"toolName", t.cfg.ToolName,
			"credentialKey", credKey,
			"toolCallID", toolCtx.ToolCallID,
		)
		return tool.AuthPromptChannelMissing(), nil
	}
	if err := toolCtx.SendSignal(signal); err != nil {
		return tool.AuthPromptUndelivered(err), nil
	}

	leaderActive = false
	return toolauth.WithToolMessage(toolauth.NewPendingAuthResult(toolCtx.ToolCallID, args, credKey), toolMessage), nil
}

var inputArtifactsRefRE = regexp.MustCompile(`INPUT_ARTIFACTS\s*\[\s*(?:"([^"]+)"|'([^']+)')\s*\]`)

func (t *Tool) autoResolveInputArtifacts(ctx context.Context, args map[string]any, toolCtx *tool.Context) {
	code, ok := args["python_code"].(string)
	if !ok || code == "" {
		return
	}
	listParam := ""
	for name, info := range t.cfg.ArtifactParamNames() {
		if info.IsList && info.FilenameKey == "" {
			if listParam != "" {
				return
			}
			listParam = name
		}
	}
	if listParam == "" {
		return
	}
	existing := make(map[string]bool)
	var current []any
	if raw, ok := args[listParam].([]any); ok {
		current = raw
		for _, item := range raw {
			if s, ok := item.(string); ok {
				fn, _ := parseArtifactSpec(s)
				existing[fn] = true
			}
		}
	}
	known, err := toolCtx.Facade().ListArtifacts(ctx)
	if err != nil {
		return
	}
	inSession := make(map[string]bool, len(known))
	for _, n := range known {
		inSession[n] = true
	}
	added := make(map[string]bool)
	for _, m := range inputArtifactsRefRE.FindAllStringSubmatch(code, -1) {
		name := m[1]
		if name == "" {
			name = m[2]
		}
		if name == "" || existing[name] || added[name] || !inSession[name] {
			continue
		}
		added[name] = true
		current = append(current, name)
		t.logger.Debug("auto-added input artifact referenced by code but omitted from input_artifacts",
			"param", listParam, "filename", name)
	}
	if len(added) > 0 {
		args[listParam] = current
	}
}

func (t *Tool) buildArtifactReferences(args map[string]any) map[string]remotetool.ArtifactReference {
	entries := t.artifactRefEntries(args)
	if len(entries) == 0 {
		return nil
	}
	refs := make(map[string]remotetool.ArtifactReference, len(entries))
	for _, e := range entries {
		filename, version := parseArtifactSpec(e.raw)
		refs[e.key] = remotetool.ArtifactReference{Filename: filename, Version: version}
	}
	return refs
}

type artifactRefEntry struct {
	key string
	raw string
}

func (t *Tool) artifactRefEntries(args map[string]any) []artifactRefEntry {
	artifactParams := t.cfg.ArtifactParamNames()
	var entries []artifactRefEntry
	for _, paramName := range slices.Sorted(maps.Keys(artifactParams)) {
		info := artifactParams[paramName]
		val, ok := args[paramName]
		if !ok {
			continue
		}

		if !info.IsList {
			if raw, isString := val.(string); isString && raw != "" {
				entries = append(entries, artifactRefEntry{key: paramName, raw: raw})
			}
			continue
		}

		list, ok := val.([]any)
		if !ok {
			continue
		}
		for i, item := range list {
			raw := listItemFilename(item, info.FilenameKey)
			if raw == "" {
				continue
			}
			entries = append(entries, artifactRefEntry{key: fmt.Sprintf("%s[%d]", paramName, i), raw: raw})
		}
	}
	return entries
}

func listItemFilename(item any, filenameKey string) string {
	if filenameKey == "" {
		s, _ := item.(string)
		return s
	}
	m, ok := item.(map[string]any)
	if !ok {
		return ""
	}
	s, _ := m[filenameKey].(string)
	return s
}

func (t *Tool) buildResult(resp *InvokeResponse) *tool.Result {
	result := resp.Result

	var r *tool.Result
	if parsed, ok := tool.UnmarshalToolResultMap(result.ToolResult); ok {
		r = parsed
		if r.Status == "" {
			r.Status = tool.StatusSuccess
		}
		if r.Status == tool.StatusError {
			r.Message = prefixRemoteError(result.ToolResult, r.Message)
		}
	} else if status, ok := result.ToolResult["status"].(string); ok && status == "error" {
		msg := prefixRemoteError(result.ToolResult, "")
		var opts []tool.ResultOption
		if errorCode, ok := result.ToolResult["error_code"].(string); ok && errorCode != "" {
			opts = append(opts, tool.WithErrorCode(errorCode))
		}
		r = tool.Error(msg, opts...)
	} else {
		msg := "success"
		if m, ok := result.ToolResult["message"].(string); ok {
			msg = m
		}
		r = tool.OK(msg, tool.WithData(result.ToolResult))
	}

	if len(result.CreatedArtifacts) > 0 {
		for _, art := range result.CreatedArtifacts {
			merged := false
			for i := range r.DataObjects {
				if r.DataObjects[i].Name != art.Filename || r.DataObjects[i].ArtifactRef != nil {
					continue
				}
				r.DataObjects[i].ArtifactRef = &tool.ArtifactRef{
					Filename:  art.Filename,
					Version:   art.Version,
					SizeBytes: art.SizeBytes,
				}
				if r.DataObjects[i].MIMEType == "" {
					r.DataObjects[i].MIMEType = art.MIMEType
				}
				merged = true
				break
			}
			if !merged {
				r.DataObjects = append(r.DataObjects, tool.DataObject{
					Name:     art.Filename,
					MIMEType: art.MIMEType,
					ArtifactRef: &tool.ArtifactRef{
						Filename:  art.Filename,
						Version:   art.Version,
						SizeBytes: art.SizeBytes,
					},
					Disposition: tool.DispositionArtifact,
				})
			}
		}
	}

	return r
}

func prefixRemoteError(payload map[string]any, fallbackMsg string) string {
	if errMsg, ok := payload["message"].(string); ok && errMsg != "" {
		return ErrorMessagePrefix + errMsg
	}
	if errMsg, ok := payload["error"].(string); ok && errMsg != "" {
		return ErrorMessagePrefix + errMsg
	}
	if fallbackMsg != "" {
		return ErrorMessagePrefix + fallbackMsg
	}
	return strings.TrimSuffix(ErrorMessagePrefix, ": ")
}

const ErrorMessagePrefix = "remote tool returned error: "

func (t *Tool) resolveArtifactContentEmbeds(ctx context.Context, args map[string]any, toolCtx *tool.Context) map[string]any {
	rawSet := make(map[string]bool, len(t.rawStringArgs))
	for _, k := range t.rawStringArgs {
		rawSet[k] = true
	}

	result := make(map[string]any, len(args))
	for k, v := range args {
		if rawSet[k] {
			result[k] = v
			continue
		}
		result[k] = resolveArtifactContentInValue(ctx, v, toolCtx)
	}
	return result
}

const embedOpen = "«"
const embedClose = "»"

func resolveArtifactContentInValue(ctx context.Context, v any, toolCtx *tool.Context) any {
	switch val := v.(type) {
	case string:
		return resolveArtifactContentInString(ctx, val, toolCtx)
	case map[string]any:
		result := make(map[string]any, len(val))
		for k, inner := range val {
			result[k] = resolveArtifactContentInValue(ctx, inner, toolCtx)
		}
		return result
	case []any:
		result := make([]any, len(val))
		for i, inner := range val {
			result[i] = resolveArtifactContentInValue(ctx, inner, toolCtx)
		}
		return result
	default:
		return v
	}
}

func resolveArtifactContentInString(ctx context.Context, s string, toolCtx *tool.Context) string {
	if !strings.Contains(s, embedOpen+"artifact_content:") {
		return s
	}

	closeLen := len(embedClose)
	var result strings.Builder
	remaining := s

	for {
		startIdx := strings.Index(remaining, embedOpen+"artifact_content:")
		if startIdx == -1 {
			result.WriteString(remaining)
			break
		}

		result.WriteString(remaining[:startIdx])

		endIdx := strings.Index(remaining[startIdx:], embedClose)
		if endIdx == -1 {
			result.WriteString(remaining[startIdx:])
			break
		}
		endIdx += startIdx + closeLen

		embedRaw := remaining[startIdx:endIdx]
		remaining = remaining[endIdx:]

		inner := embedRaw[len(embedOpen+"artifact_content:") : len(embedRaw)-closeLen]

		filename := inner
		if idx := strings.Index(filename, ">>>"); idx != -1 {
			filename = strings.TrimSpace(filename[:idx])
		}

		fn, version := parseArtifactSpec(filename)
		loc := toolCtx.ArtifactService.Resolve(toolCtx.AgentName, toolCtx.UserID, toolCtx.SessionID, fn)

		var verPtr *int
		if version >= 0 {
			verPtr = &version
		}
		content, _, err := toolCtx.ArtifactService.Inner.Load(ctx, loc, verPtr)
		if err != nil || content == nil {
			result.WriteString(embedRaw)
			continue
		}

		result.WriteString(string(content))
	}

	return result.String()
}

const latestVersionSuffix = ":-1"

func parseArtifactSpec(spec string) (filename string, version int) {
	if trimmed, ok := strings.CutSuffix(spec, latestVersionSuffix); ok && trimmed != "" {
		return trimmed, -1
	}
	name, v := artifact.SplitVersionRef(spec)
	if v == nil {
		return name, -1
	}
	return name, *v
}

func isToolProcessFailure(code remotetool.ErrorCode) bool {
	switch code {
	case remotetool.ErrToolExitNonzero,
		remotetool.ErrToolTimeout,
		remotetool.ErrToolSignaled,
		remotetool.ErrToolExecFailed,
		remotetool.ErrToolMemoryExceeded:
		return true
	default:
		return false
	}
}

func guidanceForCode(code remotetool.ErrorCode) string {
	switch code {
	case remotetool.ErrToolTimeout:
		return "The tool process exceeded its execution deadline and was killed. " +
			"This is a fixed configuration limit, not a transient outage: an identical " +
			"retry will time out again. Do NOT retry this call and do NOT tell the user " +
			"to retry. Report that the tool timed out and that an operator must raise its " +
			"timeout budget."
	case remotetool.ErrToolExitNonzero, remotetool.ErrToolSignaled:
		return "The tool process terminated abnormally. Retrying the same call will fail " +
			"the same way. Do NOT retry. Report the failure to the user."
	case remotetool.ErrToolMemoryExceeded:
		return "The tool runtime stopped this call because it was close to its memory limit " +
			"and this call used the most memory. Do NOT retry the same calls in parallel. " +
			"Report the failure to the user: if other calls ran at the same time, suggest " +
			"fewer calls at once; if this call ran alone, it needs more memory than the tool " +
			"runtime has, and a retry will fail the same way."
	case remotetool.ErrToolExecFailed:
		return "The tool could not be launched (missing or non-executable binary). This is " +
			"a deployment problem, not a transient error. Do NOT retry."
	case remotetool.ErrSandboxTimeout:
		return "The agent stopped waiting for this tool before it answered; the tool may " +
			"still be running and its result has been discarded. This is a fixed timeout " +
			"ceiling, not a transient outage, so an identical retry will very likely " +
			"expire the same way. Do NOT describe it as a temporary service problem. " +
			"Report that the tool exceeded its time budget and needs an operator to " +
			"raise it or make the tool faster."
	default:
		return ""
	}
}

func generateCorrID() string {
	return idgen.New()
}
