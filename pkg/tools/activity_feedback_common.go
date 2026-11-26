// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/analytics"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const (
	feedbackRatingWireUp   = "up"
	feedbackRatingWireDown = "down"
)

type feedbackKind int

const (
	feedbackKindSummary feedbackKind = iota
	feedbackKindRows
)

var (
	feedbackSortValues = []string{"negativeRatio", "negativeFeedback"}
)

type feedbackToolSpec struct {
	logName      string
	controlPath  string
	filePrefix   string
	title        string
	dataDesc     string
	template     string
	kind         feedbackKind
	templateData func(raw json.RawMessage, window Window, loc *time.Location) (map[string]any, error)
	digest       func(raw json.RawMessage, window Window, loc *time.Location) ([]byte, error)
}

type feedbackDisambiguation struct {
	Candidates []analytics.AgentRef `json:"candidates"`
}

func feedbackParameters(kind feedbackKind) *tool.JSONSchema {
	props := withActivityWindow(map[string]any{
		"subjects": map[string]any{
			"type":        "array",
			"items":       map[string]any{"type": "string"},
			"description": "Optional agent or workflow names (or ids) to narrow to. A name that matches several is returned as a question to ask the user; never guess between them.",
		},
		"userId": map[string]any{
			"type":        "string",
			"description": "Optional: only feedback left by this user, by their stored user id (often an email), exactly as given; names are not looked up.",
		},
		"subjectType": map[string]any{
			"type":        "string",
			"enum":        []string{"agent", "workflow"},
			"description": "Optional: restrict to agents or to workflows.",
		},
	})
	switch kind {
	case feedbackKindRows:
		props["feedbackId"] = map[string]any{
			"type":        "string",
			"description": "Optional: show this one feedback entry, by its feedback id. Needs no other filter and ignores the window.",
		}
		props["rating"] = map[string]any{
			"type":        "string",
			"enum":        []string{"positive", "negative"},
			"description": "Optional: only positive or only negative feedback.",
		}
	case feedbackKindSummary:
		props["sortBy"] = map[string]any{
			"type":        "string",
			"enum":        feedbackSortValues,
			"description": "Optional, default negativeRatio: order rows by negative ratio, or by negative count (negativeFeedback) for \"most thumbs-down\" questions.",
		}
	}
	return &tool.JSONSchema{Type: "object", Properties: props}
}

func feedbackBody(args map[string]any, kind feedbackKind) (json.RawMessage, Window, error) {
	body := map[string]any{}
	window, err := applyWindowArgs(args, body)
	if err != nil {
		return nil, Window{}, err
	}
	if names := trimmedNames(args["subjects"]); len(names) > 0 {
		body["subjects"] = names
	}
	if u := stringArg(args, "userId"); u != "" {
		body["feedbackUserId"] = u
	}
	if st := stringArg(args, "subjectType"); st != "" {
		body["subjectType"] = st
	}
	if kind == feedbackKindSummary {
		if argErr := copyEnumArg(args, body, "sortBy", feedbackSortValues); argErr != nil {
			return nil, Window{}, argErr
		}
	}
	if kind == feedbackKindRows {
		if id := stringArg(args, "feedbackId"); id != "" {
			body["feedbackId"] = id
			window.Label = "feedback " + id
		}
		switch r := stringArg(args, "rating"); r {
		case "":
		case "positive":
			body["rating"] = feedbackRatingWireUp
		case "negative":
			body["rating"] = feedbackRatingWireDown
		default:
			return nil, Window{}, fmt.Errorf("rating must be 'positive' or 'negative'")
		}
	}
	raw, err := json.Marshal(body)
	return raw, window, err
}

func copyEnumArg(args, body map[string]any, key string, allowed []string) error {
	v := stringArg(args, key)
	if v == "" {
		return nil
	}
	if !slices.Contains(allowed, v) {
		return fmt.Errorf("%s must be one of %s", key, strings.Join(allowed, ", "))
	}
	body[key] = v
	return nil
}

