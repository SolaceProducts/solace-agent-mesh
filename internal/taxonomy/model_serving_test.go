// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package taxonomy

import (
	"slices"
	"testing"
)

func TestModelServedFromReadsTheApiBaseAndNotJustTheProvider(t *testing.T) {
	cases := []struct {
		name     string
		provider string
		apiBase  string
		want     string
	}{
		{"a vendor endpoint", "openai", "https://api.openai.com/v1", ModelTypeHosted},
		{"a vendor default", "anthropic", "", ModelTypeHosted},
		{"an OpenAI-compatible server on this machine", "openai", "http://localhost:11434/v1", ModelTypeLocal},
		{"the same by loopback address", "openai", "http://127.0.0.1:8000/v1", ModelTypeLocal},
		{"the same bound to every interface", "openai", "http://0.0.0.0:8000/v1", ModelTypeLocal},
		{"the same over IPv6", "openai", "http://[::1]:8000/v1", ModelTypeLocal},
		{"the same from inside a container", "openai", "http://host.docker.internal:11434/v1", ModelTypeLocal},
		{"an endpoint the operator hosts elsewhere", "openai", "https://llm.corp.example.com/v1", ModelTypeHosted},
		{"a value that is not a URL", "openai", "not a url at all", ModelTypeHosted},
		{"ollama with no endpoint", "ollama", "", ModelTypeLocal},
		{"vllm with no endpoint", "vllm", "", ModelTypeLocal},
		{"sglang with no endpoint", "sgl", "", ModelTypeLocal},
		{"ollama reached over a network", "ollama", "https://gpu-box.corp.example.com/v1", ModelTypeLocal},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ModelServedFrom(c.provider, c.apiBase); got != c.want {
				t.Errorf("ModelServedFrom(%q, %q) = %q, want %q", c.provider, c.apiBase, got, c.want)
			}
		})
	}
}

func TestModelServedFromOnlyEverAnswersFromTheClosedSet(t *testing.T) {
	for _, provider := range []string{"", "openai", "acme-internal-vllm-cluster", "OLLAMA"} {
		for _, apiBase := range []string{"", "https://acme-secret.internal/v1", "http://localhost:1/v1", "%%"} {
			got := ModelServedFrom(provider, apiBase)
			if !slices.Contains(ModelTypes, got) {
				t.Errorf("ModelServedFrom(%q, %q) = %q, which the catalog does not approve", provider, apiBase, got)
			}
		}
	}
}

func TestEveryLocalProviderIsAnApprovedProviderLabel(t *testing.T) {
	for _, provider := range localModelProviders {
		if !slices.Contains(ProviderLabels, provider) {
			t.Errorf("%q is treated as locally served but is not a provider label a run can report", provider)
		}
	}
}

func TestModelServedFromNamesTheFreeModel(t *testing.T) {
	linked := freeModelAPIBase
	t.Cleanup(func() { freeModelAPIBase = linked })
	freeModelAPIBase = "https://free-llm.example.com/"

	for apiBase, want := range map[string]string{
		"https://free-llm.example.com/v1":       ModelTypeFreeModel,
		"https://free-llm.example.com":          ModelTypeFreeModel,
		"https://free-llm.example.com.evil/v1":  ModelTypeHosted,
		"https://free-llm.example.community/v1": ModelTypeHosted,
		"https://api.openai.com/v1":             ModelTypeHosted,
	} {
		if got := ModelServedFrom("openai", apiBase); got != want {
			t.Errorf("ModelServedFrom(openai, %q) = %q, want %q", apiBase, got, want)
		}
	}

	freeModelAPIBase = ""
	if got := ModelServedFrom("openai", "https://free-llm.example.com/v1"); got != ModelTypeHosted {
		t.Errorf("a build without the free model reports %q", got)
	}
}
