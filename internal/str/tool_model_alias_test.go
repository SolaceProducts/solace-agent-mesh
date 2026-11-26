// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
)

func TestToolConfigModelAlias(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		tc      map[string]any
		want    string
		wantErr bool
	}{
		{name: "nil map", tc: nil, want: ""},
		{name: "absent key", tc: map[string]any{"other": "x"}, want: ""},
		{name: "explicit nil is absent", tc: map[string]any{ToolConfigModelAliasKey: nil}, want: ""},
		{name: "present", tc: map[string]any{ToolConfigModelAliasKey: "planning"}, want: "planning"},
		{name: "trimmed", tc: map[string]any{ToolConfigModelAliasKey: "  fast  "}, want: "fast"},
		{name: "blank is no override", tc: map[string]any{ToolConfigModelAliasKey: "   "}, want: ""},
		{name: "int errors", tc: map[string]any{ToolConfigModelAliasKey: 42}, wantErr: true},
		{name: "bool errors", tc: map[string]any{ToolConfigModelAliasKey: true}, wantErr: true},
		{name: "map errors", tc: map[string]any{ToolConfigModelAliasKey: map[string]any{"a": 1}}, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := toolConfigModelAlias(tc.tc)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("toolConfigModelAlias() = (%q, nil), want an error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("toolConfigModelAlias() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestToolConfigModelAliasKeyIsNamespaced(t *testing.T) {
	t.Parallel()
	if !strings.HasPrefix(ToolConfigModelAliasKey, "_") {
		t.Errorf("ToolConfigModelAliasKey = %q, want an underscore-prefixed reserved name", ToolConfigModelAliasKey)
	}
}

func TestToolModelAliasIsHostChosen(t *testing.T) {
	t.Parallel()
	hasModelField := func(v any) string {
		rt := reflect.TypeOf(v)
		for i := range rt.NumField() {
			name := rt.Field(i).Name
			lower := strings.ToLower(name)
			if strings.Contains(lower, "model") || strings.Contains(lower, "alias") {
				return name
			}
		}
		return ""
	}

	if f := hasModelField(remotetool.InvokeParams{}); f != "" {
		t.Errorf("InvokeParams grew field %q; the tool model alias must stay in tool_config, host-side", f)
	}
	if f := hasModelField(llmCallParams{}); f != "" {
		t.Errorf("IPC llmCallParams grew field %q; a sandboxed tool must not be able to select its model", f)
	}
	chatModelFields := []string{"Model"}
	rt := reflect.TypeOf(chatParams{})
	for i := range rt.NumField() {
		name := rt.Field(i).Name
		lower := strings.ToLower(name)
		if (strings.Contains(lower, "model") || strings.Contains(lower, "alias")) && !slices.Contains(chatModelFields, name) {
			t.Errorf("IPC chatParams grew field %q; the only model a tool may name is chatParams.Model, honoured behind the tool_chat_model flag", name)
		}
	}
	chatModels, err := toolConfigChatModels(map[string]any{ToolConfigModelAliasKey: "general"}, false, "")
	if err != nil {
		t.Fatalf("toolConfigChatModels(flag off): %v", err)
	}
	if got, _ := chatModels.pick("opus"); got.alias != "general" || got.requested {
		t.Errorf("flag off: pick(opus) = %+v, want the host-chosen general alias", got)
	}
}