func trimmedNames(v any) []string {
	names := stringSliceArg(v)
	kept := make([]string, 0, len(names))
	for _, s := range names {
		if s = strings.TrimSpace(s); s != "" {
			kept = append(kept, s)
		}
	}
	return kept
}

func runFeedbackTool(ctx context.Context, args map[string]any, toolCtx *tool.Context, spec feedbackToolSpec) (*tool.Result, error) {
	if toolCtx == nil || toolCtx.ControlQuerier == nil {
		return tool.Error("Activity service is not available. Ensure the gateway is running."), nil
	}
	body, window, err := feedbackBody(args, spec.kind)
	if err != nil {
		return tool.Error(err.Error()), nil
	}

	logger := slog.Default().With("logger_name", spec.logName, "traceID", toolTraceID(toolCtx))
	raw, err := toolCtx.ControlQuerier.Custom(ctx, gatewayControlApp, "get", spec.controlPath, body)
	if err != nil {
		logger.Warn("feedback control call failed", "error", err)
		return tool.Error(fmt.Sprintf("Failed to read feedback: %s", err)), nil
	}

	var dis feedbackDisambiguation
	if err = json.Unmarshal(raw, &dis); err != nil {
		logger.Warn("feedback response parse failed", "error", err, "responseBytes", len(raw))
		return feedbackParseFailed(err), nil
	}
	if len(dis.Candidates) > 0 {
		return tool.OK(candidatesMessage(dis.Candidates)), nil
	}

	loc := userLocation(ctx, toolCtx, logger)
	data, err := spec.templateData(raw, window, loc)
	if err != nil {
		logger.Warn("feedback response parse failed", "error", err, "responseBytes", len(raw))
		return feedbackParseFailed(err), nil
	}
	rendered, err := renderActivityMarkdown(spec.template, data)
	if err != nil {
		logger.Error("feedback render failed", "error", err)
		return tool.Error(fmt.Sprintf("Failed to render feedback: %s", err)), nil
	}

	filters := activityFilters{TargetUserID: stringArg(args, "userId"), CallerUserID: callerUserID(toolCtx)}
	filename := reportFilenameStem(spec.filePrefix, window, resolveActivityScope(toolCtx), filters) + feedbackFilterSlug(args)
	report, err := saveActivityMarkdown(ctx, toolCtx, filename+".md", rendered, spec.title)
	if err != nil {
		logger.Warn("feedback save failed", "error", err)
		return tool.Error(fmt.Sprintf("Failed to save feedback: %s", err)), nil
	}

	objects := []tool.DataObject{{
		Name:        filename + "_data.json",
		Content:     withoutEmbedDelimiters(withFeedbackDisplayFields(raw, loc)),
		MIMEType:    "application/json",
		Disposition: tool.DispositionArtifact,
		Description: spec.dataDesc,
		Tags:        []string{a2a.ArtifactTagWorking},
	}}
	if spec.digest != nil {
		digest, derr := spec.digest(raw, window, loc)
		if derr != nil {
			logger.Warn("feedback digest failed", "error", derr, "responseBytes", len(raw))
			return feedbackParseFailed(derr), nil
		}
		objects = append(objects, traceJSON(filename+"_digest.json", "Feedback summary digest (LLM reasoning only)", digest))
	}
	return tool.OK(report, tool.WithDataObjects(objects...)), nil
}

func feedbackFilterSlug(args map[string]any) string {
	names := trimmedNames(args["subjects"])
	slices.Sort(names)
	var slug string
	if len(names) > 0 {
		slug += "_for-" + sanitizeFilenameSegment(strings.Join(names, "+"))
	}
	if st := stringArg(args, "subjectType"); st != "" {
		slug += "_" + sanitizeFilenameSegment(st)
	}
	for _, key := range []string{"feedbackId", "rating", "sortBy"} {
		if v := stringArg(args, key); v != "" {
			slug += "_" + sanitizeFilenameSegment(v)
		}
	}
	return slug
}

