// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package embed //nolint:revive // embed is the domain name

type Signal struct {
	Type string
	Data any
}

const (
	SignalStatusUpdate        = "status_update"
	SignalArtifactReturn      = "artifact_return"
	SignalInlineBinaryContent = "inline_binary_content"
	SignalDeepResearchReport  = "deep_research_report"
)

type ArtifactReturnData struct {
	Filename string
	Version  string
}

type InlineBinaryData struct {
	Bytes    []byte
	MIMEType string
	Name     string
}

type DeepResearchReportData struct {
	Filename string
	Content  string
}
