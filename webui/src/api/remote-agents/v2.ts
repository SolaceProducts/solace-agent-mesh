// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { AgentStatus, RemoteAgent, RemoteAgentProtocolStatus, SyncStatus } from "@/types";

import type { AgentDeploymentResult } from "../agent-deployments/types";
import { postAgentDeploymentV2 } from "../agent-deployments/v2";
import { ApiV2Error, fetchAllPages, instantToIso, pageQuery, platformV2, type RuntimeStatusV2, toRuntimeStatus, withoutNulls } from "../common";

import type { FetchAgentCardRequest } from "./service";
import type { AgentCardResponse, AuthConfig, CreateRemoteAgentRequest, DeployedRemoteAgentInfo, RemoteAgentProtocolPin, RemoteAgentResponse, SkillDefinition } from "./types";

type NullMembers<T> = { [K in keyof T]-?: Exclude<T[K], undefined> | null };

type DeployedRemoteAgentV2 = Omit<DeployedRemoteAgentInfo, "deployedAt" | "description" | "agentCardUrl" | "agentCardAuthType"> &
    NullMembers<Pick<DeployedRemoteAgentInfo, "description" | "agentCardUrl" | "agentCardAuthType">> & { deployedTime: string | null };

interface RemoteAgentV2 {
    id: string;
    name: string;
    instanceName: string;
    description: string | null;
    agentUrl: string;
    agentCardLocation: RemoteAgent["agentCardLocation"];
    agentCardUrl: string | null;
    agentCardHeaders: Record<string, string> | null;
    agentCardAuthType: string | null;
    agentCardAuthConfig: NullMembers<AuthConfig> | null;
    agentAuthType: string;
    agentAuthConfig: NullMembers<AuthConfig> | null;
    taskHeaders: Record<string, string> | null;
    requestTimeoutSeconds: number;
    protocolVersion: RemoteAgentProtocolPin;
    trustedCardJwksUrls: string[];
    requireSignedCard: boolean;
    remoteProtocol: RemoteAgentProtocolStatus | null;
    skills: SkillDefinition[];
    inputModes: string[];
    outputModes: string[];
    deploymentStatus: AgentStatus;
    syncStatus: SyncStatus | null;
    runtimeStatus: RuntimeStatusV2;
    deployed: DeployedRemoteAgentV2 | null;
    cardData: AgentCardResponse | null;
    cardFetchError: string | null;
    createdBy: string;
    updatedBy: string;
    createdTime: string | null;
    updatedTime: string | null;
}

function toDeployed({ deployedTime, ...deployed }: DeployedRemoteAgentV2): DeployedRemoteAgentInfo {
    return { ...withoutNulls(deployed), deployedAt: instantToIso(deployedTime) };
}

const toAuthConfig = (config: NullMembers<AuthConfig> | null) => (config ? (withoutNulls(config) as AuthConfig) : undefined);

function toRemoteAgent({ agentCardAuthConfig, agentAuthConfig, runtimeStatus, deployed, createdTime, updatedTime, ...fields }: RemoteAgentV2): RemoteAgent & RemoteAgentResponse {
    return {
        ...withoutNulls(fields),
        agentCardAuthConfig: toAuthConfig(agentCardAuthConfig),
        agentAuthConfig: toAuthConfig(agentAuthConfig),
        runtimeStatus: toRuntimeStatus(runtimeStatus),
        deployed: deployed ? toDeployed(deployed) : undefined,
        createdAt: instantToIso(createdTime),
        updatedAt: instantToIso(updatedTime),
    };
}

const FEATURE_ABSENT = new Set([404, 501]);

export async function getAllRemoteAgentsV2(): Promise<RemoteAgent[]> {
    try {
        const all = await fetchAllPages(params => platformV2.list<RemoteAgentV2>(`/remoteAgents?${pageQuery(params)}`));
        return all.map(toRemoteAgent);
    } catch (error) {
        if (error instanceof ApiV2Error && FEATURE_ABSENT.has(error.status)) return [];
        throw error;
    }
}

export async function createRemoteAgentV2(request: CreateRemoteAgentRequest): Promise<RemoteAgentResponse> {
    return toRemoteAgent(await platformV2.post<RemoteAgentV2>("/remoteAgents", request));
}

export async function getRemoteAgentV2(agentId: string, includeCard?: boolean): Promise<RemoteAgentResponse> {
    return toRemoteAgent(await platformV2.get<RemoteAgentV2>(`/remoteAgents/${agentId}${includeCard ? "?expand=card" : ""}`));
}

export async function updateRemoteAgentV2(agentId: string, request: CreateRemoteAgentRequest): Promise<RemoteAgentResponse> {
    return toRemoteAgent(await platformV2.put<RemoteAgentV2>(`/remoteAgents/${agentId}`, request));
}

export async function deleteRemoteAgentV2(agentId: string): Promise<void> {
    await platformV2.delete(`/remoteAgents/${agentId}`);
}

export async function deployRemoteAgentV2(agentId: string, action: "deploy" | "undeploy" = "deploy"): Promise<AgentDeploymentResult> {
    return postAgentDeploymentV2({ agentId, action });
}

export async function fetchAgentCardV2({ agentCardUrl, authenticationType, authConfig, customHeaders }: FetchAgentCardRequest): Promise<AgentCardResponse> {
    try {
        return await platformV2.put<AgentCardResponse>("/remoteAgents/fetchCard", { agentCardUrl, authConfig: toCardFetchAuth(authenticationType, authConfig ?? {}), customHeaders });
    } catch (error) {
        throw error instanceof ApiV2Error && namesAFieldTheFormLacks(error) ? new ApiV2Error(error.status, error.message, { errorId: error.errorId, location: error.location }) : error;
    }
}

function namesAFieldTheFormLacks(error: ApiV2Error): boolean {
    return Object.keys(error.validationDetails ?? {}).some(field => field !== "agentCardUrl");
}

function toCardFetchAuth(type: string | undefined, c: Record<string, string | undefined>) {
    if (!type) return undefined;
    return {
        type,
        token: c.bearerToken,
        apiKey: c.apikeyValue,
        headerName: c.apikeyHeaderName,
        tokenEndpoint: c.oauth2TokenUrl,
        clientId: c.oauth2ClientId,
        clientSecret: c.oauth2ClientSecret,
        scopes: c.oauth2Scope?.split(" ").filter(Boolean),
        username: c.basicUsername,
        password: c.basicPassword,
    };
}
