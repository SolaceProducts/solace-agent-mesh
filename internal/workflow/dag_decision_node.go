// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"runtime/debug"
	"slices"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/decision"
)

type DecisionEvaluator = decision.Evaluator

const (
	escalationLowConfidence = "low_confidence"
	escalationProviderError = "provider_error"
)

func (e *DAGExecutor) dispatchDecisionNode(ctx context.Context, node *NodeConfig, subTaskID string) {
	input, err := e.resolveNodeInput(node)
	if err != nil {
		e.handleNodeError(node.ID, fmt.Errorf("resolve input: %w", err))
		return
	}
	e.recordInputPreview(node.ID, input)

	e.state.TrackSubTask(subTaskID, node.ID)
	e.state.AddPending(node.ID)

	if _, saveErr := e.saveInputArtifact(ctx, node.ID, subTaskID, input); saveErr != nil {
		e.handleNodeError(node.ID, fmt.Errorf("save input artifact: %w", saveErr))
		return
	}

	routing := NodeCompletion{NodeID: node.ID, SubTaskID: subTaskID}
	e.armNodeTimer(node, subTaskID, routing)
	e.invokeDecisionNode(node, input, routing, e.state.NodeOutputs)
}

func (e *DAGExecutor) invokeDecisionNode(node *NodeConfig, input map[string]any, routing NodeCompletion, outputs map[string]map[string]any) {
	dispatchCtx := e.execCtx.ToolCtx
	evaluator := e.instance.decisions
	iteration := routing.MapNodeID != "" || routing.LoopNodeID != ""
	nodeLogger := e.logger.With("node", node.ID, "subTaskID", routing.SubTaskID)
	if iteration {
		nodeLogger = nodeLogger.With("itemIndex", routing.ItemIndex)
	}
	mediaRefs, mediaRefsErr := e.resolveDecisionMediaRefs(node, outputs, nodeLogger)
	call := decisionCall{
		node:           node,
		topLevelNodeID: routing.NodeID,
		evaluator:      evaluator,
		mediaRefs:      mediaRefs,
		timeout:        e.effectiveNodeTimeout(node),
		logger:         nodeLogger,
		req: decision.Request{
			Model:     cmp.Or(node.Model, e.instance.decisionModel()),
			State:     input,
			Questions: node.Questions,
			Logger:    nodeLogger,
		},
	}

	fail := func(err error) {
		_ = e.Advance(dispatchCtx, &EventNodeFailed{
			NodeID:     routing.NodeID,
			SubTaskID:  routing.SubTaskID,
			Err:        err,
			ErrKind:    ErrorKindError,
			MapNodeID:  routing.MapNodeID,
			LoopNodeID: routing.LoopNodeID,
			ItemIndex:  routing.ItemIndex,
		})
	}

	e.instance.wg.Go(func() {
		defer func() {
			if r := recover(); r != nil {
				e.recordDecisionPanic(nodeLogger, node.ID, iteration, r)
				fail(fmt.Errorf("decision node %q panicked: %v", node.ID, r))
			}
		}()
		if evaluator == nil {
			fail(errDecisionsDisabled(node.ID))
			return
		}
		if key := mediaInputKey(node); key != "" && !decisionMediaEnabled(e.instance.svc) {
			fail(errDecisionMediaDisabled(node.ID, key))
			return
		}
		if mediaRefsErr != nil {
			fail(mediaRefsErr)
			return
		}
		output, err := e.decide(dispatchCtx, call)
		if err != nil {
			fail(err)
			return
		}
		_ = e.Advance(dispatchCtx, &EventNodeComplete{
			NodeID:     routing.NodeID,
			SubTaskID:  routing.SubTaskID,
			Output:     output,
			MapNodeID:  routing.MapNodeID,
			LoopNodeID: routing.LoopNodeID,
			ItemIndex:  routing.ItemIndex,
		})
	})
}

type decisionCall struct {
	node           *NodeConfig
	topLevelNodeID string
	evaluator      DecisionEvaluator
	req            decision.Request
	mediaRefs      []mediaRef
	timeout        time.Duration
	logger         *slog.Logger
}

func (e *DAGExecutor) decide(dispatchCtx context.Context, call decisionCall) (map[string]any, error) {
	start := time.Now()
	reply, err := e.evaluateWithinNodeBudget(dispatchCtx, call)
	if err != nil {
		return nil, err
	}
	elapsed := time.Since(start)
	call.logger.Debug("decision evaluated",
		"questions", len(call.node.Questions), "mediaRefs", len(call.mediaRefs), "media", len(reply.sent),
		"model", reply.result.Model, "duration_ms", elapsed.Milliseconds(),
		"failed", reply.providerErr != nil, "phase", string(reply.phase))

	if reply.providerErr != nil && dispatchCtx.Err() != nil {
		return nil, decisionNodeError(call.node.ID, reply.providerErr)
	}
	output, err := decisionOutput(call.logger, call.node, reply, elapsed)
	if err != nil {
		return nil, err
	}
	addSentMedia(output, reply.sent)
	return output, nil
}

