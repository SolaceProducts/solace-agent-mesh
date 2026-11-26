// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Agent, RemoteAgent } from "@/types";

export const lookupAgent = (displayName: string | undefined, userAgents: Agent[], remoteAgents?: RemoteAgent[]): Agent | undefined => {
    if (!displayName) return undefined;

    const foundUserAgent = userAgents.find(ua => ua.name === displayName);
    if (foundUserAgent) return foundUserAgent;

    const foundRemoteAgent = remoteAgents?.find(ra => ra.name === displayName);
    if (foundRemoteAgent) {
        return {
            id: foundRemoteAgent.id,
            name: foundRemoteAgent.name,
            description: foundRemoteAgent.description ?? "",
            skills: foundRemoteAgent.skills,
            inputModes: foundRemoteAgent.inputModes,
            outputModes: foundRemoteAgent.outputModes,
            deploymentStatus: foundRemoteAgent.deploymentStatus,
            createdBy: foundRemoteAgent.createdBy,
            updatedBy: foundRemoteAgent.updatedBy,
            createdAt: foundRemoteAgent.createdAt,
            updatedAt: foundRemoteAgent.updatedAt,
        };
    }

    return undefined;
};
