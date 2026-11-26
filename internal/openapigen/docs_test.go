// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package openapigen

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestADescriptionDropsTheGoNameItOpensWith(t *testing.T) {
	cases := []struct {
		name, goName, doc, want string
	}{
		{"verb follows the name", "ConnectorIDs", "ConnectorIDs lists the UUIDs of the connectors.", "Lists the UUIDs of the connectors."},
		{"is follows the name", "CreatedTime", "CreatedTime is when the run was triggered.", "When the run was triggered."},
		{"are follows the name", "Items", "Items are the entries on this page.", "The entries on this page."},
		{"comma follows the name", "SkipIfCurrent", "SkipIfCurrent, when true, returns the latest deployment.", "When true, returns the latest deployment."},
		{"type name", "Agent", "Agent is an agent that answers questions.", "An agent that answers questions."},
		{"name later in the sentence stays", "ConnectorIDs", "The UUIDs ConnectorIDs holds.", "The UUIDs ConnectorIDs holds."},
		{"only the name", "ConnectorIDs", "ConnectorIDs", ""},
		{"JSON name stays", "ConnectorIDs", "connectorIds lists the UUIDs.", "connectorIds lists the UUIDs."},
		{"longer word stays", "Agent", "Agents answer questions.", "Agents answer questions."},
		{"possessive stays", "Agent", "Agent's tools come from its connectors.", "Agent's tools come from its connectors."},
		{"word starting with is is kept", "Status", "Status issues a warning.", "Issues a warning."},
		{"snake_case wire value keeps its case", "SyncStatus", "SyncStatus is in_sync when the agent matches.", "in_sync when the agent matches."},
		{"camelCase wire name keeps its case", "SkillID", "SkillID skillId wins over skillName.", "skillId wins over skillName."},
		{"name on its own line", "Name", "Name\nis the display name.", "The display name."},
		{"empty comment", "Name", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, withoutGoName(tc.doc, tc.goName, ""))
		})
	}
}

func TestDocSourceStripsTheGoNameFromTypesAndFields(t *testing.T) {
	docs := testDocs(t)

	assert.Equal(t, "A thing the fixtures manage.", docs.Type(fixturePkg, "Widget"))
	assert.Equal(t, "The display name.", docs.Field(fixturePkg, "Widget", "Name"))
}
