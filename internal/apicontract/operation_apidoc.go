// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apicontract

import "github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"

func (Operation) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `Operation is work that Agent Mesh accepted to finish later. The call that
starts it returns 202 with the operation and a Location header. Poll that
location until status is succeeded, failed, or cancelled.`,
		Fields: map[string]apidoc.Field{
			"ID":            {Doc: "ID is the operation's UUID."},
			"Type":          {Doc: "Type is always \"operation\"."},
			"ResourceID":    {Doc: "ResourceID is the UUID of the resource the work acts on. Null while a generate_dataset operation has not yet created its dataset, and stays null if that operation fails."},
			"OperationType": {Doc: "OperationType names the work: run for an evaluation experiment's run, generate_dataset for an evaluation dataset an AI generates, or generate_examples for examples an AI adds to one."},
			"CreatedBy":     {Doc: "CreatedBy identifies the user who started the work."},
			"CreatedTime":   {Doc: "CreatedTime is when Agent Mesh accepted the work."},
			"CompletedTime": {Doc: "CompletedTime is when the work finished, or null while it is pending or in progress."},
			"Status":        {Doc: "Status is the state of the work: pending until it starts, inProgress while it runs, then succeeded, failed, or cancelled."},
			"Error":         {Doc: "Error says why the work failed, or is null unless status is failed."},
		},
	}
}

func (OperationError) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "OperationError says why an operation failed.",
		Fields: map[string]apidoc.Field{
			"Message": {Doc: "Message is a sentence Agent Mesh wrote about the failure. The errorId field says what failed."},
			"ErrorID": {Doc: "ErrorID identifies what failed. For an evaluation run, it is the UUID of the failed run, which GET /evaluations/runs/{id} returns. For an AI generation, it is the ID Agent Mesh logged the cause under."},
		},
	}
}
