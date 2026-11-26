// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package freellm

import "testing"

func link(t *testing.T, baseURL, generalModel, planningModel, audience string) {
	t.Helper()
	prev := [4]string{buildBaseURL, buildGeneralModel, buildPlanningModel, buildAudience}
	buildBaseURL, buildGeneralModel, buildPlanningModel, buildAudience = baseURL, generalModel, planningModel, audience
	t.Cleanup(func() {
		buildBaseURL, buildGeneralModel, buildPlanningModel, buildAudience = prev[0], prev[1], prev[2], prev[3]
	})
}

func TestABuildWithoutABaseURLHasNoFreeModel(t *testing.T) {
	link(t, "", "free", "free-planning", "https://audience.example.test")
	if _, ok := ConfigFromBuild(); ok {
		t.Fatal("a build with no base URL reports a free model")
	}
}

func TestALinkedFreeLLMIsReportedWithoutATrailingSlash(t *testing.T) {
	link(t, "https://llm.example.test/", "free", "free-planning", "https://audience.example.test")
	cfg, ok := ConfigFromBuild()
	if !ok {
		t.Fatal("a fully linked build reports no free model")
	}
	want := Config{BaseURL: "https://llm.example.test", GeneralModel: "free", PlanningModel: "free-planning", Audience: "https://audience.example.test"}
	if cfg != want {
		t.Errorf("ConfigFromBuild() = %+v, want %+v", cfg, want)
	}
}

func TestABuildWithoutAPlanningModelHasNoFreeModel(t *testing.T) {
	link(t, "https://llm.example.test", "free", "", "https://audience.example.test")
	if _, ok := ConfigFromBuild(); ok {
		t.Fatal("a build with no planning model name reports a free model")
	}
}
