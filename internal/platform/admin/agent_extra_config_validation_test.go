// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func TestValidateAdditionalConfigurations_AcceptedShapes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		raw  string
	}{
		{"empty", ""},
		{"empty object", "{}"},
		{"streaming", `{"supportsStreaming":true}`},
		{"nested allowed key", `{"agentCardPublishing":{"intervalSeconds":30}}`},
		{"deeply nested array", `{"a":{"b":[1,2,{"c":"x"}]}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := validateAdditionalConfigurations(json.RawMessage(tc.raw))
			if err != nil {
				t.Fatalf("expected accept, got error: %v", err)
			}
		})
	}
}

func TestValidateAdditionalConfigurations_RejectsNonObjectShapes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		raw  string
	}{
		{"array", `["a","b"]`},
		{"scalar string", `"hi"`},
		{"scalar number", `42`},
		{"scalar bool", `true`},
		{"null", `null`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := validateAdditionalConfigurations(json.RawMessage(tc.raw))
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			ve, ok := errors.AsType[*ValidationError](err)
			if !ok {
				t.Fatalf("expected *ValidationError, got %T: %v", err, err)
			}
			if !strings.Contains(ve.Message, "must be a JSON object") &&
				!strings.Contains(ve.Message, "not valid JSON") {
				t.Fatalf("unexpected error message: %q", ve.Message)
			}
		})
	}
}

func TestValidateAdditionalConfigurations_RejectsOversizedPayload(t *testing.T) {
	t.Parallel()
	pad := strings.Repeat("a", additionalConfigurationsMaxBytes+128)
	blob := []byte(`{"k":"` + pad + `"}`)
	if len(blob) <= additionalConfigurationsMaxBytes {
		t.Fatalf("test setup wrong: blob size %d not over the limit", len(blob))
	}
	err := validateAdditionalConfigurations(blob)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	ve, ok := errors.AsType[*ValidationError](err)
	if !ok {
		t.Fatalf("expected *ValidationError, got %T", err)
	}
	if !strings.Contains(ve.Message, "exceeds") {
		t.Fatalf("unexpected error message: %q", ve.Message)
	}
}

func TestValidateAdditionalConfigurations_RejectsDeepNesting(t *testing.T) {
	t.Parallel()
	depth := additionalConfigurationsMaxDepth + 2
	var b strings.Builder
	for range depth {
		b.WriteString(`{"a":`)
	}
	b.WriteString(`true`)
	for range depth {
		b.WriteString(`}`)
	}
	err := validateAdditionalConfigurations([]byte(b.String()))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	ve, ok := errors.AsType[*ValidationError](err)
	if !ok {
		t.Fatalf("expected *ValidationError, got %T", err)
	}
	if !strings.Contains(ve.Message, "nesting") {
		t.Fatalf("unexpected error message: %q", ve.Message)
	}
}

func TestValidateAdditionalConfigurations_AcceptsAtMaxDepth(t *testing.T) {
	t.Parallel()
	depth := additionalConfigurationsMaxDepth
	var b strings.Builder
	for range depth - 1 {
		b.WriteString(`{"a":`)
	}
	b.WriteString(`{"b":1}`)
	for range depth - 1 {
		b.WriteString(`}`)
	}
	err := validateAdditionalConfigurations([]byte(b.String()))
	if err != nil {
		t.Fatalf("expected accept at exactly maxDepth, got error: %v", err)
	}
}

func TestValidateAdditionalConfigurations_RejectsForbiddenStructuredKeys(t *testing.T) {
	t.Parallel()
	for key := range forbiddenAdditionalConfigStructuredKeys {
		t.Run(key, func(t *testing.T) {
			t.Parallel()
			blob, _ := json.Marshal(map[string]any{key: "x"})
			err := validateAdditionalConfigurations(blob)
			if err == nil {
				t.Fatalf("expected rejection for key %q", key)
			}
			ve, ok := errors.AsType[*ValidationError](err)
			if !ok {
				t.Fatalf("expected *ValidationError, got %T", err)
			}
			if !strings.Contains(ve.Message, "structured agent request") {
				t.Fatalf("unexpected error message: %q", ve.Message)
			}
		})
	}
}

func TestValidateAdditionalConfigurations_RejectsForbiddenComputedKeys(t *testing.T) {
	t.Parallel()
	for key := range forbiddenAdditionalConfigComputedKeys {
		t.Run(key, func(t *testing.T) {
			t.Parallel()
			blob, _ := json.Marshal(map[string]any{key: "x"})
			err := validateAdditionalConfigurations(blob)
			if err == nil {
				t.Fatalf("expected rejection for key %q", key)
			}
			ve, ok := errors.AsType[*ValidationError](err)
			if !ok {
				t.Fatalf("expected *ValidationError, got %T", err)
			}
			if !strings.Contains(ve.Message, "computed by the platform") {
				t.Fatalf("unexpected error message: %q", ve.Message)
			}
		})
	}
}

func TestValidateAdditionalConfigurations_AcceptsUnsupportedKeys(t *testing.T) {
	t.Parallel()
	for key := range strippedUnsupportedKeys {
		t.Run(key, func(t *testing.T) {
			t.Parallel()
			blob, _ := json.Marshal(map[string]any{key: map[string]any{"require_approval": true}})
			if err := validateAdditionalConfigurations(blob); err != nil {
				t.Fatalf("unimplemented key %q must not block the write, got: %v", key, err)
			}
		})
	}
}

func TestNormalizeAdditionalConfigurations_StripsUnsupportedKeys(t *testing.T) {
	t.Parallel()
	for key := range strippedUnsupportedKeys {
		t.Run(key, func(t *testing.T) {
			t.Parallel()
			blob, _ := json.Marshal(map[string]any{
				key:         map[string]any{"require_approval": true},
				"volumes":   []any{"vol-a"},
				"agentCard": map[string]any{"description": "keep me"},
			})
			var got map[string]json.RawMessage
			if err := json.Unmarshal(normalizeAdditionalConfigurations(blob), &got); err != nil {
				t.Fatalf("normalized blob does not parse: %v", err)
			}
			if _, present := got[key]; present {
				t.Errorf("%q survived normalization; it must be stripped on store", key)
			}
			for _, keep := range []string{"volumes", "agentCard"} {
				if _, present := got[keep]; !present {
					t.Errorf("stripping %q also dropped %q — only the unsupported key may be removed", key, keep)
				}
			}
		})
	}
}

func TestNormalizeAdditionalConfigurations_LeavesCleanBlobsByteIdentical(t *testing.T) {
	t.Parallel()
	blob := json.RawMessage(`{"volumes":["vol-a"],"agentCard":{"description":"unchanged"}}`)
	if got := normalizeAdditionalConfigurations(blob); !bytes.Equal(got, blob) {
		t.Errorf("blob without an unsupported key was rewritten:\n got %s\nwant %s", got, blob)
	}
}

func TestStrippedUnsupportedKeys_CoversAgentLevelHIL(t *testing.T) {
	t.Parallel()
	if _, ok := strippedUnsupportedKeys["hil"]; !ok {
		t.Error("strippedUnsupportedKeys must contain \"hil\": HIL is per-tool, and an agent-level block gates nothing")
	}
}

func TestValidateAdditionalConfigurations_AllowsForbiddenKeysInNested(t *testing.T) {
	t.Parallel()
	blob := []byte(`{"some_block":{"tools":[{"x":1}]}}`)
	if err := validateAdditionalConfigurations(blob); err != nil {
		t.Fatalf("expected accept, got error: %v", err)
	}
}

func TestValidateAdditionalConfigurations_RejectsNULInKey(t *testing.T) {
	t.Parallel()
	blob := []byte("{\"foo\\u0000bar\":1}")
	err := validateAdditionalConfigurations(blob)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	ve, ok := errors.AsType[*ValidationError](err)
	if !ok {
		t.Fatalf("expected *ValidationError, got %T", err)
	}
	if !strings.Contains(ve.Message, "NUL") {
		t.Fatalf("unexpected error message: %q", ve.Message)
	}
}

func TestValidateAdditionalConfigurations_RejectsOversizedKey(t *testing.T) {
	t.Parallel()
	bigKey := strings.Repeat("k", additionalConfigurationsMaxKeyLen+1)
	blob, _ := json.Marshal(map[string]any{bigKey: 1})
	err := validateAdditionalConfigurations(blob)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	ve, ok := errors.AsType[*ValidationError](err)
	if !ok {
		t.Fatalf("expected *ValidationError, got %T", err)
	}
	if !strings.Contains(ve.Message, "exceeds") {
		t.Fatalf("unexpected error message: %q", ve.Message)
	}
}

func TestValidateAdditionalConfigurations_AcceptsAtKeyLengthLimit(t *testing.T) {
	t.Parallel()
	exactKey := strings.Repeat("k", additionalConfigurationsMaxKeyLen)
	blob, _ := json.Marshal(map[string]any{exactKey: 1})
	if err := validateAdditionalConfigurations(blob); err != nil {
		t.Fatalf("expected accept at exact limit, got error: %v", err)
	}
}

func TestNormalizeAdditionalConfigurations_DefaultsToEmptyObject(t *testing.T) {
	t.Parallel()
	got := normalizeAdditionalConfigurations(nil)
	if string(got) != "{}" {
		t.Fatalf("expected {}, got %q", got)
	}
	got = normalizeAdditionalConfigurations(json.RawMessage{})
	if string(got) != "{}" {
		t.Fatalf("expected {}, got %q", got)
	}
}

func TestCloneAdditionalConfigurations_ElidesEmptyObject(t *testing.T) {
	t.Parallel()
	if got := cloneAdditionalConfigurations(nil); got != nil {
		t.Fatalf("expected nil for nil input, got %q", got)
	}
	if got := cloneAdditionalConfigurations([]byte("{}")); got != nil {
		t.Fatalf("expected nil for empty object, got %q", got)
	}
	if got := cloneAdditionalConfigurations([]byte("  {}  ")); got != nil {
		t.Fatalf("expected nil for whitespace-padded empty object, got %q", got)
	}
	src := []byte(`{"k":1}`)
	got := cloneAdditionalConfigurations(src)
	if string(got) != `{"k":1}` {
		t.Fatalf("expected exact copy, got %q", got)
	}
	src[0] = '['
	if string(got) != `{"k":1}` {
		t.Fatalf("clone aliased source bytes: %q", got)
	}
}

func TestComputeAgentConfigHash_AdditionalConfigurations(t *testing.T) {
	t.Parallel()
	base := &store.Agent{
		Name:                     "agent",
		Description:              strPtr("desc"),
		SystemPrompt:             strPtr("prompt"),
		AdditionalConfigurations: []byte(`{"a":1,"b":2}`),
	}
	mutated := &store.Agent{
		Name:                     "agent",
		Description:              strPtr("desc"),
		SystemPrompt:             strPtr("prompt"),
		AdditionalConfigurations: []byte(`{"a":1,"b":3}`),
	}
	reordered := &store.Agent{
		Name:                     "agent",
		Description:              strPtr("desc"),
		SystemPrompt:             strPtr("prompt"),
		AdditionalConfigurations: []byte(`{"b":2,"a":1}`),
	}
	args := func() (toolsets, in, out, conn []string) {
		return []string{"tools"}, []string{"text"}, []string{"text"}, []string{"c1"}
	}
	bts, bin, bout, bconn := args()
	hBase := computeAgentConfigHash(base, bts, bin, bout, bconn, nil)
	hMut := computeAgentConfigHash(mutated, bts, bin, bout, bconn, nil)
	hReorder := computeAgentConfigHash(reordered, bts, bin, bout, bconn, nil)

	if hBase == hMut {
		t.Errorf("expected hash to change when blob mutates: both %s", hBase)
	}
	if hBase != hReorder {
		t.Errorf("expected hash stable across key reordering: %s vs %s", hBase, hReorder)
	}
}

func TestCanonicalizeAdditionalConfigurationsForHash_StableForReorderings(t *testing.T) {
	t.Parallel()
	a := canonicalizeAdditionalConfigurationsForHash([]byte(`{"a":1,"b":2}`))
	b := canonicalizeAdditionalConfigurationsForHash([]byte(`{"b":2,"a":1}`))
	if a != b {
		t.Fatalf("expected stable hash for reordering, got %q vs %q", a, b)
	}
	if c := canonicalizeAdditionalConfigurationsForHash(nil); c != "{}" {
		t.Fatalf("expected {} for nil, got %q", c)
	}
	if c := canonicalizeAdditionalConfigurationsForHash([]byte(`not json`)); c != "{}" {
		t.Fatalf("expected {} for unparseable, got %q", c)
	}
}

func TestAgentService_AdditionalConfigurations_RoundTrip(t *testing.T) {
	t.Parallel()
	svc := newAgentServiceForTest(t)
	ctx := t.Context()

	req := CreateAgentRequest{
		Name:                     "round-trip-agent",
		Description:              "A valid description that is long enough",
		SystemPrompt:             "You are helpful.",
		Toolsets:                 []string{"builtin_artifact_tools"},
		AdditionalConfigurations: json.RawMessage(`{"supportsStreaming":true,"agentCardPublishing":{"intervalSeconds":30}}`),
	}
	created, err := svc.Create(ctx, req, "u")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.AdditionalConfigurations == nil {
		t.Fatalf("expected response to carry AdditionalConfigurations, got nil")
	}

	got, err := svc.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.AdditionalConfigurations == nil {
		t.Fatalf("expected GetByID to return AdditionalConfigurations, got nil")
	}

	var parsedIn, parsedOut map[string]any
	if err := json.Unmarshal(req.AdditionalConfigurations, &parsedIn); err != nil {
		t.Fatalf("parse input: %v", err)
	}
	if err := json.Unmarshal(got.AdditionalConfigurations, &parsedOut); err != nil {
		t.Fatalf("parse output: %v", err)
	}
	if !mapsEquivalent(parsedIn, parsedOut) {
		t.Fatalf("round-trip mismatch:\n  in:  %s\n  out: %s", req.AdditionalConfigurations, got.AdditionalConfigurations)
	}
}

func TestAgentService_AdditionalConfigurations_DefaultElidedFromResponse(t *testing.T) {
	t.Parallel()
	svc := newAgentServiceForTest(t)
	ctx := t.Context()

	created, err := svc.Create(ctx, CreateAgentRequest{
		Name:         "default-blob-agent",
		Description:  "A valid description that is long enough",
		SystemPrompt: "You are helpful.",
		Toolsets:     []string{"builtin_artifact_tools"},
	}, "u")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.AdditionalConfigurations != nil {
		t.Errorf("expected nil AdditionalConfigurations on default response, got %s", created.AdditionalConfigurations)
	}

	wire, err := json.Marshal(created)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(wire), "additionalConfigurations") {
		t.Errorf("expected 'additionalConfigurations' to be omitted; wire JSON: %s", wire)
	}
}

func TestAgentService_Create_RejectsInvalidAdditionalConfigurations(t *testing.T) {
	t.Parallel()
	svc := newAgentServiceForTest(t)

	_, err := svc.Create(t.Context(), CreateAgentRequest{
		Name:                     "bad-blob-agent",
		Description:              "A valid description that is long enough",
		SystemPrompt:             "You are helpful.",
		Toolsets:                 []string{"builtin_artifact_tools"},
		AdditionalConfigurations: json.RawMessage(`"not an object"`),
	}, "u")
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	if _, ok := errors.AsType[*ValidationError](err); !ok {
		t.Fatalf("expected *ValidationError, got %T: %v", err, err)
	}
}

func mapsEquivalent(a, b map[string]any) bool {
	if len(a) != len(b) {
		return false
	}
	for k, va := range a {
		vb, ok := b[k]
		if !ok {
			return false
		}
		if !valuesEquivalent(va, vb) {
			return false
		}
	}
	return true
}

func valuesEquivalent(a, b any) bool {
	switch av := a.(type) {
	case map[string]any:
		bv, ok := b.(map[string]any)
		return ok && mapsEquivalent(av, bv)
	case []any:
		bv, ok := b.([]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for i := range av {
			if !valuesEquivalent(av[i], bv[i]) {
				return false
			}
		}
		return true
	default:
		return a == b
	}
}
