// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/logging"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/websearch"
)

var minRuntimeSeconds = 60

const (
	defaultMaxIterationsQuick = 3
	defaultMaxIterationsDeep  = 10
	defaultMaxRuntimeQuick    = 300
	defaultMaxRuntimeDeep     = 600
	defaultMaxSourcesPerIter  = 5
	fetchTimeout              = 30 * time.Second
	reportGenerationBudget    = 3 * time.Minute
	fetchMaxSize              = 1024 * 1024
)

type researchFinding struct {
	Title          string
	URL            string
	Content        string
	Snippet        string
	CitationID     string
	SourceType     string
	RelevanceScore float64
	Fetched        bool
}

type DeepResearchTool struct {
	tool.SideEffectingTool
	authoredSchemas
	artifactToolBase
	config map[string]any
}

func NewDeepResearchTool(config map[string]any) *DeepResearchTool {
	if config == nil {
		config = make(map[string]any)
	}
	return &DeepResearchTool{config: config}
}

func (t *DeepResearchTool) Name() string { return "deep_research" }

func (t *DeepResearchTool) Description() string {
	return "Performs deep, iterative research on a topic using web search and LLM analysis. Produces a comprehensive research report with citations. Takes 3-10 minutes depending on depth."
}

func (t *DeepResearchTool) IsLongRunning() bool { return true }

func (t *DeepResearchTool) Parameters() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type: "object",
		Properties: map[string]any{
			"research_question": map[string]any{
				"type":        "string",
				"description": "The research question or topic to investigate.",
			},
			"research_type": map[string]any{
				"type":        "string",
				"description": "Type of research: 'quick' (3 iterations, 5 min) or 'in-depth' (10 iterations, 10 min). Default: 'quick'.",
				"enum":        []string{"quick", "in-depth"},
			},
			"max_iterations": map[string]any{
				"type":        "integer",
				"description": "Override maximum number of research iterations.",
			},
			"max_runtime_minutes": map[string]any{
				"type":        "integer",
				"description": "Maximum runtime in minutes (1-10). The software converts this to seconds internally. Overrides tool_config and research_type if provided.",
			},
			"max_runtime_seconds": map[string]any{
				"type":        "integer",
				"description": "Maximum runtime in seconds (60-600). Use max_runtime_minutes instead for easier specification. Overrides tool_config and research_type if provided.",
			},
			"sources": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "string",
					"enum": []string{"web", "kb"},
				},
				"description": "Sources to search. Default from tool_config or ['web']. 'web' searches the web through the configured web search provider.",
			},
			"kb_ids": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "string",
				},
				"description": "Specific knowledge base IDs to search (only used if 'kb' is in sources)",
			},
		},
		Required: []string{"research_question"},
	}
}

func (t *DeepResearchTool) ResultSchema() *tool.JSONSchema {
	return resultSchema(
		resultField{name: "status", typ: "string",
			description: "\"success\" when a report was produced, \"partial\" when the report could not be generated and only the gathered sources were saved, \"cancelled\" when plan verification timed out or the user declined."},
		resultField{name: "reason", typ: "string", optional: true,
			description: "Cancellation cause on a cancelled run (\"timeout\" or \"user_cancelled\"), or the explanation on a partial run."},
		resultField{name: "plan_id", typ: "string", optional: true,
			description: "Identifies the research plan that was not confirmed. Cancelled runs only."},
		resultField{name: "total_sources", typ: "integer", optional: true,
			description: "Number of findings gathered. Successful runs only, and at least 1 — zero sources is an error, not a success."},
		resultField{name: "iterations_completed", typ: "integer", optional: true,
			description: "Research loop iterations performed. Successful runs only."},
		resultField{name: "rag_metadata", typ: "array", optional: true,
			description: "Per-source citation records. Successful runs only. Element keys vary: only citationId, contentPreview and relevanceScore are always present."},
		resultField{name: "artifact_filename", typ: "string", optional: true,
			description: "Artifact holding the generated report. Successful runs only. Bind downstream nodes to this."},
		resultField{name: "artifact_version", typ: "integer", optional: true,
			description: "Version of the report artifact. Successful runs only. 0 is ambiguous — it is both a legitimate first version and what is reported when the save was skipped."},
		resultField{name: "response_artifact", typ: "object", optional: true,
			description: "The report artifact as {filename, version}, duplicating the two fields above. Successful runs only."},
	)
}

