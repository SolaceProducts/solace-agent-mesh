// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/websearch"
)

func newDRToolCtx(args, toolConfig map[string]any) (*DeepResearchTool, *tool.Context) {
	return &DeepResearchTool{}, &tool.Context{
		AgentName:    "test-agent",
		UserID:       "u",
		SessionID:    "s",
		ParentTaskID: "t",
		ToolConfig:   toolConfig,
	}
}

func TestDeepResearch_ToolIdentity(t *testing.T) {
	dr := &DeepResearchTool{}
	if dr.Name() != "deep_research" {
		t.Errorf("Name = %q, want %q (must match Python's `deep_research`)", dr.Name(), "deep_research")
	}
	if !dr.IsLongRunning() {
		t.Error("IsLongRunning should be true (deep research takes 3-10 minutes)")
	}
}

func TestDeepResearch_ParameterSchemaMatchesPython(t *testing.T) {
	dr := &DeepResearchTool{}
	schema := dr.Parameters()
	if schema.Type != "object" {
		t.Errorf("schema type = %q, want %q", schema.Type, "object")
	}

	wantProps := []string{
		"research_question", "research_type",
		"max_iterations", "max_runtime_minutes", "max_runtime_seconds",
		"sources", "kb_ids",
	}
	for _, p := range wantProps {
		if _, ok := schema.Properties[p]; !ok {
			t.Errorf("schema missing property %q", p)
		}
	}

	if _, exposed := schema.Properties["max_sources_per_iteration"]; exposed {
		t.Error("max_sources_per_iteration must not be in the LLM-facing schema (Python only allows it as a function default / tool_config knob, not a schema parameter)")
	}

	if len(schema.Required) != 1 || schema.Required[0] != "research_question" {
		t.Errorf("schema.Required = %v, want [research_question]", schema.Required)
	}

	rt, _ := schema.Properties["research_type"].(map[string]any)
	enum, _ := rt["enum"].([]string)
	if len(enum) != 2 || enum[0] != "quick" || enum[1] != "in-depth" {
		t.Errorf("research_type enum = %v, want [quick in-depth]", enum)
	}

	sources, _ := schema.Properties["sources"].(map[string]any)
	items, _ := sources["items"].(map[string]any)
	srcEnum, _ := items["enum"].([]string)
	if len(srcEnum) != 2 || srcEnum[0] != "web" || srcEnum[1] != "kb" {
		t.Errorf("sources.items.enum = %v, want [web kb]", srcEnum)
	}
}

func TestDeepResearch_ResearchTypeDefaults(t *testing.T) {
	cases := []struct {
		name       string
		args       map[string]any
		wantIter   int
		wantRuntim int
	}{
		{"default (no research_type) is quick", map[string]any{}, 3, 300},
		{"explicit quick", map[string]any{"research_type": "quick"}, 3, 300},
		{"explicit in-depth", map[string]any{"research_type": "in-depth"}, 10, 600},
		{"case-insensitive IN-DEPTH", map[string]any{"research_type": "IN-DEPTH"}, 10, 600},
		{"underscore indepth", map[string]any{"research_type": "in_depth"}, 10, 600},
		{"alias deep", map[string]any{"research_type": "deep"}, 10, 600},
		{"unknown type falls back to quick", map[string]any{"research_type": "ultra-deep"}, 3, 300},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dr, toolCtx := newDRToolCtx(nil, nil)
			cfg := dr.resolveConfig(tc.args, toolCtx)
			if cfg.maxIterations != tc.wantIter {
				t.Errorf("maxIterations = %d, want %d", cfg.maxIterations, tc.wantIter)
			}
			if cfg.maxRuntimeSeconds != tc.wantRuntim {
				t.Errorf("maxRuntimeSeconds = %d, want %d", cfg.maxRuntimeSeconds, tc.wantRuntim)
			}
		})
	}
}

