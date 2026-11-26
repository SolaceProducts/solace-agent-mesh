// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/discovery"
	"github.com/SolaceProducts/solace-agent-mesh/internal/health"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const (
	ExtensionURIAgentType         = "https://solace.com/a2a/extensions/agent-type"
	ExtensionURIDisplayName       = "https://solace.com/a2a/extensions/display-name"
	ExtensionURISchemas           = a2a.ExtensionURISchemas
	ExtensionURIWelcome           = "https://solace.com/a2a/extensions/sam/welcome"
	ExtensionURIInternal          = "https://solace.com/a2a/extensions/sam/internal"
	ExtensionURIPriority          = a2a.ExtensionURIPriority
	ExtensionURIDeployment        = "https://solace.com/a2a/extensions/sam/deployment"
	ExtensionURIPeerAgentTopology = "https://solace.com/a2a/extensions/peer-agent-topology"
	ExtensionURIEphemeral         = a2a.EphemeralAgentExtensionURI
	ExtensionURIToolRegistrations = "https://solace.com/a2a/extensions/sam/tool-registrations"
)

const peerToolPrefix = "peer_"

var peerToolParameters = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"task_description": map[string]any{"type": "string", "description": "The full request for this agent."},
		"artifacts": map[string]any{
			"type":        "array",
			"description": "Files this agent needs for the request.",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"filename": map[string]any{"type": "string"},
					"version":  map[string]any{"type": "string", "description": "A version number or 'latest' (the default)."},
				},
				"required": []any{"filename"},
			},
		},
	},
	"required": []any{"task_description"},
}

func isInternalCard(card a2a.AgentCard) bool {
	for _, ext := range card.Capabilities.Extensions {
		if ext.URI == ExtensionURIInternal {
			if v, ok := ext.Params["internal"].(bool); ok {
				return v
			}
		}
	}
	return false
}

func displayNameFromCard(card a2a.AgentCard) string {
	for _, ext := range card.Capabilities.Extensions {
		if ext.URI == ExtensionURIDisplayName {
			if name, ok := ext.Params["display_name"].(string); ok {
				return name
			}
		}
	}
	return ""
}

func peerMatchNames(card a2a.AgentCard) []string {
	names := []string{card.Name}
	if dn := displayNameFromCard(card); dn != "" && dn != card.Name {
		names = append(names, dn)
	}
	return names
}

func agentCardExtensions(card a2a.AgentCard) (agentType string, inputSchema map[string]any) {
	agentType = "standard"
	for _, ext := range card.Capabilities.Extensions {
		switch ext.URI {
		case ExtensionURIAgentType:
			if t, ok := ext.Params["type"].(string); ok {
				agentType = t
			}
		case ExtensionURISchemas:
			if s, ok := ext.Params["input_schema"].(map[string]any); ok {
				inputSchema = s
			}
		}
	}
	return
}

type peerView struct {
	mu sync.Mutex

	dir     *discovery.Directory
	ownsDir bool

	cards      map[string]*a2a.AgentCard
	dirVersion uint64
	syncedAt   time.Time
	synced     bool

	expired []string
	dirSeen uint64

	now func() time.Time

	version atomic.Uint64
}

func (a *Agent) SetPeerDirectory(d *discovery.Directory) {
	a.peers.mu.Lock()
	defer a.peers.mu.Unlock()
	a.peers.dir = d
	a.peers.ownsDir = false
}

func (a *Agent) discoveryEnabled() bool {
	return a.cfg.DiscoveryEnabled == nil || *a.cfg.DiscoveryEnabled
}

func (a *Agent) peerDirectoryLocked() *discovery.Directory {
	if a.peers.dir != nil && (a.peers.ownsDir || a.discoveryEnabled()) {
		return a.peers.dir
	}
	var brk broker.Broker
	if a.svc != nil {
		brk = a.svc.Broker()
	}
	logger := a.logger
	if logger == nil {
		logger = slog.Default()
	}
	a.peers.dir = discovery.NewDirectory(brk, a.cfg.Namespace, logger)
	a.peers.ownsDir = true
	return a.peers.dir
}

