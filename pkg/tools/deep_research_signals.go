// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"fmt"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func emitResearchProgress(
	toolCtx *tool.Context,
	phase, statusText string,
	progress int,
	iteration, totalIterations, sourcesFound int,
	currentQuery string,
	fetchingURLs []map[string]string,
	elapsed, maxRuntime int,
) {
	if toolCtx.SendSignal == nil {
		return
	}
	_ = toolCtx.SendSignal(a2a.DeepResearchProgressData{
		Type:               string(a2a.SignalDeepResearchProgress),
		Phase:              phase,
		StatusText:         statusText,
		ProgressPercentage: progress,
		CurrentIteration:   iteration,
		TotalIterations:    totalIterations,
		SourcesFound:       sourcesFound,
		CurrentQuery:       currentQuery,
		FetchingURLs:       fetchingURLs,
		ElapsedSeconds:     elapsed,
		MaxRuntimeSeconds:  maxRuntime,
	})
}

func emitRAGInfoUpdate(
	toolCtx *tool.Context,
	title, query string,
	sources []a2a.RAGSource,
	isComplete bool,
) {
	if toolCtx.SendSignal == nil {
		return
	}
	_ = toolCtx.SendSignal(a2a.RAGInfoUpdateData{
		Type:       string(a2a.SignalRAGInfoUpdate),
		Title:      title,
		Query:      query,
		SearchType: "deep_research",
		Sources:    sources,
		IsComplete: isComplete,
		Timestamp:  time.Now().UTC().Format(time.RFC3339),
	})
}

func emitResearchReport(
	toolCtx *tool.Context,
	filename string,
	version int,
	sessionID, title string,
	sourcesCount int,
) {
	if toolCtx.SendSignal == nil {
		return
	}
	uri := fmt.Sprintf("artifact://%s/%s?version=%d", sessionID, filename, version)
	_ = toolCtx.SendSignal(a2a.DeepResearchReportData{
		Type:         string(a2a.SignalDeepResearchReport),
		Filename:     filename,
		Version:      version,
		URI:          uri,
		Title:        title,
		SourcesCount: sourcesCount,
	})
}
