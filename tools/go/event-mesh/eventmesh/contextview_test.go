// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eventmesh

import "testing"

func TestContextView_Lookup(t *testing.T) {
	view := &contextView{userID: "u1", sessionID: "s1", appName: "agent-x"}

	cases := []struct {
		expr string
		want any
		ok   bool
	}{
		{"user_id", "u1", true},
		{"a2a_user_config.user_profile.id", "u1", true},
		{"a2a_user_config.user_profile.user_id", "u1", true},
		{"session_id", "s1", true},
		{"a2a_context.session_id", "s1", true},
		{"app_name", "agent-x", true},
		{"a2a_context.app_name", "agent-x", true},
		{"dummy:user_id", "u1", true},
		{"  user_id  ", "u1", true},
		{"unknown.path", nil, false},
		{"", nil, false},
	}

	for _, tc := range cases {
		got, ok := view.Lookup(tc.expr)
		if ok != tc.ok {
			t.Errorf("Lookup(%q): ok=%v, want %v", tc.expr, ok, tc.ok)
			continue
		}
		if ok && got != tc.want {
			t.Errorf("Lookup(%q): got %v, want %v", tc.expr, got, tc.want)
		}
	}
}

func TestContextView_EmptyFieldsReportMiss(t *testing.T) {
	view := &contextView{}
	if _, ok := view.Lookup("user_id"); ok {
		t.Error("empty userID should not resolve")
	}
	if _, ok := view.Lookup("session_id"); ok {
		t.Error("empty sessionID should not resolve")
	}
	if _, ok := view.Lookup("app_name"); ok {
		t.Error("empty appName should not resolve")
	}
}

func TestContextView_NilSafe(t *testing.T) {
	var view *contextView
	if _, ok := view.Lookup("user_id"); ok {
		t.Error("nil receiver should return ok=false")
	}
}
