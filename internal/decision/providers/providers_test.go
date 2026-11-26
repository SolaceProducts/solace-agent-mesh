// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package providers

import (
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/decision"
	"github.com/SolaceProducts/solace-agent-mesh/internal/decision/gemini"
	"github.com/SolaceProducts/solace-agent-mesh/internal/decision/openai"
	"github.com/SolaceProducts/solace-agent-mesh/internal/decision/typesafe"
)

func TestNewEvaluatorBuildsTheNamedProvider(t *testing.T) {
	ts, err := NewEvaluator(Config{Provider: decision.ProviderTypeSafe, APIKey: "k"})
	if err != nil {
		t.Fatalf("typesafe: %v", err)
	}
	if _, ok := ts.(*typesafe.Client); !ok {
		t.Errorf("typesafe evaluator is %T", ts)
	}
	oa, err := NewEvaluator(Config{Provider: decision.ProviderOpenAI, APIKey: "k"})
	if err != nil {
		t.Fatalf("openai: %v", err)
	}
	if _, ok := oa.(*openai.Client); !ok {
		t.Errorf("openai evaluator is %T", oa)
	}
	gm, err := NewEvaluator(Config{Provider: decision.ProviderGoogleAIStudio, APIKey: "k"})
	if err != nil {
		t.Fatalf("gemini: %v", err)
	}
	if _, ok := gm.(*gemini.Client); !ok {
		t.Errorf("gemini evaluator is %T", gm)
	}
}

func TestNewEvaluatorRejectsAnUnknownProvider(t *testing.T) {
	_, err := NewEvaluator(Config{Provider: "acme", APIKey: "k"})
	if decision.Classify(err) != decision.FailureConfig {
		t.Fatalf("err = %v, want a config failure", err)
	}
	if !strings.Contains(err.Error(), `"acme"`) || !strings.Contains(err.Error(), "typesafe, google_ai_studio, openai") {
		t.Errorf("err = %q, want the provider named and the supported list", err)
	}
}

func TestNewEvaluatorPassesClientErrorsThrough(t *testing.T) {
	for _, provider := range Supported() {
		evaluator, err := NewEvaluator(Config{Provider: provider})
		if decision.Classify(err) != decision.FailureConfig {
			t.Errorf("%s without a key: err = %v, want a config failure", provider, err)
		}
		if evaluator != nil {
			t.Errorf("%s without a key: evaluator = %#v, want a nil interface", provider, evaluator)
		}
	}
}

func TestSupportedListsTypeSafeFirst(t *testing.T) {
	if got := Supported(); len(got) != 3 || got[0] != decision.ProviderTypeSafe {
		t.Errorf("Supported() = %v, want typesafe first", got)
	}
}

func TestDefaults(t *testing.T) {
	cases := map[string][2]string{
		decision.ProviderTypeSafe:       {typesafe.DefaultModel, typesafe.DefaultBaseURL},
		decision.ProviderOpenAI:         {openai.DefaultModel, openai.DefaultBaseURL},
		decision.ProviderGoogleAIStudio: {gemini.DefaultModel, gemini.DefaultBaseURL},
		"acme":                          {"", ""},
	}
	for provider, want := range cases {
		if got := DefaultModel(provider); got != want[0] {
			t.Errorf("DefaultModel(%q) = %q, want %q", provider, got, want[0])
		}
		if got := DefaultBaseURL(provider); got != want[1] {
			t.Errorf("DefaultBaseURL(%q) = %q, want %q", provider, got, want[1])
		}
	}
}

func TestAcceptsImages(t *testing.T) {
	cases := []struct {
		provider, model string
		want            bool
	}{
		{decision.ProviderTypeSafe, "", true},
		{decision.ProviderTypeSafe, "jev-1.13.0", true},
		{decision.ProviderTypeSafe, "jev-1.14.0", true},
		{decision.ProviderTypeSafe, "typesafe/jev-1.13", true},
		{decision.ProviderTypeSafe, "acme/decider-4b", false},
		{decision.ProviderTypeSafe, "openai/gpt-6-luna-decisions", true},
		{decision.ProviderTypeSafe, "openai/gpt-6-luna-decisions-20261006", true},
		{decision.ProviderOpenAI, "", true},
		{decision.ProviderOpenAI, "gpt-6-luna", true},
		{decision.ProviderGoogleAIStudio, "", true},
		{decision.ProviderGoogleAIStudio, "gemini-decision-1", true},
		{"acme", "openai/gpt-6-luna-decisions", false},
	}
	for _, c := range cases {
		if got := Accepts(c.provider, c.model, decision.MediaImage); got != c.want {
			t.Errorf("Accepts image(%q, %q) = %v, want %v", c.provider, c.model, got, c.want)
		}
	}
}

func TestAcceptsByKind(t *testing.T) {
	kinds := []decision.MediaKind{decision.MediaImage, decision.MediaAudio, decision.MediaVideo, decision.MediaDocument}
	cases := []struct {
		provider, model string
		reads           []decision.MediaKind
	}{
		{decision.ProviderTypeSafe, "jev-1.14.0", []decision.MediaKind{decision.MediaImage}},
		{decision.ProviderTypeSafe, "acme/decider-4b", nil},
		{decision.ProviderOpenAI, "", []decision.MediaKind{decision.MediaImage}},
		{decision.ProviderGoogleAIStudio, "", kinds},
		{"acme", "", nil},
	}
	for _, c := range cases {
		for _, kind := range kinds {
			want := slices.Contains(c.reads, kind)
			if got := Accepts(c.provider, c.model, kind); got != want {
				t.Errorf("Accepts(%q, %q, %s) = %v, want %v", c.provider, c.model, kind, got, want)
			}
		}
	}
}

func TestMIMETypesPerProvider(t *testing.T) {
	stillTypes := []string{"image/png", "image/jpeg", "image/webp", "image/gif", "image/bmp", "image/tiff"}
	cases := []struct {
		provider, model string
		want            []string
	}{
		{decision.ProviderOpenAI, "", stillTypes},
		{decision.ProviderTypeSafe, "", stillTypes},
		{decision.ProviderGoogleAIStudio, "", decision.MediaMIMETypes(decision.MediaImage)},
	}
	for _, c := range cases {
		got := MIMETypes(c.provider, c.model, decision.MediaImage)
		if !slices.Equal(slices.Sorted(slices.Values(got)), slices.Sorted(slices.Values(c.want))) {
			t.Errorf("MIMETypes(%q, image) = %v, want %v", c.provider, got, c.want)
		}
	}
	if types := MIMETypes(decision.ProviderGoogleAIStudio, "", decision.MediaImage); !slices.Contains(types, "image/heic") {
		t.Errorf("Gemini image types %v leave out image/heic", types)
	}
	for _, provider := range []string{decision.ProviderOpenAI, decision.ProviderTypeSafe} {
		if types := MIMETypes(provider, "", decision.MediaImage); slices.Contains(types, "image/heic") {
			t.Errorf("%s image types %v include image/heic, which nothing can convert", provider, types)
		}
	}
}
