// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/whatsapp"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func whatsappCredentials() map[string]any {
	return map[string]any{
		"phone_number_id": "1234567890",
		"access_token":    "EAAtoken",
		"app_secret":      "appsecret",
		"verify_token":    "verifytoken",
	}
}

func TestWhatsAppCreateStampsTheDefaultResponseFormat(t *testing.T) {
	normalized := normalizeValuesForType("whatsapp", whatsappCredentials())

	if got, _ := normalized["responseFormat"].(string); got != whatsapp.DefaultResponseFormat {
		t.Fatalf("responseFormat = %q, want the WhatsApp default", got)
	}
}

func TestWhatsAppDefaultResponseFormatCoversBothCorrections(t *testing.T) {
	const format = whatsapp.DefaultResponseFormat

	if !strings.Contains(format, "artifact_return") {
		t.Error("the default never names the embed that puts a saved file in the chat, so an agent that saves one leaves the sender nothing to open")
	}
	for _, marker := range []string{"*bold*", "_italic_"} {
		if !strings.Contains(format, marker) {
			t.Errorf("the default does not show %s, so it does not say what WhatsApp actually renders", marker)
		}
	}
	if !strings.Contains(format, "[label](url)") {
		t.Error("the default does not name link syntax, which is the markdown that costs the reader an address they cannot tap")
	}
	if len(format) > 4000 {
		t.Errorf("the default is %d chars, past the %d the platform accepts for a prompt field", len(format), 4000)
	}
}

func TestWhatsAppResponseFormatKeepsAnAuthoredValue(t *testing.T) {
	for _, alias := range []string{"responseFormat", "response_format"} {
		t.Run(alias, func(t *testing.T) {
			values := whatsappCredentials()
			values[alias] = "Answer in Klingon."

			normalized := normalizeValuesForType("whatsapp", values)

			if got, _ := normalized[alias].(string); got != "Answer in Klingon." {
				t.Errorf("%s = %q, want the authored value", alias, got)
			}
			if alias == "response_format" {
				if _, stamped := normalized["responseFormat"]; stamped {
					t.Error("a camelCase default was stamped beside the operator's legacy key; the deploy builder prefers camelCase, so their wording would be dropped")
				}
			}
		})
	}
}

func TestWhatsAppResponseFormatStaysCleared(t *testing.T) {
	values := whatsappCredentials()
	values["responseFormat"] = ""

	normalized := normalizeValuesForType("whatsapp", values)

	if got, _ := normalized["responseFormat"].(string); got != "" {
		t.Fatalf("responseFormat = %q, want the clear to hold", got)
	}
}

func TestNormalizeWhatsAppValuesDoesNotMutateInput(t *testing.T) {
	original := whatsappCredentials()

	normalizeValuesForType("whatsapp", original)

	if _, ok := original["responseFormat"]; ok {
		t.Fatal("normalizeWhatsAppValues mutated its input")
	}
}

func TestWhatsAppDefaultResponseFormatReachesTheRuntimeConfig(t *testing.T) {
	t.Setenv("OBJECT_STORAGE_TYPE", "")
	t.Setenv("S3_BUCKET_NAME", "")

	stored, err := json.Marshal(normalizeValuesForType("whatsapp", whatsappCredentials()))
	if err != nil {
		t.Fatalf("marshal values: %v", err)
	}
	values := string(stored)
	description := "response format fixture"
	gw := &store.Gateway{
		ID: "wa-rf", Name: "WhatsApp RF", Description: &description,
		Type: "whatsapp", Values: &values,
	}

	runtimeYAML, err := NewYAMLService("test-ns", slog.Default()).GenerateGatewayYAML(gw)
	if err != nil {
		t.Fatalf("GenerateGatewayYAML: %v", err)
	}
	if !strings.Contains(runtimeYAML, "response_format:") {
		t.Fatalf("the generated runtime config carries no response_format, so the stored default never reaches an agent:\n%s", runtimeYAML)
	}
	if !strings.Contains(runtimeYAML, "artifact_return") {
		t.Errorf("response_format reached the runtime config without the artifact half:\n%s", runtimeYAML)
	}
}

