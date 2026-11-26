// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

const unfilledMarker = "<<__SAM_REQUIRED__>>"

func TestKeepStoredOverPlaceholders(t *testing.T) {
	t.Parallel()
	secrets := map[string]struct{}{"password": {}}
	cases := []struct {
		name         string
		authored     map[string]any
		stored       map[string]any
		want         map[string]any
		wantOverride []string
	}{
		{
			name:         "placeholder over a stored value keeps the stored value",
			authored:     map[string]any{"hostname": unfilledMarker},
			stored:       map[string]any{"hostname": "db.internal"},
			want:         map[string]any{"hostname": "db.internal"},
			wantOverride: []string{"hostname"},
		},
		{
			name:     "placeholder with nothing stored stays a placeholder",
			authored: map[string]any{"hostname": unfilledMarker},
			stored:   map[string]any{},
			want:     map[string]any{"hostname": unfilledMarker},
		},
		{
			name:     "placeholder over a stored placeholder stays",
			authored: map[string]any{"hostname": unfilledMarker},
			stored:   map[string]any{"hostname": unfilledMarker},
			want:     map[string]any{"hostname": unfilledMarker},
		},
		{
			name:     "placeholder over an empty stored string stays",
			authored: map[string]any{"hostname": unfilledMarker},
			stored:   map[string]any{"hostname": ""},
			want:     map[string]any{"hostname": unfilledMarker},
		},
		{
			name:     "placeholder over a redaction marker stays",
			authored: map[string]any{"hostname": unfilledMarker},
			stored:   map[string]any{"hostname": "<REDACTED>"},
			want:     map[string]any{"hostname": unfilledMarker},
		},
		{
			name:     "placeholder over a stored null stays",
			authored: map[string]any{"hostname": unfilledMarker},
			stored:   map[string]any{"hostname": nil},
			want:     map[string]any{"hostname": unfilledMarker},
		},
		{
			name:     "a registered secret with a cleartext stored value is left to the write",
			authored: map[string]any{"password": unfilledMarker},
			stored:   map[string]any{"password": "cleartext-secret"},
			want:     map[string]any{"password": unfilledMarker},
		},
		{
			name:     "a registered secret is left to the write",
			authored: map[string]any{"password": unfilledMarker},
			stored:   map[string]any{"password": "<REDACTED>"},
			want:     map[string]any{"password": unfilledMarker},
		},
		{
			name:     "an authored real value wins over the stored one",
			authored: map[string]any{"hostname": "db.new"},
			stored:   map[string]any{"hostname": "db.internal"},
			want:     map[string]any{"hostname": "db.new"},
		},
		{
			name:         "a stored number is kept over a placeholder",
			authored:     map[string]any{"port": unfilledMarker},
			stored:       map[string]any{"port": 5432},
			want:         map[string]any{"port": 5432},
			wantOverride: []string{"port"},
		},
		{
			name:         "several keys come back sorted",
			authored:     map[string]any{"hostname": unfilledMarker, "database": unfilledMarker, "username": "svc"},
			stored:       map[string]any{"hostname": "h", "database": "d", "username": "other"},
			want:         map[string]any{"hostname": "h", "database": "d", "username": "svc"},
			wantOverride: []string{"database", "hostname"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := keepStoredOverPlaceholders(tc.authored, tc.stored, secrets)
			if !slices.Equal(got, tc.wantOverride) {
				t.Errorf("overridden = %v, want %v", got, tc.wantOverride)
			}
			if !reflect.DeepEqual(tc.authored, tc.want) {
				t.Errorf("values = %v, want %v", tc.authored, tc.want)
			}
		})
	}
}

func TestAddUnsetStoredValues_SkipsPlaceholdersRedactionsAndSecrets(t *testing.T) {
	t.Parallel()
	out := map[string]any{"hostname": "db.new"}
	stored := map[string]any{
		"hostname": "db.old", "port": 5432, "database": "app",
		"region": unfilledMarker, "token": "<REDACTED>", "password": "cleartext-secret",
	}
	kept := addUnsetStoredValues(out, stored, map[string]struct{}{"password": {}})
	if want := []string{"database", "port"}; !slices.Equal(kept, want) {
		t.Errorf("kept = %v, want %v", kept, want)
	}
	for _, key := range []string{"region", "token", "password"} {
		if _, carried := out[key]; carried {
			t.Errorf("%s was carried onto the write", key)
		}
	}
	if out["hostname"] != "db.new" {
		t.Errorf("hostname = %v, want the authored value to win", out["hostname"])
	}
}

func TestMergeStoredValues_ReportsAPlaceholderKeptOffAStoredValue(t *testing.T) {
	t.Parallel()
	merged, warnings := mergeStoredValues(
		map[string]any{"hostname": unfilledMarker, "password": unfilledMarker},
		map[string]any{"hostname": "db.internal", "password": "<REDACTED>", "port": 5432},
		[]string{"password"},
	)
	got, _ := merged.(map[string]any)
	if got["hostname"] != "db.internal" || got["port"] != 5432 {
		t.Errorf("merged = %v, want the stored hostname and port", got)
	}
	joined := strings.Join(warnings, "\n")
	if !strings.Contains(joined, "over a "+unfilledMarker+" placeholder: hostname") || !strings.Contains(joined, "which this config does not set: port") {
		t.Errorf("warnings = %q, want both kept groups named", joined)
	}
	if strings.Contains(joined, "db.internal") {
		t.Errorf("warnings = %q must name keys, not values", joined)
	}
}

func TestApply_PlaceholderDoesNotOverwriteAValueSetOnTheConnectorsPage(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	ctx := t.Context()

	seed := sqlConnector("page-filled", "postgres", fullSQLValues())
	if _, err := svc.Apply(ctx, ApplyBuildRequest{Components: []BuildComponentInput{seed}}, testActor); err != nil {
		t.Fatalf("seed apply: %v", err)
	}

	stale := sqlConnector("page-filled", "postgres", map[string]any{
		"hostname": unfilledMarker, "port": 5432, "database": "app", "username": "svc", "password": unfilledMarker,
	})
	staleReq := ApplyBuildRequest{Components: []BuildComponentInput{stale}}

	for round := 1; round <= 2; round++ {
		plan, err := svc.Apply(ctx, ApplyBuildRequest{DryRun: true, Components: staleReq.Components}, testActor)
		if err != nil {
			t.Fatalf("round %d dry run: %v", round, err)
		}
		applied, err := svc.Apply(ctx, staleReq, testActor)
		if err != nil {
			t.Fatalf("round %d apply: %v", round, err)
		}
		planned, wrote := opFor(t, plan, kindConnector, "page-filled"), opFor(t, applied, kindConnector, "page-filled")
		if planned.Action != wrote.Action || !slices.Equal(planned.ChangedFields, wrote.ChangedFields) {
			t.Errorf("round %d: dry run planned %s %v, apply did %s %v", round, planned.Action, planned.ChangedFields, wrote.Action, wrote.ChangedFields)
		}
		if wrote.Action != actionUnchanged {
			t.Errorf("round %d: action = %s, want %s: a stale placeholder changes nothing on the platform", round, wrote.Action, actionUnchanged)
		}

		_, _, stored, err := svc.deps.Connectors.GetValuesCleartext(ctx, wrote.ResourceID)
		if err != nil {
			t.Fatalf("round %d read back: %v", round, err)
		}
		if stored["hostname"] != "db" || stored["password"] != "real-password" {
			t.Errorf("round %d: stored hostname=%v password-kept=%v, want the page-filled values", round, stored["hostname"], stored["password"] == "real-password")
		}
	}
}
