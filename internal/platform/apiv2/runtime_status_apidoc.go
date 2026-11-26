// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import "github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"

func (RuntimeStatus) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "RuntimeStatus is what a running component reports about itself.",
		Fields: map[string]apidoc.Field{
			"Status": {Doc: `Status is the component's state: running, degraded (running with problems), starting,
disconnected, or stopped.`},
			"Problems": {Doc: "Problems are the problems the component reports. Empty unless status is degraded."},
		},
	}
}

func (RuntimeProblem) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "RuntimeProblem is one problem a degraded component reports.",
		Fields: map[string]apidoc.Field{
			"Key":       {Doc: "Key is a name for the kind of problem, not a resource ID."},
			"Severity":  {Doc: "Severity is how serious the problem is: degraded or fatal."},
			"Detail":    {Doc: "Detail describes the problem."},
			"SinceTime": {Doc: "SinceTime is when the problem began. Null when unknown."},
		},
	}
}
