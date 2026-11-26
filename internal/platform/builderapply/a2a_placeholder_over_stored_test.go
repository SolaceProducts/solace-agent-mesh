// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

const (
	agentSurface = "agentAuthConfig"
	cardSurface  = "agentCardAuthConfig"
)

func remoteAgentArtifact(name, surface string, authConfig map[string]any) string {
	return remoteAgentWithSurfaces(name, map[string]map[string]any{surface: authConfig})
}

func remoteAgentWithSurfaces(name string, surfaces map[string]map[string]any) string {
	body := map[string]any{
		"name": name, "description": "A remote agent used in tests.",
		"agentUrl": "https://remote.example.com", "agentCardLocation": "well_known",
	}
	for surface, authConfig := range surfaces {
		body[surface] = authConfig
	}
	raw, err := json.Marshal(body)
	if err != nil {
		panic(err)
	}
	return string(raw)
}

func storedRemoteAgentAuthConfig(ctx context.Context, t *testing.T, svc *Service, name, surface string) map[string]any {
	t.Helper()
	row, err := store.NewA2AAgentRepo(svc.deps.DB).GetByName(ctx, name)
	if err != nil || row == nil {
		t.Fatalf("read stored row: %v", err)
	}
	raw := row.AgentAuthConfig
	if surface == cardSurface {
		raw = row.AgentCardAuthConfig
	}
	if raw == nil {
		t.Fatalf("%s is not stored", surface)
	}
	var stored map[string]any
	if err := json.Unmarshal([]byte(*raw), &stored); err != nil {
		t.Fatalf("stored %s is not JSON: %v", surface, err)
	}
	return stored
}

func TestApply_A2APlaceholderDoesNotOverwriteAValueSetOnTheAgentsPage(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		surface string
		seed    map[string]any
		stale   map[string]any
		want    map[string]any
	}{
		{
			name: "basic", surface: agentSurface,
			seed:  map[string]any{"type": "basic", "username": "svc-real", "password": "pw-real"},
			stale: map[string]any{"type": "basic", "username": unfilledMarker, "password": unfilledMarker},
			want:  map[string]any{"username": "svc-real", "password": "pw-real"},
		},
		{
			name: "apikey", surface: agentSurface,
			seed:  map[string]any{"type": "apikey", "apiKey": "key-real", "headerName": "X-Real-Key"},
			stale: map[string]any{"type": "apikey", "apiKey": unfilledMarker, "headerName": unfilledMarker},
			want:  map[string]any{"api_key": "key-real", "header_name": "X-Real-Key"},
		},
		{
			name: "oauth2", surface: agentSurface,
			seed: map[string]any{"type": "oauth2", "clientId": "id-real", "clientSecret": "secret-real",
				"tokenEndpoint": "https://idp.example.com/token"},
			stale: map[string]any{"type": "oauth2", "clientId": unfilledMarker, "clientSecret": unfilledMarker,
				"tokenEndpoint": unfilledMarker},
			want: map[string]any{"client_id": "id-real", "client_secret": "secret-real",
				"token_endpoint": "https://idp.example.com/token"},
		},
		{
			name: "card apikey", surface: cardSurface,
			seed:  map[string]any{"type": "apikey", "apiKey": "card-key-real", "headerName": "X-Card-Key"},
			stale: map[string]any{"type": "apikey", "apiKey": unfilledMarker, "headerName": unfilledMarker},
			want:  map[string]any{"api_key": "card-key-real", "header_name": "X-Card-Key"},
		},
		{
			name: "card oauth2", surface: cardSurface,
			seed: map[string]any{"type": "oauth2", "clientId": "card-id", "clientSecret": "card-secret",
				"tokenEndpoint": "https://idp.example.com/card-token"},
			stale: map[string]any{"type": "oauth2", "clientId": unfilledMarker, "clientSecret": unfilledMarker,
				"tokenEndpoint": unfilledMarker},
			want: map[string]any{"client_id": "card-id", "client_secret": "card-secret",
				"token_endpoint": "https://idp.example.com/card-token"},
		},
	}
	svc := newTestService(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			name := "remote-" + strings.ReplaceAll(tc.name, " ", "-")
			seedReq := ApplyBuildRequest{Components: []BuildComponentInput{{Kind: kindA2AAgent, Name: name, Content: remoteAgentArtifact(name, tc.surface, tc.seed)}}}
			if _, err := svc.Apply(ctx, seedReq, testActor); err != nil {
				t.Fatalf("seed apply: %v", err)
			}
			staleReq := ApplyBuildRequest{Components: []BuildComponentInput{{Kind: kindA2AAgent, Name: name, Content: remoteAgentArtifact(name, tc.surface, tc.stale)}}}

			for round := 1; round <= 2; round++ {
				plan, err := svc.Apply(ctx, ApplyBuildRequest{DryRun: true, Components: staleReq.Components}, testActor)
				if err != nil {
					t.Fatalf("round %d dry run: %v", round, err)
				}
				applied, err := svc.Apply(ctx, staleReq, testActor)
				if err != nil {
					t.Fatalf("round %d apply: %v", round, err)
				}
				planned, wrote := opFor(t, plan, kindA2AAgent, name), opFor(t, applied, kindA2AAgent, name)
				if planned.Action != wrote.Action || !slices.Equal(planned.ChangedFields, wrote.ChangedFields) {
					t.Errorf("round %d: dry run planned %s %v, apply did %s %v", round, planned.Action, planned.ChangedFields, wrote.Action, wrote.ChangedFields)
				}
				if wrote.Action != actionUnchanged {
					t.Errorf("round %d: action = %s %v, want %s: a placeholder changes nothing on the platform", round, wrote.Action, wrote.ChangedFields, actionUnchanged)
				}
				if !slices.Equal(planned.Warnings, wrote.Warnings) {
					t.Errorf("round %d: dry run warned %q, apply warned %q", round, planned.Warnings, wrote.Warnings)
				}
				joined := strings.Join(wrote.Warnings, "\n")
				if !strings.Contains(joined, "kept these stored "+tc.surface+" fields over a "+unfilledMarker+" placeholder: ") {
					t.Errorf("round %d: warnings = %q, want the kept %s fields named", round, joined, tc.surface)
				}
				for _, value := range tc.want {
					if s, _ := value.(string); strings.Contains(joined, s) {
						t.Errorf("round %d: warnings = %q must name keys, not values", round, joined)
					}
				}
				stored := storedRemoteAgentAuthConfig(ctx, t, svc, name, tc.surface)
				for key, want := range tc.want {
					if stored[key] != want {
						t.Errorf("round %d: stored %s = %v, want %v (stored config %v)", round, key, stored[key], want, stored)
					}
				}
				for key, value := range stored {
					if s, _ := value.(string); strings.Contains(s, unfilledMarker) {
						t.Errorf("round %d: stored %s holds the placeholder", round, key)
					}
				}
			}
		})
	}
}

