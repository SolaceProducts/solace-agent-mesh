// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestResolveRenderSections(t *testing.T) {
	cases := []struct {
		name              string
		headless          *bool
		templates, embeds *bool
		wantTemplates     bool
		wantEmbeds        bool
		wantChatRendered  bool
	}{
		{"unset keeps today's behaviour", nil, nil, nil, true, true, true},
		{"headless keeps embeds, drops chat guidance", new(true), nil, nil, false, true, false},
		{"headless false is the default", new(false), nil, nil, true, true, true},
		{"explicit templates win over headless", new(true), new(true), nil, true, true, false},
		{"explicit embeds do not restore chat guidance", new(true), nil, new(true), false, true, false},
		{"explicit embeds off wins over headless", new(true), nil, new(false), false, false, false},
		{"explicit off without headless", nil, new(false), new(false), false, false, false},
		{"headless with both explicit on", new(true), new(true), new(true), true, true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			templates, embeds, chatRendered := resolveRenderSections(&AgentConfig{
				Headless:                   c.headless,
				EnableTemplateInstructions: c.templates,
				EnableEmbedResolution:      c.embeds,
			})
			if templates != c.wantTemplates {
				t.Errorf("templatesEnabled = %v, want %v", templates, c.wantTemplates)
			}
			if embeds != c.wantEmbeds {
				t.Errorf("embedsEnabled = %v, want %v", embeds, c.wantEmbeds)
			}
			if chatRendered != c.wantChatRendered {
				t.Errorf("chatRendered = %v, want %v", chatRendered, c.wantChatRendered)
			}
		})
	}
}

func TestWithoutHeadlessMatchesPreviousBehaviour(t *testing.T) {
	for _, headless := range []*bool{nil, new(false)} {
		for _, templates := range []*bool{nil, new(true), new(false)} {
			for _, embeds := range []*bool{nil, new(true), new(false)} {
				cfg := AgentConfig{
					Headless:                   headless,
					EnableTemplateInstructions: templates,
					EnableEmbedResolution:      embeds,
				}
				wantTemplates := templates == nil || *templates
				wantEmbeds := embeds == nil || *embeds

				gotTemplates, gotEmbeds, gotChatRendered := resolveRenderSections(&cfg)
				if gotTemplates != wantTemplates || gotEmbeds != wantEmbeds {
					t.Errorf("headless=%v templates=%v embeds=%v: got (%v,%v), want (%v,%v)",
						headless, templates, embeds, gotTemplates, gotEmbeds, wantTemplates, wantEmbeds)
				}
				if gotChatRendered != gotEmbeds {
					t.Errorf("headless=%v templates=%v embeds=%v: chatRendered=%v must equal embedsEnabled=%v when headless is off",
						headless, templates, embeds, gotChatRendered, gotEmbeds)
				}
			}
		}
	}
}

func TestHeadlessKeepsWhatSomethingStillResolves(t *testing.T) {
	agent := fileReadingTestAgent(t)
	agent.cfg.Headless = new(true)

	prompt := agent.buildSystemPrompt(t.Context(), nil, gatewayContext{}, promptSkillState{}, false)

	for _, want := range []string{
		EmbedOpen + "math:",
		EmbedOpen + "datetime:",
		EmbedOpen + "artifact_content:filename",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("headless prompt dropped %q", want)
		}
	}
	for _, notWant := range []string{
		"Inline Liquid Templates",
		"Conversation Flow and Response Formatting",
	} {
		if strings.Contains(prompt, notWant) {
			t.Errorf("headless prompt still carries chat-rendering guidance %q", notWant)
		}
	}
}

func TestHeadlessDefaultsAreOverridable(t *testing.T) {
	agent, _, _ := newTestAgent()
	agent.cfg.Headless = new(true)
	agent.cfg.EnableTemplateInstructions = new(true)

	prompt := agent.buildSystemPrompt(t.Context(), nil, gatewayContext{}, promptSkillState{}, false)
	if !strings.Contains(promptSectionText(t, prompt, "Inline Liquid Templates"), "Data Context for Liquid Templates:") {
		t.Error("explicit enable_template_instructions must override headless")
	}
}

func promptSectionLabels(prompt string) []string {
	var out []string
	for _, line := range strings.Split(prompt, "\n") {
		if strings.HasPrefix(line, "## ") {
			out = append(out, strings.TrimPrefix(line, "## "))
		}
	}
	return out
}

func TestHeadlessRemovesExactlyTheChatSections(t *testing.T) {
	ui := gatewayContext{DisplaysTemplates: true}
	base := fileReadingTestAgent(t)
	base.cfg.EnableTemplateInstructions = new(true)
	basePrompt := base.buildSystemPrompt(t.Context(), nil, ui, promptSkillState{}, false)

	hl := fileReadingTestAgent(t)
	hl.cfg.Headless = new(true)
	hlPrompt := hl.buildSystemPrompt(t.Context(), nil, gatewayContext{}, promptSkillState{}, false)

	present := map[string]bool{}
	for _, l := range promptSectionLabels(hlPrompt) {
		present[l] = true
	}
	var removed []string
	for _, l := range promptSectionLabels(basePrompt) {
		if !present[l] {
			removed = append(removed, l)
		}
	}

	want := []string{"Inline Liquid Templates", "Conversation Flow and Response Formatting"}
	if !slices.Equal(removed, want) {
		t.Errorf("headless removed %v, want %v", removed, want)
	}
}

func TestHeadlessRespectsArtifactContentFlag(t *testing.T) {
	agent, _, _ := newTestAgent()
	agent.cfg.Headless = new(true)
	agent.cfg.EnableArtifactContentInstruction = new(false)

	prompt := agent.buildSystemPrompt(t.Context(), nil, gatewayContext{}, promptSkillState{}, false)
	if strings.Contains(prompt, EmbedOpen+"artifact_content:") {
		t.Error("explicit enable_artifact_content_instruction=false must be honoured under headless")
	}
}

