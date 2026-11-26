// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"errors"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func TestRefuseWebSearchKeyMove(t *testing.T) {
	t.Parallel()
	const custom = "https://mirror.example/search"
	cases := []struct {
		name              string
		stored, requested map[string]any
		wantRefused       bool
	}{
		{"no api_base in the request", map[string]any{}, map[string]any{"max_results": 5}, false},
		{"the default made explicit", map[string]any{}, map[string]any{"api_base": "https://api.tavily.com"}, false},
		{"the default with a trailing slash", map[string]any{}, map[string]any{"api_base": "https://API.tavily.com/"}, false},
		{"the same custom base with a slash", map[string]any{"api_base": custom}, map[string]any{"api_base": custom + "/"}, false},
		{"a custom base back to the default", map[string]any{"api_base": custom}, map[string]any{"api_base": "https://api.tavily.com"}, false},
		{"a custom base cleared", map[string]any{"api_base": custom}, map[string]any{"api_base": ""}, false},
		{"the default moved to a custom host", map[string]any{}, map[string]any{"api_base": "https://collector.example"}, true},
		{"a custom base moved to another host", map[string]any{"api_base": custom}, map[string]any{"api_base": "https://collector.example"}, true},
		{"the default in upper case with its port", map[string]any{}, map[string]any{"api_base": "https://API.TAVILY.COM:443"}, false},
		{"a placeholder base restated", map[string]any{"api_base": "<<__SAM_REQUIRED__>>"}, map[string]any{"api_base": "<<__SAM_REQUIRED__>>"}, false},
		{"an unparseable base restated", map[string]any{"api_base": "http://[bad"}, map[string]any{"api_base": "http://[bad"}, false},
		{"an unparseable base", map[string]any{}, map[string]any{"api_base": "http://[bad"}, true},
		{"another port", map[string]any{}, map[string]any{"api_base": "https://api.tavily.com:8443"}, true},
		{"the default over http", map[string]any{}, map[string]any{"api_base": "http://api.tavily.com"}, true},
		{"the default over http on its port", map[string]any{}, map[string]any{"api_base": "http://api.tavily.com:443"}, true},
		{"the default as ftp", map[string]any{}, map[string]any{"api_base": "ftp://api.tavily.com"}, true},
		{"the default with non-ASCII letters that fold to it", map[string]any{}, map[string]any{"api_base": "https://apİ.tavİly.com"}, true},
		{"the default with a percent-encoded non-ASCII letter", map[string]any{}, map[string]any{"api_base": "https://ap%C4%B0.tav%C4%B0ly.com"}, true},
		{"a stored base against a Kelvin sign", map[string]any{"api_base": "https://k.example"}, map[string]any{"api_base": "https://K.example"}, true},
		{"a custom base with a non-ASCII letter", map[string]any{"api_base": custom}, map[string]any{"api_base": "https://mİrror.example/search"}, true},
		{"the default with a fragment", map[string]any{}, map[string]any{"api_base": "https://api.tavily.com#x"}, true},
		{"the default with an empty query", map[string]any{}, map[string]any{"api_base": "https://api.tavily.com?"}, true},
		{"a custom base with its path encoded differently", map[string]any{"api_base": custom}, map[string]any{"api_base": "https://mirror.example/%73earch"}, true},
		{"a host without a name", map[string]any{}, map[string]any{"api_base": "https://:443"}, true},
		{"a custom base downgraded to http", map[string]any{"api_base": custom}, map[string]any{"api_base": "http://mirror.example/search"}, true},
		{"the default with another path", map[string]any{}, map[string]any{"api_base": "https://api.tavily.com/collect"}, true},
		{"a custom base with another path", map[string]any{"api_base": custom}, map[string]any{"api_base": "https://mirror.example/collect"}, true},
		{"the default with a user name", map[string]any{}, map[string]any{"api_base": "https://collector@api.tavily.com"}, true},
		{"the default with a query", map[string]any{}, map[string]any{"api_base": "https://api.tavily.com?to=collector.example"}, true},
		{"a move with the key as an environment reference", map[string]any{}, map[string]any{"api_base": "https://collector.example", "api_key": "${TAVILY_API_KEY}"}, true},
		{"a move with the key as the redacted placeholder", map[string]any{}, map[string]any{"api_base": "https://collector.example", "api_key": redactedSentinel}, true},
		{"a move with the key as a Builder placeholder", map[string]any{}, map[string]any{"api_base": "https://collector.example", "api_key": "<<__SAM_REQUIRED__>>"}, true},
		{"a move with a fresh key", map[string]any{}, map[string]any{"api_base": "https://collector.example", "api_key": "tvly-new"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertKeyMoveRefused(t, refuseWebSearchKeyMove("tavily", tc.requested, tc.stored), tc.wantRefused)
		})
	}
}

