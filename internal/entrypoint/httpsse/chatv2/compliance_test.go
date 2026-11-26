// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package chatv2

import (
	"reflect"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/apicontract/compliancetest"
)

var answersNotResources = map[reflect.Type]bool{
	configType():                            true,
	loginConfigType():                       true,
	reflect.TypeFor[ChatUserCapabilities](): true,
	reflect.TypeFor[SendResponse]():         true,
}

func TestChatRoutesMeetTheV2Contract(t *testing.T) {
	compliancetest.Check(t, compliancetest.Config{
		Gates:   GateDocs,
		Results: answersNotResources,
		OpenObjects: map[string]string{
			"SendMessage.parts":    "A2A content parts are a polymorphic union (text, data, file) that the gateway passes to the agent as sent",
			"SendMessage.metadata": "A2A message metadata is open by the protocol; the gateway reads agent_name and project_id and passes the rest on",
			"PendingInput.request": "The user_input_request is the A2A data part the agent defines, with a surface in the A2UI catalog; the gateway passes it on as the agent sent it",
			"UserInputAnswer.data": "The answer is the data model of the question's A2UI surface, which the agent defines and the gateway passes on unread",
			"TaskMessage.parts":    "A2A content parts are a polymorphic union (text, data, file) that the agent defines and the gateway passes on unread",
			"Bubble.parts":         "A bubble's parts are A2A content parts, a polymorphic union (text, data, file) that the client writes and the gateway reads only for the files they name",
			"UnconsumedEvent.data": "The data is the A2A event the agent defined, which the gateway buffered and passes on unread",
			"ChatArtifact.schema":  "The schema is the structure the store inferred from a CSV, JSON or YAML file, a JSON Schema whose keys depend on the file",
			"TaskArtifact.parts":   "A2A content parts are a polymorphic union (text, data, file) that the agent defines and the gateway passes on unread",
		},
		WirePackage: configType().PkgPath(),
	}, Routes(Deps{}))
}
