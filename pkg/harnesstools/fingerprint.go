// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harnesstools

type EchoResponse struct {
	Sentinel string `json:"sentinel"`

	ToolID string `json:"toolId"`

	Process ProcessRef `json:"process"`

	InvokedByAgent string `json:"invokedByAgent"`

	LoadedFromSkill string `json:"loadedFromSkill"`

	ReceivedConfig map[string]any `json:"receivedConfig"`
}

type ProcessRef struct {
	Name string `json:"name"`

	PID int `json:"pid"`
}
