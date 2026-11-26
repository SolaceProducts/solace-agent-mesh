// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool/hilsurface"
)

const nodePreviewMaxRunes = 4000

var errNoHumanInEvalRun = errors.New("the node asked for human input, which an evaluation run has nobody to give")

func (e *DAGExecutor) evalRun() bool {
	return e.execCtx != nil && isEvalTrigger(e.execCtx.UserProps)
}

func isEvalTrigger(props a2a.UserProperties) bool {
	return a2a.NormalizeTrigger(props.Trigger) == a2a.TriggerEval
}

func (e *DAGExecutor) refusePauseInEvalRun(ctx context.Context, evt *EventNodePaused) {
	evt.unparked = tool.RefusalNoHumanInEvalRun
	if evt.Kind == PauseKindApproval {
		e.auditApprovalRefusal(ctx, evt.ToolName, tool.RefusalNoHumanInEvalRun)
	}
	e.closeRefusedPauseInOrder(evt, hilRefusalResult(tool.RefusalNoHumanInEvalRun))
	e.handleCompletion(NodeCompletion{
		NodeID:     evt.NodeID,
		SubTaskID:  evt.SubTaskID,
		Error:      errNoHumanInEvalRun,
		ErrorKind:  ErrorKindError,
		MapNodeID:  evt.MapNodeID,
		LoopNodeID: evt.LoopNodeID,
		ItemIndex:  evt.ItemIndex,
	})
}

func (e *DAGExecutor) endEarly(reason string) {
	if e.state.TerminationReason == "" {
		e.state.TerminationReason = reason
	}
}

const failureReasonError = "error"

func failureReasonOf(err error) string {
	if errors.Is(err, errNoHumanInEvalRun) {
		return a2a.FailureReasonPausedForInput
	}
	return failureReasonError
}

func nodeFailureMetadata(failureReason string) map[string]any {
	if failureReason != a2a.FailureReasonPausedForInput {
		return nil
	}
	return map[string]any{a2a.MetadataFailureReason: failureReason}
}

func (e *DAGExecutor) addTerminationMetadata(metadata map[string]any, fallback string) {
	reason := e.state.TerminationReason
	if failed := e.state.ErrorState; failed != nil {
		metadata[a2a.MetadataFailedNodeID] = failed.FailedNodeID
		if reason == "" {
			reason = a2a.TerminationNodeFailed
			if failed.FailureReason == a2a.FailureReasonPausedForInput {
				reason = a2a.TerminationPausedForInput
			}
		}
	}
	if reason == "" {
		reason = fallback
	}
	if reason != "" {
		metadata[a2a.MetadataTerminationReason] = reason
	}
}

func (e *DAGExecutor) recordInputPreview(nodeID string, input map[string]any) {
	if !e.evalRun() {
		return
	}
	if e.inputPreviews == nil {
		e.inputPreviews = map[string]string{}
	}
	e.inputPreviews[nodeID] = previewOf(hilsurface.RedactToolArgs(input))
}

func (e *DAGExecutor) withNodePreviews(nodeID string, metadata map[string]any) map[string]any {
	if !e.evalRun() {
		return metadata
	}
	input, hasInput := e.inputPreviews[nodeID]
	output, hasOutput := e.state.NodeOutputs[nodeID]["output"]
	if !hasInput && !hasOutput {
		return metadata
	}
	withPreviews := maps.Clone(metadata)
	if withPreviews == nil {
		withPreviews = map[string]any{}
	}
	if hasInput {
		withPreviews[a2a.MetadataInputPreview] = input
	}
	if hasOutput {
		withPreviews[a2a.MetadataOutputPreview] = previewOf(hilsurface.RedactValue(output))
	}
	return withPreviews
}

func previewOf(value any) string {
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return fmt.Sprintf("<unserializable: %T>", value)
	}
	return truncateRunes(strings.TrimSuffix(encoded.String(), "\n"), nodePreviewMaxRunes)
}

func (w *WorkflowInstance) requestModelOverride(logger *slog.Logger, metadata map[string]any, props a2a.UserProperties) string {
	alias, present, usable := modelOverrideAlias(metadata)
	switch {
	case !present:
		return ""
	case !isEvalTrigger(props):
		logger.Warn("request model override ignored: only evaluation runs may set one")
		return ""
	case !usable:
		logger.Warn("request model override ignored: it is not {model_id: <alias>}")
		return ""
	}
	return alias
}

func modelOverrideAlias(metadata map[string]any) (alias string, present, usable bool) {
	raw := metadata[a2a.MetadataModelOverride]
	if raw == nil {
		return "", false, false
	}
	override, _ := raw.(map[string]any)
	alias, _ = override[a2a.MetadataModelID].(string)
	return alias, true, alias != ""
}
