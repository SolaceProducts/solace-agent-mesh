// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useMemo, useState } from "react";
import { ExternalLink, FileSliders, Network, Workflow } from "lucide-react";

import { getSectionTitle, PAGE_COMMON_CLASSES, PageLabel, PageSection } from "@/common/layout";

import { matchesConditionalDisplay, valuesWithSchemaDefaults } from "@/utils/conditionalDisplay";

import { MessageBanner, Metadata } from "@/common";
import type { EventRule, Gateway, GatewayFieldSchema, GatewayNetworkingDetails, GatewaySchema } from "@/types";
import { cn } from "@/utils";

import { GatewayApiPathList, GatewayConfigurationRenderer } from "./common";
import { EventRuleDialog } from "./EventRuleDialog";
import { GatewayDisplayField } from "./GatewayDisplayField";
import { GatewayStatusIndicator } from "./GatewayStatusIndicator";
import { GatewayTypeChip } from "./GatewayTypeChip";
import { useAgentOptions } from "./hooks";
import { TeamsWebhookUrlSection } from "./TeamsWebhookUrlSection";
import { resolveEntrypointEndpointUrl, resolveGatewayBaseUrl, TEAMS_CONFIG_DOCS_URL, WEBHOOK_COPIED_MESSAGE } from "./utils/teams";

interface NetworkingInfo {
    networkingDetails: GatewayNetworkingDetails | null;
    isLoading: boolean;
    error: string | null;
}

interface GatewayViewProps {
    gateway: Gateway;
    schemaInfo: GatewaySchema;
    networkingInfo?: NetworkingInfo;
    addNotification?: (message: string, type?: "success" | "info") => void;
}

const NetworkingDetailsContent = ({ networkingInfo, gateway, addNotification }: Pick<GatewayViewProps, "networkingInfo" | "gateway" | "addNotification">) => {
    if (networkingInfo?.error) return <MessageBanner variant="error" message={networkingInfo.error} />;
    if (networkingInfo?.isLoading) return <div className="text-sm text-(--secondary-w40)">Loading...</div>;
    if (!networkingInfo?.networkingDetails) return null;

    const { networkingDetails } = networkingInfo;
    return (
        <div className="flex flex-col gap-6">
            {gateway.type === "teams" ? (
                <div className="flex flex-col gap-4">
                    <PageLabel>Webhook URL</PageLabel>
                    <TeamsWebhookUrlSection webhookUrl={resolveEntrypointEndpointUrl(gateway.type, networkingDetails)} onCopy={() => addNotification?.(WEBHOOK_COPIED_MESSAGE, "success")} />
                    <a href={TEAMS_CONFIG_DOCS_URL} target="_blank" rel="noopener noreferrer" className="flex items-center gap-1 text-sm font-semibold text-(--primary-wMain) hover:underline">
                        Learn about Teams configuration <ExternalLink className="size-4" />
                    </a>
                </div>
            ) : (
                <GatewayDisplayField label="URL" value={resolveGatewayBaseUrl(networkingDetails.url)} />
            )}
            <GatewayApiPathList baseUrl={networkingDetails.url} apiPaths={networkingDetails.apiPaths} />
        </div>
    );
};

export const GatewayView = ({ gateway, schemaInfo, networkingInfo, addNotification }: GatewayViewProps) => {
    const agents = useAgentOptions();
    const [viewDialogOpen, setViewDialogOpen] = useState(false);
    const [viewingRule, setViewingRule] = useState<EventRule | null>(null);

    const handleViewRule = (rule: EventRule) => {
        setViewingRule(rule);
        setViewDialogOpen(true);
    };

    const effectiveValues = useMemo(() => valuesWithSchemaDefaults(gateway.values, schemaInfo?.schema), [gateway.values, schemaInfo?.schema]);
    const shouldDisplayField = (fieldConfig: GatewayFieldSchema): boolean => matchesConditionalDisplay(fieldConfig.conditionalDisplay, effectiveValues);

    return (
        <div className={cn(PAGE_COMMON_CLASSES, "flex flex-1 flex-col")}>
            {getSectionTitle(Workflow, "Entrypoint Details")}
            <PageSection className="mb-8 gap-4 py-4">
                <div className="grid grid-cols-[100px_1fr] gap-4">
                    <PageLabel>Name</PageLabel>
                    <div className="flex items-center" data-testid="gatewayDetailName">
                        {gateway.name}
                    </div>
                    <PageLabel>Description</PageLabel>
                    <div className="flex items-center" data-testid="gatewayDetailDescription">
                        {gateway.description}
                    </div>
                    <PageLabel>Type</PageLabel>
                    <div className="flex items-center">
                        <GatewayTypeChip type={gateway.type} />
                    </div>
                    <PageLabel>Status</PageLabel>
                    <div className="flex items-center gap-2">
                        <GatewayStatusIndicator gateway={gateway} />
                    </div>
                </div>
            </PageSection>

            {getSectionTitle(FileSliders, "Entrypoint Configuration")}
            <PageSection className="mb-4 gap-4 py-4">
                <GatewayConfigurationRenderer schemaInfo={schemaInfo} values={gateway.values} shouldDisplayField={shouldDisplayField} onViewRule={handleViewRule} />
            </PageSection>

            {networkingInfo && (
                <>
                    {networkingInfo.error || networkingInfo.isLoading || networkingInfo.networkingDetails ? <>{getSectionTitle(Network, "Networking Details")}</> : null}
                    <PageSection className="mb-8 gap-4 py-4">
                        <NetworkingDetailsContent networkingInfo={networkingInfo} gateway={gateway} addNotification={addNotification} />
                    </PageSection>
                </>
            )}

            <Metadata
                className="mt-auto pt-8"
                metadata={{
                    id: gateway.id || "",
                    createdBy: gateway.createdBy || "",
                    createdAt: gateway.createdAt || "",
                    updatedBy: gateway.updatedBy || "",
                    updatedAt: gateway.updatedAt || "",
                }}
            />

            <EventRuleDialog open={viewDialogOpen} onClose={() => setViewDialogOpen(false)} onSave={() => {}} initialRule={viewingRule} agents={agents} existingNames={[]} readOnly={true} />
        </div>
    );
};
