// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { AuthConfig, CreateRemoteAgentRequest, RemoteAgentResponse, SkillDefinition } from "@/api/remote-agents";

import type { AdditionalInfoFormData } from "./AdditionalInfoStep";
import { AUTH_TYPES } from "./AuthenticationFields";
import { parseTrustedKeySetUrls } from "./cardTrust";
import type { ConnectAgentFormData } from "./ConnectAgentForm";
import { buildWellKnownUrl } from "./connectAgentHelpers";

function headersArrayToObject(headers: { key: string; value: string }[]): Record<string, string> | undefined {
    const obj: Record<string, string> = {};
    let hasValues = false;

    headers.forEach(({ key, value }) => {
        if (key && value) {
            obj[key] = value;
            hasValues = true;
        }
    });

    return hasValues ? obj : undefined;
}

function buildAuthConfig(authType: string | undefined, formData: Partial<ConnectAgentFormData> | Partial<AdditionalInfoFormData>, fieldPrefix: "auth" | "taskAuth"): AuthConfig | undefined {
    if (authType === AUTH_TYPES.BEARER) {
        const tokenField = `${fieldPrefix}BearerToken` as keyof typeof formData;
        const token = (formData[tokenField] as string) || "";

        return {
            type: "bearer",
            token,
        };
    }

    if (authType === AUTH_TYPES.APIKEY) {
        const apikeyField = `${fieldPrefix}ApikeyValue` as keyof typeof formData;
        const apiKey = (formData[apikeyField] as string) || "";

        return {
            type: "apikey",
            headerName: "X-API-Key",
            apiKey,
        };
    }

    if (authType === AUTH_TYPES.OAUTH2) {
        const tokenUrlField = `${fieldPrefix}Oauth2TokenUrl` as keyof typeof formData;
        const clientIdField = `${fieldPrefix}Oauth2ClientId` as keyof typeof formData;
        const clientSecretField = `${fieldPrefix}Oauth2ClientSecret` as keyof typeof formData;
        const scopeField = `${fieldPrefix}Oauth2Scope` as keyof typeof formData;

        const clientSecret = (formData[clientSecretField] as string) || "";
        const scope = formData[scopeField] as string | undefined;
        const scopes = scope ? scope.split(/[\s,]+/).filter(Boolean) : [];

        return {
            type: "oauth2",
            clientId: (formData[clientIdField] as string) || "",
            clientSecret,
            tokenEndpoint: (formData[tokenUrlField] as string) || "",
            scopes: scopes.length > 0 ? scopes : undefined,
        };
    }

    if (authType === "basic") {
        const usernameField = `${fieldPrefix}BasicUsername` as keyof typeof formData;
        const passwordField = `${fieldPrefix}BasicPassword` as keyof typeof formData;
        const password = (formData[passwordField] as string) || "";

        return {
            type: "basic",
            username: (formData[usernameField] as string) || "",
            password,
        };
    }

    return { type: "none" };
}

export function transformToCreateRemoteAgentRequest(connectFormData: Partial<ConnectAgentFormData>, additionalInfoFormData: Partial<AdditionalInfoFormData>, fetchedAgentCard?: import("@/types").ExternalAgentCard | null): CreateRemoteAgentRequest {
    const agentCardLocation = (connectFormData.agentCardLocation as "well_known" | "custom" | "none") || "none";

    const agentAuthConfig = buildAuthConfig(additionalInfoFormData.taskAuthenticationType, additionalInfoFormData, "taskAuth");

    const agentCardAuthConfig = agentCardLocation !== "none" && connectFormData.authenticationType ? buildAuthConfig(connectFormData.authenticationType, connectFormData, "auth") : undefined;

    const agentCardHeaders = connectFormData.agentCardHeaders ? headersArrayToObject(connectFormData.agentCardHeaders) : undefined;
    const taskHeaders = additionalInfoFormData.taskHeaders ? headersArrayToObject(additionalInfoFormData.taskHeaders) : undefined;

    const hasAgentCard = agentCardLocation !== "none";
    const useFetchedCard = hasAgentCard && fetchedAgentCard;

    const skills: SkillDefinition[] | undefined =
        useFetchedCard && fetchedAgentCard.skills && fetchedAgentCard.skills.length > 0
            ? fetchedAgentCard.skills.map(skill => ({
                  name: skill.name,
                  description: skill.description,
              }))
            : additionalInfoFormData.skills && additionalInfoFormData.skills.length > 0
              ? additionalInfoFormData.skills
              : undefined;

    const inputModes: string[] | undefined =
        useFetchedCard && fetchedAgentCard.inputModes && fetchedAgentCard.inputModes.length > 0
            ? fetchedAgentCard.inputModes
            : additionalInfoFormData.inputModes && additionalInfoFormData.inputModes.length > 0
              ? additionalInfoFormData.inputModes
              : undefined;

    const outputModes: string[] | undefined =
        useFetchedCard && fetchedAgentCard.outputModes && fetchedAgentCard.outputModes.length > 0
            ? fetchedAgentCard.outputModes
            : additionalInfoFormData.outputModes && additionalInfoFormData.outputModes.length > 0
              ? additionalInfoFormData.outputModes
              : undefined;

    const name = agentCardLocation === "none" ? additionalInfoFormData.agentName || "" : additionalInfoFormData.solaceDisplayName || "";

    const description = fetchedAgentCard?.description ? fetchedAgentCard.description : additionalInfoFormData.agentDescription || undefined;

    const request: CreateRemoteAgentRequest = {
        agentUrl: connectFormData.agentUrl || "",
        name,
        agentCardLocation,
        ...(agentAuthConfig && { agentAuthConfig }),
        ...(connectFormData.agentCardUrl && { agentCardUrl: connectFormData.agentCardUrl }),
        ...(description && { description }),
        ...(agentCardHeaders && { agentCardHeaders }),
        ...(taskHeaders && { taskHeaders }),
        ...(agentCardAuthConfig && { agentCardAuthConfig }),
        ...(skills && { skills }),
        ...(inputModes && { inputModes }),
        ...(outputModes && { outputModes }),
        protocolVersion: additionalInfoFormData.protocolVersion || "auto",
        trustedCardJwksUrls: hasAgentCard ? parseTrustedKeySetUrls(additionalInfoFormData.trustedCardJwksUrls) : [],
        requireSignedCard: hasAgentCard && !!additionalInfoFormData.requireSignedCard,
    } as CreateRemoteAgentRequest;

    return request;
}

