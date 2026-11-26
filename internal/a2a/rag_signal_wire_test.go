// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package a2a

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRAGInfoUpdateDataCarriesSearchTurnZero(t *testing.T) {
	b, err := json.Marshal(RAGInfoUpdateData{
		Type:       string(SignalRAGInfoUpdate),
		Query:      "q",
		SearchType: "web_search",
		SearchTurn: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"search_turn":0`) {
		t.Errorf("search_turn omitted for turn 0, the first search of every task: %s", b)
	}
}