func (a *Agent) peerDirectory() *discovery.Directory {
	a.peers.mu.Lock()
	defer a.peers.mu.Unlock()
	return a.peerDirectoryLocked()
}

func (a *Agent) peerTTL() time.Duration {
	ttl := a.cfg.HealthCheckTTLSeconds
	if ttl <= 0 {
		ttl = defaultHealthCheckTTLSeconds
	}
	return time.Duration(ttl) * time.Second
}

func (a *Agent) peerRecheckInterval() time.Duration {
	interval := a.cfg.HealthCheckIntervalSeconds
	if interval <= 0 {
		interval = defaultHealthCheckIntervalSeconds
	}
	return time.Duration(interval) * time.Second
}

func (a *Agent) syncPeers() {
	a.peers.mu.Lock()
	defer a.peers.mu.Unlock()
	dir := a.peerDirectoryLocked()
	registry := dir.Registry()
	now := time.Now()
	if a.peers.now != nil {
		now = a.peers.now()
	}
	ttl := a.peerTTL()
	cutoff := now.Add(-ttl)
	dirVersion := registry.Version()
	dirSeen := registry.Seen()
	if a.peers.synced && dirVersion == a.peers.dirVersion &&
		now.Sub(a.peers.syncedAt) < a.peerRecheckInterval() &&
		!a.expiredPeerReturnedLocked(registry, dirSeen, cutoff) {
		return
	}
	visible := make(map[string]*a2a.AgentCard)
	registered := make(map[string]bool)
	var expired []string
	registry.EachSince(cutoff, func(card *a2a.AgentCard, fresh bool) {
		registered[card.Name] = true
		if !a.acceptsPeer(*card) {
			return
		}
		if !fresh {
			expired = append(expired, card.Name)
			return
		}
		visible[card.Name] = card
	})
	if a.applyPeerToolsLocked(visible, func(name string) []any {
		switch {
		case !registered[name]:
			return []any{"reason", "removed"}
		case slices.Contains(expired, name):
			return []any{"reason", "expired", "ttl_s", int(ttl.Seconds()),
				"discoveryDegraded", len(dir.HealthSnapshot()) > 0}
		default:
			return []any{"reason", "filtered"}
		}
	}) {
		a.peers.version.Add(1)
	}
	a.peers.cards = visible
	a.peers.dirVersion = dirVersion
	a.peers.dirSeen = dirSeen
	a.peers.expired = expired
	a.peers.syncedAt = now
	a.peers.synced = true
}

func (a *Agent) expiredPeerReturnedLocked(registry *discovery.Registry, dirSeen uint64, cutoff time.Time) bool {
	if len(a.peers.expired) == 0 || dirSeen == a.peers.dirSeen {
		return false
	}
	a.peers.dirSeen = dirSeen
	for _, name := range a.peers.expired {
		if registry.LookupSince(name, cutoff) != nil {
			return true
		}
	}
	return false
}

func (a *Agent) applyPeerToolsLocked(visible map[string]*a2a.AgentCard, whyGone func(name string) []any) bool {
	logger := a.logger
	if logger == nil {
		logger = slog.Default()
	}
	changed := false
	for name, card := range a.peers.cards {
		if _, ok := visible[name]; !ok {
			a.tools.Unregister(peerToolName(*card))
			logger.Info("peer removed", append([]any{"agentName", name}, whyGone(name)...)...)
			changed = true
		}
	}
	for name, card := range visible {
		prev := a.peers.cards[name]
		if prev == card {
			continue
		}
		changed = true
		peerTool := a.newPeerTool(*card)
		switch {
		case prev == nil:
			logger.Debug("discovered peer", "agentName", name)
			if err := a.tools.Register(peerTool); err != nil {
				logger.Warn("failed to register peer tool", "agentName", name, "error", err)
			}
		case peerToolName(*prev) != peerTool.Name():
			logger.Debug("updated peer agent", "agentName", name)
			a.tools.Unregister(peerToolName(*prev))
			if err := a.tools.Register(peerTool); err != nil {
				logger.Warn("failed to register peer tool", "agentName", name, "error", err)
			}
		default:
			logger.Debug("updated peer agent", "agentName", name)
			a.tools.Replace(peerTool)
		}
	}
	return changed
}

