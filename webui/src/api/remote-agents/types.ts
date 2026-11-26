// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { RemoteAgentInterface, RemoteAgentProtocolStatus } from "@/types";

export type AuthConfigType = "oauth2" | "apikey" | "bearer" | "basic" | "none";

export interface OAuth2AuthConfig {
    type: "oauth2";
    clientId: string;
    clientSecret: string;
    tokenEndpoint: string;
    scopes?: string[];
}

export interface APIKeyAuthConfig {
    type: "apikey";
    headerName: string;
    apiKey: string;
}

export interface BearerAuthConfig {
    type: "bearer";
    token: string;
}

export interface BasicAuthConfig {
    type: "basic";
    username: string;
    password: string;
}

export interface NoneAuthConfig {
    type: "none";
}

export type AuthConfig = OAuth2AuthConfig | APIKeyAuthConfig | BearerAuthConfig | BasicAuthConfig | NoneAuthConfig;

export interface SkillDefinition {
    name: string;
    description: string;
}

export interface CreateRemoteAgentRequest {
    agentUrl: string;
    name: string;
    agentCardLocation: "well_known" | "custom" | "none";
    agentAuthConfig: AuthConfig;
    agentCardUrl?: string;
    description?: string;
    agentCardHeaders?: Record<string, string>;
    taskHeaders?: Record<string, string>;
    agentCardAuthConfig?: AuthConfig;
    skills?: SkillDefinition[];
    inputModes?: string[];
    outputModes?: string[];
    requestTimeoutSeconds?: number;
    protocolVersion?: RemoteAgentProtocolPin;
    trustedCardJwksUrls?: string[];
    requireSignedCard?: boolean;
}

export type RemoteAgentProtocolPin = "auto" | "v1_0" | "v0_3";

export interface DeployedRemoteAgentInfo {
    agentUrl: string;
    name: string;
    description?: string;
    agentCardLocation: string;
    agentCardUrl?: string;
    agentCardAuthType?: string;
    agentAuthType: string;
    requestTimeoutSeconds: number;
    skills?: SkillDefinition[];
    inputModes?: string[];
    outputModes?: string[];
    deployedAt: string;
    deployedBy: string;
}

export interface RemoteAgentResponse {
    id: string;
    agentUrl: string;
    name: string;
    description?: string;
    agentCardLocation: string;
    agentCardUrl?: string;
    agentCardAuthType?: string;
    agentAuthType: string;
    agentAuthConfig?: AuthConfig;
    agentCardAuthConfig?: AuthConfig;
    agentCardHeaders?: Record<string, string>;
    taskHeaders?: Record<string, string>;
    requestTimeoutSeconds: number;
    protocolVersion?: RemoteAgentProtocolPin;
    trustedCardJwksUrls?: string[];
    requireSignedCard?: boolean;
    remoteProtocol?: RemoteAgentProtocolStatus;
    skills?: SkillDefinition[];
    inputModes?: string[];
    outputModes?: string[];
    deploymentStatus?: string;
    syncStatus?: string;
    deployed?: DeployedRemoteAgentInfo;
    createdBy: string;
    updatedBy: string;
    createdAt: string;
    updatedAt: string;
    cardData?: AgentCardResponse;
    cardFetchError?: string;
}

export interface AgentCardResponse {
    name: string;
    description: string;
    skills?: SkillDefinition[];
    inputModes?: string[];
    outputModes?: string[];
    interfaces?: RemoteAgentInterface[];
    signed?: boolean;
}
