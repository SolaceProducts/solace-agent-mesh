// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { ExternalLink, Network } from "lucide-react";

import { getSectionTitle, PageLabel, PageSection } from "@/common/layout";

import { MessageBanner } from "@/common";
import type { GatewayNetworkingDetails } from "@/types";

import { GatewayApiPathList } from "./common";
import { TeamsWebhookUrlSection } from "./TeamsWebhookUrlSection";
import { GATEWAY_ENDPOINT_COPIED_MESSAGE, resolveEntrypointEndpointUrl, TEAMS_CONFIG_DOCS_URL } from "./utils/teams";
import { WHATSAPP_CONFIG_DOCS_URL } from "./utils/whatsapp";

const CONFIG_DOCS: Record<string, { url: string; label: string }> = {
    teams: { url: TEAMS_CONFIG_DOCS_URL, label: "Learn about Teams configuration" },
    whatsapp: { url: WHATSAPP_CONFIG_DOCS_URL, label: "Learn more about WhatsApp configuration" },
};

interface NetworkingInfo {
    networkingDetails: GatewayNetworkingDetails | null;
    isLoading: boolean;
    error: string | null;
}

interface GatewayNetworkViewProps {
    networkingInfo: NetworkingInfo;
    gatewayType: string;
    addNotification?: (message: string, type?: "success" | "info") => void;
}

export const GatewayNetworkView = ({ networkingInfo, gatewayType, addNotification }: GatewayNetworkViewProps) => {
    const { networkingDetails, isLoading, error } = networkingInfo;

    if (!networkingDetails && !error && !isLoading) {
        return null;
    }

    const renderBody = () => {
        if (error) {
            return <MessageBanner variant="error" message={error} />;
        }
        if (isLoading) {
            return <div className="text-sm text-(--secondary-w40)">Loading...</div>;
        }
        if (!networkingDetails) {
            return null;
        }
        const configDocs = CONFIG_DOCS[gatewayType];
        const webhookUrl = resolveEntrypointEndpointUrl(gatewayType, networkingDetails);
        return (
            <div className="flex flex-col gap-6">
                <div className="flex flex-col gap-3">
                    <div className="flex flex-col gap-2">
                        <PageLabel>Entrypoint Endpoint</PageLabel>
                        <TeamsWebhookUrlSection webhookUrl={webhookUrl} onCopy={() => addNotification?.(GATEWAY_ENDPOINT_COPIED_MESSAGE, "success")} />
                    </div>

                    {configDocs && (
                        <a href={configDocs.url} target="_blank" rel="noopener noreferrer" className="flex items-center gap-1 text-sm font-semibold text-(--primary-wMain) hover:underline">
                            {configDocs.label} <ExternalLink className="size-4" aria-hidden="true" />
                        </a>
                    )}
                </div>

                <GatewayApiPathList baseUrl={networkingDetails.url} apiPaths={networkingDetails.apiPaths} />
            </div>
        );
    };

    return (
        <div className="flex max-w-[800px] flex-col gap-8 leading-[21px]">
            {getSectionTitle(Network, "Network Details")}

            <PageSection className="gap-6 py-4">{renderBody()}</PageSection>
        </div>
    );
};