type decisionReply struct {
	result      decision.Result
	sent        []sentMedia
	providerErr error
	phase       decisionPhase
}

type decisionPhase string

const (
	phaseLoad     decisionPhase = "load"
	phaseProvider decisionPhase = "provider"
)

const nodeBudgetShare = 0.9

func (e *DAGExecutor) evaluateWithinNodeBudget(dispatchCtx context.Context, call decisionCall) (decisionReply, error) {
	ctx := dispatchCtx
	if call.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(dispatchCtx, time.Duration(float64(call.timeout)*nodeBudgetShare))
		defer cancel()
	}
	media, sent, err := e.loadDecisionMedia(ctx, call.logger, call.mediaRefs)
	if err != nil {
		err = asBudgetTimeout(ctx, dispatchCtx, err)
		if _, providerFailure := errors.AsType[*decision.Error](err); providerFailure {
			return decisionReply{providerErr: err, phase: phaseLoad}, nil
		}
		return decisionReply{}, decisionNodeError(call.node.ID, err)
	}
	call.req.Media = media
	call.req.ImageResolution = call.node.ImageResolution
	call.req.VideoResolution = call.node.VideoResolution
	_, maxImageBytes := decisionImageCaps(e.instance.cfg.Decision)
	call.req.ImageBytesLimit = int64(maxImageBytes)
	reply := decisionReply{sent: sent, phase: phaseProvider}
	reply.result, reply.providerErr = e.metered(call).Evaluate(ctx, call.req)
	if reply.providerErr == nil {
		reply.providerErr = reply.result.Validate(call.node.Questions)
	}
	return reply, nil
}

func asBudgetTimeout(budgetCtx, dispatchCtx context.Context, err error) error {
	if _, providerFailure := errors.AsType[*decision.Error](err); providerFailure {
		return err
	}
	if errors.Is(err, context.DeadlineExceeded) && budgetCtx.Err() != nil && dispatchCtx.Err() == nil {
		return &decision.Error{Kind: decision.FailureTimeout, Err: err}
	}
	return err
}

func (e *DAGExecutor) recordDecisionPanic(logger *slog.Logger, nodeID string, iteration bool, r any) {
	record := logger.Error
	args := []any{"panicValue", fmt.Sprint(r)}
	if _, repeat := e.panickedNodes.LoadOrStore(nodeID, struct{}{}); !repeat {
		args = append(args, "stack", string(debug.Stack()))
	} else if iteration {
		record = logger.Debug
	}
	record("decision node panicked", args...)
}

func decisionOutput(logger *slog.Logger, node *NodeConfig, reply decisionReply, elapsed time.Duration) (map[string]any, error) {
	result := reply.result
	output := map[string]any{
		"answers":           map[string]any{},
		"selected_branch":   "",
		"routed_branch":     "",
		"escalated":         false,
		"escalation_reason": "",
		"model":             result.Model,
		"usage": map[string]any{
			"input_tokens":  result.Usage.InputTokens,
			"output_tokens": result.Usage.OutputTokens,
		},
		"duration_ms": elapsed.Milliseconds(),
	}

	if reply.providerErr != nil {
		return providerFailureOutput(logger, node, output, reply.providerErr, reply.phase)
	}

	answers := make(map[string]any, len(node.Questions))
	for _, q := range node.Questions {
		answers[q.Key] = result.Answers[q.Key].Output()
	}
	output["answers"] = answers

	if node.RouteOn == "" {
		return output, nil
	}
	branch, reason, err := selectDecisionBranch(node, result.Answers[node.RouteOn])
	if err != nil {
		return nil, err
	}
	output["selected_branch"] = branch
	output["escalated"] = reason != ""
	output["escalation_reason"] = reason
	if reason == "" {
		output["routed_branch"] = branch
	}
	return output, nil
}

func providerFailureOutput(logger *slog.Logger, node *NodeConfig, output map[string]any, evalErr error, phase decisionPhase) (map[string]any, error) {
	if errors.Is(evalErr, budget.ErrExhausted) {
		return nil, decisionNodeError(node.ID, evalErr)
	}
	kind := decision.Classify(evalErr)
	if kind.Permanent() || node.OnProviderError != onProviderErrorEscalate {
		return nil, &providerFailureError{nodeID: node.ID, kind: kind, phase: phase, loading: loadingNoun(node), cause: evalErr}
	}
	logger.Warn("decision provider failed; escalating",
		"escalateTo", node.Escalate, "failureKind", string(kind), "phase", string(phase), "error", evalErr)
	output["selected_branch"] = node.Escalate
	output["escalated"] = true
	output["escalation_reason"] = escalationProviderError
	output["provider_error"] = string(kind)
	return output, nil
}

