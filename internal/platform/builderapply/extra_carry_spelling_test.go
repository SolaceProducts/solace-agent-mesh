// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
)

func TestLayExtraConfig_TypedListItemsInARuntimeSpelledBlockAreRuntimeSpelled(t *testing.T) {
	t.Parallel()
	stored, err := decodeLikeDeploy([]byte(`{"agent_card":{"version":"1"}}`))
	if err != nil {
		t.Fatal(err)
	}
	stated, err := decodeLikeDeploy([]byte(`{"agentCard":{"welcome":{"suggestions":[{"label":"Go","autoSend":true}]}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if problems := layExtraConfig(stored, stated); len(problems) != 0 {
		t.Fatalf("problems = %q", problems)
	}
	got, err := marshalWithoutHTMLEscaping(stored)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"agent_card":{"version":"1","welcome":{"suggestions":[{"label":"Go","auto_send":true}]}}}`; !jsonEqual(t, got, []byte(want)) {
		t.Errorf("stored = %s, want %s", got, want)
	}
}

func TestApplyExtraConfig_BothSpellingsOfACardAgreeAfterTheArtifactStatesTheWholeList(t *testing.T) {
	t.Parallel()
	svc, ctx := newTestService(t), t.Context()
	seedStoredRuntimeConfig(t, svc, "twin_card", json.RawMessage(
		`{"agentCard":{"welcome":{"suggestions":[{"label":"x"}]}},"agent_card":{"welcome":{"suggestions":[{"label":"x"}]}}}`))
	build := []BuildComponentInput{agentWithCardExtras(t, "twin_card", "        welcome: {suggestions: [{label: Go, auto_send: true}]}\n")}

	resp, err := svc.Apply(ctx, ApplyBuildRequest{Components: build}, testActor)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if w := warningsContaining(opFor(t, resp, kindAgent, "twin_card").Warnings, "stored in both spellings"); len(w) != 0 {
		t.Errorf("warnings = %q, want none once the artifact states the whole list", w)
	}
	blob := storedAdditional(ctx, t, svc, "twin_card")
	itemOf := func(card any) map[string]any {
		welcome, _ := card.(map[string]any)["welcome"].(map[string]any)
		items, _ := welcome["suggestions"].([]any)
		return items[0].(map[string]any)
	}
	if got, want := itemOf(blob["agentCard"]), map[string]any{"label": "Go", "autoSend": true}; !reflect.DeepEqual(got, want) {
		t.Errorf("agentCard item = %v, want %v", got, want)
	}
	if got, want := itemOf(blob["agent_card"]), map[string]any{"label": "Go", "auto_send": true}; !reflect.DeepEqual(got, want) {
		t.Errorf("agent_card item = %v, want %v", got, want)
	}
}

func TestApplyExtraConfig_DivergentCopiesAreReportedUntilTheArtifactSettlesThem(t *testing.T) {
	t.Parallel()
	const seed = `{"autoSummarization":{"enabled":true},"auto_summarization":{"enabled":false,"summary_max_tokens":5}}`
	for _, tc := range []struct {
		name, line  string
		wantWarning bool
	}{
		{"a partial block leaves the copies apart", "auto_summarization: {summary_max_tokens: 900}", true},
		{"the whole block brings them together", "auto_summarization: {enabled: true, summary_max_tokens: 900}", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc := newTestService(t)
			seedStoredRuntimeConfig(t, svc, "diverged", json.RawMessage(seed))
			resp, err := svc.Apply(t.Context(), ApplyBuildRequest{Components: []BuildComponentInput{agentComponentWithKey(t, "diverged", tc.line)}}, testActor)
			if err != nil {
				t.Fatalf("Apply: %v", err)
			}
			got := warningsContaining(opFor(t, resp, kindAgent, "diverged").Warnings, "auto_summarization is stored in both spellings")
			if tc.wantWarning != (len(got) == 1) || len(got) > 1 {
				t.Errorf("warnings = %q, want a divergence warning: %v", got, tc.wantWarning)
			}
		})
	}
}

func TestApplyExtraConfig_ValuesTheSaveCannotStore(t *testing.T) {
	t.Parallel()
	svc, ctx := newTestService(t), t.Context()
	for _, line := range []string{"max_tokens: .nan", "output_schema: {type: object, minimum: .inf}"} {
		_, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{agentComponentWithKey(t, "unstorable", line)}}, testActor)
		invalid, ok := errors.AsType[*entplatform.ValidationError](err)
		if !ok || !strings.Contains(invalid.Message, "is not finite (.nan or .inf)") {
			t.Errorf("%s: error = %v, want a refusal saying the number is not finite", line, err)
		}
	}
}

func TestApplyExtraConfig_ARuntimeCopiedBlockThatIsNotAnObjectIsRefused(t *testing.T) {
	t.Parallel()
	svc, ctx := newTestService(t), t.Context()
	seedStoredRuntimeConfig(t, svc, "not_a_block", json.RawMessage(`{"autoSummarization":"on"}`))
	_, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{agentComponentWithKey(t, "not_a_block", "auto_summarization: {enabled: true}")}}, testActor)
	refusal := refusalOf(t, "apply", err)
	if !strings.Contains(refusal.Message, "cannot be merged into what is stored") {
		t.Errorf("message = %q, want the stored non-block named", refusal.Message)
	}
}

func TestDesiredAgent_InternalIsManagedAndSilent(t *testing.T) {
	t.Parallel()
	got, err := desiredAgent("internal_probe", probeAgentContent(t, "internal", true), newToolsetCatalog())
	if err != nil {
		t.Fatalf("desiredAgent: %v", err)
	}
	if len(got.warnings) != 0 {
		t.Errorf("warnings = %q, want silence for the managed internal key", got.warnings)
	}
	if blob := createdAdditional(t, got); blob["internal"] != nil {
		t.Errorf("stored blob = %v, want internal never saved", blob)
	}
}
