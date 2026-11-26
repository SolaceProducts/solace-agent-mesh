// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import "encoding/json"

const (
	describeMaxRefs              = 25
	describeMaxDescription       = 300
	describeMaxSuggestions       = 3
	describeMaxListedTools       = 25
	describeMaxNameLength        = 256
	describeMaxToolsPerConnector = 50
)

const (
	describeStatusOK           = "ok"
	describeStatusNotFound     = "not_found"
	describeStatusNotPermitted = "not_permitted"
	describeStatusUnavailable  = "unavailable"
)

const (
	describeKindBuiltin   = "builtin"
	describeKindToolset   = "toolset"
	describeKindConnector = "connector"
)

type DescribeToolsRequest struct {
	Tools []DescribeToolRef `json:"tools"`
}

type DescribeToolRef struct {
	ToolName  string `json:"toolName,omitempty"`
	Connector string `json:"connector,omitempty"`
	Tool      string `json:"tool,omitempty"`
}

type DescribeToolsResponse struct {
	Tools []DescribedTool `json:"tools"`
}

type DescribedTool struct {
	Ref           DescribeToolRef `json:"ref"`
	Status        string          `json:"status"`
	Reason        string          `json:"reason,omitempty"`
	Kind          string          `json:"kind,omitempty"`
	ConnectorName string          `json:"connectorName,omitempty"`
	ConnectorType string          `json:"connectorType,omitempty"`
	Name          string          `json:"name,omitempty"`
	ConcreteName  string          `json:"concreteName,omitempty"`
	Description   string          `json:"description,omitempty"`
	Parameters    json.RawMessage `json:"parameters"`
	Result        json.RawMessage `json:"result"`
	Effect        string          `json:"effect,omitempty"`
	ToolNode      ToolNodeVerdict `json:"toolNode"`
	Notes         []string        `json:"notes,omitempty"`
	Suggestions   []string        `json:"suggestions,omitempty"`
}

type ToolNodeVerdict struct {
	Usable bool   `json:"usable"`
	Reason string `json:"reason,omitempty"`
}
