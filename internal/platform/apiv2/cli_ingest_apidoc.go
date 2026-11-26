// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import "github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"

func (CLIIngestRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `CLIIngestRequest is the report a sam CLI run sends about itself. Its field
names match /api/v1, so the CLI sends the same body to either version.`,
		Fields: map[string]apidoc.Field{
			"Platform": {Doc: "Platform describes the computer the sam CLI ran on, not the platform service."},
			"Events":   {Doc: "Events are the run's events. Up to 64."},
		},
	}
}

func (CLIIngestPlatform) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `CLIIngestPlatform is the operating system, architecture, and version of the
sam CLI that ran. A value Agent Mesh does not recognize is dropped rather than refused.`,
		Fields: map[string]apidoc.Field{
			"OS":         {Doc: "OS is the Go operating system name the CLI was built for."},
			"Arch":       {Doc: "Arch is the Go architecture name the CLI was built for."},
			"AppVersion": {Doc: "AppVersion is the sam CLI's own version."},
		},
	}
}

func (CLIIngestEvent) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `CLIIngestEvent is one event of the run. The platform may replace the event
ID and time, and keeps only the properties its catalog allows.`,
		Fields: map[string]apidoc.Field{
			"EventID":   {Doc: "EventID identifies the event. The platform generates one when it is missing."},
			"SessionID": {Doc: "SessionID ties together the events of one CLI session."},
			"OccurredAt": {Doc: `OccurredAt is when the event happened, in ISO 8601. When it is missing or unusable, the
platform uses the time it received the report.`},
			"Event":      {Doc: "Event names the event, such as cli:command_run."},
			"Properties": {Doc: "Properties are the event's properties. Any that the analytics catalog does not allow are dropped."},
		},
	}
}
