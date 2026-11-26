// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"
	_ "embed"
	"fmt"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/liquidsafe"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

//go:embed embed/explain_task.md
var explainTaskTemplate string

//go:embed embed/list_failed_tasks.md
var listFailedTasksTemplate string

//go:embed embed/summarize_feedback.md
var summarizeFeedbackTemplate string

//go:embed embed/list_feedback.md
var listFeedbackTemplate string

//go:embed embed/explain_feedback.md
var explainFeedbackTemplate string

func withActivityWindow(props map[string]any) map[string]any {
	props["window"] = map[string]any{
		"type":        "string",
		"description": "PREFERRED for any 'last N' / 'past N' / 'today' / 'current' / 'recent' ask. Any positive Go-style duration is accepted: '30m', '3h', '90m', '24h', '2h30m', or the 'Nd' shorthand for days ('7d', '30d'). Server computes exact times from ITS own clock — do NOT compute or pass startDate/endDate for relative asks. Server maximum is 90 days. When the user names an exact duration ('past 3 hours'), pass it verbatim ('3h'). Default 24h.",
	}
	props["startDate"] = map[string]any{
		"type":        "string",
		"description": "ISO 8601 UTC start of an ABSOLUTE date range (e.g. 'from June 1 to June 15'). Do NOT use for relative asks — use `window` instead. Ignored when `window` is set.",
	}
	props["endDate"] = map[string]any{
		"type":        "string",
		"description": "ISO 8601 UTC end of an ABSOLUTE date range (exclusive). Do NOT use for relative asks — use `window` instead. Ignored when `window` is set. Server clamps values in the future down to now.",
	}
	return props
}

func applyWindowArgs(args map[string]any, body map[string]any) (Window, error) {
	window, err := parseWindowArgsWithKey(
		stringArg(args, "window"), stringArg(args, "startDate"), stringArg(args, "endDate"), time.Now())
	if err != nil {
		return Window{}, err
	}
	if err := applyWindow(body, window); err != nil {
		return Window{}, err
	}
	return window, nil
}

const markdownMIMEType = "text/markdown"

const activityWorkingJSONInlineMaxBytes = 102400

func renderActivityMarkdown(src string, data map[string]any) (string, error) {
	out, err := liquidsafe.RenderStringWithDeadline(liquidsafe.NewEngine(), src, data, liquidsafe.DefaultRenderDeadline)
	if err != nil {
		return "", fmt.Errorf("render activity markdown: %w", err)
	}
	return out, nil
}

func saveActivityMarkdown(ctx context.Context, toolCtx *tool.Context, filename, body, description string) (string, error) {
	res, err := tool.NewFacade(toolCtx).SaveArtifactWithMetadata(ctx, filename, []byte(body), markdownMIMEType,
		artifact.MetadataOpts{Description: description, Tags: []string{a2a.ArtifactTagWorking}})
	if err != nil {
		return "", fmt.Errorf("save %s: %w", filename, err)
	}
	return fmt.Sprintf("«artifact_content:%s:%d»", filename, res.DataVersion), nil
}

func traceJSON(name, description string, content []byte) tool.DataObject {
	return tool.DataObject{
		Name:           name,
		Content:        withoutEmbedDelimiters(content),
		MIMEType:       "application/json",
		Disposition:    tool.DispositionInline,
		InlineMaxBytes: activityWorkingJSONInlineMaxBytes,
		Description:    description,
		Tags:           []string{a2a.ArtifactTagWorking},
	}
}

var markdownUnsafe = strings.NewReplacer(`\`, `\\`, "`", "'", "|", `\|`, "\r", " ", "\n", " ", "«", "", "»", "")

var codeBlockUnsafe = strings.NewReplacer("`", "'", "\r", " ", "\n", " ", "«", "", "»", "")

func mdSafe(s string) string {
	return markdownUnsafe.Replace(s)
}

func codeSafe(s string) string {
	return codeBlockUnsafe.Replace(s)
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func titleStatus(status string) string {
	if status == "" {
		return "Unknown"
	}
	return strings.ToUpper(status[:1]) + strings.ReplaceAll(status[1:], "_", " ")
}

func withoutEmbedDelimiters(content []byte) []byte {
	return []byte(embedDelimiters.Replace(string(content)))
}

var embedDelimiters = strings.NewReplacer("«", "", "»", "")