func (a *Agent) acceptsPeer(card a2a.AgentCard) bool {
	if card.Name == a.cfg.Name {
		return false
	}
	if isInternalCard(card) {
		return false
	}
	if a2a.IsEphemeralCard(card) {
		return false
	}
	return a.isPeerAllowed(card)
}

func (a *Agent) peerVersion() uint64 {
	a.syncPeers()
	return a.peers.version.Load()
}

func (a *Agent) peerCards() []a2a.AgentCard {
	a.syncPeers()
	a.peers.mu.Lock()
	defer a.peers.mu.Unlock()
	names := make([]string, 0, len(a.peers.cards))
	for name := range a.peers.cards {
		names = append(names, name)
	}
	slices.Sort(names)
	cards := make([]a2a.AgentCard, 0, len(names))
	for _, name := range names {
		cards = append(cards, *a.peers.cards[name])
	}
	return cards
}

func (a *Agent) peerNames() []string {
	a.syncPeers()
	a.peers.mu.Lock()
	defer a.peers.mu.Unlock()
	names := make([]string, 0, len(a.peers.cards))
	for name := range a.peers.cards {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func (a *Agent) peerDiscoveryProblems() []health.Problem {
	if !a.discoveryEnabled() {
		return nil
	}
	return a.peerDirectory().HealthSnapshot()
}

func (a *Agent) startPeerDiscovery() {
	if !a.discoveryEnabled() {
		a.logger.Info("agent discovery disabled by config")
		return
	}
	if err := a.peerDirectory().Start(); err != nil {
		a.logger.Warn("discovery subscription failed, retrying in background", "error", err)
	}
	a.peers.mu.Lock()
	a.peers.synced = false
	a.peers.mu.Unlock()
	a.syncPeers()
}

func (a *Agent) stopPeerDiscovery() {
	a.peers.mu.Lock()
	dir, owns := a.peers.dir, a.peers.ownsDir
	a.peers.mu.Unlock()
	if dir == nil || !owns {
		return
	}
	if err := dir.Close(); err != nil {
		a.logger.Warn("terminate discovery subscription failed", "error", err)
	}
}

type peerAgentTool struct {
	tool.SideEffectingTool
	peerName  string
	card      a2a.AgentCard
	agent     *Agent
	namespace string
}

func (t *peerAgentTool) Name() string { return "peer_" + t.peerName }

func (t *peerAgentTool) Description() string {
	name := displayNameFromCard(t.card)
	if name == "" {
		name = t.peerName
	}
	desc := fmt.Sprintf(a2a.PeerToolLeadFormat, name)
	if t.card.Description != "" {
		desc += " " + t.card.Description
	}
	if skills := peerSkillNames(t.card); len(skills) > 0 {
		desc += " " + a2a.ToolSkillsLabel + " " + strings.Join(skills, ", ")
	}
	return desc
}

const skillsExtensionURI = "https://solace.com/a2a/extensions/sam/skills"

const builtinSkillPrefix = "sam-"

func peerSkillNames(card a2a.AgentCard) []string {
	names := make([]string, 0, len(card.Skills))
	for _, s := range card.Skills {
		names = append(names, s.Name)
	}
	for _, ext := range card.Capabilities.Extensions {
		if ext.URI != skillsExtensionURI {
			continue
		}
		list, _ := ext.Params["skills"].([]any)
		for _, item := range list {
			entry, _ := item.(map[string]any)
			name, _ := entry["name"].(string)
			if name != "" && !strings.HasPrefix(name, builtinSkillPrefix) && !slices.Contains(names, name) {
				names = append(names, name)
			}
		}
	}
	return names
}

func (t *peerAgentTool) Parameters() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type: "object",
		Properties: map[string]any{
			"task_description": map[string]any{
				"type":        "string",
				"description": "Detailed description of the task for the peer agent.",
			},
			"artifacts": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"filename": map[string]any{
							"type":        "string",
							"description": "The filename of the artifact.",
						},
						"version": map[string]any{
							"type":        "string",
							"description": "The version of the artifact (e.g., 'latest' or a number). Defaults to 'latest'.",
						},
					},
					"required": []string{"filename"},
				},
				"description": "A list of artifacts to provide as context to the peer agent.",
			},
		},
		Required: []string{"task_description"},
	}
}

