// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package webhook

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTriggerSourceStripsControlCharactersAndCapsLength(t *testing.T) {
	r := httptest.NewRequest("POST", "/hook", nil)
	r.Header.Set("X-Gitlab-Event", "Push Hook\x00\x1b[31m")
	r.Header.Set("X-Gitlab-Event-UUID", strings.Repeat("a", 500))

	source := triggerSource(r, &RouteConfig{Name: "gitlab"}, nil)

	if source.TriggerEvent != "Push Hook[31m" {
		t.Errorf("event = %q, want control characters removed", source.TriggerEvent)
	}
	if got := len([]rune(source.TriggerDeliveryID)); got != maxTriggerValueRunes {
		t.Errorf("delivery id has %d runes, want it capped at %d", got, maxTriggerValueRunes)
	}
}

func TestTriggerSourceStripsCharactersThatChangeHowTheValueRenders(t *testing.T) {
	r := httptest.NewRequest("POST", "/hook", nil)
	r.Header.Set("X-Gitlab-Event", "Push\u202e Hook\u200b\u2066")

	source := triggerSource(r, &RouteConfig{Name: "gitlab"}, nil)

	if source.TriggerEvent != "Push Hook" {
		t.Errorf("event = %q, want bidi and zero-width characters removed", source.TriggerEvent)
	}
}

func TestTriggerSourceAppendsTheGitHubBodyAction(t *testing.T) {
	r := httptest.NewRequest("POST", "/hook", nil)
	r.Header.Set("X-GitHub-Event", "check_suite")
	r.Header.Set("X-GitHub-Delivery", "72d3162e-cc78-11e3-81ab-4c9367dc0958")

	source := triggerSource(r, &RouteConfig{Name: "github"}, []byte(`{"action":"completed"}`))

	if source.TriggerEvent != "check_suite.completed" {
		t.Errorf("event = %q, want check_suite.completed", source.TriggerEvent)
	}
	if source.TriggerDeliveryID != "72d3162e-cc78-11e3-81ab-4c9367dc0958" {
		t.Errorf("delivery id = %q", source.TriggerDeliveryID)
	}
}

func TestTriggerSourceKeepsTheGitHubEventWhenTheBodyHasNoAction(t *testing.T) {
	r := httptest.NewRequest("POST", "/hook", nil)
	r.Header.Set("X-GitHub-Event", "push")

	for _, body := range []string{`{"ref":"main"}`, `{"action":7}`, `not json`} {
		source := triggerSource(r, &RouteConfig{Name: "github"}, []byte(body))
		if source.TriggerEvent != "push" {
			t.Errorf("body %s: event = %q, want push", body, source.TriggerEvent)
		}
	}
}
