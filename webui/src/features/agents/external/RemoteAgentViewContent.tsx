// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useMemo } from "react";

import { PageLabel, PageSection } from "@/common/layout";

import type { ExternalAgentCard, RemoteAgentProtocolStatus } from "@/types";

import { AgentAbilityCard } from "../AgentAbilityCard";
import { AgentCommunicationMode } from "../AgentCommunicationMode";

import type { AdditionalInfoFormData } from "./AdditionalInfoStep";
import { AUTH_TYPE_LABELS } from "./AuthenticationFields";
import { parseTrustedKeySetUrls } from "./cardTrust";
import type { ConnectAgentFormData } from "./ConnectAgentForm";
import { RemoteProtocolSection } from "./RemoteProtocolDetails";

interface RemoteAgentViewContentProps {
    formData: Partial<ConnectAgentFormData>;
    additionalInfoFormData: Partial<AdditionalInfoFormData>;
    fetchedAgentCard?: ExternalAgentCard | null;
    isLoadingAgentCard?: boolean;
    remoteProtocol?: RemoteAgentProtocolStatus;
    showDeployedStatus?: boolean;
}

const maskSensitiveValue = (value: string | undefined): string => {
    if (!value) return "Not set";
    return "••••••••";
};

const formatValue = (value: string | undefined): string => {
    if (!value) return "Not set";
    return value;
};

