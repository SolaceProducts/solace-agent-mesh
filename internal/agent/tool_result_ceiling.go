// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/strutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const (
	inlineScalarMaxBytes      = 512
	inlineDiagnosticMaxBytes  = 8192
	oversizedMessageMaxBytes  = 2048
	saveFailureReasonMaxBytes = 200
)

var diagnosticKeys = map[string]bool{
	"code": true, "stderr_tail": true, "stack_trace": true, "guidance": true,
	"retryable": true, "error_code": true, "artifact_schema": true,
	"reply_parts": true, "artifacts": true, "produced_artifacts": true,
}

var lastResortKeys = []string{"view_truncated", "artifact", "task_state", "error_code", "code", "retryable"}

const requiredScalarMaxBytes = 64

const (
	windowResumeNote = "Content truncated after %d complete lines (%d bytes) to fit the tool-result limit. " +
		"Continue with load_artifact filename=%q version=%d offset=%d, or search it with artifact_grep if available."
	windowLongLineNote = "Line %d does not fit in the room the tool-result limit leaves, so only its first %d " +
		"bytes are shown. Continue after it with load_artifact filename=%q version=%d offset=%d, or search it with " +
		"artifact_grep if available."
	windowSingleLineNote = "This content is one line longer than the tool-result limit, so only its first %d bytes " +
		"are shown and paging by line cannot reach the rest. Search it with artifact_grep, or query it with a data " +
		"tool such as query_data_with_sql, if available."
	spilledNote = "The full result (%d bytes) was over the tool-result limit, so it was saved as artifact %q " +
		"version %d (indented JSON) and only its small fields are shown here. If the tools are available, search it " +
		"with artifact_grep or query it with query_data_with_sql; load_artifact offset and limit page it by line, " +
		"which helps for arrays but not for one long string. Without those tools, call again with a narrower " +
		"request (fewer rows or columns, or an aggregate)."
	unsavedNote = "The result (%d bytes) was over the tool-result limit and could not be saved as an artifact " +
		"(%s), so only its small fields and the beginning of the rest (result_head) are shown."
	noArtifactService = "no artifact service is configured"
)

func (a *Agent) boundOversizedPayload(ctx context.Context, r *tool.Result, combined map[string]any,
	encoded []byte, toolCtx *tool.Context, sp *StatusPublisher) (text string, withheldBytes int64) {
	_, ceiling := a.toolOutputCaps()
	if len(encoded) <= ceiling || !a.toolResultProcessingEnabled() {
		return string(encoded), 0
	}
	if w := r.ContentWindow; w != nil && w.Filename != "" && w.FirstLine >= 1 && w.Version >= 0 {
		if out := truncateWindowToCeiling(combined, w, ceiling); out != "" {
			a.logger.Debug("tool result over the ceiling", "bytes", len(encoded), "ceiling", ceiling, "action", "truncated")
			return out, 0
		}
	}
	var saved *savedArtifactInfo
	reason := noArtifactService
	if toolCtx != nil && toolCtx.ArtifactService != nil {
		saved, reason = a.spillPayload(ctx, r, combined, toolCtx, sp)
	}
	view := boundedView(r, combined, encoded, ceiling, saved, reason)
	a.warnIfProducedFilesDropped(combined, view, ceiling)
	if saved == nil {
		a.logger.Warn("tool result over the ceiling could not be saved", "bytes", len(encoded), "ceiling", ceiling,
			"action", "head")
		return view, 0
	}
	a.logger.Debug("tool result over the ceiling", "bytes", len(encoded), "ceiling", ceiling, "action", "spilled")
	return view, spilledResultWithheldBytes(encoded, view)
}

const producedArtifactsKey = "produced_artifacts"

func (a *Agent) warnIfProducedFilesDropped(combined map[string]any, view string, ceiling int) {
	produced, ok := combined[producedArtifactsKey]
	if !ok {
		return
	}
	var kept map[string]json.RawMessage
	if json.Unmarshal([]byte(view), &kept) == nil {
		if _, stays := kept[producedArtifactsKey]; stays {
			return
		}
	}
	files, _ := produced.([]any)
	a.logger.Warn("tool result over the ceiling dropped its list of produced files",
		"key", producedArtifactsKey, "fileCount", len(files), "ceiling", ceiling)
}