func TestDeepResearch_ExplicitParamsWinOverResearchType(t *testing.T) {
	t.Run("max_iterations overrides research_type", func(t *testing.T) {
		dr, toolCtx := newDRToolCtx(nil, nil)
		cfg := dr.resolveConfig(map[string]any{
			"research_type":  "quick",
			"max_iterations": float64(7),
		}, toolCtx)
		if cfg.maxIterations != 7 {
			t.Errorf("maxIterations = %d, want 7", cfg.maxIterations)
		}
		if cfg.maxRuntimeSeconds != 300 {
			t.Errorf("maxRuntimeSeconds = %d, want 300 (unchanged by max_iterations)", cfg.maxRuntimeSeconds)
		}
	})

	t.Run("max_runtime_minutes overrides research_type", func(t *testing.T) {
		dr, toolCtx := newDRToolCtx(nil, nil)
		cfg := dr.resolveConfig(map[string]any{
			"research_type":       "quick",
			"max_runtime_minutes": float64(8),
		}, toolCtx)
		if cfg.maxRuntimeSeconds != 480 {
			t.Errorf("maxRuntimeSeconds = %d, want 480 (8 minutes)", cfg.maxRuntimeSeconds)
		}
	})

	t.Run("max_runtime_minutes wins over max_runtime_seconds", func(t *testing.T) {
		dr, toolCtx := newDRToolCtx(nil, nil)
		cfg := dr.resolveConfig(map[string]any{
			"max_runtime_minutes": float64(4),
			"max_runtime_seconds": float64(200),
		}, toolCtx)
		if cfg.maxRuntimeSeconds != 240 {
			t.Errorf("maxRuntimeSeconds = %d, want 240 (4 min wins over 200 sec)", cfg.maxRuntimeSeconds)
		}
	})

	t.Run("max_runtime_seconds without minutes", func(t *testing.T) {
		dr, toolCtx := newDRToolCtx(nil, nil)
		cfg := dr.resolveConfig(map[string]any{
			"max_runtime_seconds": float64(450),
		}, toolCtx)
		if cfg.maxRuntimeSeconds != 450 {
			t.Errorf("maxRuntimeSeconds = %d, want 450", cfg.maxRuntimeSeconds)
		}
	})
}

func TestDeepResearch_ToolConfigOverrides(t *testing.T) {
	t.Run("tool_config.max_iterations", func(t *testing.T) {
		dr, toolCtx := newDRToolCtx(nil, map[string]any{
			"max_iterations": float64(6),
		})
		cfg := dr.resolveConfig(map[string]any{}, toolCtx)
		if cfg.maxIterations != 6 {
			t.Errorf("maxIterations = %d, want 6", cfg.maxIterations)
		}
	})

	t.Run("tool_config.duration_minutes converts to seconds", func(t *testing.T) {
		dr, toolCtx := newDRToolCtx(nil, map[string]any{
			"duration_minutes": float64(4),
		})
		cfg := dr.resolveConfig(map[string]any{}, toolCtx)
		if cfg.maxRuntimeSeconds != 240 {
			t.Errorf("maxRuntimeSeconds = %d, want 240 (4 minutes)", cfg.maxRuntimeSeconds)
		}
	})

	t.Run("explicit arg wins over tool_config", func(t *testing.T) {
		dr, toolCtx := newDRToolCtx(nil, map[string]any{
			"max_iterations": float64(6),
		})
		cfg := dr.resolveConfig(map[string]any{
			"max_iterations": float64(9),
		}, toolCtx)
		if cfg.maxIterations != 9 {
			t.Errorf("maxIterations = %d, want 9 (explicit wins)", cfg.maxIterations)
		}
	})
}

func TestDeepResearch_RuntimeClamp60to600(t *testing.T) {
	cases := []struct {
		name    string
		seconds int
		want    int
	}{
		{"below 60 clamps to 60", 30, 60},
		{"exactly 60", 60, 60},
		{"59 clamps to 60", 59, 60},
		{"exactly 600", 600, 600},
		{"above 600 clamps to 600", 900, 600},
		{"normal 300 unchanged", 300, 300},
		{"zero clamps to 60 after falling through to research_type default (300), then fine", 0, 300},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dr, toolCtx := newDRToolCtx(nil, nil)
			args := map[string]any{}
			if tc.seconds > 0 {
				args["max_runtime_seconds"] = float64(tc.seconds)
			}
			cfg := dr.resolveConfig(args, toolCtx)
			if cfg.maxRuntimeSeconds != tc.want {
				t.Errorf("maxRuntimeSeconds = %d, want %d", cfg.maxRuntimeSeconds, tc.want)
			}
		})
	}
}

