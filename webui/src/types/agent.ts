// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { AgentCard, AgentSkill } from "./a2a";

export interface WelcomeSuggestion {
    label: string;
    prompt: string;
    auto_send?: boolean;
}

export interface AgentWelcomeConfig {
    welcome_message?: string;
    suggestions?: WelcomeSuggestion[];
    welcome_icon?: string;
    welcome_icon_size?: "small" | "medium" | "large";
}

export interface AgentInfo extends AgentCard {
    display_name?: string;
    last_seen?: string;
    peer_agents?: string[];
    tools?: AgentSkill[];
}

export interface AgentCardInfoFE extends AgentInfo {
    displayName?: string;
    peerAgents?: string[];
    tools?: AgentSkill[];
    isWorkflow?: boolean;
    isInternal?: boolean;
    isEphemeral?: boolean;
    remoteProtocol?: RemoteAgentProtocolStatus;
    inputSchema?: Record<string, unknown>;
    priority?: number;
    welcome?: AgentWelcomeConfig;
    resolvedSkills?: ResolvedAgentSkill[];
}

export interface RemoteAgentProtocolStatus {
    selectedVersion: "v1_0" | "v0_3";
    binding: "jsonrpc" | "http_json";
    deprecated: boolean;
    cardSignature: "verified" | "unsigned" | "unchecked" | "refused";
}

export interface ResolvedAgentSkill {
    name: string;
    description?: string;
}
