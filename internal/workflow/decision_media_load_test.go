// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"log/slog"
	"strings"
	"sync"
	"testing"
)

func TestInitAndCheckLoadableRefuseMediaOnAModelThatCannotReadIt(t *testing.T) {
	openAI := "decision:\n  provider: openai\n  api_key: test-key\n  model: gpt-6-luna\n"
	audioNode := "    audio: [call.wav]\n"
	onOpenAI := strings.Replace(geminiMediaWorkflow(audioNode, ""), geminiBlock, openAI, 1)
	initWith := func(t *testing.T, doc string) error {
		t.Helper()
		cfg, err := parseYAMLWorkflow(t, doc)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		svc := &stubServices{brk: stubBroker{}, features: decisionFeatureRegistry(t, true)}
		return NewWorkflowInstance(cfg).Init(t.Context(), nil, svc)
	}
	if err := initWith(t, onOpenAI); err == nil || !strings.Contains(err.Error(), "sends audio") {
		t.Errorf("Init on OpenAI = %v, want the workflow refused for sending audio", err)
	}
	if err := initWith(t, geminiMediaWorkflow(audioNode, "")); err != nil {
		t.Errorf("Init on Gemini = %v, want it to load", err)
	}
	appConfig := map[string]any{
		"decision":       map[string]any{"provider": "openai", "api_key": "${KEY}"},
		"output_mapping": map[string]any{"r": "{{triage.output}}"},
		"nodes": []any{map[string]any{
			"id": "triage", "type": "decision", "audio": []any{"call.wav"},
			"questions": map[string]any{"urgent": map[string]any{"type": "boolean", "instructions": "is it urgent"}},
		}},
	}
	if err := CheckLoadable("wf", appConfig); err == nil || !strings.Contains(err.Error(), "sends audio") {
		t.Errorf("CheckLoadable on OpenAI = %v, want the audio refusal", err)
	}
	appConfig["decision"] = map[string]any{"provider": "google_ai_studio", "api_key": "${KEY}"}
	if err := CheckLoadable("wf", appConfig); err != nil {
		t.Errorf("CheckLoadable on Gemini = %v, want it to load", err)
	}
}

func TestInitWarnsForAMediaNodeWhileTheFlagIsOff(t *testing.T) {
	cfg, err := parseYAMLWorkflow(t, geminiMediaWorkflow("    video: [clip.mp4]\n", ""))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	logs := &lockedWriter{w: &strings.Builder{}, mu: &sync.Mutex{}}
	svc := &stubServices{brk: stubBroker{}, features: decisionFeatureRegistry(t, true), logger: slog.New(slog.NewTextHandler(logs, nil))}
	if err := NewWorkflowInstance(cfg).Init(t.Context(), nil, svc); err != nil {
		t.Fatalf("Init: %v", err)
	}
	logs.assertSays(t, `level=WARN msg="decision node uses media input but the feature is disabled; it will fail when reached"`,
		"node=triage mediaField=video feature=decision_media_input")
}