func (t *peerAgentTool) Instructions() string { return "" }

func (t *peerAgentTool) IsLongRunning() bool { return true }

func (t *peerAgentTool) RawStringArgs() []string { return nil }

func (t *peerAgentTool) ArtifactParams() map[string]tool.ArtifactParamInfo { return nil }

func (t *peerAgentTool) ContextParam() string { return "" }

func runBasedDelegationMetadata(toolCtx *tool.Context, agentName string) map[string]any {
	metadata := map[string]any{
		"sessionBehavior":  "RUN_BASED",
		"parentTaskId":     toolCtx.ParentTaskID,
		"function_call_id": toolCtx.ToolCallID,
		"agent_name":       agentName,
	}
	if toolCtx.DisplaysTemplateSignals {
		metadata[a2a.MetadataDisplaysTemplateSignals] = true
	}
	return metadata
}

func PeerContextMessage(callerName, taskDesc string) string {
	return "Request from " + callerName + ". " + PeerTaskSentinel + "\n\n" + taskDesc
}

func PeerRequest(callerDisplayName, peerName, subTaskID, taskDesc string, toolCtx *tool.Context, extraMetadata map[string]any) a2a.Request {
	contextID := toolCtx.SessionID
	if contextID == "" {
		contextID = subTaskID
	}
	metadata := runBasedDelegationMetadata(toolCtx, peerName)
	maps.Copy(metadata, extraMetadata)
	msg := a2a.Message{
		Kind:      "message",
		MessageID: fmt.Sprintf("peer-%s-%d", peerName, time.Now().UnixNano()),
		ContextID: contextID,
		Role:      a2a.RoleUser,
		Parts:     []a2a.Part{a2a.NewTextPart(PeerContextMessage(callerDisplayName, taskDesc))},
		Metadata:  metadata,
	}
	return a2a.Request{
		JSONRPC: "2.0",
		Method:  "message/send",
		ID:      subTaskID,
		Params: map[string]any{
			"message": msg,
		},
	}
}

func PeerRequestProperties(namespace, callerName, subTaskID string, toolCtx *tool.Context) map[string]string {
	peerUserProps := map[string]string{
		"replyTo":        a2a.PeerResponseTopic(namespace, callerName, subTaskID),
		"userId":         toolCtx.UserID,
		"a2aStatusTopic": a2a.PeerStatusTopic(namespace, callerName, subTaskID),
	}
	if toolCtx.A2AUserConfig != "" {
		peerUserProps["a2aUserConfig"] = toolCtx.A2AUserConfig
	}
	if toolCtx.Identity != nil && toolCtx.Identity.AuthToken != "" {
		peerUserProps["authToken"] = toolCtx.Identity.AuthToken
	}
	peerUserProps["delegating_agent_name"] = callerName
	peerUserProps["callDepth"] = strconv.Itoa(toolCtx.CallDepth + 1)
	a2a.AddTraceID(peerUserProps, toolCtx.TraceID)
	a2a.AddTrigger(peerUserProps, toolCtx.Trigger)
	a2a.AddRoot(peerUserProps, toolCtx.RootKind, toolCtx.RootID, toolCtx.RootNodeID)
	a2a.AddOrigin(peerUserProps, toolCtx.Origin)
	return peerUserProps
}

const PeerTaskSentinel = "Answer from your own tools and say what you could not find."

