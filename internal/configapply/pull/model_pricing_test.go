// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pull

import (
	"net/http"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
)

func TestPullOmitsUnstatedRates(t *testing.T) {
	input, output := "3", "15"
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+platform.APIV2Prefix+"/models", func(w http.ResponseWriter, _ *http.Request) {
		writeV2Page(w, []wire.ModelResponse{{
			ID: "id-1", Alias: "priced", Provider: new("anthropic"), ModelName: new("claude"),
			Pricing: &apiv2.Pricing{Input: &input, Output: &output},
		}})
	})

	entries, _, err := pullModels(t.Context(), newV2PlatformClient(t, mux), Options{}, &Result{})
	if err != nil || len(entries) != 1 {
		t.Fatalf("pullModels: %d entries, err %v", len(entries), err)
	}
	yaml := string(entries[0].content)
	if !strings.Contains(yaml, "pricing:") || !strings.Contains(yaml, `input: "3"`) {
		t.Fatalf("pulled model lost its price:\n%s", yaml)
	}
	if strings.Contains(yaml, "cacheRead") || strings.Contains(yaml, "cacheWrite") {
		t.Errorf("pulled model carries unstated cache rates:\n%s", yaml)
	}
}

func TestPullOmitsTheDerivedAuthType(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+platform.APIV2Prefix+"/models", func(w http.ResponseWriter, _ *http.Request) {
		writeV2Page(w, []wire.ModelResponse{{
			ID: "id-1", Alias: "keyed", Provider: new("anthropic"), ModelName: new("claude"),
			AuthType: "apikey", AuthConfig: map[string]any{"type": "apikey", "api_key": "<REDACTED>"},
		}})
	})

	entries, _, err := pullModels(t.Context(), newV2PlatformClient(t, mux), Options{}, &Result{})
	if err != nil || len(entries) != 1 {
		t.Fatalf("pullModels: %d entries, err %v", len(entries), err)
	}
	yaml := string(entries[0].content)
	if strings.Contains(yaml, "authType") {
		t.Errorf("pulled model carries the derived authType:\n%s", yaml)
	}
	if !strings.Contains(yaml, "type: apikey") {
		t.Errorf("pulled model lost authConfig.type:\n%s", yaml)
	}
}

func TestPullLeavesOutGrantModels(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+platform.APIV2Prefix+"/models", func(w http.ResponseWriter, _ *http.Request) {
		writeV2Page(w, []wire.ModelResponse{
			{ID: "id-1", Alias: "general", Provider: new("openai"), ModelName: new("free"), AuthType: platform.AuthTypeOAuth2Delegated,
				AuthConfig: map[string]any{"type": platform.AuthTypeOAuth2Delegated, "grant_id": "0192f7e2-8a4b-7c3d-9e5f-1a2b3c4d5e6f"}},
			{ID: "id-2", Alias: "keyed", Provider: new("anthropic"), ModelName: new("claude"),
				AuthType: "apikey", AuthConfig: map[string]any{"type": "apikey", "api_key": "<REDACTED>"}},
		})
	})

	entries, names, err := pullModels(t.Context(), newV2PlatformClient(t, mux), Options{}, &Result{})
	if err != nil {
		t.Fatalf("pullModels: %v", err)
	}
	if len(entries) != 1 || len(names) != 1 || names[0] != "keyed" {
		t.Fatalf("pulled %v; want only the apikey model", names)
	}
}
