// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apicontract

import "github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"

func (errorEnvelope) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "errorEnvelope is the body of every error response.",
		Fields: map[string]apidoc.Field{
			"Message":           {Doc: "Message describes what went wrong."},
			"ErrorID":           {Doc: "ErrorID identifies this failure in the server log. Quote it when reporting a problem."},
			"ValidationDetails": {Doc: "ValidationDetails lists the problems found, keyed by field name."},
		},
	}
}