func (t *peerAgentTool) Execute(ctx context.Context, args map[string]any, toolCtx *tool.Context) (*tool.Result, error) {
	taskDesc, _ := args["task_description"].(string)
	if taskDesc == "" {
		return tool.Error("task_description is required"), nil
	}

	if authzErr := t.agent.svc.AgentAuthorizer().AuthorizeAgent(ctx, toolCtx.Identity, t.peerName); authzErr != nil {
		return tool.Error(fmt.Sprintf("agent delegation denied: %v", authzErr)), nil
	}

	reqID := idgen.New()

	extraMetadata := map[string]any{}
	if rawArtifacts, ok := args["artifacts"].([]any); ok && len(rawArtifacts) > 0 {
		extraMetadata["invoked_with_artifacts"] = rawArtifacts
	}

	if vids := t.agent.resolvedVolumeIDsForSession(toolCtx.SessionID); len(vids) > 0 {
		extraMetadata["volume_context"] = vids
	}

	a2aReq := PeerRequest(agentDisplayName(&t.agent.cfg), t.peerName, reqID, taskDesc, toolCtx, extraMetadata)

	reqBytes, err := json.Marshal(a2aReq)
	if err != nil {
		return tool.Error("failed to marshal peer request: " + err.Error()), nil
	}

	if t.agent.cfg.MaxMessageSizeBytes > 0 && len(reqBytes) > t.agent.cfg.MaxMessageSizeBytes {
		return tool.Error(fmt.Sprintf(
			"Message size %d bytes exceeds maximum limit of %d bytes",
			len(reqBytes), t.agent.cfg.MaxMessageSizeBytes,
		)), nil
	}

	peerTopic := a2a.AgentRequestTopic(t.namespace, t.peerName)
	replyTo := a2a.PeerResponseTopic(t.namespace, t.agent.cfg.Name, reqID)

	peerTimeout := resolveDelegationTimeout(t.agent.cfg.PeerRequestTimeoutSeconds, config.DefaultPeerRequestTimeoutSecs)

	var delegationAuthToken string
	if toolCtx.Identity != nil {
		delegationAuthToken = toolCtx.Identity.AuthToken
	}

	completeCh := t.agent.peerRouter.Register(
		reqID,
		toolCtx.A2AStatusTopic,
		toolCtx.ParentTaskID,
		t.agent.cfg.Name,
		t.peerName,
		toolCtx.ToolCallID,
		toolCtx.ClientID,
		toolCtx.UserID,
		toolCtx.A2AUserConfig,
		peerTimeout,
		WithTraceID(toolCtx.TraceID),
		WithAuthToken(delegationAuthToken),
	)

	peerUserProps := PeerRequestProperties(t.namespace, t.agent.cfg.Name, reqID, toolCtx)

	if pubErr := t.agent.svc.Broker().PublishGuaranteed(ctx, peerTopic, &broker.Message{
		Payload:        reqBytes,
		ReplyTo:        replyTo,
		UserProperties: peerUserProps,
		TTL:            peerTimeout,
	}); pubErr != nil {
		t.agent.peerRouter.Cancel(reqID)
		return tool.Error(fmt.Sprintf("peer agent %q publish failed: %v", t.peerName, pubErr)), nil
	}

	if toolCtx.PauseResume {
		peerName := t.peerName
		result := tool.Pending(fmt.Sprintf("Waiting for peer agent %q", peerName), tool.WithData(map[string]any{
			"sub_task_id": reqID,
			"peer_name":   peerName,
		}))
		result.OnDelivery = func(deliver func(*tool.Result)) {
			go func() {
				respMsg := <-completeCh
				if respMsg == nil {
					deliver(tool.Error(fmt.Sprintf("peer agent %q request timed out or was cancelled", peerName)))
					return
				}
				parsed, _ := t.parseResponse(respMsg)
				deliver(parsed)
			}()
		}
		return result, nil
	}

	select {
	case respMsg := <-completeCh:
		if respMsg == nil {
			return tool.Error(fmt.Sprintf("peer agent %q request timed out", t.peerName)), nil
		}
		return t.parseResponse(respMsg)
	case <-ctx.Done():
		t.agent.peerRouter.CancelWithPropagation(reqID, t.namespace)
		return tool.Error("peer request cancelled"), nil
	}
}