func (t *DeepResearchTool) Execute(ctx context.Context, args map[string]any, toolCtx *tool.Context) (*tool.Result, error) {
	question, _ := args["research_question"].(string)
	if question == "" {
		return tool.ValidationErr("research_question is required"), nil
	}

	if toolCtx.InternalLLMCall == nil {
		return tool.Error("deep_research requires an LLM client (InternalLLMCall not available)"), nil
	}

	cfg := t.resolveConfig(args, toolCtx)
	logger := slog.Default().With("logger_name", "deep_research", "traceID", toolCtx.TraceID, "taskID", toolCtx.ParentTaskID, "sessionID", toolCtx.SessionID)

	safeSearch, err := websearch.SafeSearch(cfg.merged)
	if err != nil {
		return websearch.SafeSearchConfigError(t.Name(), err), nil
	}
	provider, configErr := buildSearchProvider(cfg.merged)
	if configErr != nil {
		return configErr, nil
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, time.Duration(cfg.maxRuntimeSeconds)*time.Second)
	defer cancel()

	startTime := time.Now()
	maxRuntime := cfg.maxRuntimeSeconds

	emitResearchProgress(toolCtx, "planning",
		"Planning research strategy and generating search queries...",
		5, 0, cfg.maxIterations, 0, "", nil,
		0, maxRuntime)

	queryLLM := llmCallForPhase(toolCtx, phaseQueryGeneration, cfg.merged)
	queries, _ := generateInitialQueries(timeoutCtx, queryLLM, question)
	logger.Debug("generated initial queries", "count", len(queries))

	title := generateResearchTitle(timeoutCtx, queryLLM, question)
	logger.Debug("generated research title")

	emitRAGInfoUpdate(toolCtx, title, question, nil, false)

	verificationFlagOn := true
	if v, ok := cfg.merged["interactive_plan_verification"].(bool); ok {
		verificationFlagOn = v
	}
	clientSupportsVerification := isInteractivePlanClient(toolCtx, cfg.merged)
	interactiveVerification := verificationFlagOn && clientSupportsVerification

	if verificationFlagOn {
		planSteps := generateResearchPlan(timeoutCtx, queryLLM, question, queries)
		logger.Debug("generated research plan steps", "count", len(planSteps))

		if interactiveVerification {
			planID := newPlanID()
			logger.Info("requesting plan verification", "planID", planID)

			researchTypeNorm, _ := args["research_type"].(string)
			if researchTypeNorm == "" {
				researchTypeNorm = "quick"
			}
			outcome := runPlanVerification(
				timeoutCtx,
				toolCtx,
				planID,
				title,
				question,
				researchTypeNorm,
				planSteps,
				cfg.sources,
				cfg.maxIterations,
				cfg.maxRuntimeSeconds,
			)
			if outcome.Cancelled {
				if outcome.CancelledTimeout {
					return tool.OK(fmt.Sprintf(
						"Research was cancelled because the plan was not confirmed "+
							"within %d seconds. Do not retry; acknowledge to the user "+
							"and wait for their next instruction.",
						planResponseTimeoutSeconds,
					), tool.WithData(map[string]any{
						"status":  "cancelled",
						"reason":  "timeout",
						"plan_id": planID,
					})), nil
				}
				return tool.OK(
					"The user explicitly cancelled this research. Do NOT re-invoke "+
						"deep research on this request. Acknowledge the cancellation "+
						"briefly and wait for the user's next instruction.",
					tool.WithData(map[string]any{
						"status":  "cancelled",
						"reason":  "user_cancelled",
						"plan_id": planID,
					}),
				), nil
			}

			if !slices.Equal(outcome.Steps, planSteps) {
				logger.Info("plan steps edited; regenerating queries from steps")
				queries = regenerateQueriesFromSteps(timeoutCtx, queryLLM, outcome.Steps, question)
			}
		}
		startTime = time.Now()
	}

	var allFindings []researchFinding
	searchFailures := &searchFailureLog{}
	seenURLs := make(map[string]bool)
	citationCounter := 0
	lastIteration := 0

	for iter := 1; iter <= cfg.maxIterations; iter++ {
		lastIteration = iter
		elapsed := int(time.Since(startTime).Seconds())

		if timeoutCtx.Err() != nil {
			logger.Info("research timeout reached", "iteration", iter)
			emitResearchProgress(toolCtx, "writing",
				fmt.Sprintf("Research time limit reached (%ds). Proceeding to generate report from %d sources...",
					elapsed, len(allFindings)),
				80, iter, cfg.maxIterations, len(allFindings), "", nil,
				elapsed, maxRuntime)
			break
		}

		logger.Debug("starting iteration", "iteration", iter, "maxIterations", cfg.maxIterations)

		in := iterationInputs{
			ctx:        timeoutCtx,
			toolCtx:    toolCtx,
			cfg:        cfg,
			provider:   provider,
			safeSearch: safeSearch,
			failures:   searchFailures,
			iter:       iter,
			startTime:  startTime,
			maxRuntime: maxRuntime,
			title:      title,
			question:   question,
			logger:     logger,
		}
		var terminate bool
		allFindings, citationCounter, queries, terminate = t.iterateOnce(in, queries, allFindings, seenURLs, citationCounter)
		if terminate {
			break
		}
	}

	if len(allFindings) == 0 {
		if cause := searchFailures.cause(); cause != nil {
			return tool.Error(fmt.Sprintf("Deep research search failed: %v", cause), websearch.ErrorCode(cause)), nil
		}
		return tool.Error("Deep research found no sources. Check search API configuration."), nil
	}

	elapsed := int(time.Since(startTime).Seconds())
	emitResearchProgress(toolCtx, "writing",
		fmt.Sprintf("Writing comprehensive research report from %d sources...", len(allFindings)),
		85, cfg.maxIterations, cfg.maxIterations, len(allFindings), "", nil,
		elapsed, maxRuntime)

	reportLLM := llmCallForPhase(toolCtx, phaseReportGeneration, cfg.merged)
	reportCtx, cancelReport := context.WithTimeout(ctx, reportGenerationBudget)
	defer cancelReport()
	report, usedFindings, err := generateReportWithFallback(reportCtx, logger, reportLLM, question, title, allFindings)
	if gaveUp, ok := errors.AsType[*reportGaveUpError](err); ok {
		if ctx.Err() != nil {
			return tool.Error(fmt.Sprintf("Failed to generate report: %s", llm.SanitizeErrorText(gaveUp.cause)), llmErrorCode(gaveUp.cause)), nil
		}
		return t.partialResearchResult(ctx, toolCtx, logger, partialResearch{
			gaveUp:     gaveUp,
			title:      title,
			question:   question,
			findings:   allFindings,
			iterations: lastIteration,
		}), nil
	}
	if err != nil {
		return tool.Error(fmt.Sprintf("Failed to generate report: %s", llm.SanitizeErrorText(err)), llmErrorCode(err)), nil
	}
	allFindings = usedFindings

	report += generateSourcesSection(allFindings)
	report += generateMethodologySection(allFindings)

	filename := sanitizeToFilename(question, 50, "_deep_research_report.md")
	version := 0

	facade := toolCtx.Facade()
	if facade != nil {
		v, saveErr := facade.SaveArtifact(ctx, filename, []byte(report), "text/markdown")
		if saveErr != nil {
			logger.Error("failed to save report artifact", "error", saveErr)
		} else {
			version = v
		}
	}

	elapsed = int(time.Since(startTime).Seconds())
	emitResearchProgress(toolCtx, "writing",
		fmt.Sprintf("✅ Research complete! Report saved as '%s'", filename),
		100, cfg.maxIterations, cfg.maxIterations, len(allFindings), "", nil,
		elapsed, maxRuntime)

	emitResearchReport(toolCtx, filename, version, toolCtx.SessionID, title, len(allFindings))

	ragSources := findingsToRAGSources(allFindings)
	emitRAGInfoUpdate(toolCtx, title, question, ragSources, true)

	resultMsg := fmt.Sprintf(
		"Deep research complete: analyzed %d sources. "+
			"The comprehensive report '%s' (version %d) "+
			"has been sent to the user and will be displayed automatically. "+
			"Do NOT include any artifact embeds or summarize the report - it is already being displayed.",
		len(allFindings), filename, version)

	return tool.OK(resultMsg, tool.WithData(map[string]any{
		"status":               "success",
		"total_sources":        len(allFindings),
		"iterations_completed": lastIteration,
		"rag_metadata":         ragSources,
		"artifact_filename":    filename,
		"artifact_version":     version,
		"response_artifact":    map[string]any{"filename": filename, "version": version},
	})), nil
}

