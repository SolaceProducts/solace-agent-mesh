// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package systemone

import (
	"reflect"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/decision"
)

var severity = decision.Question{
	Key:  "severity",
	Type: decision.TypeScore,
	Criteria: []decision.Criterion{
		{Key: "low", Description: "Minor"},
		{Key: "high"},
	},
}

func float(v float64) *float64 { return &v }

func TestWireType(t *testing.T) {
	if got := WireType(decision.TypeBoolean); got != WireTypeBoolean {
		t.Errorf("boolean wire type = %q", got)
	}
	if got := WireType(decision.TypeChoice); got != "choice" {
		t.Errorf("choice wire type = %q", got)
	}
}

func TestScoreLevels(t *testing.T) {
	got, err := ScoreLevels(severity.Criteria)
	if err != nil || string(got) != `["Minor","high"]` {
		t.Errorf("levels = %s, %v; want descriptions, falling back to keys", got, err)
	}
	if _, err := ScoreLevels(severity.Criteria[:1]); err == nil {
		t.Error("one score level encoded without error")
	}
}

func TestCriteriaObjectKeepsDeclarationOrder(t *testing.T) {
	criteria := []decision.Criterion{{Key: "zeta", Description: "last letter"}, {Key: "alpha"}}
	got, err := CriteriaObject(criteria, UndescribedNull)
	if err != nil || string(got) != `{"zeta":"last letter","alpha":null}` {
		t.Errorf("criteria = %s, %v", got, err)
	}
	got, err = CriteriaObject(criteria, UndescribedEmpty)
	if err != nil || string(got) != `{"zeta":"last letter","alpha":""}` {
		t.Errorf("criteria with empty-string undescribed = %s, %v", got, err)
	}
	if got, err := CriteriaObject(nil, UndescribedNull); got != nil || err != nil {
		t.Errorf("no criteria = %s, %v; want none", got, err)
	}
}

func TestDecodeAnswersFitsScoresToTheAuthorsLevels(t *testing.T) {
	raw := map[string]Answer{
		"severity": {Type: "score", Score: float(0.8), Legend: map[string]any{"0": "Minor", "1": "high"},
			Probabilities: map[string]float64{"0": 0.2, "1": 0.8}, Confidence: float(0.8)},
		"urgent": {Type: WireTypeBoolean, Noul: float(0.7)},
	}
	answers, err := DecodeAnswers(raw, []decision.Question{severity, {Key: "urgent", Type: decision.TypeBoolean}})
	if err != nil {
		t.Fatalf("DecodeAnswers: %v", err)
	}
	got := answers["severity"]
	if !reflect.DeepEqual(got.Probabilities, map[string]float64{"low": 0.2, "high": 0.8}) ||
		!reflect.DeepEqual(got.Legend, map[string]any{"0": "low", "1": "high"}) {
		t.Errorf("severity = %+v", got)
	}
	if answers["urgent"].Probability != 0.7 {
		t.Errorf("urgent = %+v", answers["urgent"])
	}
}

func TestDecodeAnswersRejectsMalformedAnswers(t *testing.T) {
	cases := map[string]Answer{
		"noul without a probability": {Type: WireTypeBoolean},
		"choice without a choice":    {Type: "choice"},
		"score without a score":      {Type: "score"},
		"unknown type":               {Type: "ranking"},
		"legend off the levels":      {Type: "score", Score: float(1), Legend: map[string]any{"5": "x"}},
		"probability off the levels": {Type: "score", Score: float(1), Probabilities: map[string]float64{"01": 1}},
	}
	for name, a := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeAnswers(map[string]Answer{"severity": a}, []decision.Question{severity}); err == nil {
				t.Error("decoded without error")
			}
		})
	}
}