func (t *peerAgentTool) parseResponse(respMsg *broker.Message) (*tool.Result, error) {
	return parsePeerResponse(t.peerName, respMsg)
}

func delegatedTaskUnsuccessful(task map[string]any) bool {
	status, _ := task["status"].(map[string]any)
	state, _ := status["state"].(string)
	switch a2a.TaskState(state) {
	case a2a.TaskStateFailed, a2a.TaskStateCancelled, a2a.TaskStateRejected:
		return true
	}
	return false
}

func delegatedTaskError(message string, opts ...tool.ResultOption) *tool.Result {
	return tool.Error(message, append(opts, tool.WithErrorCode(tool.ErrorCodeDelegatedTaskFailed))...)
}

func DelegatedTaskReply(task map[string]any) (text, structuredErr string) {
	status, _ := task["status"].(map[string]any)
	msg, _ := status["message"].(map[string]any)
	parts, _ := msg["parts"].([]any)
	for _, p := range parts {
		part, _ := p.(map[string]any)
		switch part["kind"] {
		case "text":
			t, _ := part["text"].(string)
			text += t
		case "data":
			if data, _ := part["data"].(map[string]any); data["type"] == "structured_invocation_result" {
				if errMsg, ok := data["error_message"].(string); ok {
					structuredErr = errMsg
				}
			}
		}
	}
	return text, structuredErr
}

func llmResultData(r *tool.Result) map[string]any {
	if !r.DelegatedTask {
		return r.Data
	}
	task := r.Data
	status, _ := task["status"].(map[string]any)
	view := map[string]any{}
	if state, ok := status["state"].(string); ok {
		view["task_state"] = state
	}
	msg, _ := status["message"].(map[string]any)
	parts, _ := msg["parts"].([]any)
	var replyParts []any
	for _, p := range parts {
		if part, _ := p.(map[string]any); part["kind"] != "text" {
			replyParts = append(replyParts, withoutFileBytes(p))
		}
	}
	if len(replyParts) > 0 {
		view["reply_parts"] = replyParts
	}
	if artifacts, _ := task["artifacts"].([]any); len(artifacts) > 0 {
		view["artifacts"] = artifactsWithoutFileBytes(artifacts)
	}
	metadata, _ := task["metadata"].(map[string]any)
	if produced, ok := metadata["produced_artifacts"]; ok {
		view["produced_artifacts"] = produced
	}
	if text, structuredErr := DelegatedTaskReply(task); delegatedTaskUnsuccessful(task) &&
		text == "" && structuredErr == "" && len(replyParts) == 0 && len(metadata) > 0 {
		reason := maps.Clone(metadata)
		delete(reason, "token_usage")
		view["metadata"] = reason
	}
	return view
}

func artifactsWithoutFileBytes(artifacts []any) []any {
	out := make([]any, 0, len(artifacts))
	for _, a := range artifacts {
		artifact, ok := a.(map[string]any)
		if !ok {
			out = append(out, a)
			continue
		}
		artifact = maps.Clone(artifact)
		if parts, ok := artifact["parts"].([]any); ok {
			kept := make([]any, 0, len(parts))
			for _, p := range parts {
				kept = append(kept, withoutFileBytes(p))
			}
			artifact["parts"] = kept
		}
		out = append(out, artifact)
	}
	return out
}

func withoutFileBytes(p any) any {
	part, ok := p.(map[string]any)
	if !ok || part["kind"] != "file" {
		return p
	}
	file, ok := part["file"].(map[string]any)
	if !ok {
		return p
	}
	encoded, _ := file["bytes"].(string)
	if uri, _ := file["uri"].(string); strings.HasPrefix(uri, "data:") {
		encoded += uri
	}
	if encoded == "" {
		return p
	}
	file = maps.Clone(file)
	delete(file, "bytes")
	if uri, _ := file["uri"].(string); strings.HasPrefix(uri, "data:") {
		delete(file, "uri")
	}
	file["bytes_omitted"] = fmt.Sprintf(fileBytesOmittedNote, len(encoded))
	part = maps.Clone(part)
	part["file"] = file
	return part
}

