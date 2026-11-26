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
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type subTaskTool struct {
	tool.SideEffectingTool
	agent *Agent
}

const subTaskToolName = "sub_task"

func (t *subTaskTool) Name() string { return subTaskToolName }

func (t *subTaskTool) Description() string {
	return "Run a substantial piece of work in its own conversation and get back only its final " +
		"response and the artifacts it produced. Each sub-task is a fresh agent loop that re-pays the " +
		"system prompt and tool definitions before doing anything, so it pays off for several " +
		"independent pieces: call sub_task multiple times in one response and they run in parallel, " +
		"each producing an artifact or a long result you do not need to follow step by step. " +
		"Do a single step, a short lookup, or work whose result you need before continuing yourself, " +
		"and never delegate the whole task. Use 'fork' when the sub-task needs this conversation and " +
		"'fresh' for independent work. A 'fresh' sub-task cannot see this conversation, so its " +
		"instruction must carry everything it needs and say what to hand back, or it will rediscover " +
		"what you already know. 'skills' pre-loads skills chosen from those available to you: with " +
		"'fresh' they are the sub-task's whole skill set, with 'fork' they are added to the skills you " +
		"already have loaded."
}

func (t *subTaskTool) Parameters() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type: "object",
		Properties: map[string]any{
			"instruction": map[string]any{
				"type":        "string",
				"description": "Detailed instruction for what the sub-task should accomplish. Be specific about the expected output.",
			},
			"type": map[string]any{
				"type":        "string",
				"enum":        []string{"fork", "fresh"},
				"description": "Sub-task type: 'fork' inherits the current conversation history, 'fresh' starts with no history.",
			},
			"skills": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "string",
				},
				"description": "Optional list of skill names to pre-load for the sub-task.",
			},
		},
		Required: []string{"instruction", "type"},
	}
}

func (t *subTaskTool) Instructions() string { return "" }

func (t *subTaskTool) IsLongRunning() bool { return true }

func (t *subTaskTool) RawStringArgs() []string { return nil }

func (t *subTaskTool) ArtifactParams() map[string]tool.ArtifactParamInfo { return nil }

func (t *subTaskTool) ContextParam() string { return "" }

func subTaskContextMessage(callerName, depthLine, instruction string) string {
	return fmt.Sprintf(
		"You are running as a sub-task of agent '%s' — a fresh instance of that SAME agent. %s\n\n"+
			"You have exactly the same tools and the same set of available skills as the parent, and nothing "+
			"more: you cannot do anything the parent could not do itself (you may have different skills "+
			"currently loaded, but only ones already available to the agent). If this task needs a capability "+
			"none of your tools provide, do NOT spawn another sub-task hoping it can — say plainly that it "+
			"cannot be done and why.\n\n"+
			"YOU are the one who must do this work. Perform the task yourself with your own tools. Do NOT "+
			"create a sub_task that merely restates this task — that just hands the whole job to another "+
			"identical copy of you, does no work, and is a delegation loop. Only spawn a sub_task for a "+
			"genuinely smaller, independent piece of the work, never for the task as a whole.\n\n"+
			"Complete the task below and return a clear, complete answer. The parent agent will only see your "+
			"final response text and any artifacts you create — not your intermediate steps or conversation "+
			"history.\n\n"+
			"If the task requires information you don't have, state what's missing rather than guessing.\n\n"+
			"Task:\n\n%s",
		callerName, depthLine, instruction,
	)
}