func TestDeepResearch_SourcesResolution(t *testing.T) {
	t.Run("default is web", func(t *testing.T) {
		dr, toolCtx := newDRToolCtx(nil, nil)
		cfg := dr.resolveConfig(map[string]any{}, toolCtx)
		if len(cfg.sources) != 1 || cfg.sources[0] != "web" {
			t.Errorf("sources = %v, want [web]", cfg.sources)
		}
	})

	t.Run("explicit args web kb", func(t *testing.T) {
		dr, toolCtx := newDRToolCtx(nil, nil)
		cfg := dr.resolveConfig(map[string]any{
			"sources": []any{"web", "kb"},
		}, toolCtx)
		if len(cfg.sources) != 2 || cfg.sources[0] != "web" || cfg.sources[1] != "kb" {
			t.Errorf("sources = %v, want [web kb]", cfg.sources)
		}
	})

	t.Run("invalid sources are dropped silently", func(t *testing.T) {
		dr, toolCtx := newDRToolCtx(nil, nil)
		cfg := dr.resolveConfig(map[string]any{
			"sources": []any{"web", "tavily", "kb", "brave"},
		}, toolCtx)
		if len(cfg.sources) != 2 || cfg.sources[0] != "web" || cfg.sources[1] != "kb" {
			t.Errorf("sources = %v, want [web kb] (others dropped)", cfg.sources)
		}
	})

	t.Run("all-invalid args falls back to default", func(t *testing.T) {
		dr, toolCtx := newDRToolCtx(nil, nil)
		cfg := dr.resolveConfig(map[string]any{
			"sources": []any{"exa", "tavily"},
		}, toolCtx)
		if len(cfg.sources) != 1 || cfg.sources[0] != "web" {
			t.Errorf("sources = %v, want [web] fallback when no valid sources", cfg.sources)
		}
	})

	t.Run("tool_config sources", func(t *testing.T) {
		dr, toolCtx := newDRToolCtx(nil, map[string]any{
			"sources": []any{"kb"},
		})
		cfg := dr.resolveConfig(map[string]any{}, toolCtx)
		if len(cfg.sources) != 1 || cfg.sources[0] != "kb" {
			t.Errorf("sources = %v, want [kb] from tool_config", cfg.sources)
		}
	})

	t.Run("args override tool_config sources", func(t *testing.T) {
		dr, toolCtx := newDRToolCtx(nil, map[string]any{
			"sources": []any{"kb"},
		})
		cfg := dr.resolveConfig(map[string]any{
			"sources": []any{"web"},
		}, toolCtx)
		if len(cfg.sources) != 1 || cfg.sources[0] != "web" {
			t.Errorf("sources = %v, want [web] (args override)", cfg.sources)
		}
	})
}

func TestDeepResearch_KbIDsPassed(t *testing.T) {
	dr, toolCtx := newDRToolCtx(nil, nil)
	cfg := dr.resolveConfig(map[string]any{
		"kb_ids": []any{"kb-1", "kb-2", "kb-3"},
	}, toolCtx)
	if len(cfg.kbIDs) != 3 {
		t.Fatalf("kbIDs len = %d, want 3", len(cfg.kbIDs))
	}
	want := []string{"kb-1", "kb-2", "kb-3"}
	for i, id := range cfg.kbIDs {
		if id != want[i] {
			t.Errorf("kbIDs[%d] = %q, want %q", i, id, want[i])
		}
	}
}

func TestDeepResearch_KbIDsNonStringsSkipped(t *testing.T) {
	dr, toolCtx := newDRToolCtx(nil, nil)
	cfg := dr.resolveConfig(map[string]any{
		"kb_ids": []any{"kb-1", 42, true, "kb-2"},
	}, toolCtx)
	if len(cfg.kbIDs) != 2 {
		t.Fatalf("kbIDs = %v, want len 2 (non-strings dropped)", cfg.kbIDs)
	}
	if cfg.kbIDs[0] != "kb-1" || cfg.kbIDs[1] != "kb-2" {
		t.Errorf("kbIDs = %v, want [kb-1 kb-2]", cfg.kbIDs)
	}
}

func TestDeepResearch_MaxSourcesPerIterationDefault5(t *testing.T) {
	dr, toolCtx := newDRToolCtx(nil, nil)
	cfg := dr.resolveConfig(map[string]any{}, toolCtx)
	if cfg.maxSourcesPerIter != 5 {
		t.Errorf("maxSourcesPerIter = %d, want 5 (Python default)", cfg.maxSourcesPerIter)
	}
}

func TestDeepResearch_MaxSourcesPerIterationFromToolConfig(t *testing.T) {
	dr, toolCtx := newDRToolCtx(nil, map[string]any{
		"max_sources_per_iteration": float64(8),
	})
	cfg := dr.resolveConfig(map[string]any{}, toolCtx)
	if cfg.maxSourcesPerIter != 8 {
		t.Errorf("maxSourcesPerIter = %d, want 8 (from tool_config)", cfg.maxSourcesPerIter)
	}
}

func TestDeepResearch_MaxSourcesPerIterationIgnoredFromArgs(t *testing.T) {
	dr, toolCtx := newDRToolCtx(nil, nil)
	cfg := dr.resolveConfig(map[string]any{
		"max_sources_per_iteration": float64(99),
	}, toolCtx)
	if cfg.maxSourcesPerIter != 5 {
		t.Errorf("maxSourcesPerIter = %d, want 5 (LLM-supplied arg must be ignored)", cfg.maxSourcesPerIter)
	}
}

