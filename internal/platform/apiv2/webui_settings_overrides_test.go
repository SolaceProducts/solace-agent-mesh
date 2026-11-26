// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"net/http"
	"testing"
)

func TestWebuiSettings_ExplicitEmptyAndFalseOverridesSurviveARead(t *testing.T) {
	api, _ := newWebuiSettingsAPI(t)
	expectStatus(t, api.call(http.MethodPut, webuiSettingsPath, map[string]any{
		"appName":         "Acme Assistant",
		"disclaimerText":  "",
		"systemPurpose":   "You are a support assistant",
		"collectFeedback": false,
		"publishFeedback": true,
	}), http.StatusOK)

	got := rawData(t, api.call(http.MethodGet, webuiSettingsPath, nil))
	expectNull(t, got, "welcomeMessage")
	if got["appName"] != "Acme Assistant" || got["systemPurpose"] != "You are a support assistant" {
		t.Errorf("appName %v, systemPurpose %v", got["appName"], got["systemPurpose"])
	}
	if text, ok := got["disclaimerText"].(string); !ok || text != "" {
		t.Errorf("disclaimerText = %#v, want an explicit empty string", got["disclaimerText"])
	}
	if collect, ok := got["collectFeedback"].(bool); !ok || collect {
		t.Errorf("collectFeedback = %#v, want an explicit false", got["collectFeedback"])
	}
	if publish, ok := got["publishFeedback"].(bool); !ok || !publish {
		t.Errorf("publishFeedback = %#v, want true", got["publishFeedback"])
	}
}