type providerFailureError struct {
	nodeID  string
	kind    decision.FailureKind
	phase   decisionPhase
	loading string
	cause   error
}

func (e *providerFailureError) Error() string {
	msg := fmt.Sprintf("decision node %q: %s failure", e.nodeID, e.kind)
	if e.phase == phaseLoad {
		msg += " while loading " + e.loading
	}
	if _, transport := errors.AsType[*url.Error](e.cause); transport {
		return msg + "; see the AWE log for the cause"
	}
	return msg + ": " + e.cause.Error()
}

func (e *providerFailureError) LogValue() slog.Value {
	return slog.StringValue(fmt.Sprintf("decision node %q: %s failure in the %s phase: %v", e.nodeID, e.kind, e.phase, e.cause))
}

func (e *providerFailureError) Unwrap() error { return e.cause }

func selectDecisionBranch(node *NodeConfig, answer decision.Answer) (branch, escalationReason string, err error) {
	if node.MinConfidence > 0 && (answer.Confidence == nil || *answer.Confidence < node.MinConfidence) {
		return node.Escalate, escalationLowConfidence, nil
	}
	if target, ok := node.Routes[answer.Choice]; ok {
		return target, "", nil
	}
	if node.Default != "" {
		return node.Default, "", nil
	}
	return "", "", fmt.Errorf("decision node %q chose %q for %q, which has no route, and the node has no default",
		node.ID, answer.Choice, node.RouteOn)
}

func (e *DAGExecutor) completeDecisionNode(node *NodeConfig, output map[string]any) {
	e.state.CompletedNodes[node.ID] = node.Type
	e.publishNodeResultMeta(node.ID, "success", nil, decisionResultMetadata(output))

	selected, _ := output["selected_branch"].(string)
	escalated, _ := output["escalated"].(bool)
	awaitsEscalationAnswer := escalated && node.reroutesEscalation()
	for _, target := range node.decisionTargets() {
		if target.nodeID == selected || awaitsEscalationAnswer {
			continue
		}
		e.skipBranch(target.nodeID, node, SkipCauseNotSelected, false)
	}
	if awaitsEscalationAnswer && e.state.IsNodeSkipped(node.Escalate) {
		e.abandonEscalationRouting(node.Escalate)
	}
	e.logger.Debug("decision node completed", "node", node.ID, "selectedBranch", selected)
}

func (e *DAGExecutor) routeOnEscalationAnswer(escalateID string) error {
	for _, node := range e.cfg.Nodes {
		if node.Type != "decision" || node.Escalate != escalateID || !node.reroutesEscalation() || !e.decisionEscalated(node.ID) {
			continue
		}
		selected, err := e.escalationRoute(node)
		if err != nil {
			return err
		}
		e.decisionOutputOf(node.ID)["routed_branch"] = selected
		escalateNode := e.dag.Nodes[escalateID]
		for _, target := range node.routedTargets() {
			if target.nodeID != selected {
				e.skipBranch(target.nodeID, escalateNode, SkipCauseNotSelected, false)
			}
		}
		e.logger.Debug("decision node routed on the escalation's answer",
			"node", node.ID, "escalateTo", escalateID, "routedBranch", selected)
	}
	return nil
}

func (e *DAGExecutor) abandonEscalationRouting(skippedID string) {
	for _, node := range e.cfg.Nodes {
		if node.Type != "decision" || node.Escalate != skippedID || !node.reroutesEscalation() || !e.decisionEscalated(node.ID) {
			continue
		}
		reason := fmt.Sprintf("escalate target %q of decision %q was skipped, so no route was chosen", skippedID, node.ID)
		for _, target := range node.routedTargets() {
			if !e.state.IsNodeCompleted(target.nodeID) && !e.state.IsNodeSkipped(target.nodeID) {
				e.skipNodeCause(target.nodeID, reason, e.state.SkipCause(skippedID), false)
			}
		}
	}
}

func (e *DAGExecutor) decisionEscalated(nodeID string) bool {
	escalated, _ := e.decisionOutputOf(nodeID)["escalated"].(bool)
	return escalated
}

func (e *DAGExecutor) decisionOutputOf(nodeID string) map[string]any {
	output, _ := e.state.NodeOutputs[nodeID]["output"].(map[string]any)
	return output
}