type searchFailureLog struct {
	failure   error
	succeeded bool
}

func (l *searchFailureLog) recordFailure(err error) {
	if l.failure == nil || (isContextError(l.failure) && !isContextError(err)) {
		l.failure = err
	}
}

func (l *searchFailureLog) recordSuccess() { l.succeeded = true }

func (l *searchFailureLog) cause() error {
	if l.succeeded {
		return nil
	}
	return l.failure
}

func isContextError(err error) bool {
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)
}

type iterationInputs struct {
	ctx        context.Context
	toolCtx    *tool.Context
	cfg        deepResearchConfig
	provider   websearch.SearchProvider
	safeSearch bool
	failures   *searchFailureLog
	iter       int
	startTime  time.Time
	maxRuntime int
	title      string
	question   string
	logger     *slog.Logger
}

func (t *DeepResearchTool) iterateOnce(
	in iterationInputs,
	queries []string,
	allFindings []researchFinding,
	seenURLs map[string]bool,
	citationCounter int,
) ([]researchFinding, int, []string, bool) {
	elapsed := int(time.Since(in.startTime).Seconds())

	for _, query := range queries {
		displayQuery := query
		if len(displayQuery) > 60 {
			displayQuery = displayQuery[:60]
		}

		emitResearchProgress(in.toolCtx, "searching",
			fmt.Sprintf("%s...", displayQuery),
			int(progressPercent(in.iter, in.cfg.maxIterations, 0.1, 0.7)),
			in.iter, in.cfg.maxIterations, len(allFindings), query, nil,
			elapsed, in.maxRuntime)

		results, err := in.provider.Search(in.ctx, query, in.cfg.maxSourcesPerIter, websearch.SearchOptions{SafeSearch: in.safeSearch})
		if err != nil {
			in.logger.Warn("search failed", "error", err)
			in.failures.recordFailure(err)
			continue
		}
		in.failures.recordSuccess()

		for _, r := range results {
			dedupKey := r.Link
			if dedupKey == "" {
				dedupKey = "web:" + r.Title
			}
			if seenURLs[dedupKey] {
				continue
			}
			seenURLs[dedupKey] = true
			allFindings = append(allFindings, researchFinding{
				Title:          r.Title,
				URL:            r.Link,
				Content:        r.Snippet,
				Snippet:        r.Snippet,
				CitationID:     fmt.Sprintf("research%d", citationCounter),
				SourceType:     "web",
				RelevanceScore: 0.85,
				Fetched:        false,
			})
			citationCounter++
		}
	}

	if len(allFindings) == 0 {
		in.logger.Warn("no findings after search phase", "iteration", in.iter)
		return allFindings, citationCounter, queries, true
	}

	ragSources := findingsToRAGSources(allFindings)
	emitRAGInfoUpdate(in.toolCtx, in.title, in.question, ragSources, false)

	elapsed = int(time.Since(in.startTime).Seconds())
	unfetched := unfetchedFindings(allFindings)
	var fetchingURLList []map[string]string
	var selectedIndices []int

	if len(unfetched) > 0 {
		sourcesToFetch := 3
		if len(unfetched) < sourcesToFetch {
			sourcesToFetch = len(unfetched)
		}
		var err error
		selectionLLM := llmCallForPhase(in.toolCtx, phaseSourceSelection, in.cfg.merged)
		selectedIndices, err = selectSourcesToFetch(in.ctx, selectionLLM, in.question, unfetched, sourcesToFetch)
		if err == nil && len(selectedIndices) > 0 {
			for _, idx := range selectedIndices {
				if idx < len(unfetched) {
					src := unfetched[idx]
					favicon := ""
					if src.URL != "" {
						favicon = fmt.Sprintf("https://www.google.com/s2/favicons?domain=%s&sz=32", src.URL)
					}
					fetchingURLList = append(fetchingURLList, map[string]string{
						"url":         src.URL,
						"title":       src.Title,
						"favicon":     favicon,
						"source_type": src.SourceType,
					})
				}
			}
		}
	}

	emitResearchProgress(in.toolCtx, "analyzing",
		fmt.Sprintf("Analyzing %d sources (reading %d in detail)...",
			len(allFindings), len(fetchingURLList)),
		int(progressPercent(in.iter, in.cfg.maxIterations, 0.3, 0.7)),
		in.iter, in.cfg.maxIterations, len(allFindings), "", fetchingURLList,
		elapsed, in.maxRuntime)

	if len(selectedIndices) > 0 {
		fetchBase, _ := in.cfg.merged["fetch_base_url"].(string)
		allowLoopback, _ := in.cfg.merged["allow_loopback"].(bool)
		t.fetchSources(in.ctx, unfetched, selectedIndices, allFindings, fetchBase, allowLoopback, in.logger)
	}

	elapsed = int(time.Since(in.startTime).Seconds())
	emitResearchProgress(in.toolCtx, "analyzing",
		fmt.Sprintf("Analyzing %d sources and identifying knowledge gaps...",
			len(allFindings)),
		int(progressPercent(in.iter, in.cfg.maxIterations, 0.5, 0.7)),
		in.iter, in.cfg.maxIterations, len(allFindings), "", fetchingURLList,
		elapsed, in.maxRuntime)

	summary := prepareFindingsSummary(allFindings)
	reflectionLLM := llmCallForPhase(in.toolCtx, phaseReflection, in.cfg.merged)
	reflection, err := reflectOnFindings(in.ctx, reflectionLLM, in.question, summary, in.iter, in.cfg.maxIterations)
	if err != nil {
		in.logger.Warn("reflection failed", "error", err)
		return allFindings, citationCounter, queries, true
	}

	in.logger.Debug("reflection result",
		"quality", reflection.QualityScore,
		"continue", reflection.ShouldContinue,
		"gaps", len(reflection.Gaps))

	if !reflection.ShouldContinue {
		return allFindings, citationCounter, queries, true
	}

	if len(reflection.SuggestedQueries) > 0 {
		queries = reflection.SuggestedQueries
	}
	return allFindings, citationCounter, queries, false
}

