// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

var updateGoldens = flag.Bool("update", false, "rewrite the prompt goldens in testdata/")

func checkGolden(t *testing.T, name, got string) {
	t.Helper()
	path := "testdata/" + name
	if *updateGoldens {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("update golden: %v", err)
		}
		t.Logf("updated %s (%d bytes)", path, len(got))
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden: %v (run with -update to create it)", err)
	}
	if d := cmp.Diff(string(want), got); d != "" {
		t.Errorf("%s changed (-want +got):\n%s", name, d)
	}
}

func TestAddExamplesDefaultsUnchanged(t *testing.T) {
	pb := NewPromptBuilder()
	pb.AddExamples(true, true)
	checkGolden(t, "examples_all_on.txt", pb.Build())
}

func TestAddExamplesWithholdsExamplesOfGatedFeatures(t *testing.T) {
	t.Run("templates off drops the template_liquid demonstrations", func(t *testing.T) {
		pb := NewPromptBuilder()
		pb.AddExamples(false, true)
		got := pb.Build()
		if strings.Contains(got, "template_liquid") {
			t.Error("templates off, yet the examples still demonstrate template_liquid")
		}
		for _, want := range []string{"status_update", "math:", "search_database"} {
			if !strings.Contains(got, want) {
				t.Errorf("dropped %q, which does not depend on templates", want)
			}
		}
	})

	t.Run("artifact_content off drops the embed example", func(t *testing.T) {
		pb := NewPromptBuilder()
		pb.AddExamples(true, false)
		if got := pb.Build(); strings.Contains(got, "artifact_content") {
			t.Error("artifact_content off, yet an example still embeds one")
		}
	})

	t.Run("everything off still teaches the always-available embeds", func(t *testing.T) {
		pb := NewPromptBuilder()
		pb.AddExamples(false, false)
		got := pb.Build()
		if !strings.Contains(got, "status_update") {
			t.Error("status_update example withheld, but the agent resolves it regardless")
		}
		if !strings.Contains(got, "math:") {
			t.Error("math example withheld, but math is an early embed")
		}
	})
}

func TestAddExamplesNumbersContiguously(t *testing.T) {
	for _, c := range []struct {
		name                       string
		templates, artifactContent bool
		wantCount                  int
	}{
		{"all on", true, true, 6},
		{"templates off", false, true, 3},
		{"artifact content off", true, false, 5},
		{"both off", false, false, 2},
	} {
		t.Run(c.name, func(t *testing.T) {
			pb := NewPromptBuilder()
			pb.AddExamples(c.templates, c.artifactContent)
			got := pb.Build()
			for i := 1; i <= c.wantCount; i++ {
				if !strings.Contains(got, fmt.Sprintf("Example %d:", i)) {
					t.Errorf("missing Example %d, so the numbering has a gap", i)
				}
			}
			if strings.Contains(got, fmt.Sprintf("Example %d:", c.wantCount+1)) {
				t.Errorf("found Example %d, want only %d", c.wantCount+1, c.wantCount)
			}
		})
	}
}