func TestDeepResearch_SignalSearchType(t *testing.T) {
	var captured any
	toolCtx := &tool.Context{
		SendSignal: func(sig any) error {
			captured = sig
			return nil
		},
	}
	emitRAGInfoUpdate(toolCtx, "test title", "test query", nil, true)
	if captured == nil {
		t.Fatal("signal not sent")
	}
	type fieldCheck struct {
		SearchType string
	}
	type ragData interface {
		GetSignalType() string
	}
	rd, ok := captured.(ragData)
	if !ok {
		t.Fatalf("signal is not a ragData: %T", captured)
	}
	if rd.GetSignalType() != "rag_info_update" {
		t.Errorf("signal type = %q, want rag_info_update", rd.GetSignalType())
	}
	_ = fieldCheck{}
}

func TestDeepResearch_ReportArtifactNameSuffix(t *testing.T) {
	cases := []struct {
		question string
		want     string
	}{
		{"What is AI?", "what_is_ai_deep_research_report.md"},
		{"Research: Deep Learning!", "research_deep_learning_deep_research_report.md"},
		{"", "unnamed_deep_research_report.md"},
		{
			"a very long research question about many topics and more things beyond",
			"a_very_long_research_question_about_many_topics_an_deep_research_report.md",
		},
	}
	for _, tc := range cases {
		got := sanitizeToFilename(tc.question, 50, "_deep_research_report.md")
		if got != tc.want {
			t.Errorf("sanitizeToFilename(%q) = %q, want %q", tc.question, got, tc.want)
		}
		if !strings.HasSuffix(got, "_deep_research_report.md") {
			t.Errorf("filename %q missing required suffix", got)
		}
	}
}

func TestDeepResearch_FindingsToRAGSourcesCitationFormat(t *testing.T) {
	findings := []researchFinding{
		{Title: "A", URL: "https://a.com", Content: "a snippet", CitationID: "research0", SourceType: "web", RelevanceScore: 0.85},
		{Title: "B", URL: "https://b.com", Content: "b snippet", CitationID: "research1", SourceType: "web", RelevanceScore: 0.85},
		{Title: "C", URL: "https://c.com", Content: "c snippet", CitationID: "research2", SourceType: "web", RelevanceScore: 0.85},
	}
	sources := findingsToRAGSources(findings)
	if len(sources) != 3 {
		t.Fatalf("sources len = %d, want 3", len(sources))
	}
	for i, s := range sources {
		wantID := "research" + string(rune('0'+i))
		if s.CitationID != wantID {
			t.Errorf("sources[%d].CitationID = %q, want %q", i, s.CitationID, wantID)
		}
		wantFileID := "deep_research_" + string(rune('0'+i+1))
		if s.FileID != wantFileID {
			t.Errorf("sources[%d].FileID = %q, want %q (Python uses 1-based counter)", i, s.FileID, wantFileID)
		}
		if s.SourceType != "web" {
			t.Errorf("sources[%d].SourceType = %q, want web", i, s.SourceType)
		}
	}
}

func TestDeepResearch_FindingsToRAGSourcesPreviewLength(t *testing.T) {
	long := strings.Repeat("x", 800)

	findings := []researchFinding{
		{Title: "unfetched", URL: "https://a.com", Content: long, CitationID: "research0", SourceType: "web", Fetched: false},
		{Title: "fetched", URL: "https://b.com", Content: long, CitationID: "research1", SourceType: "web", Fetched: true},
	}
	sources := findingsToRAGSources(findings)

	gotUnfetched := sources[0].ContentPreview
	if len(gotUnfetched) != 203 {
		t.Errorf("unfetched preview len = %d, want 203 (200 chars + \"...\")", len(gotUnfetched))
	}
	gotFetched := sources[1].ContentPreview
	if len(gotFetched) != 503 {
		t.Errorf("fetched preview len = %d, want 503 (500 chars + \"...\")", len(gotFetched))
	}
	if !strings.HasSuffix(gotUnfetched, "...") {
		t.Errorf("unfetched preview should end with ellipsis: %q", gotUnfetched[:10])
	}
}

func TestDeepResearch_FindingsToRAGSourcesURLFallback(t *testing.T) {
	findings := []researchFinding{
		{Title: "No URL", URL: "", Content: "x", CitationID: "research0", SourceType: "web"},
	}
	sources := findingsToRAGSources(findings)
	if len(sources) != 1 {
		t.Fatalf("sources len = %d, want 1", len(sources))
	}
	if sources[0].SourceURL != "N/A" {
		t.Errorf("SourceURL = %q, want \"N/A\" (Python fallback)", sources[0].SourceURL)
	}
	if sources[0].URL != "" {
		t.Errorf("URL = %q, want \"\" (empty, not N/A)", sources[0].URL)
	}
}