func TestWhatsAppResponseFormatSurvivesAnUnrelatedEdit(t *testing.T) {
	stored := normalizeValuesForType("whatsapp", whatsappCredentials())

	merged, err := mergeGatewayValuesForUpdate("whatsapp",
		map[string]any{"default_agent_name": "Orchestrator"}, stored, nil)
	if err != nil {
		t.Fatalf("mergeGatewayValuesForUpdate: %v", err)
	}

	if got, _ := merged["responseFormat"].(string); got != whatsapp.DefaultResponseFormat {
		t.Fatalf("responseFormat = %q after editing an unrelated field, want it preserved", got)
	}
}

func TestNormalizeWhatsAppValuesDropsTheRetiredAllowlist(t *testing.T) {
	authored := whatsappCredentials()
	stored := whatsappCredentials()
	stored["responseFormat"] = "keep me"
	stored["allowed_senders"] = []any{"15551230001"}
	stored["allowed_prefixes"] = "1555"

	normalizedAuthored := normalizeValuesForType("whatsapp", authored)
	normalizedStored := normalizeValuesForType("whatsapp", stored)

	for _, retired := range whatsapp.RetiredAllowlistKeys {
		if _, present := normalizedStored[retired]; present {
			t.Errorf("%s survived normalization; every sam config apply would re-deploy the entrypoint", retired)
		}
		if _, present := stored[retired]; !present {
			t.Errorf("%s was deleted from the caller's map; normalization must not mutate its input", retired)
		}
	}
	if len(normalizedAuthored) != len(normalizedStored) {
		t.Errorf("authored bag has %d keys, stored bag %d — the hashes these feed must match",
			len(normalizedAuthored), len(normalizedStored))
	}
}

func TestGenerateGatewayYAML_WarnsWhenTheRetiredAllowlistIsStored(t *testing.T) {
	t.Setenv("OBJECT_STORAGE_TYPE", "")
	t.Setenv("S3_BUCKET_NAME", "")

	for _, tt := range []struct {
		name  string
		extra map[string]any
		want  bool
	}{
		{"a stored allowlist is called out", map[string]any{"allowed_senders": []any{"15551230001"}}, true},
		{"prefixes alone count too", map[string]any{"allowed_prefixes": "1555"}, true},
		{"a row that never had one stays quiet", nil, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			values := whatsappCredentials()
			for k, v := range tt.extra {
				values[k] = v
			}
			stored, err := json.Marshal(values)
			if err != nil {
				t.Fatalf("marshal values: %v", err)
			}
			storedValues := string(stored)

			var buf bytes.Buffer
			svc := NewYAMLService("test-ns", slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
			gw := &store.Gateway{ID: "wa-legacy", Name: "Legacy Support", Type: "whatsapp", Values: &storedValues}

			if _, err := svc.GenerateGatewayYAML(gw); err != nil {
				t.Fatalf("GenerateGatewayYAML: %v", err)
			}

			out := buf.String()
			if got := strings.Contains(out, "no longer restricts anyone"); got != tt.want {
				t.Errorf("warning present = %v, want %v; output = %q", got, tt.want, out)
			}
			if !tt.want {
				return
			}
			for _, want := range []string{"wa-legacy", "Legacy Support"} {
				if !strings.Contains(out, want) {
					t.Errorf("warning does not name the entrypoint (%q); an operator cannot find which one to edit", want)
				}
			}
		})
	}
}

func TestGenerateGatewayYAML_RetiredAllowlistWarnSurvivesANilLogger(t *testing.T) {
	t.Setenv("OBJECT_STORAGE_TYPE", "")
	t.Setenv("S3_BUCKET_NAME", "")

	values := whatsappCredentials()
	values["allowed_senders"] = []any{"15551230001"}
	stored, err := json.Marshal(values)
	if err != nil {
		t.Fatalf("marshal values: %v", err)
	}
	storedValues := string(stored)
	gw := &store.Gateway{ID: "wa-nil-logger", Name: "Nil Logger", Type: "whatsapp", Values: &storedValues}

	if _, err := NewYAMLService("test-ns", nil).GenerateGatewayYAML(gw); err != nil {
		t.Fatalf("GenerateGatewayYAML: %v", err)
	}
}
