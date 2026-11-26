// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func TestCloseNames(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		query      string
		candidates []string
		want       []string
	}{
		{"a typo within three edits", "get_current_tme", []string{"get_current_time", "web_request"}, []string{"get_current_time"}},
		{"case is ignored", "GET_CURRENT_TIME", []string{"get_current_time"}, []string{"get_current_time"}},
		{"four edits and no shared prefix is too far", "clock_now", []string{"get_current_time"}, nil},
		{"a shared four-character prefix is close however far the rest", "pdf_to_markdown", []string{"pdf_ops", "web_request"}, []string{"pdf_ops"}},
		{"a three-character prefix is not", "pdf_to_markdown", []string{"pdx_something_else"}, nil},
		{"ranked by distance, then name", "abcdef", []string{"zzzzzz", "abcxyz", "abcdefgh", "abxdef", "abcdeg"}, []string{"abcdeg", "abxdef", "abcdefgh"}},
		{"at most three", "list_a", []string{"list_b", "list_c", "list_d", "list_e"}, []string{"list_b", "list_c", "list_d"}},
		{"a repeated candidate counts once", "slak", []string{"slack", "slack"}, []string{"slack"}},
		{"no candidates", "anything", nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := closeNames(tc.query, tc.candidates)
			if !slices.Equal(got, tc.want) && (len(got) != 0 || len(tc.want) != 0) {
				t.Errorf("closeNames(%q, %v) = %v, want %v", tc.query, tc.candidates, got, tc.want)
			}
		})
	}
}

func TestCloseNames_AHugeQueryIsCheapAndStillMatchesByPrefix(t *testing.T) {
	t.Parallel()
	query := "pdf_ops" + strings.Repeat("x", 200_000)

	start := time.Now()
	got := closeNames(query, []string{"pdf_ops", "web_request", "pdf_ops"})

	if !slices.Equal(got, []string{"pdf_ops"}) {
		t.Errorf("closeNames = %v, want the prefix match only", got)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("a 200k-character query took %s: edit distance must not run when the lengths are far apart", elapsed)
	}
}

func TestLevenshtein(t *testing.T) {
	t.Parallel()
	cases := []struct {
		a, b string
		want int
	}{
		{"", "", 0}, {"abc", "", 3}, {"", "abc", 3}, {"kitten", "sitting", 3}, {"same", "same", 0}, {"résumé", "resume", 2}, {"ab", "ba", 2},
	}
	for _, tc := range cases {
		if got := levenshtein(tc.a, tc.b); got != tc.want {
			t.Errorf("levenshtein(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestToolsDescribe_AMistypedBuiltinSuggestsTheRealOne(t *testing.T) {
	f := newDescribeFixture(t)
	got := f.one(t, toolRef("get_current_tme"))
	if got.Status != describeStatusNotFound || got.Reason != reasonToolNotFound || !slices.Equal(got.Suggestions, []string{"get_current_time"}) {
		t.Fatalf("got %+v, want not_found suggesting get_current_time", got)
	}
	if got := f.one(t, toolRef("send_email")); got.Status != describeStatusUnavailable || got.Reason != reasonTypeDisabled {
		t.Errorf("send_email: got %+v, want unavailable: its connector type is off here", got)
	}
}

func TestToolsDescribe_SuggestionsOnlyNameWhatABuiltAgentCanUse(t *testing.T) {
	f := newDescribeFixture(t)
	f.seedPackage(t, "acme-tools", "ready", map[string]string{"lookup_order": ""})
	f.seedPackage(t, "slow-tools", "pending", map[string]string{"lookup_order": ""})

	got := f.one(t, toolRef("acme-tool__lookup_order"))
	if !slices.Equal(got.Suggestions, []string{"acme-tools__lookup_order"}) {
		t.Errorf("suggestions = %v, want the ready package's tool and not the pending one's", got.Suggestions)
	}
	if got := f.one(t, toolRef("validate_component_confi")); len(got.Suggestions) != 0 {
		t.Errorf("suggestions = %v, want none: a Builder tool is not bindable by built agents", got.Suggestions)
	}
	f.withSTRSchemas(nil)
	if got := f.one(t, toolRef("pdf_opps")); len(got.Suggestions) == 0 || got.Suggestions[0] != "pdf_ops" {
		t.Errorf("suggestions = %v, want the STR built-in pdf_ops first even with no row and no registry entry", got.Suggestions)
	}
}

func TestToolsDescribe_SuggestionsIncludeConnectorsThatAreEnabled(t *testing.T) {
	enabled := newDescribeFixture(t, "email_connector")
	createHydrationConnector(t, enabled.connectors, "email-connector", "email", "smtp", emailConnectorValues())
	if got := enabled.one(t, toolRef("email-connectr")); !slices.Equal(got.Suggestions, []string{"email-connector"}) {
		t.Errorf("email on: suggestions = %v, want the connector", got.Suggestions)
	}

	disabled := newDescribeFixture(t)
	createHydrationConnector(t, disabled.connectors, "email-connector", "email", "smtp", emailConnectorValues())
	if got := disabled.one(t, toolRef("email-connectr")); len(got.Suggestions) != 0 {
		t.Errorf("email off: suggestions = %v, want none: the Builder must not be pointed at a type this deployment has off", got.Suggestions)
	}
}

func TestToolsDescribe_ASuggestionIsOnlyDrawnFromWhatTheCallerMayRead(t *testing.T) {
	f := newDescribeFixture(t)
	f.apiConnector(t, "carrier-api", "")

	cases := []struct {
		name   string
		scopes []string
		ref    DescribeToolRef
		want   []string
	}{
		{"tool read sees no connector names", []string{rbac.ScopeToolRead}, toolRef("carrier-ap"), nil},
		{"tool and connector read see both", []string{rbac.ScopeToolRead, rbac.ScopeConnectorRead}, toolRef("carrier-ap"), []string{"carrier-api"}},
		{"connector read sees connector names", []string{rbac.ScopeConnectorRead}, connectorRef("carier-api", ""), []string{"carrier-api"}},
		{"a connector ref is never offered a tool", []string{rbac.ScopeToolRead, rbac.ScopeConnectorRead}, connectorRef("get_current_tim", ""), nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := f.callAs(t, &auth.Identity{Scopes: tc.scopes}, tc.ref)
			if err != nil {
				t.Fatalf("tools/describe: %v", err)
			}
			got := resp.Tools[0]
			if got.Status != describeStatusNotFound || !slices.Equal(got.Suggestions, tc.want) && (len(got.Suggestions) != 0 || len(tc.want) != 0) {
				t.Errorf("got %+v, want not_found suggesting %v", got, tc.want)
			}
		})
	}
}

func TestToolsDescribe_AMistypedConnectorSuggestsTheStoredName(t *testing.T) {
	f := newDescribeFixture(t)
	f.apiConnector(t, "carrier-api", "")
	got := f.one(t, connectorRef("carier-api", ""))
	if got.Status != describeStatusNotFound || got.Reason != reasonConnectorNotFound || !slices.Equal(got.Suggestions, []string{"carrier-api"}) {
		t.Fatalf("got %+v, want not_found suggesting carrier-api", got)
	}
}
