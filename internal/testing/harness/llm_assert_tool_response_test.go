// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import "testing"

func TestToolResponseTextChecksFailOnlyWhenTheTextSaysSo(t *testing.T) {
	const marker = "file_converter"
	cases := []struct {
		name     string
		content  string
		match    ToolResponseMatch
		wantFail bool
	}{
		{"contains: present", "listed " + marker, ToolResponseMatch{ResponseContains: StringOrList{marker}}, false},
		{"contains: absent", "listed nothing", ToolResponseMatch{ResponseContains: StringOrList{marker}}, true},
		{"not contains: absent", "listed nothing", ToolResponseMatch{ResponseNotContains: StringOrList{marker}}, false},
		{"not contains: present", "listed " + marker, ToolResponseMatch{ResponseNotContains: StringOrList{marker}}, true},
		{"both on one response", "listed " + marker, ToolResponseMatch{ResponseContains: StringOrList{"listed"}, ResponseNotContains: StringOrList{marker}}, true},
		{"no text check", "anything", ToolResponseMatch{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs := toolResponseTextErrors(tc.content, tc.match)
			if got := len(errs) > 0; got != tc.wantFail {
				t.Errorf("failed = %v (%v), want %v", got, errs, tc.wantFail)
			}
		})
	}
}