function headersObjectToArray(headers?: Record<string, string>): { key: string; value: string }[] {
    if (!headers) return [];
    return Object.entries(headers).map(([key, value]) => ({ key, value }));
}

const BACKEND_REDACTED_SENTINEL = "<REDACTED>";

function extractAuthFields(authConfig: AuthConfig | string | undefined, fieldPrefix: "auth" | "taskAuth"): Partial<ConnectAgentFormData> | Partial<AdditionalInfoFormData> {
    if (!authConfig || typeof authConfig === "string") {
        return {};
    }

    const fields: any = {};

    const filterSentinel = (value: string | undefined): string => {
        return value === BACKEND_REDACTED_SENTINEL ? "" : value || "";
    };

    if (authConfig.type === "bearer") {
        fields[`${fieldPrefix}BearerToken`] = filterSentinel(authConfig.token);
    } else if (authConfig.type === "apikey") {
        fields[`${fieldPrefix}ApikeyValue`] = filterSentinel(authConfig.apiKey);
    } else if (authConfig.type === "oauth2") {
        fields[`${fieldPrefix}Oauth2TokenUrl`] = authConfig.tokenEndpoint || "";
        fields[`${fieldPrefix}Oauth2ClientId`] = authConfig.clientId || "";
        fields[`${fieldPrefix}Oauth2ClientSecret`] = filterSentinel(authConfig.clientSecret);
        fields[`${fieldPrefix}Oauth2Scope`] = authConfig.scopes?.join(" ") || "";
    } else if (authConfig.type === "basic") {
        fields[`${fieldPrefix}BasicUsername`] = authConfig.username || "";
        fields[`${fieldPrefix}BasicPassword`] = filterSentinel(authConfig.password);
    }

    return fields;
}

export function transformRemoteAgentToFormData(remoteAgent: RemoteAgentResponse): {
    connectFormData: Partial<ConnectAgentFormData>;
    additionalInfoFormData: Partial<AdditionalInfoFormData>;
} {
    if (!remoteAgent) {
        console.error("transformRemoteAgentToFormData: remoteAgent is null or undefined");
        throw new Error("Remote agent data is required");
    }

    if (!remoteAgent.agentUrl) {
        console.error("transformRemoteAgentToFormData: remoteAgent.agentUrl is missing", remoteAgent);
        throw new Error("Remote agent data is incomplete - agentUrl is missing");
    }

    const agentAuthConfig: AuthConfig | undefined = remoteAgent.agentAuthConfig;
    const agentCardAuthConfig: AuthConfig | undefined = remoteAgent.agentCardAuthConfig;

    const connectFormData: Partial<ConnectAgentFormData> = {
        agentUrl: remoteAgent.agentUrl,
        agentCardLocation: remoteAgent.agentCardLocation as "well_known" | "custom" | "none",
        agentCardUrl: remoteAgent.agentCardUrl || (remoteAgent.agentCardLocation === "well_known" ? buildWellKnownUrl(remoteAgent.agentUrl) : ""),
        authenticationType: remoteAgent.agentCardAuthType || agentCardAuthConfig?.type || "none",
        agentCardHeaders: headersObjectToArray(remoteAgent.agentCardHeaders),
        ...extractAuthFields(agentCardAuthConfig, "auth"),
    };

    const additionalInfoFormData: Partial<AdditionalInfoFormData> = {
        taskAuthenticationType: remoteAgent.agentAuthType || undefined,
        taskHeaders: headersObjectToArray(remoteAgent.taskHeaders),
        skills: remoteAgent.skills || [],
        inputModes: remoteAgent.inputModes,
        outputModes: remoteAgent.outputModes,
        protocolVersion: remoteAgent.protocolVersion ?? "auto",
        trustedCardJwksUrls: (remoteAgent.trustedCardJwksUrls ?? []).join("\n"),
        requireSignedCard: !!remoteAgent.requireSignedCard,
        ...extractAuthFields(agentAuthConfig, "taskAuth"),
    };

    if (remoteAgent.agentCardLocation === "none") {
        additionalInfoFormData.agentName = remoteAgent.name;
    } else {
        additionalInfoFormData.externalAgentName = remoteAgent.name;
        additionalInfoFormData.solaceDisplayName = remoteAgent.name;
    }

    if (remoteAgent.description) {
        additionalInfoFormData.agentDescription = remoteAgent.description;
    }

    return {
        connectFormData,
        additionalInfoFormData,
    };
}

export function withFieldsTheFormKeeps(request: CreateRemoteAgentRequest, stored: RemoteAgentResponse | undefined): CreateRemoteAgentRequest {
    if (!stored) {
        throw new Error("The agent's saved settings did not load, so saving now would reset settings this form does not show. Reload the page and try again.");
    }
    return { ...request, requestTimeoutSeconds: stored.requestTimeoutSeconds };
}