func TestApply_A2AWarningsAreOneNotePerSurfaceAndNeverBlank(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()
	both := func(agent, card map[string]any) ApplyBuildRequest {
		content := remoteAgentWithSurfaces("remote-notes", map[string]map[string]any{agentSurface: agent, cardSurface: card})
		return ApplyBuildRequest{Components: []BuildComponentInput{{Kind: kindA2AAgent, Name: "remote-notes", Content: content}}}
	}
	seeded := both(
		map[string]any{"type": "basic", "username": "svc-real", "password": "pw-real"},
		map[string]any{"type": "apikey", "apiKey": "card-key", "headerName": "X-Card-Key"},
	)
	if _, err := svc.Apply(ctx, seeded, testActor); err != nil {
		t.Fatalf("seed apply: %v", err)
	}

	same, err := svc.Apply(ctx, ApplyBuildRequest{DryRun: true, Components: seeded.Components}, testActor)
	if err != nil {
		t.Fatalf("dry run of the same build: %v", err)
	}
	for _, warning := range opFor(t, same, kindA2AAgent, "remote-notes").Warnings {
		if strings.TrimSpace(warning) == "" {
			t.Errorf("warnings = %q, want no blank entry when nothing was kept over a placeholder", opFor(t, same, kindA2AAgent, "remote-notes").Warnings)
		}
	}

	stale := both(
		map[string]any{"type": "basic", "username": unfilledMarker, "password": unfilledMarker},
		map[string]any{"type": "apikey", "apiKey": unfilledMarker, "headerName": unfilledMarker},
	)
	plan, err := svc.Apply(ctx, ApplyBuildRequest{DryRun: true, Components: stale.Components}, testActor)
	if err != nil {
		t.Fatalf("dry run of the stale build: %v", err)
	}
	warnings := opFor(t, plan, kindA2AAgent, "remote-notes").Warnings
	if len(warnings) != 2 || !strings.Contains(warnings[0], "stored "+agentSurface+" fields over") || !strings.Contains(warnings[1], "stored "+cardSurface+" fields over") {
		t.Errorf("warnings = %q, want the agent note, then the card note", warnings)
	}
}