func buildSearchProvider(cfg map[string]any) (websearch.SearchProvider, *tool.Result) {
	if websearch.NamesProvider(cfg) {
		provider, err := websearch.ResolveProvider(cfg)
		if err != nil {
			return nil, tool.Error(err.Error())
		}
		return provider, nil
	}
	apiKey, cx, apiBase, err := websearch.GoogleConfig(cfg)
	if err != nil {
		return nil, tool.Error(err.Error())
	}
	if apiKey == "" || cx == "" {
		return nil, tool.Error("Google Search API key and CSE ID must be configured (google_search_api_key, google_cse_id)")
	}
	if apiBase == "" {
		return nil, tool.Error("Google Search 'api_base' must be configured — no public-endpoint fallback")
	}
	return websearch.NewGoogleSearchProvider(apiKey, cx, apiBase), nil
}

type deepResearchConfig struct {
	maxIterations     int
	maxRuntimeSeconds int
	maxSourcesPerIter int
	sources           []string
	kbIDs             []string
	merged            map[string]any
}

func (t *DeepResearchTool) resolveConfig(args map[string]any, toolCtx *tool.Context) deepResearchConfig {
	researchType, _ := args["research_type"].(string)
	maxIter := defaultMaxIterationsQuick
	maxRuntime := defaultMaxRuntimeQuick
	maxSources := defaultMaxSourcesPerIter

	switch strings.ToLower(researchType) {
	case "in-depth", "indepth", "in_depth", "deep", "comprehensive":
		maxIter = defaultMaxIterationsDeep
		maxRuntime = defaultMaxRuntimeDeep
	}

	merged := make(map[string]any, len(t.config)+len(toolCtx.ToolConfig))
	for k, v := range t.config {
		merged[k] = v
	}
	for k, v := range toolCtx.ToolConfig {
		merged[k] = v
	}

	if v, ok := merged["max_iterations"].(float64); ok {
		maxIter = int(v)
	} else if v, ok := merged["max_iterations"].(int); ok {
		maxIter = v
	}

	if v := configInt(merged, "max_runtime_seconds"); v > 0 {
		maxRuntime = v
	} else if v := configInt(merged, "duration_seconds"); v > 0 {
		maxRuntime = v
	} else if v := configInt(merged, "duration_minutes"); v > 0 {
		maxRuntime = v * 60
	}

	if v, ok := args["max_iterations"].(float64); ok {
		maxIter = int(v)
	}
	if v, ok := args["max_runtime_minutes"].(float64); ok {
		maxRuntime = int(v) * 60
	} else if v, ok := args["max_runtime_seconds"].(float64); ok {
		maxRuntime = int(v)
	}
	if v := configInt(merged, "max_sources_per_iteration"); v > 0 {
		maxSources = v
	}

	if maxRuntime < minRuntimeSeconds {
		maxRuntime = minRuntimeSeconds
	}
	if maxRuntime > 600 {
		maxRuntime = 600
	}

	sources := resolveSourcesList(args, merged)

	var kbIDs []string
	if arr, ok := args["kb_ids"].([]any); ok {
		for _, v := range arr {
			if s, ok := v.(string); ok {
				kbIDs = append(kbIDs, s)
			}
		}
	}

	return deepResearchConfig{
		maxIterations:     maxIter,
		maxRuntimeSeconds: maxRuntime,
		maxSourcesPerIter: maxSources,
		sources:           sources,
		kbIDs:             kbIDs,
		merged:            merged,
	}
}