func (a *Agent) toolResultProcessingEnabled() bool {
	return a.cfg.ToolResultProcessingEnabled == nil || *a.cfg.ToolResultProcessingEnabled
}

func truncateWindowToCeiling(combined map[string]any, window *tool.ContentWindow, ceiling int) string {
	content, ok := combined["content"].(string)
	if !ok {
		return ""
	}
	render := func(keep int) string {
		kept := strutil.Truncate(content, keep, "")
		if end := strings.LastIndexByte(kept, '\n'); end >= 0 {
			kept = kept[:end+1]
		}
		out := maps.Clone(combined)
		out["content"] = kept
		out["content_truncated"] = true
		out["content_returned_bytes"] = len(kept)
		lines := strings.Count(kept, "\n")
		switch {
		case lines > 0:
			out["next_offset"] = window.FirstLine + lines
			out["truncation_note"] = fmt.Sprintf(windowResumeNote, lines, len(kept), window.Filename,
				window.Version, window.FirstLine+lines)
		case strings.Contains(content, "\n"):
			out["next_offset"] = window.FirstLine + 1
			out["truncation_note"] = fmt.Sprintf(windowLongLineNote, window.FirstLine, len(kept), window.Filename,
				window.Version, window.FirstLine+1)
		default:
			out["truncation_note"] = fmt.Sprintf(windowSingleLineNote, len(kept))
		}
		return mustMarshal(out)
	}
	if len(render(0)) > ceiling {
		return ""
	}
	return render(longestFit(min(len(content), ceiling), ceiling, func(n int) int { return len(render(n)) }))
}