func TestProgressPercent(t *testing.T) {
	cases := []struct {
		name                 string
		iter, maxIter        int
		rangeStart, rangeEnd float64
		want                 float64
	}{
		{"midway of full range", 5, 10, 0.0, 1.0, 50.0},
		{"midway of 10-70%", 5, 10, 0.1, 0.7, 40.0},
		{"first iteration", 1, 10, 0.1, 0.7, 16.0},
		{"last iteration", 10, 10, 0.1, 0.7, 70.0},
		{"zero max falls back to rangeStart", 0, 0, 0.1, 0.7, 10.0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := progressPercent(tc.iter, tc.maxIter, tc.rangeStart, tc.rangeEnd)
			if got != tc.want {
				t.Errorf("progressPercent(%d, %d, %v, %v) = %v, want %v",
					tc.iter, tc.maxIter, tc.rangeStart, tc.rangeEnd, got, tc.want)
			}
		})
	}
}

func TestTruncate(t *testing.T) {
	cases := []struct {
		in     string
		maxLen int
		want   string
	}{
		{"hello", 10, "hello"},
		{"exactly len", 11, "exactly len"},
		{"too long", 4, "too ..."},
		{"", 5, ""},
	}
	for _, tc := range cases {
		got := truncate(tc.in, tc.maxLen)
		if got != tc.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", tc.in, tc.maxLen, got, tc.want)
		}
	}
}

func TestConfigInt(t *testing.T) {
	cases := []struct {
		name string
		m    map[string]any
		key  string
		want int
	}{
		{"float64", map[string]any{"k": float64(7)}, "k", 7},
		{"int", map[string]any{"k": 7}, "k", 7},
		{"missing", map[string]any{}, "k", 0},
		{"wrong type", map[string]any{"k": "7"}, "k", 0},
		{"zero", map[string]any{"k": float64(0)}, "k", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := configInt(tc.m, tc.key)
			if got != tc.want {
				t.Errorf("configInt(%v, %q) = %d, want %d", tc.m, tc.key, got, tc.want)
			}
		})
	}
}

func TestDeepResearchFetchSourcesBlocksSSRF(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		_, _ = w.Write([]byte("internal page body"))
	}))
	defer srv.Close()

	newFinding := func() []researchFinding {
		return []researchFinding{{URL: srv.URL, CitationID: "research0"}}
	}

	tr := &DeepResearchTool{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	blocked := newFinding()
	tr.fetchSources(t.Context(), blocked, []int{0}, blocked, "", false, logger)
	if blocked[0].Fetched {
		t.Errorf("loopback source was fetched with allow_loopback=false (SSRF not blocked)")
	}
	if n := atomic.LoadInt32(&hits); n != 0 {
		t.Errorf("server was hit %d times despite SSRF block", n)
	}

	allowed := newFinding()
	tr.fetchSources(t.Context(), allowed, []int{0}, allowed, "", true, logger)
	if !allowed[0].Fetched {
		t.Errorf("loopback source was not fetched with allow_loopback=true")
	}
	if n := atomic.LoadInt32(&hits); n != 1 {
		t.Errorf("server hit %d times with allow_loopback=true, want 1", n)
	}
}

func TestDeepResearchResolveConfigAllowLoopbackDefault(t *testing.T) {
	tr := &DeepResearchTool{}

	cfg := tr.resolveConfig(map[string]any{}, &tool.Context{})
	if v, _ := cfg.merged["allow_loopback"].(bool); v {
		t.Errorf("allow_loopback defaulted to true; want false (secure default)")
	}

	cfg = tr.resolveConfig(map[string]any{}, &tool.Context{ToolConfig: map[string]any{"allow_loopback": true}})
	if v, _ := cfg.merged["allow_loopback"].(bool); !v {
		t.Errorf("allow_loopback=true in tool_config did not propagate to merged config")
	}
}

