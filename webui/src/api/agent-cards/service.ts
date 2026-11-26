// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { api } from "@/api/client";

import type { AgentCard } from "@/types/a2a";

import type { AgentCardInfo } from "@/types";

import { transformAgentCard } from "./transform";

export async function getAgentCards(): Promise<AgentCardInfo[]> {
    const body: { data: AgentCard[] } = await api.webui.get("/api/v1/agentCards");
    return body.data.map(transformAgentCard).filter(card => !card.isEphemeral);
}
