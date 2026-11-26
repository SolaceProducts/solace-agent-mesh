// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"maps"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
)

func TestAddModelTypeToRaw(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		raw       map[string]any
		modelType string
		provider  string
		want      map[string]any
	}{
		{
			name:      "decision model sets the type and provider keys",
			raw:       map[string]any{"model": "jev-1.13.0"},
			modelType: store.ModelTypeDecision,
			provider:  ProviderTypeSafe,
			want:      map[string]any{"model": "jev-1.13.0", RawModelTypeKey: store.ModelTypeDecision, RawDecisionProviderKey: ProviderTypeSafe},
		},
		{
			name:      "openai decision model names its provider",
			raw:       map[string]any{"model": "gpt-6-luna"},
			modelType: store.ModelTypeDecision,
			provider:  ProviderOpenAI,
			want:      map[string]any{"model": "gpt-6-luna", RawModelTypeKey: store.ModelTypeDecision, RawDecisionProviderKey: ProviderOpenAI},
		},
		{
			name:      "chat model with forged keys has them removed",
			raw:       map[string]any{"model": "gpt-4o", RawModelTypeKey: store.ModelTypeDecision, RawDecisionProviderKey: ProviderOpenAI},
			modelType: store.ModelTypeChat,
			provider:  ProviderOpenAI,
			want:      map[string]any{"model": "gpt-4o"},
		},
		{
			name:      "decision model with a forged provider gets its stored one",
			raw:       map[string]any{"model": "jev-1.13.0", RawDecisionProviderKey: ProviderOpenAI},
			modelType: store.ModelTypeDecision,
			provider:  ProviderTypeSafe,
			want:      map[string]any{"model": "jev-1.13.0", RawModelTypeKey: store.ModelTypeDecision, RawDecisionProviderKey: ProviderTypeSafe},
		},
		{
			name:      "chat model without the keys is unchanged",
			raw:       map[string]any{"model": "gpt-4o"},
			modelType: store.ModelTypeChat,
			provider:  ProviderOpenAI,
			want:      map[string]any{"model": "gpt-4o"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			AddModelTypeToRaw(tc.raw, tc.modelType, tc.provider)
			if !maps.Equal(tc.raw, tc.want) {
				t.Errorf("raw = %v, want %v", tc.raw, tc.want)
			}
			if got, want := samapp.IsDecisionModel(tc.raw), tc.modelType == store.ModelTypeDecision; got != want {
				t.Errorf("IsDecisionModel = %v, want %v", got, want)
			}
		})
	}
}

func TestResolveModelType(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		modelType string
		provider  string
		want      string
		wantErr   string
	}{
		{name: "omitted type is chat", modelType: "", provider: ProviderOpenAI, want: store.ModelTypeChat},
		{name: "decision on typesafe", modelType: store.ModelTypeDecision, provider: ProviderTypeSafe, want: store.ModelTypeDecision},
		{name: "decision on openai", modelType: store.ModelTypeDecision, provider: ProviderOpenAI, want: store.ModelTypeDecision},
		{name: "chat on openai", modelType: store.ModelTypeChat, provider: ProviderOpenAI, want: store.ModelTypeChat},
		{name: "decision on a chat-only provider", modelType: store.ModelTypeDecision, provider: "anthropic", wantErr: `must use provider "typesafe" or "google_ai_studio" or "openai", got "anthropic"`},
		{name: "typesafe with an omitted type", modelType: "", provider: ProviderTypeSafe, wantErr: "serves decision models only"},
		{name: "unknown type", modelType: "embedding", provider: ProviderOpenAI, wantErr: "modelType must be"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ResolveModelType(tc.modelType, tc.provider)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("ResolveModelType(%q, %q) = %q, %v; want an error mentioning %q", tc.modelType, tc.provider, got, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("ResolveModelType(%q, %q) = %q, want %q", tc.modelType, tc.provider, got, tc.want)
			}
		})
	}
}

func TestValidateModelTypeUpdate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name           string
		stored         string
		storedProvider string
		provider       string
		wantErr        bool
	}{
		{name: "chat model on a chat provider", stored: store.ModelTypeChat, storedProvider: ProviderOpenAI, provider: ProviderOpenAI},
		{name: "chat model moved between chat providers", stored: store.ModelTypeChat, storedProvider: ProviderOpenAI, provider: "anthropic"},
		{name: "decision model on typesafe", stored: store.ModelTypeDecision, storedProvider: ProviderTypeSafe, provider: ProviderTypeSafe},
		{name: "decision model moved from typesafe to openai", stored: store.ModelTypeDecision, storedProvider: ProviderTypeSafe, provider: ProviderOpenAI, wantErr: true},
		{name: "decision model moved from openai to typesafe", stored: store.ModelTypeDecision, storedProvider: ProviderOpenAI, provider: ProviderTypeSafe, wantErr: true},
		{name: "decision model moved to a chat provider", stored: store.ModelTypeDecision, storedProvider: ProviderTypeSafe, provider: "anthropic", wantErr: true},
		{name: "chat model moved onto typesafe", stored: store.ModelTypeChat, storedProvider: ProviderOpenAI, provider: ProviderTypeSafe, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateModelTypeUpdate(tc.stored, tc.storedProvider, tc.provider)
			if gotErr := err != nil; gotErr != tc.wantErr {
				t.Errorf("err = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}