func (e *DAGExecutor) escalationRoute(node *NodeConfig) (string, error) {
	value, err := resolveTemplate(EscalationAnswerTemplate(node, e.dag.Nodes[node.Escalate]), e.state.NodeOutputs, e.namedOutputNodes())
	if err != nil {
		return "", fmt.Errorf("decision node %q: read the escalation's answer: %w; %s", node.ID, err, rerouteHandoffHint(node))
	}
	routeOn, _ := node.decisionQuestion(node.RouteOn)
	choice, isString := value.(string)
	choice = strings.TrimSpace(choice)
	if !isString || !routeOn.HasCriterion(choice) {
		return "", fmt.Errorf("escalation target %q answered %s for decision node %q, which is not an option of %q (options: %s); %s",
			node.Escalate, describeEscalationAnswer(value), node.ID, node.RouteOn, strings.Join(routeOn.CriterionKeys(), ", "), rerouteHandoffHint(node))
	}
	if target, ok := node.Routes[choice]; ok {
		return target, nil
	}
	return node.Default, nil
}

func rerouteHandoffHint(node *NodeConfig) string {
	return fmt.Sprintf("set escalation_mode: handoff on %q if %q should end the routing rather than answer %q", node.ID, node.Escalate, node.RouteOn)
}

func describeEscalationAnswer(value any) string {
	switch v := value.(type) {
	case nil:
		return "nothing"
	case string:
		return fmt.Sprintf("a %d-byte string", len(v))
	default:
		return fmt.Sprintf("a %T", v)
	}
}

func (e *DAGExecutor) completedNode(nc NodeCompletion) *NodeConfig {
	switch {
	case nc.MapNodeID != "":
		if mapNode := e.dag.Nodes[nc.MapNodeID]; mapNode != nil {
			return e.dag.Nodes[mapNode.TemplateNode]
		}
		return nil
	case nc.LoopNodeID != "":
		if loopNode := e.dag.Nodes[nc.LoopNodeID]; loopNode != nil {
			return e.dag.Nodes[loopNode.BodyNode]
		}
		return nil
	default:
		return e.dag.Nodes[nc.NodeID]
	}
}

func (e *DAGExecutor) recordDecisionUsage(nc NodeCompletion) {
	if nc.Error != nil || nc.Output == nil {
		return
	}
	if node := e.completedNode(nc); node == nil || node.Type != "decision" {
		return
	}
	usage, _ := nc.Output["usage"].(map[string]any)
	tokens := DecisionTokens{InputTokens: toInt(usage["input_tokens"]), OutputTokens: toInt(usage["output_tokens"])}
	if tokens.InputTokens == 0 && tokens.OutputTokens == 0 {
		return
	}
	model, _ := nc.Output["model"].(string)
	if e.state.DecisionUsage == nil {
		e.state.DecisionUsage = make(map[string]DecisionTokens)
	}
	total := e.state.DecisionUsage[model]
	total.InputTokens += tokens.InputTokens
	total.OutputTokens += tokens.OutputTokens
	e.state.DecisionUsage[model] = total
}

func (e *DAGExecutor) addDecisionTokenUsage(metadata map[string]any) {
	if len(e.state.DecisionUsage) == 0 {
		return
	}
	var input, output int
	byModel := make(map[string]any, len(e.state.DecisionUsage))
	for model, tokens := range e.state.DecisionUsage {
		input += tokens.InputTokens
		output += tokens.OutputTokens
		byModel[model] = map[string]any{"input_tokens": tokens.InputTokens, "output_tokens": tokens.OutputTokens, "kind": "decision"}
	}
	metadata["token_usage"] = map[string]any{
		"total_input_tokens":        input,
		"total_output_tokens":       output,
		"total_cached_input_tokens": 0,
		"total_tokens":              input + output,
		"by_source":                 map[string]any{"decision": map[string]any{"input_tokens": input, "output_tokens": output}},
		"by_model":                  byModel,
	}
}

func decisionResultMetadata(output map[string]any) map[string]any {
	metadata := map[string]any{
		"answers":           output["answers"],
		"escalated":         output["escalated"],
		"escalation_reason": output["escalation_reason"],
		"model":             output["model"],
		"duration_ms":       output["duration_ms"],
	}
	if selected, _ := output["selected_branch"].(string); selected != "" {
		metadata["selected_branch"] = selected
	}
	if routed, _ := output["routed_branch"].(string); routed != "" {
		metadata["routed_branch"] = routed
	}
	if kind, _ := output["provider_error"].(string); kind != "" {
		metadata["provider_error"] = kind
	}
	return metadata
}

func hasDecisionNodes(nodes []*NodeConfig) bool {
	return slices.ContainsFunc(nodes, func(n *NodeConfig) bool { return n.Type == "decision" })
}

var errDecisionProviderMissing = errors.New("decision nodes require a workflow `decision:` provider block")

func decisionNodeError(nodeID string, err error) error {
	return fmt.Errorf("decision node %q: %w", nodeID, err)
}
