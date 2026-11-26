// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"maps"
	"net/http"
	"testing"
)

const (
	entrypointsTeamsAppID    = "12345678-1234-1234-1234-123456789abc"
	entrypointsTeamsNewAppID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	entrypointsTeamsTenantID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	entrypointsTeamsPassword = "microsoft_app_password"
)

func entrypointsTeamsCreate(t *testing.T, api *entrypointAPI, name string, values map[string]any) Entrypoint {
	t.Helper()
	rec := api.call(http.MethodPost, "/entrypoints", map[string]any{
		"name":           name,
		"description":    "Teams entrypoint the test creates.",
		"entrypointType": "teams",
		"values":         values,
	})
	expectStatus(t, rec, http.StatusCreated)
	return decodeData[Entrypoint](t, rec)
}

func TestEntrypointSchemas_ListsMicrosoftTeams(t *testing.T) {
	api := newEntrypointAPI(t, nil)

	for _, s := range decodeList[EntrypointSchema](t, api.call(http.MethodGet, "/entrypointSchemas?pageSize=100", nil)).Data {
		if s.EntrypointType == "teams" {
			if s.Name != "Microsoft Teams" {
				t.Errorf("teams schema name = %q, want Microsoft Teams", s.Name)
			}
			return
		}
	}
	t.Fatal("no teams schema listed")
}

func TestEntrypoints_TeamsPatchChangesWhatItSendsAndKeepsTheRest(t *testing.T) {
	cases := []struct {
		name     string
		create   map[string]any
		patch    map[string]any
		want     map[string]any
		password string
	}{
		{
			name:     "new app id and password",
			create:   map[string]any{"microsoft_app_id": entrypointsTeamsAppID, entrypointsTeamsPassword: "original-secret"},
			patch:    map[string]any{"values": map[string]any{"microsoft_app_id": entrypointsTeamsNewAppID, entrypointsTeamsPassword: "new-secret"}},
			want:     map[string]any{"microsoft_app_id": entrypointsTeamsNewAppID, entrypointsTeamsPassword: redacted},
			password: "new-secret",
		},
		{
			name: "values without the required app id",
			create: map[string]any{
				"microsoft_app_id": entrypointsTeamsAppID, entrypointsTeamsPassword: "original-secret", "default_agent_name": "Orchestrator",
			},
			patch: map[string]any{"values": map[string]any{"default_agent_name": "WeatherAgent"}},
			want: map[string]any{
				"microsoft_app_id": entrypointsTeamsAppID, entrypointsTeamsPassword: redacted, "default_agent_name": "WeatherAgent",
			},
			password: "original-secret",
		},
		{
			name: "tenant id",
			create: map[string]any{
				"microsoft_app_id": entrypointsTeamsAppID, entrypointsTeamsPassword: "secret", "microsoft_app_tenant_id": entrypointsTeamsTenantID,
			},
			patch: map[string]any{"values": map[string]any{
				"microsoft_app_id": entrypointsTeamsAppID, entrypointsTeamsPassword: "secret", "microsoft_app_tenant_id": "11111111-2222-3333-4444-555555555555",
			}},
			want:     map[string]any{"microsoft_app_tenant_id": "11111111-2222-3333-4444-555555555555"},
			password: "secret",
		},
		{
			name:     "description only",
			create:   map[string]any{"microsoft_app_id": "11111111-2222-3333-4444-555555555555", entrypointsTeamsPassword: "original-secret"},
			patch:    map[string]any{"description": "Description updated; password must be preserved"},
			want:     map[string]any{"microsoft_app_id": "11111111-2222-3333-4444-555555555555", entrypointsTeamsPassword: redacted},
			password: "original-secret",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api := newEntrypointAPI(t, nil)
			created := entrypointsTeamsCreate(t, api, "teams-patch-gw", tc.create)
			createdWant := maps.Clone(tc.create)
			createdWant[entrypointsTeamsPassword] = redacted
			for key, want := range createdWant {
				if created.Values[key] != want {
					t.Errorf("after create values.%s = %v, want %v", key, created.Values[key], want)
				}
			}

			expectStatus(t, api.call(http.MethodPatch, "/entrypoints/"+created.ID, tc.patch), http.StatusOK)

			got := decodeData[Entrypoint](t, api.call(http.MethodGet, "/entrypoints/"+created.ID, nil)).Values
			for key, want := range tc.want {
				if got[key] != want {
					t.Errorf("after PATCH values.%s = %v, want %v", key, got[key], want)
				}
			}

			diff := api.call(http.MethodPut, "/entrypoints/"+created.ID+"/secretsDiff",
				map[string]any{"values": map[string]any{entrypointsTeamsPassword: tc.password}})
			expectStatus(t, diff, http.StatusOK)
			if !decodeData[SecretsDiffResult](t, diff).Secrets[entrypointsTeamsPassword].Matches {
				t.Errorf("stored password after PATCH is not %q: %s", tc.password, diff.Body.String())
			}
		})
	}
}