func (t *subTaskTool) Execute(ctx context.Context, args map[string]any, toolCtx *tool.Context) (*tool.Result, error) {
	taskLogger := t.agent.logger.With(
		"taskID", toolCtx.ParentTaskID, "traceID", toolCtx.TraceID, "userID", toolCtx.UserID)

	instruction, _ := args["instruction"].(string)
	if instruction == "" {
		return tool.Error("instruction is required"), nil
	}

	taskType, _ := args["type"].(string)
	if taskType != "fork" && taskType != "fresh" {
		return tool.Error("type must be 'fork' or 'fresh'"), nil
	}

	if parentTask := coreTaskInstruction(toolCtx.IncomingInstruction); instructionsNearlyIdentical(parentTask, instruction) {
		taskLogger.Warn("sub-task refused: instruction near-identical to the current task",
			"callDepth", toolCtx.CallDepth)
		return tool.Error("This sub-task instruction is essentially the same as the task you are " +
			"already working on. Spawning it would re-delegate the whole job to another copy of yourself " +
			"and do no actual work. Do this task yourself, now, with your own tools. Only use sub_task to " +
			"break off a genuinely smaller, independent piece of the work."), nil
	}

	childDepth := toolCtx.CallDepth + 1
	if t.agent.cfg.MaxCallDepth > 0 && childDepth > t.agent.cfg.MaxCallDepth {
		taskLogger.Warn("sub-task refused: would exceed max call depth",
			"childDepth", childDepth, "maxCallDepth", t.agent.cfg.MaxCallDepth)
		return tool.Error(fmt.Sprintf(taxonomy.SubTaskDepthExceededFormat, t.agent.cfg.MaxCallDepth)), nil
	}

	if toolCtx.MaxSubTaskFanout > 0 && toolCtx.SubTaskCount != nil &&
		toolCtx.SubTaskCount.Add(1) > int64(toolCtx.MaxSubTaskFanout) {
		taskLogger.Warn("sub-task refused: fan-out limit reached for this task",
			"maxSubTaskFanout", toolCtx.MaxSubTaskFanout, "callDepth", toolCtx.CallDepth)
		return tool.Error(fmt.Sprintf("This task has reached its sub-task fan-out limit (%d). Do the "+
			"remaining work yourself with your own tools rather than spawning more sub-tasks.",
			toolCtx.MaxSubTaskFanout)), nil
	}

	var skills []string
	if rawSkills, ok := args["skills"].([]any); ok {
		for _, s := range rawSkills {
			if name, ok := s.(string); ok && name != "" {
				skills = append(skills, name)
			}
		}
	}

	reqID := idgen.New()

	contextID := toolCtx.SessionID
	if contextID == "" {
		contextID = reqID
	}

	effectiveSessionID := idgen.New()

	if taskType == "fork" {
		if err := t.prepareForkedSession(ctx, toolCtx, effectiveSessionID, contextID, reqID, skills); err != nil {
			return tool.Error(fmt.Sprintf("failed to prepare forked session: %v", err)), nil
		}
	} else if len(skills) > 0 {
		if err := t.prepareFreshSessionWithSkills(ctx, toolCtx, effectiveSessionID, contextID, reqID, skills); err != nil {
			return tool.Error(fmt.Sprintf("failed to prepare session: %v", err)), nil
		}
	}

	depthLine := fmt.Sprintf("You are at sub-task depth %d", childDepth)
	if t.agent.cfg.MaxCallDepth > 0 {
		depthLine += fmt.Sprintf(" of a hard maximum of %d — any sub-task you spawn is deeper still, "+
			"and once the limit is reached further sub-tasks are refused outright", t.agent.cfg.MaxCallDepth)
	}
	depthLine += "."

	contextMessage := subTaskContextMessage(agentDisplayName(&t.agent.cfg), depthLine, instruction)

	metadata := runBasedDelegationMetadata(toolCtx, t.agent.cfg.Name)

	if vids := t.agent.resolvedVolumeIDsForSession(toolCtx.SessionID); len(vids) > 0 {
		metadata["volume_context"] = vids
	}

	metadata["parentSessionID"] = toolCtx.SessionID

	msg := a2a.Message{
		Kind:      "message",
		MessageID: fmt.Sprintf("subtask-%s-%s", t.agent.cfg.Name, idgen.New()),
		ContextID: contextID,
		Role:      a2a.RoleUser,
		Parts:     []a2a.Part{a2a.NewTextPart(contextMessage)},
		Metadata:  metadata,
	}

	a2aReq := a2a.Request{
		JSONRPC: "2.0",
		Method:  "message/send",
		ID:      reqID,
		Params: map[string]any{
			"message": msg,
		},
	}

	reqBytes, err := json.Marshal(a2aReq)
	if err != nil {
		return tool.Error("failed to marshal sub-task request: " + err.Error()), nil
	}

	selfTopic := a2a.AgentRequestTopic(t.agent.cfg.Namespace, t.agent.cfg.Name)
	replyTo := a2a.PeerResponseTopic(t.agent.cfg.Namespace, t.agent.cfg.Name, reqID)

	isBuilder := isBuilderAgent(t.agent.cfg.Name)

	var registerOpts []RegisterOption
	if isBuilder {
		registerOpts = append(registerOpts, WithForwardArtifactProgress())
	}

	subTaskTimeout := resolveDelegationTimeout(
		t.agent.cfg.SubTaskTimeoutSeconds, config.DefaultSubTaskTimeoutSecs)

	registerOpts = append(registerOpts, WithTraceID(toolCtx.TraceID))
	completeCh := t.agent.peerRouter.Register(
		reqID,
		toolCtx.A2AStatusTopic,
		toolCtx.ParentTaskID,
		t.agent.cfg.Name,
		t.agent.cfg.Name,
		toolCtx.ToolCallID,
		toolCtx.ClientID,
		toolCtx.UserID,
		toolCtx.A2AUserConfig,
		subTaskTimeout,
		registerOpts...,
	)

	var compInfo *builderComponentInfo
	if isBuilder && toolCtx.SendSignal != nil {
		compInfo = extractComponentInfo(instruction)
		if compInfo != nil {
			_ = toolCtx.SendSignal(a2a.BuilderComponentProgressData{
				Type:          string(a2a.SignalBuilderComponentProgress),
				ComponentID:   compInfo.id,
				ComponentType: compInfo.typ,
				ComponentName: compInfo.name,
				State:         "active",
				StatusText:    fmt.Sprintf("Generating %s configuration...", compInfo.name),
			})
		}
	}

	subTaskStatusTopic := a2a.PeerStatusTopic(t.agent.cfg.Namespace, t.agent.cfg.Name, reqID)

	userProps := map[string]string{
		"replyTo": replyTo,
		"userId":  toolCtx.UserID,
	}
	if subTaskStatusTopic != "" {
		userProps["a2aStatusTopic"] = subTaskStatusTopic
	}
	if toolCtx.A2AUserConfig != "" {
		userProps["a2aUserConfig"] = toolCtx.A2AUserConfig
	}
	if toolCtx.Identity != nil && toolCtx.Identity.AuthToken != "" {
		userProps["authToken"] = toolCtx.Identity.AuthToken
	}
	userProps["delegating_agent_name"] = t.agent.cfg.Name
	userProps["callDepth"] = strconv.Itoa(toolCtx.CallDepth + 1)
	a2a.AddTraceID(userProps, toolCtx.TraceID)
	a2a.AddTrigger(userProps, toolCtx.Trigger)
	a2a.AddRoot(userProps, toolCtx.RootKind, toolCtx.RootID, toolCtx.RootNodeID)
	a2a.AddOrigin(userProps, toolCtx.Origin)

	if pubErr := t.agent.svc.Broker().PublishGuaranteed(ctx, selfTopic, &broker.Message{
		Payload:        reqBytes,
		ReplyTo:        replyTo,
		UserProperties: userProps,
	}); pubErr != nil {
		t.agent.peerRouter.Cancel(reqID)
		return tool.Error(fmt.Sprintf("sub-task publish failed: %v", pubErr)), nil
	}

	taskLogger.Info("sub-task published as self peer call",
		"reqID", reqID, "type", taskType, "skills", skills)

	result := tool.Pending(fmt.Sprintf("Running sub-task (%s mode)", taskType), tool.WithData(map[string]any{
		"sub_task_id": reqID,
		"peer_name":   t.agent.cfg.Name,
	}))
	result.OnDelivery = func(deliver func(*tool.Result)) {
		go func() {
			respMsg := <-completeCh
			if respMsg == nil {
				if compInfo != nil && toolCtx.SendSignal != nil {
					_ = toolCtx.SendSignal(a2a.BuilderComponentProgressData{
						Type:          string(a2a.SignalBuilderComponentProgress),
						ComponentID:   compInfo.id,
						ComponentType: compInfo.typ,
						ComponentName: compInfo.name,
						State:         "failed",
						StatusText:    "Sub-task timed out or was cancelled",
					})
				}
				deliver(tool.Error("sub-task timed out or was cancelled"))
				return
			}
			parsed, _ := t.parseResponse(respMsg)

			if compInfo != nil && toolCtx.SendSignal != nil {
				state := "completed"
				statusText := fmt.Sprintf("%s configuration generated", compInfo.name)
				if parsed != nil && parsed.Status == "error" {
					state = "failed"
					statusText = fmt.Sprintf("Failed to generate %s configuration", compInfo.name)
				}
				_ = toolCtx.SendSignal(a2a.BuilderComponentProgressData{
					Type:          string(a2a.SignalBuilderComponentProgress),
					ComponentID:   compInfo.id,
					ComponentType: compInfo.typ,
					ComponentName: compInfo.name,
					State:         state,
					StatusText:    statusText,
				})
			}

			deliver(parsed)
		}()
	}
	return result, nil
}

