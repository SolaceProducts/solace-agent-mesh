// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export interface AgentDiagramConfig {
    name: string;
    description?: string;
    instruction?: string;
    skills?: Array<{ name: string; description?: string }>;
    tools?: Array<{
        tool_type: string;
        tool_name?: string;
        group_name?: string;
        tool_description?: string;
    }>;
    toolsets?: string[];
    inputModes?: string[];
    outputModes?: string[];
}