func configInt(m map[string]any, key string) int {
	if v, ok := m[key].(float64); ok {
		return int(v)
	}
	if v, ok := m[key].(int); ok {
		return v
	}
	return 0
}

func resolveSourcesList(args map[string]any, merged map[string]any) []string {
	allowed := map[string]bool{"web": true, "kb": true}

	if arr, ok := args["sources"].([]any); ok {
		var sources []string
		for _, v := range arr {
			if s, ok := v.(string); ok && allowed[s] {
				sources = append(sources, s)
			}
		}
		if len(sources) > 0 {
			return sources
		}
	}

	if arr, ok := merged["sources"].([]any); ok {
		var sources []string
		for _, v := range arr {
			if s, ok := v.(string); ok && allowed[s] {
				sources = append(sources, s)
			}
		}
		if len(sources) > 0 {
			return sources
		}
	}
	if arr, ok := merged["sources"].([]string); ok {
		var sources []string
		for _, s := range arr {
			if allowed[s] {
				sources = append(sources, s)
			}
		}
		if len(sources) > 0 {
			return sources
		}
	}

	return []string{"web"}
}

type researchPhase string

const (
	phaseQueryGeneration  researchPhase = "query_generation"
	phaseReflection       researchPhase = "reflection"
	phaseSourceSelection  researchPhase = "source_selection"
	phaseReportGeneration researchPhase = "report_generation"
)