func runDeepResearch(t *testing.T, toolConfig map[string]any) *tool.Result {
	t.Helper()
	cfg := map[string]any{"interactive_plan_verification": false}
	maps.Copy(cfg, toolConfig)
	toolCtx := &tool.Context{
		ParentTaskID: "t",
		ToolConfig:   cfg,
		InternalLLMCall: func(context.Context, string, string, float64) (string, error) {
			return "", errors.New("no model in this test")
		},
	}
	args := map[string]any{"research_question": "q", "max_runtime_seconds": float64(60)}
	r, err := NewDeepResearchTool(nil).Execute(t.Context(), args, toolCtx)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestDeepResearch_ResolvesItsSearchProviderBeforeSearching(t *testing.T) {
	cases := []struct {
		name string
		cfg  map[string]any
		want string
	}{
		{"tavily without a key", map[string]any{"provider": "tavily", "api_base": "http://search.internal"}, "tavily has no API key"},
		{"no provider, no credentials", map[string]any{}, "Google Search API key and CSE ID must be configured (google_search_api_key, google_cse_id)"},
		{"no provider, no api base", map[string]any{"google_search_api_key": "g", "google_cse_id": "cx"}, "Google Search 'api_base' must be configured — no public-endpoint fallback"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if r := runDeepResearch(t, tc.cfg); !strings.Contains(r.Message, tc.want) {
				t.Errorf("result = %q, want it to contain %q", r.Message, tc.want)
			}
		})
	}
}

func TestDeepResearch_SearchesWhateverSourcesSays(t *testing.T) {
	fake := newProviderFake(t, fakeResponse{http.StatusOK, exaCannedResponse})

	r := runDeepResearch(t, map[string]any{
		"sources": []any{"kb"}, "provider": "exa", "api_key": "EXA_KEY", "api_base": fake.server.URL,
	})

	if len(fake.recorded()) == 0 {
		t.Fatalf("a kb-only run never searched; result = %q", r.Message)
	}
	if strings.Contains(r.Message, "found no sources") {
		t.Errorf("result = %q, want the search results used", r.Message)
	}
}

func TestDeepResearch_GoogleAPIBaseDefaultIsNeverAnotherProvidersBase(t *testing.T) {
	for _, provider := range []string{searchProviderExa, searchProviderTavily} {
		t.Run(provider, func(t *testing.T) {
			google := newProviderFake(t, fakeResponse{http.StatusOK, `{}`})

			r := runDeepResearch(t, map[string]any{
				"provider": provider, "api_key": "KEY", "google_api_base": google.server.URL,
			})

			if want := provider + " has no API base"; !strings.Contains(r.Message, want) {
				t.Errorf("result = %q, want it to contain %q", r.Message, want)
			}
			if got := google.recorded(); len(got) != 0 {
				t.Errorf("the Google default received %d requests carrying the %s key", len(got), provider)
			}
		})
	}
}

func TestDeepResearch_RefusesAProviderBesideAGoogleKey(t *testing.T) {
	for _, provider := range []string{searchProviderExa, searchProviderTavily} {
		t.Run(provider, func(t *testing.T) {
			mirror := newTestCSEServer(t, cannedWebResponse, http.StatusOK)

			r := runDeepResearch(t, map[string]any{
				"google_search_api_key": "GOOGLE_KEY", "google_cse_id": "CX", "api_base": mirror.baseURL(),
				"provider": provider, "api_key": "PROVIDER_KEY",
			})

			if r.Status == tool.StatusSuccess || !strings.Contains(r.Message, "provider "+provider) || !strings.Contains(r.Message, "google_search_api_key") {
				t.Errorf("result = %+v, want a refusal naming provider %s and google_search_api_key", r, provider)
			}
			for _, key := range []string{"GOOGLE_KEY", "PROVIDER_KEY"} {
				if strings.Contains(r.Message, key) {
					t.Errorf("message leaks a key: %q", r.Message)
				}
			}
			if mirror.lastParams() != nil {
				t.Errorf("the Google mirror received a request carrying the %s key", provider)
			}
		})
	}
}

func TestDeepResearch_GoogleBranchReadsAPIBaseThenTheClusterDefault(t *testing.T) {
	cases := []struct {
		name            string
		providerKey     string
		withAPIBase     bool
		wantDefaultHits bool
	}{
		{"cluster default only", "", false, true},
		{"api_base wins", "", true, false},
		{"explicit google provider, cluster default only", searchProviderGoogle, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			agentBase := newTestCSEServer(t, cannedWebResponse, http.StatusOK)
			clusterDefault := newTestCSEServer(t, cannedWebResponse, http.StatusOK)
			cfg := map[string]any{
				"google_search_api_key": "G", "google_cse_id": "CX", "google_api_base": clusterDefault.baseURL(),
			}
			if tc.providerKey != "" {
				cfg["provider"] = tc.providerKey
			}
			if tc.withAPIBase {
				cfg["api_base"] = agentBase.baseURL()
			}

			runDeepResearch(t, cfg)

			if got := clusterDefault.lastParams() != nil; got != tc.wantDefaultHits {
				t.Errorf("cluster default searched = %v, want %v", got, tc.wantDefaultHits)
			}
			if got := agentBase.lastParams() != nil; got != tc.withAPIBase {
				t.Errorf("api_base searched = %v, want %v", got, tc.withAPIBase)
			}
		})
	}
}

