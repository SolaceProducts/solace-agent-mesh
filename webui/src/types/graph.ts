// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export interface CommunicationEdgeData extends Record<string, unknown> {
    communicationType: "bidirectional" | "unidirectional";
    sourceHandle?: string;
    targetHandle?: string;
}

export interface AgentNodeData extends Record<string, unknown> {
    label: string;
    agentName: string;
    status: "online" | "offline";
    description?: string;
}