func getModelForPhase(phase researchPhase, merged map[string]any) string {
	phaseStr := string(phase)

	if models, ok := merged["models"].(map[string]any); ok {
		if model, ok := models[phaseStr].(string); ok && model != "" {
			return model
		}
	}

	if modelConfigs, ok := merged["model_configs"].(map[string]any); ok {
		if phaseConfig, ok := modelConfigs[phaseStr].(map[string]any); ok {
			if model, ok := phaseConfig["model"].(string); ok && model != "" {
				return model
			}
		}
	}

	return ""
}

func getTemperatureForPhase(phase researchPhase, merged map[string]any, defaultTemp float64) float64 {
	if modelConfigs, ok := merged["model_configs"].(map[string]any); ok {
		if phaseConfig, ok := modelConfigs[string(phase)].(map[string]any); ok {
			if temp, ok := phaseConfig["temperature"].(float64); ok {
				return temp
			}
		}
	}
	return defaultTemp
}

func llmCallForPhase(toolCtx *tool.Context, phase researchPhase, merged map[string]any) llmCallFunc {
	model := getModelForPhase(phase, merged)

	if model == "" || toolCtx.InternalLLMCallWithModel == nil {
		return toolCtx.InternalLLMCall
	}

	return func(ctx context.Context, systemPrompt, userPrompt string, temperature float64) (string, error) {
		temp := getTemperatureForPhase(phase, merged, temperature)
		return toolCtx.InternalLLMCallWithModel(ctx, model, systemPrompt, userPrompt, temp)
	}
}