func TestDecideA2AAuthSurface_PlaceholderOverStoredValues(t *testing.T) {
	t.Parallel()
	stored := map[string]any{"type": "basic", "username": "svc-real", "password": "<REDACTED>"}

	t.Run("same type keeps the stored username", func(t *testing.T) {
		authored := map[string]any{"type": "basic", "username": unfilledMarker, "password": unfilledMarker}
		got, note, err := decideA2AAuthSurface("agentAuthConfig", authored, "", stored, "basic")
		if err != nil {
			t.Fatal(err)
		}
		if note == "" {
			t.Error("want a note naming the kept field")
		}
		block, _ := got.Config.(map[string]any)
		if block["username"] != "svc-real" || block["password"] != nil {
			t.Errorf("block = %v, want the stored username and no password", block)
		}
		if got.Rotates {
			t.Error("a placeholder is not a rotation")
		}
		if authored["username"] != unfilledMarker {
			t.Error("the caller's block was modified")
		}
	})

	t.Run("a new type has nothing stored to keep", func(t *testing.T) {
		authored := map[string]any{"type": "oauth2", "clientId": unfilledMarker, "clientSecret": unfilledMarker}
		got, note, err := decideA2AAuthSurface("agentAuthConfig", authored, "oauth2", stored, "basic")
		if err != nil {
			t.Fatal(err)
		}
		block, _ := got.Config.(map[string]any)
		if note != "" || block["clientId"] != unfilledMarker {
			t.Errorf("note = %q, block = %v, want the placeholder carried to the new type", note, block)
		}
	})

	t.Run("a block that states no type anywhere keeps the stored username", func(t *testing.T) {
		authored := map[string]any{"username": unfilledMarker, "password": unfilledMarker}
		got, note, err := decideA2AAuthSurface("agentAuthConfig", authored, "", stored, "basic")
		if err != nil {
			t.Fatal(err)
		}
		block, _ := got.Config.(map[string]any)
		if note == "" || block["username"] != "svc-real" {
			t.Errorf("note = %q, block = %v, want the stored username kept", note, block)
		}
	})

	t.Run("a block that switches type at the top level keeps nothing of the old one", func(t *testing.T) {
		authored := map[string]any{"apiKey": "real-key", "username": unfilledMarker}
		_, note, err := decideA2AAuthSurface("agentAuthConfig", authored, "apikey", stored, "basic")
		if err != nil {
			t.Fatal(err)
		}
		if note != "" {
			t.Errorf("note = %q, want none: the stored username does not survive the switch", note)
		}
	})

	t.Run("a block that switches type keeps nothing of the old one", func(t *testing.T) {
		authored := map[string]any{"type": "apikey", "apiKey": "real-key", "username": unfilledMarker}
		_, note, err := decideA2AAuthSurface("agentAuthConfig", authored, "", stored, "basic")
		if err != nil {
			t.Fatal(err)
		}
		if note != "" {
			t.Errorf("note = %q, want none: the stored username does not survive the switch", note)
		}
	})

	t.Run("a placeholder as the discriminator is an unknown type, not the stored one", func(t *testing.T) {
		authored := map[string]any{"type": unfilledMarker, "username": "svc"}
		if _, _, err := decideA2AAuthSurface("agentAuthConfig", authored, "", stored, "basic"); err == nil {
			t.Error("a placeholder type was read as the stored type")
		}
	})

	t.Run("an authored real value still wins", func(t *testing.T) {
		authored := map[string]any{"type": "basic", "username": "svc-new", "password": unfilledMarker}
		got, note, err := decideA2AAuthSurface("agentAuthConfig", authored, "", maps.Clone(stored), "basic")
		if err != nil {
			t.Fatal(err)
		}
		block, _ := got.Config.(map[string]any)
		if note != "" || block["username"] != "svc-new" {
			t.Errorf("note = %q, block = %v, want the authored username", note, block)
		}
	})
}
