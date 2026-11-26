// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import (
	"slices"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/samcli/render"
)

type ExpectedRendering struct {
	Findings []string `yaml:"findings"`

	TableRows []int `yaml:"table_rows"`

	Cards []string `yaml:"cards"`
}

const (
	segmentText      = "text"
	renderBlockTable = "table"
)

func (h *TestHarness) AssertRendering(t *testing.T, collected []CollectedEvent) {
	t.Helper()
	expected := h.scenario.ExpectedRendering
	if expected == nil {
		return
	}
	if _, ok := h.gateway.(*HTTPGateway); !ok {
		t.Error("expected_rendering requires go-http gateway mode")
		return
	}

	events := make([]render.Event, 0, len(collected))
	for i, ev := range collected {
		events = append(events, render.Event{Seq: int64(i + 1), Data: ev.Payload})
	}
	report := render.NewReport(render.Fold(events), render.Options{})

	findings := []string{}
	for _, f := range report.Findings {
		if !slices.Contains(findings, f.Check) {
			findings = append(findings, f.Check)
		}
	}
	want := slices.Clone(expected.Findings)
	slices.Sort(findings)
	slices.Sort(want)
	if !slices.Equal(findings, want) && (len(findings) > 0 || len(want) > 0) {
		t.Errorf("expected_rendering: findings = %v, want %v\n%+v", findings, want, report.Findings)
	}

	tableRows := []int{}
	cards := []string{}
	for _, b := range report.Bubbles {
		if b.Role != "agent" {
			continue
		}
		for _, s := range b.Segments {
			if s.Kind != segmentText {
				cards = append(cards, s.Name)
				continue
			}
			for _, block := range s.Blocks {
				if block.Kind == renderBlockTable {
					tableRows = append(tableRows, block.Rows)
				}
			}
		}
	}
	if expected.TableRows != nil && !slices.Equal(tableRows, expected.TableRows) {
		t.Errorf("expected_rendering: table rows = %v, want %v", tableRows, expected.TableRows)
	}
	if expected.Cards != nil && !slices.Equal(cards, expected.Cards) {
		t.Errorf("expected_rendering: cards = %v, want %v", cards, expected.Cards)
	}
}
