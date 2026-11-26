// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type reportGaveUpError struct {
	attempts    int
	interrupted bool
	rejected    bool
	cause       error
}

func (e *reportGaveUpError) Error() string { return "report generation gave up" }

func (e *reportGaveUpError) Unwrap() error { return e.cause }

const (
	reportFallbackLargeSourceCount = 8
	reportFallbackSmallSourceCount = 3
	partialResultSourceLimit       = 15
	partialResultSnippetMaxRunes   = 300
	partialResultNote              = "The synthesized report could not be generated because the AI provider rejected the report request at every input size. The sources and findings gathered are listed below and in the saved artifact."
	reportFailedResultNote         = "The synthesized report could not be generated because the AI provider returned an error. The sources and findings gathered are listed below and in the saved artifact."
	timedOutResultNote             = "The synthesized report could not be generated because the research ran out of time. The sources and findings gathered are listed below and in the saved artifact."
)

type reportAttempt struct {
	findings []researchFinding
	text     string
}

func rankedByRelevance(findings []researchFinding) []researchFinding {
	ranked := slices.Clone(findings)
	slices.SortStableFunc(ranked, func(a, b researchFinding) int {
		return cmp.Compare(b.RelevanceScore, a.RelevanceScore)
	})
	return ranked
}

func sourceSetKey(findings []researchFinding) string {
	keys := make([]string, len(findings))
	for i, f := range findings {
		keys[i] = fmt.Sprintf("%s|%s|%t", f.CitationID, f.URL, f.Fetched)
	}
	slices.Sort(keys)
	return strings.Join(keys, "\n")
}

func reportAttempts(findings []researchFinding) []reportAttempt {
	var attempts []reportAttempt
	seen := make(map[string]bool)
	add := func(candidate []researchFinding, text string) {
		key := sourceSetKey(candidate)
		if seen[key] {
			return
		}
		seen[key] = true
		attempts = append(attempts, reportAttempt{findings: candidate, text: text})
	}

	firstText, _ := prepareFindingsForReport(findings)
	add(findings, firstText)
	snippetOnly, _ := withoutFetchedContent(findings)
	ranked := rankedByRelevance(snippetOnly)
	for _, count := range []int{reportFallbackLargeSourceCount, reportFallbackSmallSourceCount} {
		text, included := prepareFindingsWithinBudget(ranked[:min(len(ranked), count)], reportPayloadMaxBytes)
		add(included, text)
	}
	return attempts
}

func generateReportWithFallback(ctx context.Context, logger *slog.Logger, llmCall llmCallFunc, question, title string, findings []researchFinding) (string, []researchFinding, error) {
	attempts := reportAttempts(findings)
	for i, attempt := range attempts {
		report, err := generateResearchReport(ctx, llmCall, question, title, attempt.text, len(attempt.findings))
		if err == nil {
			return report, attempt.findings, nil
		}
		if ctx.Err() != nil {
			return "", nil, &reportGaveUpError{attempts: i + 1, interrupted: true, cause: err}
		}
		llmErr, ok := errors.AsType[*llm.Error](err)
		if !ok || !llmErr.IsPayloadRejection() {
			return "", nil, &reportGaveUpError{attempts: i + 1, cause: err}
		}
		logger.Warn("report request rejected by the provider",
			"attempt", i+1,
			"attempts", len(attempts),
			"payloadBytes", len(attempt.text),
			"sources", len(attempt.findings),
			"httpStatus", llmErr.StatusCode)
		if i == len(attempts)-1 || (i >= 1 && llmErr.IsContentFilterRejection()) {
			return "", nil, &reportGaveUpError{attempts: i + 1, rejected: true, cause: err}
		}
	}
	return "", nil, &reportGaveUpError{}
}

func partialResearchSummary(findings []researchFinding, note string) string {
	var b strings.Builder
	b.WriteString("# Research sources and findings\n\n")
	b.WriteString(note)
	b.WriteString("\n\n")
	for i, f := range findings {
		fmt.Fprintf(&b, "%d. %s\n", i+1, plainText(f.Title))
		if f.URL != "" {
			fmt.Fprintf(&b, "   %s\n", f.URL)
		}
		snippet := strings.TrimSpace(plainText(f.Snippet))
		if snippet == "" {
			snippet = strings.TrimSpace(plainText(f.Content))
		}
		if runes := []rune(snippet); len(runes) > partialResultSnippetMaxRunes {
			snippet = string(runes[:partialResultSnippetMaxRunes]) + "..."
		}
		if snippet != "" {
			fmt.Fprintf(&b, "   %s\n", snippet)
		}
		b.WriteString("\n")
	}
	return b.String()
}

type partialResearch struct {
	gaveUp     *reportGaveUpError
	title      string
	question   string
	findings   []researchFinding
	iterations int
}

func (t *DeepResearchTool) partialResearchResult(ctx context.Context, toolCtx *tool.Context, logger *slog.Logger, in partialResearch) *tool.Result {
	gaveUp, title, question, findings, iterations := in.gaveUp, in.title, in.question, in.findings, in.iterations
	note := reportFailedResultNote
	switch {
	case gaveUp.interrupted:
		note = timedOutResultNote
		logger.Warn("report generation ran out of time; returning the gathered sources",
			"attempts", gaveUp.attempts,
			"sources", len(findings))
	default:
		if gaveUp.rejected {
			note = partialResultNote
		}
		logger.Error("report generation gave up; returning the gathered sources",
			"attempts", gaveUp.attempts,
			"sources", len(findings))
	}

	ranked := rankedByRelevance(findings)
	filename := sanitizeToFilename(question, 50, "_deep_research_sources.md")
	version := 0
	ragSources := findingsToRAGSources(findings)
	data := map[string]any{
		"status":               "partial",
		"reason":               note,
		"total_sources":        len(findings),
		"iterations_completed": iterations,
		"rag_metadata":         ragSources,
	}
	if facade := toolCtx.Facade(); facade != nil {
		v, err := facade.SaveArtifact(ctx, filename, []byte(partialResearchSummary(ranked, note)), "text/markdown")
		if err != nil {
			logger.Error("failed to save partial research artifact", "errorClass", fmt.Sprintf("%T", err))
		} else {
			version = v
			data["artifact_filename"] = filename
			data["artifact_version"] = version
			data["response_artifact"] = map[string]any{"filename": filename, "version": version}
			emitResearchReport(toolCtx, filename, version, toolCtx.SessionID, title, len(findings))
		}
	}
	emitRAGInfoUpdate(toolCtx, title, question, ragSources, true)

	return tool.Partial(partialResearchSummary(ranked[:min(len(ranked), partialResultSourceLimit)], note), tool.WithData(data))
}
