// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

var update = flag.Bool("update", false, "update golden files")

func TestNormalize_ForgedAuthresFixtureRejected(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join("testdata", "eml", "synthetic", "forged_authres.eml"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := Normalize(raw, []string{"protection.outlook.com"})
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if got.AuthResults.SPF != AuthNone || got.AuthResults.DKIM != AuthNone || got.AuthResults.DMARC != AuthNone {
		t.Fatalf("forged authres should produce all-AuthNone, got SPF=%q DKIM=%q DMARC=%q",
			got.AuthResults.SPF, got.AuthResults.DKIM, got.AuthResults.DMARC)
	}
	if got.AuthResults.AlignedFromDomain != "" {
		t.Errorf("AlignedFromDomain leaked through forged header: %q", got.AuthResults.AlignedFromDomain)
	}
}

func TestNormalize_MultiHopFixtureHonoursTopmostTrusted(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join("testdata", "eml", "synthetic", "multiple_authres_hops.eml"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := Normalize(raw, []string{"protection.outlook.com"})
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if got.AuthResults.DMARC != AuthPass {
		t.Errorf("topmost trusted hop should set DMARC=pass, got %q", got.AuthResults.DMARC)
	}
}

func TestGoldenNormalization(t *testing.T) {
	matches, err := filepath.Glob(filepath.Join("testdata", "eml", "synthetic", "*.eml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, emlPath := range matches {
		name := strings.TrimSuffix(filepath.Base(emlPath), ".eml")
		if name == "malformed_mime_boundary" {
			continue
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			raw, err := os.ReadFile(emlPath)
			if err != nil {
				t.Fatal(err)
			}
			got, err := Normalize(raw, []string{"protection.outlook.com"})
			if err != nil {
				t.Fatalf("Normalize: %v", err)
			}
			goldenPath := filepath.Join("testdata", "golden", "synthetic", name+".json")
			if *update {
				b, marshalErr := json.MarshalIndent(got, "", "  ")
				if marshalErr != nil {
					t.Fatal(marshalErr)
				}
				if mkErr := os.MkdirAll(filepath.Dir(goldenPath), 0755); mkErr != nil {
					t.Fatal(mkErr)
				}
				if writeErr := os.WriteFile(goldenPath, b, 0644); writeErr != nil {
					t.Fatal(writeErr)
				}
				return
			}
			wantBytes, err := os.ReadFile(goldenPath)
			if err != nil {
				t.Fatalf("missing golden %s — run with -update to create", goldenPath)
			}
			var want NormalizedEmail
			if err := json.Unmarshal(wantBytes, &want); err != nil {
				t.Fatal(err)
			}
			opts := cmpopts.IgnoreFields(NormalizedEmail{}, "ReceivedAt")
			if diff := cmp.Diff(&want, got, opts); diff != "" {
				t.Errorf("normalized mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