func TestNonHeadlessPromptUnchanged(t *testing.T) {
	agent := fileReadingTestAgent(t)

	prompt := agent.buildSystemPrompt(t.Context(), nil, gatewayContext{DisplaysTemplates: true}, promptSkillState{}, false)
	for _, want := range []string{
		"Inline Liquid Templates",
		EmbedOpen + "artifact_content:filename",
		EmbedOpen + "math:",
		"Conversation Flow and Response Formatting",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("default prompt is missing %q", want)
		}
	}
}

func TestWithheldPromptSections(t *testing.T) {
	cases := []struct {
		name string
		cfg  AgentConfig
		want []string
	}{
		{"default withholds nothing", AgentConfig{}, nil},
		{
			"headless withholds templates and the chat guidance",
			AgentConfig{Headless: new(true)},
			[]string{"templateInstructions", "peerEmbedRelay", "conversationFlow"},
		},
		{
			"embeds off withholds the docs and the chat guidance",
			AgentConfig{EnableEmbedResolution: new(false)},
			[]string{"embedInstructions", "peerEmbedRelay", "conversationFlow"},
		},
		{
			"headless keeping templates withholds only the chat guidance",
			AgentConfig{Headless: new(true), EnableTemplateInstructions: new(true)},
			[]string{"peerEmbedRelay", "conversationFlow"},
		},
		{
			"templates off alone",
			AgentConfig{EnableTemplateInstructions: new(false)},
			[]string{"templateInstructions"},
		},
		{
			"templates and embeds both off names all four in resolver order",
			AgentConfig{EnableTemplateInstructions: new(false), EnableEmbedResolution: new(false)},
			[]string{"templateInstructions", "embedInstructions", "peerEmbedRelay", "conversationFlow"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := withheldPromptSections(&c.cfg); !slices.Equal(got, c.want) {
				t.Errorf("withheldPromptSections = %v, want %v", got, c.want)
			}
		})
	}
}

type capturingHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *capturingHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *capturingHandler) WithAttrs([]slog.Attr) slog.Handler       { return h }
func (h *capturingHandler) WithGroup(string) slog.Handler            { return h }

func (h *capturingHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r.Clone())
	return nil
}

func (h *capturingHandler) find(msg string) (slog.Record, int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	var found slog.Record
	count := 0
	for _, r := range h.records {
		if r.Message == msg {
			found = r
			count++
		}
	}
	return found, count
}

func TestStartLogsWithheldSections(t *testing.T) {
	startAndCaptureLogs := func(t *testing.T, cfg AgentConfig) *capturingHandler {
		t.Helper()
		agent := New(cfg)
		svc := newMockServices(newMockLLM(), newMemSessionStore())
		svc.brk = newMockBroker()
		agent.svc = svc
		if err := agent.Init(context.Background(), nil, svc); err != nil {
			t.Fatal(err)
		}
		h := &capturingHandler{}
		agent.logger = slog.New(h)
		if err := agent.Start(t.Context()); err != nil {
			t.Fatal(err)
		}
		stopCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := agent.Stop(stopCtx); err != nil {
			t.Fatalf("Stop: %v", err)
		}
		return h
	}

	const msg = "prompt sections withheld by config"

	withholding := []struct {
		name string
		cfg  AgentConfig
		want []string
	}{
		{
			"one withheld section",
			AgentConfig{EnableTemplateInstructions: new(false)},
			[]string{"templateInstructions"},
		},
		{
			"several withheld sections",
			AgentConfig{EnableEmbedResolution: new(false)},
			[]string{"embedInstructions", "peerEmbedRelay", "conversationFlow"},
		},
		{
			"headless agent",
			AgentConfig{Headless: new(true)},
			[]string{"templateInstructions", "peerEmbedRelay", "conversationFlow"},
		},
	}
	for _, c := range withholding {
		t.Run(c.name, func(t *testing.T) {
			cfg := c.cfg
			cfg.Name, cfg.Namespace = "withheld-agent", "test-ns"
			h := startAndCaptureLogs(t, cfg)

			rec, count := h.find(msg)
			if count != 1 {
				t.Fatalf("got %d %q records, want 1", count, msg)
			}
			attrs := map[string]any{}
			rec.Attrs(func(a slog.Attr) bool {
				attrs[a.Key] = a.Value.Any()
				return true
			})
			got, _ := attrs["withheld"].([]string)
			if !slices.Equal(got, c.want) {
				t.Errorf("withheld = %v, want %v", got, c.want)
			}
			wantHeadless := cfg.Headless != nil && *cfg.Headless
			if attrs["headless"] != wantHeadless {
				t.Errorf("headless = %v, want %v", attrs["headless"], wantHeadless)
			}
			if rec.Level != slog.LevelInfo {
				t.Errorf("level = %v, want %v", rec.Level, slog.LevelInfo)
			}
		})
	}

	t.Run("default config stays silent", func(t *testing.T) {
		h := startAndCaptureLogs(t, AgentConfig{Name: "default-agent", Namespace: "test-ns"})
		if _, count := h.find(msg); count != 0 {
			t.Errorf("got %d %q records for a default config, want 0", count, msg)
		}
	})
}

func fileReadingTestAgent(t *testing.T) *Agent {
	t.Helper()
	agent, _, _ := newTestAgent()
	if err := agent.Tools().Register(&testBuiltinTool{name: loadArtifactToolName, description: "Load an artifact"}); err != nil {
		t.Fatal(err)
	}
	return agent
}
