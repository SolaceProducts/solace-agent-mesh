// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useMemo } from "react";

import { useAgents } from "@/api/agents";
import { useRemoteAgents } from "@/api/remote-agents";

import type { Agent, RemoteAgent } from "@/types";

import { lookupAgent } from "../utils/agentLookup";

const EMPTY_AGENTS: Agent[] = [];
const EMPTY_REMOTE_AGENTS: RemoteAgent[] = [];

export const useAgentLookup = (displayName: string | undefined): Agent | undefined => {
    const { data: userAgents = EMPTY_AGENTS } = useAgents();
    const { data: remoteAgents = EMPTY_REMOTE_AGENTS } = useRemoteAgents();

    return useMemo(() => lookupAgent(displayName, userAgents, remoteAgents), [displayName, userAgents, remoteAgents]);
};