func longestFit(limit, ceiling int, size func(int) int) int {
	lo, hi := 0, limit
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if size(mid) <= ceiling {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return lo
}

func (a *Agent) spillPayload(ctx context.Context, r *tool.Result, combined map[string]any,
	toolCtx *tool.Context, sp *StatusPublisher) (*savedArtifactInfo, string) {
	stored := maps.Clone(combined)
	for k := range r.LLMData {
		delete(stored, k)
		if v, ok := r.Data[k]; ok {
			stored[k] = v
		}
	}
	body, err := json.MarshalIndent(stored, "", " ")
	if err != nil {
		return nil, "the result could not be encoded"
	}
	digest := sha256.Sum256(body)
	_, saved, failed := a.saveDataObjectAsArtifact(ctx, tool.DataObject{
		Name:        "tool_result_" + hex.EncodeToString(digest[:6]) + ".json",
		Content:     body,
		MIMEType:    mimeJSON,
		Disposition: tool.DispositionArtifactWithPreview,
		Description: "Tool result that exceeded the tool-result limit",
		Tags:        []string{a2a.ArtifactTagWorking},
	}, toolCtx, sp)
	if failed != nil {
		return nil, failed.Error
	}
	return saved, ""
}

type viewBuilder struct {
	fields  map[string]any
	size    int
	ceiling int
}

func newViewBuilder(ceiling int) *viewBuilder {
	return &viewBuilder{fields: map[string]any{}, size: len("{}"), ceiling: ceiling}
}

func (b *viewBuilder) cost(k string, v any) int {
	if old, ok := b.fields[k]; ok {
		return len(mustMarshal(v)) - len(mustMarshal(old))
	}
	c := len(mustMarshal(k)) + 1 + len(mustMarshal(v))
	if len(b.fields) > 0 {
		c++
	}
	return c
}

func (b *viewBuilder) set(k string, v any) {
	b.size += b.cost(k, v)
	b.fields[k] = v
}

func (b *viewBuilder) add(k string, v any) bool {
	c := b.cost(k, v)
	if b.size+c > b.ceiling {
		return false
	}
	b.size += c
	b.fields[k] = v
	return true
}

func (b *viewBuilder) addCut(k, text string, maxBytes int, marker string) {
	if len(text) <= maxBytes && b.add(k, text) {
		return
	}
	cut := func(n int) string { return strutil.Truncate(text, n, marker) }
	n := longestFit(min(len(text)-1, maxBytes), b.ceiling-b.size, func(n int) int { return b.cost(k, cut(n)) })
	if n > 0 {
		b.add(k, cut(n))
	}
}

func boundedView(r *tool.Result, combined map[string]any, encoded []byte, ceiling int,
	saved *savedArtifactInfo, reason string) string {
	b := newViewBuilder(ceiling)
	status := strutil.Truncate(cmp.Or(r.Status, tool.StatusSuccess), requiredScalarMaxBytes, "")
	if saved == nil && status == tool.StatusSuccess {
		status = tool.StatusPartial
	}
	b.set("status", status)
	if state := delegatedTaskState(combined); state != "" {
		b.set("task_state", state)
	}
	if r.ErrorCode != "" {
		b.set("error_code", strutil.Truncate(r.ErrorCode, requiredScalarMaxBytes, ""))
	}
	var entry map[string]any
	if saved != nil {
		entry = map[string]any{"filename": saved.Filename, "version": saved.Version,
			"size_bytes": saved.SizeBytes, "mime_type": saved.MIMEType}
		b.set(artifactsCreatedKey, []map[string]any{entry})
		b.set("result_saved_note", fmt.Sprintf(spilledNote, len(encoded), saved.Filename, saved.Version))
	} else {
		reason = strutil.Truncate(reason, saveFailureReasonMaxBytes, "...")
		if reason != noArtifactService {
			b.set("failed_artifacts", []map[string]any{{"error": reason}})
		}
		b.set("note", fmt.Sprintf(unsavedNote, len(encoded), reason))
	}

	keys := slices.Collect(maps.Keys(combined))
	sizes := make(map[string]int, len(keys))
	for _, k := range keys {
		sizes[k] = len(mustMarshal(combined[k]))
	}
	slices.SortFunc(keys, func(x, y string) int {
		if diagnosticKeys[x] != diagnosticKeys[y] {
			if diagnosticKeys[x] {
				return -1
			}
			return 1
		}
		return cmp.Or(cmp.Compare(sizes[x], sizes[y]), strings.Compare(x, y))
	})
	addFields := func(limit func(k string) int) {
		for _, k := range keys {
			if _, taken := b.fields[k]; !taken && sizes[k] <= limit(k) {
				b.add(k, combined[k])
			}
		}
	}
	addFields(func(k string) int {
		if diagnosticKeys[k] {
			return inlineScalarMaxBytes
		}
		return -1
	})
	msg, msgIsText := combined["message"].(string)
	if msg != "" {
		marker := " [... truncated]"
		if saved != nil {
			marker = " [... truncated; full text in the artifact]"
		}
		b.addCut("message", msg, oversizedMessageMaxBytes, marker)
	}
	addFields(func(k string) int {
		switch {
		case k == "message" && msgIsText:
			return -1
		case diagnosticKeys[k]:
			return inlineDiagnosticMaxBytes
		}
		return inlineScalarMaxBytes
	})

	if saved != nil && saved.Preview != "" {
		withPreview := maps.Clone(entry)
		withPreview["preview"] = saved.Preview
		b.add(artifactsCreatedKey, []map[string]any{withPreview})
	}
	if saved == nil {
		b.addCut("result_head", string(encoded), len(encoded), "")
	}
	if out := mustMarshal(b.fields); len(out) <= ceiling {
		return out
	}
	return minimalView(combined, b.fields, saved, ceiling)
}

func delegatedTaskState(combined map[string]any) string {
	state, _ := combined["task_state"].(string)
	return strutil.Truncate(state, requiredScalarMaxBytes, "")
}

func minimalView(combined, fields map[string]any, saved *savedArtifactInfo, ceiling int) string {
	candidates := maps.Clone(combined)
	maps.Copy(candidates, fields)
	candidates["view_truncated"] = true
	delete(candidates, "artifact")
	if saved != nil {
		candidates["artifact"] = saved.Filename
	}
	status, _ := fields["status"].(string)
	if status == tool.StatusSuccess {
		status = tool.StatusPartial
	}
	b := newViewBuilder(ceiling)
	b.set("status", status)
	for _, k := range lastResortKeys {
		if v, ok := candidates[k]; ok {
			b.add(k, v)
		}
	}
	return mustMarshal(b.fields)
}

func mustMarshal(v any) string {
	out, err := json.Marshal(v)
	if err != nil {
		return "null"
	}
	return string(out)
}