func (t *subTaskTool) prepareForkedSession(ctx context.Context, toolCtx *tool.Context, effectiveSessionID, contextID, taskID string, skills []string) error {
	parentSess, err := t.agent.svc.Sessions().Get(ctx, t.agent.cfg.Name, toolCtx.UserID, toolCtx.SessionID)
	if err != nil {
		return fmt.Errorf("load parent session: %w", err)
	}

	tID := taskID
	sess := &session.Session{
		AppName:   t.agent.cfg.Name,
		UserID:    toolCtx.UserID,
		SessionID: effectiveSessionID,
		ContextID: contextID,
		TaskID:    &tID,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if parentSess != nil && len(parentSess.Messages) > 0 {
		sess.Messages = make([]session.Message, len(parentSess.Messages))
		copy(sess.Messages, parentSess.Messages)
	}

	mergedSkills := mergeSkills(parentLoadedSkills(toolCtx), skills)
	if len(mergedSkills) > 0 {
		sess.Metadata = map[string]any{
			"loaded_skills": mergedSkills,
		}
	}

	return t.agent.svc.Sessions().Create(ctx, sess)
}

func parentLoadedSkills(toolCtx *tool.Context) []string {
	if toolCtx == nil || toolCtx.SkillState == nil {
		return nil
	}
	return toolCtx.SkillState.LoadedNames()
}

func mergeSkills(parentSkills, requestedSkills []string) []string {
	seen := make(map[string]bool)
	var merged []string

	for _, name := range parentSkills {
		if name != "" && !seen[name] {
			seen[name] = true
			merged = append(merged, name)
		}
	}

	for _, name := range requestedSkills {
		if name != "" && !seen[name] {
			seen[name] = true
			merged = append(merged, name)
		}
	}

	return merged
}

func (t *subTaskTool) prepareFreshSessionWithSkills(ctx context.Context, toolCtx *tool.Context, effectiveSessionID, contextID, taskID string, skills []string) error {
	tID := taskID
	sess := &session.Session{
		AppName:   t.agent.cfg.Name,
		UserID:    toolCtx.UserID,
		SessionID: effectiveSessionID,
		ContextID: contextID,
		TaskID:    &tID,
		Metadata: map[string]any{
			"loaded_skills": skills,
		},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	return t.agent.svc.Sessions().Create(ctx, sess)
}

func (t *subTaskTool) parseResponse(respMsg *broker.Message) (*tool.Result, error) {
	if respMsg == nil {
		return tool.Error("sub-task timed out or was cancelled"), nil
	}
	var a2aResp a2a.Response
	if jsonErr := json.Unmarshal(respMsg.Payload, &a2aResp); jsonErr != nil {
		return tool.Error("failed to parse sub-task response: " + jsonErr.Error()), nil
	}

	if a2aResp.Error != nil {
		return tool.Error(fmt.Sprintf("sub-task error: %s", a2aResp.Error.Message)), nil
	}

	resultBytes, err := json.Marshal(a2aResp.Result)
	if err != nil {
		return tool.Error("failed to marshal sub-task result: " + err.Error()), nil
	}

	var task map[string]any
	if err := json.Unmarshal(resultBytes, &task); err != nil {
		return tool.Error("failed to parse sub-task result: " + err.Error()), nil
	}

	var text string
	if status, ok := task["status"].(map[string]any); ok {
		if respMsgMap, ok := status["message"].(map[string]any); ok {
			if parts, ok := respMsgMap["parts"].([]any); ok {
				for _, p := range parts {
					if partMap, ok := p.(map[string]any); ok {
						if partMap["kind"] == "text" {
							if txt, ok := partMap["text"].(string); ok {
								text += txt
							}
						}
					}
				}
			}
		}
	}

	if delegatedTaskUnsuccessful(task) {
		return delegatedTaskError("sub-task did not succeed: "+cmp.Or(text, "no text content"), tool.WithDelegatedTask(task)), nil
	}
	if text == "" {
		text = "Sub-task completed (no text content)"
	}

	return tool.OK(text, tool.WithDelegatedTask(task)), nil
}

type builderComponentInfo struct {
	id   string
	typ  string
	name string
}

func isBuilderAgent(agentName string) bool {
	return strings.EqualFold(agentName, "builder")
}

var componentNameRegex = regexp.MustCompile(
	`(?i)(?:generate|create|build|write|produce)\s+(?:the\s+|a\s+)?(?:yaml\s+|full\s+)?configuration\s+(?:file\s+)?for\s+(?:the\s+)?` +
		`["\x60]?([^"\x60\n]+?)["\x60]?\s+` +
		`(agent|skill|workflow|connector|gateway)`,
)

var componentNameFallbackRegex = regexp.MustCompile(
	`(?i)for\s+(?:the\s+)?["\x60]?([^"\x60\n]{2,60}?)["\x60]?\s+(agent|skill|workflow|connector|gateway)`,
)

var filenameRegex = regexp.MustCompile(
	`(?i)(?:save|write|output|filename|file)\s*(?:as|to|:)?\s*["\x60]?(\w[\w-]*)\.(yaml|yml|json)["\x60]?`,
)

func extractComponentInfo(instruction string) *builderComponentInfo {
	if m := componentNameRegex.FindStringSubmatch(instruction); len(m) >= 3 {
		name := strings.TrimSpace(m[1])
		typ := strings.ToLower(m[2])
		return &builderComponentInfo{
			id:   typ + ":" + sanitizeComponentID(name),
			typ:  typ,
			name: name,
		}
	}

	if m := componentNameFallbackRegex.FindStringSubmatch(instruction); len(m) >= 3 {
		name := strings.TrimSpace(m[1])
		typ := strings.ToLower(m[2])
		return &builderComponentInfo{
			id:   typ + ":" + sanitizeComponentID(name),
			typ:  typ,
			name: name,
		}
	}

	if m := filenameRegex.FindStringSubmatch(instruction); len(m) >= 2 {
		rawName := m[1]
		typ := inferComponentType(instruction)
		displayName := humanizeName(rawName)
		return &builderComponentInfo{
			id:   typ + ":" + rawName,
			typ:  typ,
			name: displayName,
		}
	}

	typ := inferComponentType(instruction)
	if typ != "" {
		shortID := fmt.Sprintf("component-%x", hashString(instruction)&0xFFFF)
		return &builderComponentInfo{
			id:   typ + ":" + shortID,
			typ:  typ,
			name: strings.Title(typ) + " Component", //nolint:staticcheck // strings.Title is fine for simple capitalization
		}
	}

	return nil
}

func sanitizeComponentID(name string) string {
	s := strings.ToLower(name)
	s = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			return r
		}
		return '_'
	}, s)
	for strings.Contains(s, "__") {
		s = strings.ReplaceAll(s, "__", "_")
	}
	return strings.Trim(s, "_")
}

