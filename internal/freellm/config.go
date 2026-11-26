// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package freellm

import "strings"

var (
	buildBaseURL       string
	buildGeneralModel  string
	buildPlanningModel string
	buildAudience      string
)

type Config struct {
	BaseURL       string
	GeneralModel  string
	PlanningModel string
	Audience      string
}

func ConfigFromBuild() (Config, bool) {
	cfg := Config{
		BaseURL:       strings.TrimRight(buildBaseURL, "/"),
		GeneralModel:  buildGeneralModel,
		PlanningModel: buildPlanningModel,
		Audience:      buildAudience,
	}
	return cfg, cfg.BaseURL != "" && cfg.GeneralModel != "" && cfg.PlanningModel != "" && cfg.Audience != ""
}