func TestRefuseWebSearchKeyMove_UnknownSubtypeFailsClosed(t *testing.T) {
	t.Parallel()
	err := refuseWebSearchKeyMove("undeclared", map[string]any{"api_base": "https://collector.example"}, map[string]any{})
	assertKeyMoveRefused(t, err, true)
	assertKeyMoveRefused(t, refuseWebSearchKeyMove("undeclared", map[string]any{"max_results": 5}, map[string]any{}), false)
}

func assertKeyMoveRefused(t *testing.T, err error, want bool) {
	t.Helper()
	var verr *ValidationError
	refused := errors.As(err, &verr) && verr.Field == "values.api_key"
	if refused != want {
		t.Fatalf("refused = %v (%v), want %v", refused, err, want)
	}
	if refused && !strings.Contains(verr.Message, "Enter the API key again") {
		t.Fatalf("message = %q, want it to ask for the key again", verr.Message)
	}
}

func keyMoveFixture(t *testing.T) (svc *ConnectorService, db *store.DB, id, storedBase string, move UpdateConnectorRequest) {
	t.Helper()
	original := newFakeSearchAPI(t, 200, searchResultsBody)
	allowProbeTo(t, original.server.URL)
	svc, db = newWebSearchService(t, discardLogger())
	id = createWebSearchConnector(t, svc, "web-move", map[string]any{"api_key": "tvly-stored", "api_base": original.server.URL})
	move = UpdateConnectorRequest{Values: map[string]any{"api_base": "https://collector.example", "api_key": redactedSentinel}}
	return svc, db, id, original.server.URL, move
}

func TestWebSearchConnectorUpdateInTx_CannotMoveTheStoredKey(t *testing.T) {
	svc, db, id, storedBase, move := keyMoveFixture(t)

	tx, err := db.BeginTx(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.UpdateInTx(t.Context(), tx, id, move, "user-1", ConnectorUpdateOptions{})
	if rbErr := tx.Rollback(); rbErr != nil {
		t.Fatal(rbErr)
	}

	assertKeyMoveRefused(t, err, true)
	if got := storedWebSearchValue(t, svc, id, "api_base"); got != storedBase {
		t.Fatalf("stored api_base = %v, want it unchanged", got)
	}
}

func TestWebSearchConnectorValidateUpdate_RefusesMovingTheStoredKey(t *testing.T) {
	svc, _, id, _, move := keyMoveFixture(t)

	_, err := svc.ValidateUpdate(t.Context(), id, move)

	assertKeyMoveRefused(t, err, true)
}

func TestWebSearchConnectorUpdateWithRedeploy_CannotMoveTheStoredKey(t *testing.T) {
	svc, db, id, storedBase, move := keyMoveFixture(t)

	_, err := UpdateConnectorWithRedeploy(t.Context(), db, svc, ConnectorRedeployUpdate{ID: id, Request: move, UserID: "user-1"}, discardLogger())

	assertKeyMoveRefused(t, err, true)
	if got := storedWebSearchValue(t, svc, id, "api_base"); got != storedBase {
		t.Fatalf("stored api_base = %v, want it unchanged", got)
	}
}

func TestWebSearchConnectorUpdate_ANonASCIIHostIsAMove(t *testing.T) {
	t.Parallel()
	svc, _ := newWebSearchService(t, discardLogger())
	id := createWebSearchConnector(t, svc, "web-lookalike", map[string]any{"api_key": "${TAVILY_API_KEY}"})

	_, err := updateWebSearchConnector(t, svc, id, map[string]any{"api_base": "https://apİ.tavİly.com", "api_key": redactedSentinel})

	assertKeyMoveRefused(t, err, true)
	if got := storedWebSearchValue(t, svc, id, "api_base"); got == "https://apİ.tavİly.com" {
		t.Fatal("the stored key now goes to the lookalike host")
	}
}

func TestWebSearchConnectorUpdate_ABlankKeyIsRefused(t *testing.T) {
	svc, _, id, _, _ := keyMoveFixture(t)

	_, err := updateWebSearchConnector(t, svc, id, map[string]any{"api_key": " \t "})

	var verr *ValidationError
	if !errors.As(err, &verr) || verr.Field != "values.api_key" {
		t.Fatalf("update = %v, want a values.api_key refusal", err)
	}
	if got := storedWebSearchValue(t, svc, id, "api_key"); got == " \t " {
		t.Fatal("the blank key replaced the stored one")
	}
}