func (t *DeepResearchTool) fetchSources(
	ctx context.Context,
	unfetched []researchFinding,
	selectedIndices []int,
	allFindings []researchFinding,
	fetchBaseURL string,
	allowLoopback bool,
	logger *slog.Logger,
) {
	client := newSSRFSafeClient(fetchTimeout, allowLoopback)

	for _, idx := range selectedIndices {
		if idx >= len(unfetched) {
			continue
		}
		f := unfetched[idx]

		fetchURL := f.URL
		if fetchBaseURL != "" {
			fetchURL = fetchBaseURL + "?url=" + url.QueryEscape(f.URL)
		}

		if !IsSafeURL(fetchURL, allowLoopback) {
			logger.Warn("skipping unsafe fetch URL", "url", logging.RedactURL(f.URL))
			continue
		}

		content, err := fetchPageContent(ctx, client, fetchURL)
		if err != nil {
			logger.Debug("failed to fetch page",
				"url", logging.RedactURL(f.URL),
				"error", logging.RedactURLQueries(err.Error()))
			continue
		}

		for i := range allFindings {
			if allFindings[i].CitationID == f.CitationID {
				allFindings[i].Content = content
				allFindings[i].Fetched = true
				break
			}
		}
	}
}

func withoutFetchedContent(findings []researchFinding) ([]researchFinding, bool) {
	reduced := slices.Clone(findings)
	dropped := false
	for i := range reduced {
		if reduced[i].Fetched {
			reduced[i].Content = reduced[i].Snippet
			reduced[i].Fetched = false
			dropped = true
		}
	}
	return reduced, dropped
}