export const RemoteAgentViewContent = ({ formData, additionalInfoFormData, fetchedAgentCard, isLoadingAgentCard, remoteProtocol, showDeployedStatus }: RemoteAgentViewContentProps) => {
    const hasAgentCard = formData.agentCardLocation !== "none" && formData.agentCardLocation !== "";
    const useFetchedCard = hasAgentCard && fetchedAgentCard;

    const externalAgentName = useMemo(() => {
        if (isLoadingAgentCard) {
            return "Loading...";
        }
        if (useFetchedCard) {
            return fetchedAgentCard.name;
        }
        if (hasAgentCard) {
            return "Unable to fetch";
        }
        return null;
    }, [hasAgentCard, useFetchedCard, fetchedAgentCard, isLoadingAgentCard]);

    const solaceDisplayName = useMemo(() => {
        if (hasAgentCard) {
            return additionalInfoFormData.solaceDisplayName || "Not set";
        }
        return additionalInfoFormData.agentName || "Not set";
    }, [hasAgentCard, additionalInfoFormData.solaceDisplayName, additionalInfoFormData.agentName]);

    const agentName = useMemo(() => {
        if (hasAgentCard) {
            return additionalInfoFormData.solaceDisplayName || "Not set";
        }
        return additionalInfoFormData.agentName || "Not set";
    }, [hasAgentCard, additionalInfoFormData.solaceDisplayName, additionalInfoFormData.agentName]);

    const agentDescription = useMemo(() => {
        if (useFetchedCard) {
            return fetchedAgentCard.description;
        }
        return additionalInfoFormData.agentDescription || "Not set";
    }, [useFetchedCard, fetchedAgentCard, additionalInfoFormData.agentDescription]);

    const displaySkills = useMemo(() => {
        if (useFetchedCard && fetchedAgentCard.skills) {
            return fetchedAgentCard.skills;
        }
        return additionalInfoFormData.skills || [];
    }, [useFetchedCard, fetchedAgentCard, additionalInfoFormData.skills]);

    const displayInputModes = useMemo(() => {
        if (useFetchedCard && fetchedAgentCard.inputModes) {
            return fetchedAgentCard.inputModes;
        }
        return additionalInfoFormData.inputModes || [];
    }, [useFetchedCard, fetchedAgentCard, additionalInfoFormData.inputModes]);

    const displayOutputModes = useMemo(() => {
        if (useFetchedCard && fetchedAgentCard.outputModes) {
            return fetchedAgentCard.outputModes;
        }
        return additionalInfoFormData.outputModes || [];
    }, [useFetchedCard, fetchedAgentCard, additionalInfoFormData.outputModes]);

    const authTypeLabel = useMemo(() => {
        const authType = additionalInfoFormData.taskAuthenticationType;
        if (!authType || authType === "none") return "No Authentication";
        return AUTH_TYPE_LABELS[authType] || authType;
    }, [additionalInfoFormData.taskAuthenticationType]);

    const hasHeaders = useMemo(() => {
        const headers = additionalInfoFormData.taskHeaders || [];
        return headers.some(h => h.key && h.value);
    }, [additionalInfoFormData.taskHeaders]);

    return (
        <div className="flex max-w-1/2 min-w-[700px] flex-col gap-8 pb-4">
            <PageSection>
                <div className="text-lg font-semibold">Agent Details</div>

                <div className="grid grid-cols-[200px_1fr] gap-4">
                    <PageLabel>Agent URL</PageLabel>
                    <div className="break-all">{formatValue(formData.agentUrl)}</div>

                    <PageLabel>Agent Card Location</PageLabel>
                    <div>{formData.agentCardLocation === "well_known" ? "Well-known URI" : formData.agentCardLocation === "custom" ? "Custom URL" : formData.agentCardLocation === "none" ? "No Agent Card" : "Not set"}</div>

                    {formData.agentCardUrl && (
                        <>
                            <PageLabel>Agent Card URL</PageLabel>
                            <div className="break-all">{formData.agentCardUrl}</div>
                        </>
                    )}

                    {hasAgentCard ? (
                        <>
                            <PageLabel>External Agent Name</PageLabel>
                            <div className={isLoadingAgentCard ? "text-(--secondary-text-wMain) italic" : ""}>{externalAgentName}</div>

                            <PageLabel>Solace Display Name</PageLabel>
                            <div>{solaceDisplayName}</div>
                        </>
                    ) : (
                        <>
                            <PageLabel>Agent Name</PageLabel>
                            <div>{agentName}</div>
                        </>
                    )}

                    <PageLabel>Description</PageLabel>
                    <div className="whitespace-pre-wrap">{agentDescription}</div>
                </div>
            </PageSection>

            <PageSection>
                <div className="text-lg font-semibold">Authentication</div>
                <div className="mb-4 text-sm text-(--secondary-text-wMain)">Task invocation authentication</div>

                <div className="grid grid-cols-[200px_1fr] gap-4">
                    <PageLabel>Authentication Type</PageLabel>
                    <div>{authTypeLabel}</div>

                    {additionalInfoFormData.taskAuthenticationType === "static_bearer" && (
                        <>
                            <PageLabel>Bearer Token</PageLabel>
                            <div className="font-mono text-sm">{maskSensitiveValue(additionalInfoFormData.taskAuthBearerToken)}</div>
                        </>
                    )}

                    {additionalInfoFormData.taskAuthenticationType === "static_apikey" && (
                        <>
                            <PageLabel>API Key</PageLabel>
                            <div className="font-mono text-sm">{maskSensitiveValue(additionalInfoFormData.taskAuthApikeyValue)}</div>
                        </>
                    )}

                    {additionalInfoFormData.taskAuthenticationType === "oauth2_client_credentials" && (
                        <>
                            <PageLabel>Token URL</PageLabel>
                            <div className="break-all">{formatValue(additionalInfoFormData.taskAuthOauth2TokenUrl)}</div>

                            <PageLabel>Client ID</PageLabel>
                            <div className="break-all">{formatValue(additionalInfoFormData.taskAuthOauth2ClientId)}</div>

                            <PageLabel>Client Secret</PageLabel>
                            <div className="font-mono text-sm">{maskSensitiveValue(additionalInfoFormData.taskAuthOauth2ClientSecret)}</div>

                            <PageLabel>Scope</PageLabel>
                            <div>{formatValue(additionalInfoFormData.taskAuthOauth2Scope) || "None"}</div>
                        </>
                    )}

                    {hasHeaders && (
                        <>
                            <PageLabel>Custom Headers</PageLabel>
                            <div className="rounded-sm border">
                                <table className="w-full">
                                    <thead>
                                        <tr className="border-b bg-(--secondary-w10)">
                                            <th className="px-4 py-2 text-left text-sm font-medium">Key</th>
                                            <th className="px-4 py-2 text-left text-sm font-medium">Value</th>
                                        </tr>
                                    </thead>
                                    <tbody>
                                        {(additionalInfoFormData.taskHeaders || [])
                                            .filter(h => h.key && h.value)
                                            .map((header, index) => (
                                                <tr key={index} className="border-b last:border-b-0">
                                                    <td className="px-4 py-2 text-sm">{header.key}</td>
                                                    <td className="px-4 py-2 text-sm">{header.value}</td>
                                                </tr>
                                            ))}
                                    </tbody>
                                </table>
                            </div>
                        </>
                    )}
                </div>
            </PageSection>

            <RemoteProtocolSection
                hasAgentCard={hasAgentCard}
                protocolVersion={additionalInfoFormData.protocolVersion}
                trustedCardJwksUrls={hasAgentCard ? parseTrustedKeySetUrls(additionalInfoFormData.trustedCardJwksUrls) : []}
                requireSignedCard={hasAgentCard && additionalInfoFormData.requireSignedCard}
                offered={useFetchedCard ? fetchedAgentCard.interfaces : undefined}
                status={remoteProtocol}
                showStatus={showDeployedStatus}
            />

            <PageSection>
                <div className="text-lg font-semibold">Agent Card</div>

                <div className="mt-4 grid grid-cols-[200px_1fr] gap-4">
                    <PageLabel>Abilities</PageLabel>
                    <div>
                        {displaySkills && displaySkills.length > 0 ? (
                            <div className="flex flex-col gap-2">
                                {displaySkills.map((skill, index) => (
                                    <AgentAbilityCard key={skill.name || index} ability={skill} className="w-full" />
                                ))}
                            </div>
                        ) : (
                            <div className="text-sm text-(--secondary-text-wMain)">No abilities configured</div>
                        )}
                    </div>
                </div>

                <div className="mt-6 grid grid-cols-[200px_1fr] gap-4">
                    <PageLabel>Communication Modes</PageLabel>
                    <div className="flex flex-col gap-4">
                        <AgentCommunicationMode label="Input Modes" modes={displayInputModes} />
                        <AgentCommunicationMode label="Output Modes" modes={displayOutputModes} />
                    </div>
                </div>
            </PageSection>
        </div>
    );
};