func unmatchedNames(names []string) string {
	quoted := make([]string, 0, len(names))
	for _, n := range names {
		quoted = append(quoted, "`"+codeSafe(n)+"`")
	}
	return strings.Join(quoted, ", ")
}

func candidatesMessage(candidates []analytics.AgentRef) string {
	var b strings.Builder
	b.WriteString("That name matches more than one agent or workflow. Ask the user which one they mean, then call the tool again with its id:\n")
	for _, c := range candidates {
		fmt.Fprintf(&b, "- %s (%s, id %s)\n", codeSafe(c.Name), orDash(c.Type), codeSafe(c.ID))
	}
	return b.String()
}

func feedbackRatingLabel(rating string) string {
	switch rating {
	case feedbackRatingWireDown:
		return "👎 Negative"
	case feedbackRatingWireUp:
		return "👍 Positive"
	}
	return orDash(mdSafe(rating))
}

func feedbackSubjectName(ref *analytics.AgentRef, safe func(string) string) string {
	return safe(subjectDisplayName(ref))
}

func subjectDisplayName(ref *analytics.AgentRef) string {
	if ref == nil {
		return "—"
	}
	return orDash(ref.Name)
}

func feedbackRowsTemplateData(rows []analytics.ActivityFeedbackRow, safe func(string) string, commentMaxRunes int, loc *time.Location) []map[string]any {
	out := make([]map[string]any, 0, len(rows))
	for i := range rows {
		r := &rows[i]
		comment := "—"
		if r.Comment != nil && *r.Comment != "" {
			comment = *r.Comment
			if commentMaxRunes > 0 {
				comment = truncateMid(comment, commentMaxRunes, 0)
			}
			comment = safe(comment)
		}
		out = append(out, map[string]any{
			"when":    safe(feedbackTime(r.UpdatedAt, loc)),
			"user":    orDash(safe(r.UserID)),
			"agent":   feedbackSubjectName(&r.Subject, safe),
			"rating":  feedbackRatingLabel(r.Rating),
			"comment": comment,
			"id":      safe(r.ID),
		})
	}
	return out
}

func userLocation(ctx context.Context, toolCtx *tool.Context, logger *slog.Logger) *time.Location {
	name := tool.GetUserTimezone(toolCtx)
	loc, err := time.LoadLocation(name)
	if err != nil {
		logger.WarnContext(ctx, "invalid user timezone, rendering UTC", "timezone", name, "error", err)
		return time.UTC
	}
	return loc
}

func feedbackTime(iso string, loc *time.Location) string {
	t, err := time.Parse(time.RFC3339Nano, iso)
	if err != nil {
		return orDash(iso)
	}
	return t.In(loc).Format("Jan 2, 3:04 PM MST")
}

var feedbackRowLists = []string{"data", "negativeSample"}

func withFeedbackDisplayFields(raw json.RawMessage, loc *time.Location) json.RawMessage {
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return raw
	}
	for _, key := range feedbackRowLists {
		rows, _ := doc[key].([]any)
		for _, r := range rows {
			row, ok := r.(map[string]any)
			if !ok {
				continue
			}
			updatedAt, _ := row["updatedAt"].(string)
			rating, _ := row["rating"].(string)
			row["when"] = feedbackTime(updatedAt, loc)
			row["ratingEmoji"] = feedbackRatingEmoji(rating)
		}
	}
	out, err := json.Marshal(doc)
	if err != nil {
		return raw
	}
	return out
}

func feedbackRatingEmoji(rating string) string {
	switch rating {
	case feedbackRatingWireDown:
		return "👎"
	case feedbackRatingWireUp:
		return "👍"
	}
	return ""
}

func feedbackParseFailed(err error) *tool.Result {
	return tool.ErrorFromCaller(fmt.Sprintf("Failed to parse feedback response: %s", err), tool.WithCause(err))
}