func pageHTMLToText(page string) (string, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(page))
	if err != nil {
		return "", err
	}
	doc.Find("script, style, svg, noscript").Remove()
	doc.Find("*").Contents().Each(func(_ int, s *goquery.Selection) {
		if goquery.NodeName(s) == "#comment" {
			s.Remove()
		}
	})
	body, err := doc.Find("body").Html()
	if err != nil {
		return "", err
	}
	return htmlToMarkdown(body)
}

func pageMediaType(header http.Header, body []byte) string {
	mediaType, _, err := mime.ParseMediaType(header.Get("Content-Type"))
	if err != nil || mediaType == "" {
		mediaType, _, _ = mime.ParseMediaType(http.DetectContentType(body))
	}
	return mediaType
}

var readableTextMediaTypes = []string{
	"text/plain",
	"text/markdown",
	"text/x-markdown",
	"text/csv",
	"text/tab-separated-values",
	"application/json",
}

func isReadableTextMediaType(mediaType string) bool {
	return slices.Contains(readableTextMediaTypes, mediaType) || strings.HasSuffix(mediaType, "+json")
}

func fetchPageContent(ctx context.Context, client *http.Client, rawURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", defaultUserAgent)

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, fetchMaxSize))
	if err != nil {
		return "", err
	}

	var text string
	switch mediaType := pageMediaType(resp.Header, body); mediaType {
	case "text/html", "application/xhtml+xml":
		text, err = pageHTMLToText(string(body))
		if err != nil {
			return "", err
		}
	default:
		if !isReadableTextMediaType(mediaType) {
			return "", fmt.Errorf("unsupported content type %q", mediaType)
		}
		text = string(body)
	}
	if strings.TrimSpace(text) == "" {
		return "", errors.New("page has no readable text")
	}
	return text, nil
}

func unfetchedFindings(findings []researchFinding) []researchFinding {
	var result []researchFinding
	for _, f := range findings {
		if !f.Fetched {
			result = append(result, f)
		}
	}
	return result
}

func findingsToRAGSources(findings []researchFinding) []a2a.RAGSource {
	sources := make([]a2a.RAGSource, 0, len(findings))
	now := time.Now().UTC().Format(time.RFC3339)
	for i, f := range findings {
		previewLen := 200
		if f.Fetched {
			previewLen = 500
		}
		preview := truncate(f.Content, previewLen)

		fileID := fmt.Sprintf("deep_research_%d", i+1)

		favicon := ""
		if f.URL != "" {
			favicon = fmt.Sprintf("https://www.google.com/s2/favicons?domain=%s&sz=32", f.URL)
		}

		sources = append(sources, a2a.RAGSource{
			CitationID:     f.CitationID,
			FileID:         fileID,
			Filename:       f.Title,
			Title:          f.Title,
			SourceType:     f.SourceType,
			SourceURL:      sourceURLOrNA(f.URL),
			URL:            f.URL,
			ContentPreview: preview,
			RelevanceScore: f.RelevanceScore,
			RetrievedAt:    now,
			Metadata: map[string]any{
				"title":       f.Title,
				"link":        f.URL,
				"type":        "web_search",
				"source_type": f.SourceType,
				"favicon":     favicon,
				"fetched":     f.Fetched,
			},
		})
	}
	return sources
}

func progressPercent(iteration, maxIterations int, rangeStart, rangeEnd float64) float64 {
	if maxIterations <= 0 {
		return rangeStart * 100
	}
	frac := float64(iteration) / float64(maxIterations)
	return (rangeStart + frac*(rangeEnd-rangeStart)) * 100
}

func sourceURLOrNA(url string) string {
	if url == "" {
		return "N/A"
	}
	return url
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
