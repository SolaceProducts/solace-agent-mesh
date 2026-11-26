// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"slices"
	"strings"
	"testing"
)

type staleSkill struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type skillEnvelope struct {
	Data []staleSkill   `json:"data"`
	Meta map[string]any `json:"meta"`
}

type looseDTO struct {
	ID           string         `json:"id"`
	ConfigSchema map[string]any `json:"configSchema"`
}

func TestDetectUnknownKeys_TopLevelExtraKey(t *testing.T) {
	raw := []byte(`{"id":"a","name":"n","contentHash":"abc123"}`)
	got := detectUnknownKeys(raw, &staleSkill{})
	if !slices.Equal(got, []string{"contentHash"}) {
		t.Fatalf("got %v, want [contentHash]", got)
	}
}

func TestDetectUnknownKeys_AllKnown(t *testing.T) {
	raw := []byte(`{"id":"a","name":"n"}`)
	if got := detectUnknownKeys(raw, &staleSkill{}); got != nil {
		t.Fatalf("got %v, want nil", got)
	}
}

func TestDetectUnknownKeys_ListElement(t *testing.T) {
	raw := []byte(`{"data":[{"id":"a","name":"n","contentHash":"h1"},{"id":"b","name":"m","contentHash":"h2"}],"meta":{}}`)
	got := detectUnknownKeys(raw, &skillEnvelope{})
	if !slices.Equal(got, []string{"contentHash"}) {
		t.Fatalf("got %v, want [contentHash] (deduped across elements)", got)
	}
}

func TestDetectUnknownKeys_CatchAllSuppressed(t *testing.T) {
	raw := []byte(`{"id":"a","configSchema":{"x":1},"somethingNew":true}`)
	if got := detectUnknownKeys(raw, &looseDTO{}); got != nil {
		t.Fatalf("got %v, want nil — catch-all struct must not report skew", got)
	}
}

func TestDetectUnknownKeys_NonStruct(t *testing.T) {
	raw := []byte(`{"x":1}`)
	if got := detectUnknownKeys(raw, new(string)); got != nil {
		t.Fatalf("got %v, want nil for non-struct dst", got)
	}
}

func TestSchemaSkewSink_ConsolidatesPaths(t *testing.T) {
	s := NewSchemaSkewSink()
	if !s.Empty() {
		t.Fatal("new sink should be empty")
	}
	s.record("/api/v1/platform/skills", []string{"contentHash"})
	s.record("/api/v1/platform/skills", []string{"contentHash"})
	s.record("/api/v1/platform/agents", []string{"contentHash", "newField"})

	if s.Empty() {
		t.Fatal("sink should not be empty after records")
	}
	findings := s.Findings()
	if len(findings) != 2 {
		t.Fatalf("got %d findings, want 2", len(findings))
	}
	if findings[0].Key != "contentHash" {
		t.Errorf("findings[0].Key = %q, want contentHash", findings[0].Key)
	}
	wantPaths := []string{"/api/v1/platform/agents", "/api/v1/platform/skills"}
	if !slices.Equal(findings[0].Paths, wantPaths) {
		t.Errorf("contentHash paths = %v, want %v", findings[0].Paths, wantPaths)
	}
}

func TestFormatSchemaSkew(t *testing.T) {
	if got := FormatSchemaSkew(NewSchemaSkewSink()); got != "" {
		t.Errorf("empty sink should format to empty string, got %q", got)
	}
	s := NewSchemaSkewSink()
	s.record("/api/v1/platform/skills", []string{"contentHash"})
	got := FormatSchemaSkew(s)
	for _, want := range []string{"contentHash on /api/v1/platform/skills", "does not recognize", "rebuild it"} {
		if !strings.Contains(got, want) {
			t.Errorf("FormatSchemaSkew = %q, want it to contain %q", got, want)
		}
	}
}