const fileBytesOmittedNote = "%d characters of inline file content are not shown here"

func parsePeerResponse(peerName string, respMsg *broker.Message) (*tool.Result, error) {
	var a2aResp a2a.Response
	if jsonErr := json.Unmarshal(respMsg.Payload, &a2aResp); jsonErr != nil {
		return tool.Error("failed to parse peer response: " + jsonErr.Error()), nil
	}

	if a2aResp.Error != nil {
		return tool.Error(fmt.Sprintf("peer agent %q: %s (code %d)", peerName, a2aResp.Error.Message, a2aResp.Error.Code)), nil
	}

	if task, ok := a2aResp.Result.(map[string]any); ok {
		text, structuredErr := DelegatedTaskReply(task)
		detail := cmp.Or(text, structuredErr)
		if delegatedTaskUnsuccessful(task) {
			return delegatedTaskError(fmt.Sprintf("peer agent %q did not succeed: %s", peerName, cmp.Or(detail, "no text content")), tool.WithDelegatedTask(task)), nil
		}
		return tool.OK(cmp.Or(detail, "Peer agent responded (no text content)"), tool.WithDelegatedTask(task)), nil
	}

	return tool.OK("Peer agent responded (no text content)"), nil
}

func (a *Agent) InjectPeerCard(card a2a.AgentCard) {
	data, err := json.Marshal(card)
	if err != nil {
		a.logger.Warn("failed to marshal peer card for injection", "error", err)
		return
	}
	a.handleDiscoveryMessage(&broker.Message{Payload: data})
}

func (a *Agent) InjectPeerRemoval(card a2a.AgentCard) {
	data, err := json.Marshal(card)
	if err != nil {
		a.logger.Warn("failed to marshal peer card for removal injection", "error", err)
		return
	}
	a.handleDiscoveryMessage(&broker.Message{
		Payload:        data,
		UserProperties: map[string]string{"removed": "true"},
	})
}

func peerToolName(card a2a.AgentCard) string {
	agentType, _ := agentCardExtensions(card)
	if agentType == "workflow" {
		return a2a.WorkflowToolName(card.Name)
	}
	return "peer_" + card.Name
}

func (a *Agent) handleDiscoveryMessage(msg *broker.Message) {
	a.peerDirectory().Ingest(msg)
	a.peers.mu.Lock()
	a.peers.synced = false
	a.peers.mu.Unlock()
	a.syncPeers()
}

func (a *Agent) newPeerTool(card a2a.AgentCard) tool.BuiltinTool {
	agentType, inputSchema := agentCardExtensions(card)
	if agentType != "workflow" {
		return &peerAgentTool{
			peerName:  card.Name,
			card:      card,
			agent:     a,
			namespace: a.cfg.Namespace,
		}
	}
	if inputSchema == nil {
		inputSchema = map[string]any{
			"type": "object",
			"properties": map[string]any{
				"text": map[string]any{"type": "string"},
			},
			"required": []any{"text"},
		}
	}
	return &workflowAgentTool{
		peerName:    card.Name,
		card:        card,
		agent:       a,
		namespace:   a.cfg.Namespace,
		inputSchema: inputSchema,
	}
}

func (a *Agent) isPeerAllowed(card a2a.AgentCard) bool {
	allowList := a.cfg.PeerAllowList
	denyList := a.cfg.PeerDenyList

	if allowList == nil {
		allowList = []string{"*"}
	}

	candidates := peerMatchNames(card)

	allowed := false
	for _, pattern := range allowList {
		for _, name := range candidates {
			if matched, _ := filepath.Match(pattern, name); matched {
				allowed = true
				break
			}
		}
		if allowed {
			break
		}
	}

	if !allowed {
		return false
	}

	for _, pattern := range denyList {
		for _, name := range candidates {
			if matched, _ := filepath.Match(pattern, name); matched {
				return false
			}
		}
	}

	return true
}

const (
	defaultHealthCheckIntervalSeconds = 60
	defaultHealthCheckTTLSeconds      = 300
)