func TestDeepResearch_SafeSearch(t *testing.T) {
	t.Run("exa moderation", func(t *testing.T) {
		fake := newProviderFake(t, fakeResponse{http.StatusOK, exaCannedResponse})
		runDeepResearch(t, map[string]any{"provider": "exa", "api_key": "K", "api_base": fake.server.URL, "safe_search": true})
		reqs := fake.recorded()
		if len(reqs) == 0 {
			t.Fatal("no search reached the provider")
		}
		assertJSONFields(t, reqs[0].body, map[string]any{"moderation": true})
	})
	t.Run("tavily safe_search", func(t *testing.T) {
		fake := newProviderFake(t, fakeResponse{http.StatusOK, tavilyCannedResponse})
		runDeepResearch(t, map[string]any{"provider": "tavily", "api_key": "K", "api_base": fake.server.URL, "safe_search": "true"})
		reqs := fake.recorded()
		if len(reqs) == 0 {
			t.Fatal("no search reached the provider")
		}
		assertJSONFields(t, reqs[0].body, map[string]any{"safe_search": true})
	})
	t.Run("not a boolean", func(t *testing.T) {
		fake := newProviderFake(t, fakeResponse{http.StatusOK, exaCannedResponse})
		r := runDeepResearch(t, map[string]any{"provider": "exa", "api_key": "K", "api_base": fake.server.URL, "safe_search": "on"})
		if r.Status == tool.StatusSuccess || !strings.Contains(r.Message, "tool_config") || !strings.Contains(r.Message, "safe_search must be true or false") {
			t.Errorf("result = %+v, want an operator-config error naming tool_config and safe_search", r)
		}
		if !strings.HasPrefix(r.Message, "deep_research tool_config is invalid") {
			t.Errorf("message = %q, want it to name the calling tool", r.Message)
		}
		if r.ValidationError != "" {
			t.Errorf("ValidationError = %q, want none: the model cannot fix operator config", r.ValidationError)
		}
		if got := fake.recorded(); len(got) != 0 {
			t.Errorf("searched %d times despite an invalid safe_search", len(got))
		}
	})
	t.Run("set but empty", func(t *testing.T) {
		fake := newProviderFake(t, fakeResponse{http.StatusOK, exaCannedResponse})
		r := runDeepResearch(t, map[string]any{"provider": "exa", "api_key": "K", "api_base": fake.server.URL, "safe_search": " "})
		if r.Status == tool.StatusSuccess || !strings.Contains(r.Message, "safe_search is set but empty") {
			t.Errorf("result = %+v, want an operator-config error for an empty safe_search", r)
		}
		if got := fake.recorded(); len(got) != 0 {
			t.Errorf("searched %d times despite an empty safe_search", len(got))
		}
	})
	for name, cfg := range map[string]map[string]any{"unset": {}, "false": {"safe_search": false}} {
		t.Run("exa moderation off when "+name, func(t *testing.T) {
			fake := newProviderFake(t, fakeResponse{http.StatusOK, exaCannedResponse})
			runDeepResearch(t, withConfig(cfg, map[string]any{"provider": "exa", "api_key": "K", "api_base": fake.server.URL}))
			reqs := fake.recorded()
			if len(reqs) == 0 {
				t.Fatal("no search reached the provider")
			}
			if _, present := reqs[0].body["moderation"]; present {
				t.Errorf("body carries moderation %v, want it absent", reqs[0].body["moderation"])
			}
		})
		t.Run("tavily safe_search off when "+name, func(t *testing.T) {
			fake := newProviderFake(t, fakeResponse{http.StatusOK, tavilyCannedResponse})
			runDeepResearch(t, withConfig(cfg, map[string]any{"provider": "tavily", "api_key": "K", "api_base": fake.server.URL}))
			reqs := fake.recorded()
			if len(reqs) == 0 {
				t.Fatal("no search reached the provider")
			}
			if _, present := reqs[0].body["safe_search"]; present {
				t.Errorf("body carries safe_search %v, want it absent", reqs[0].body["safe_search"])
			}
		})
	}
}

func withConfig(base, extra map[string]any) map[string]any {
	merged := maps.Clone(base)
	maps.Copy(merged, extra)
	return merged
}