func humanizeName(name string) string {
	s := strings.ReplaceAll(name, "_", " ")
	s = strings.ReplaceAll(s, "-", " ")
	return strings.Title(s) //nolint:staticcheck // strings.Title is fine for simple capitalization
}

func inferComponentType(instruction string) string {
	lower := strings.ToLower(instruction)
	for _, typ := range []string{"workflow", "connector", "gateway", "skill", "agent"} {
		if strings.Contains(lower, typ) {
			return typ
		}
	}
	return ""
}

func hashString(s string) uint32 {
	var h uint32
	for _, c := range s {
		h = h*31 + uint32(c)
	}
	return h
}

const subTaskTaskMarker = "Task:\n\n"

func coreTaskInstruction(incoming string) string {
	s := incoming
	if i := strings.LastIndex(s, subTaskTaskMarker); i >= 0 {
		s = s[i+len(subTaskTaskMarker):]
	}
	return strings.TrimSpace(s)
}

func instructionsNearlyIdentical(a, b string) bool {
	const minTokens = 8
	const threshold = 0.85
	ta := instructionTokenSet(a)
	tb := instructionTokenSet(b)
	if len(ta) < minTokens || len(tb) < minTokens {
		return false
	}
	intersection := 0
	for w := range ta {
		if tb[w] {
			intersection++
		}
	}
	union := len(ta) + len(tb) - intersection
	if union == 0 {
		return false
	}
	return float64(intersection)/float64(union) >= threshold
}

func instructionTokenSet(s string) map[string]bool {
	set := make(map[string]bool)
	for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	}) {
		set[w] = true
	}
	return set
}