func runDeepResearchWithQueries(t *testing.T, toolConfig map[string]any, queries ...string) *tool.Result {
	t.Helper()
	planned, err := json.Marshal(map[string]any{"queries": queries})
	if err != nil {
		t.Fatal(err)
	}
	toolCtx := &tool.Context{
		ParentTaskID: "t",
		ToolConfig:   withConfig(map[string]any{"interactive_plan_verification": false}, toolConfig),
		InternalLLMCall: func(_ context.Context, _, prompt string, _ float64) (string, error) {
			if strings.Contains(prompt, "search query specialist") {
				return string(planned), nil
			}
			return "", errors.New("no model in this test")
		},
	}
	r, err := NewDeepResearchTool(nil).Execute(t.Context(), map[string]any{"research_question": "q", "max_runtime_seconds": float64(60)}, toolCtx)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestDeepResearch_ReportsWhySearchesFailed(t *testing.T) {
	t.Run("every search fails", func(t *testing.T) {
		fake := newProviderFake(t, fakeResponse{http.StatusPaymentRequired, `{"error":"Out of credits"}`})

		r := runDeepResearch(t, map[string]any{"provider": "exa", "api_key": "K", "api_base": fake.server.URL})

		if r.Status == tool.StatusSuccess {
			t.Fatalf("result = %+v, want a failure", r)
		}
		for _, want := range []string{"exa search API error (402)", "Out of credits"} {
			if !strings.Contains(r.Message, want) {
				t.Errorf("message = %q, want it to contain %q", r.Message, want)
			}
		}
		if strings.Contains(r.Message, "found no sources") {
			t.Errorf("message = %q, want the search error, not the generic one", r.Message)
		}
		if r.ErrorCode != taxonomy.ToolFailureRateLimited {
			t.Errorf("code = %q, want %q", r.ErrorCode, taxonomy.ToolFailureRateLimited)
		}
	})
	t.Run("searches succeed but find nothing", func(t *testing.T) {
		fake := newProviderFake(t, fakeResponse{http.StatusOK, `{"results":[]}`})

		r := runDeepResearch(t, map[string]any{"provider": "exa", "api_key": "K", "api_base": fake.server.URL})

		if !strings.Contains(r.Message, "Deep research found no sources") {
			t.Errorf("message = %q, want the generic no-sources message", r.Message)
		}
	})
	t.Run("one search fails and another finds nothing", func(t *testing.T) {
		for name, responses := range map[string][]fakeResponse{
			"failure first": {{http.StatusPaymentRequired, `{"error":"Out of credits"}`}, {http.StatusOK, `{"results":[]}`}},
			"failure last":  {{http.StatusOK, `{"results":[]}`}, {http.StatusPaymentRequired, `{"error":"Out of credits"}`}},
		} {
			t.Run(name, func(t *testing.T) {
				fake := newProviderFake(t, responses...)

				r := runDeepResearchWithQueries(t, map[string]any{"provider": "exa", "api_key": "K", "api_base": fake.server.URL}, "first", "second")

				if got := len(fake.recorded()); got != 2 {
					t.Fatalf("searched %d times, want 2", got)
				}
				if !strings.Contains(r.Message, "Deep research found no sources") || strings.Contains(r.Message, "402") {
					t.Errorf("message = %q, want the generic no-sources message: one search answered", r.Message)
				}
				if r.ErrorCode != "" {
					t.Errorf("code = %q, want none", r.ErrorCode)
				}
			})
		}
	})
}

func TestSearchFailureLog(t *testing.T) {
	quota := errors.New("quota")
	timeout := fmt.Errorf("search: %w", context.DeadlineExceeded)
	record := func(errs ...error) *searchFailureLog {
		log := &searchFailureLog{}
		for _, err := range errs {
			log.recordFailure(err)
		}
		return log
	}

	t.Run("the first failure that is not a context error wins", func(t *testing.T) {
		if got := record(timeout, quota, errors.New("later")).cause(); got != quota {
			t.Errorf("cause = %v, want %v", got, quota)
		}
		if got := record(quota, timeout).cause(); got != quota {
			t.Errorf("cause = %v, want %v", got, quota)
		}
	})
	t.Run("only context errors keeps the first", func(t *testing.T) {
		if got := record(timeout, context.Canceled).cause(); got != timeout {
			t.Errorf("cause = %v, want %v", got, timeout)
		}
	})
	t.Run("a deadline is coded as a dispatch timeout", func(t *testing.T) {
		got := tool.Error("x", websearch.ErrorCode(timeout)).ErrorCode
		if want := tool.DispatchErrorCode(taxonomy.DispatchClassTimeout); got != want {
			t.Errorf("code = %q, want %q", got, want)
		}
	})
	t.Run("a success clears the cause", func(t *testing.T) {
		log := record(quota)
		log.recordSuccess()
		if got := log.cause(); got != nil {
			t.Errorf("cause = %v, want none", got)
		}
	})
}
