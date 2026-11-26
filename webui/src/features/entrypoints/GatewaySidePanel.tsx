// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useMemo, useState } from "react";
import { ExternalLink, Info, X } from "lucide-react";

import { useChatContext, useUserContext } from "@/providers/contexts";

import { PageLabel } from "@/common/layout";

import { getEntityDeploymentActions, isEntityErrorState } from "@/utils/deployment";

import { Button } from "@/ui/button";
import type { Gateway } from "@/types/models";

import { ErrorDialog, MessageBanner, Metadata } from "@/common";
import { DOC_LINKS } from "@/utils";

import { GatewayComparisonDialog } from "./GatewayComparisonDialog";
import { GatewayDeploymentDialog } from "./GatewayDeploymentDialog";
import { GatewayStatusIndicator } from "./GatewayStatusIndicator";
import { GatewayTypeChip } from "./GatewayTypeChip";
import { useGatewayDeployment, useGatewayNetworkingDetails } from "./hooks";
import { TeamsWebhookUrlSection } from "./TeamsWebhookUrlSection";
import { GATEWAY_ENDPOINT_COPIED_MESSAGE, resolveEntrypointEndpointUrl, TEAMS_CONFIG_DOCS_URL } from "./utils/teams";
import { WHATSAPP_CONFIG_DOCS_URL } from "./utils/whatsapp";

const ENDPOINT_DOCS: Record<string, { label: string; url: string }> = {
    teams: { label: "Learn about Teams configuration", url: TEAMS_CONFIG_DOCS_URL },
    whatsapp: { label: "Learn about WhatsApp configuration", url: WHATSAPP_CONFIG_DOCS_URL },
    google_chat: { label: "Learn about Google Chat configuration", url: DOC_LINKS.buildingEntrypointsGoogleChat },
};

interface GatewaySidePanelProps {
    gateway: Gateway;
    onClose?: () => void;
    onUpdate?: () => void;
}

export const GatewaySidePanel = ({ gateway, onClose, onUpdate }: GatewaySidePanelProps) => {
    const { navigate, addNotification, getGatewaySchema } = useChatContext();
    const { userHasCapability, CAPABILITIES } = useUserContext();
    const { networkingDetails } = useGatewayNetworkingDetails(gateway.id);
    const { deploy, error, setError } = useGatewayDeployment();

    const canDeployGateways = userHasCapability(CAPABILITIES.deployGateways);
    const canUpdateGateways = userHasCapability(CAPABILITIES.updateGateways);

    const [deploymentDialogOpen, setDeploymentDialogOpen] = useState<boolean>(false);
    const [deploymentAction, setDeploymentAction] = useState<"deploy" | "undeploy" | "update" | null>(null);
    const [isComparisonOpen, setIsComparisonOpen] = useState(false);

    const schemaInfo = useMemo(() => getGatewaySchema(gateway.type), [gateway.type, getGatewaySchema]);

    const isErrorState = isEntityErrorState(gateway);
    const isOutOfSync = gateway.syncStatus === "out_of_sync";
    const showOOSInfo = isOutOfSync && canUpdateGateways;
    const previewButtonLabel = "Preview and Deploy";
    const deployActions = useMemo(() => (canDeployGateways ? getEntityDeploymentActions(gateway) : []), [canDeployGateways, gateway]);
    const primaryAction = deployActions[0];

    const handleViewDetails = useCallback(() => {
        navigate(`/entrypoints/${gateway.id}`);
    }, [gateway.id, navigate]);

    const handleViewUpdate = useCallback(() => {
        setIsComparisonOpen(true);
    }, []);

    const openDeploymentDialog = useCallback((action: "deploy" | "undeploy" | "update") => {
        setDeploymentAction(action);
        setDeploymentDialogOpen(true);
    }, []);

    const handleDeploy = useCallback(async () => {
        if (gateway?.id && deploymentAction) {
            const successLabel = deploymentAction === "deploy" ? "Deployed" : deploymentAction === "undeploy" ? "Undeployed" : "Updated";

            await deploy({
                gatewayId: gateway.id,
                action: deploymentAction,
                onSuccess: async () => {
                    addNotification(`${successLabel} successfully.`, "success");
                    setDeploymentDialogOpen(false);
                    onUpdate?.();
                },
            });
        }
    }, [gateway, deploymentAction, deploy, addNotification, onUpdate]);

    const warningMessage = useMemo(() => {
        if (isErrorState && showOOSInfo) {
            if (gateway.deploymentStatus === "deploy_failed") return `A previous deployment failed for ${gateway.name}. There are also undeployed changes.`;
            return `${gateway.name} is deployed but not running. There are also undeployed changes.`;
        }
        if (isErrorState) {
            if (gateway.deploymentStatus === "deploy_failed") return `A previous deployment failed for ${gateway.name}.`;
            return `${gateway.name} is deployed but not running. You can redeploy to restart it.`;
        }
        if (showOOSInfo) return `There are undeployed changes for ${gateway.name}. Please review and deploy these updates.`;
        return null;
    }, [isErrorState, showOOSInfo, gateway]);

    return gateway ? (
        <div className="flex h-full w-full flex-col border-l bg-(--background-w10)">
            <div className="flex h-16 items-center justify-between gap-2 border-b px-4">
                <div className="truncate text-xl font-semibold" title={gateway.name}>
                    {gateway.name}
                </div>
                <div className="flex gap-2">
                    <Button variant="ghost" tooltip="Close" onClick={onClose}>
                        <X />
                    </Button>
                </div>
            </div>

            <div className="flex flex-1 flex-col overflow-y-auto p-4">
                <div className="flex-grow">
                    {warningMessage && (
                        <div className="mb-4 pb-2">
                            <MessageBanner variant={isErrorState ? "warning" : "info"} message={warningMessage} />
                        </div>
                    )}

                    <div className="mb-4 flex flex-col gap-2 rounded-sm bg-(--secondary-w10) p-4">
                        <div className="text-base font-semibold">Entrypoint Details</div>
                        <div className="line-clamp-3">{gateway.description || "No description provided"}</div>

                        <div className="grid grid-cols-2 gap-4 p-4">
                            <div>
                                <PageLabel className="mb-2">Status</PageLabel>
                                {gateway.deploymentStatus ? <GatewayStatusIndicator gateway={gateway} badgeClassName="" showConnectionStatus={false} /> : <span>Not Deployed</span>}
                            </div>
                            <div>
                                <PageLabel className="mb-2">Type</PageLabel>
                                <GatewayTypeChip type={gateway.type} badgeClassName="" />
                            </div>
                        </div>

                        <div className="flex flex-col gap-2">
                            {canDeployGateways && showOOSInfo && (
                                <Button data-testid="previewUpdates" title={previewButtonLabel} onClick={handleViewUpdate}>
                                    {previewButtonLabel}
                                </Button>
                            )}

                            {canDeployGateways && !showOOSInfo && primaryAction && (
                                <Button data-testid={`${primaryAction.action}Gateway`} title={primaryAction.label} onClick={() => openDeploymentDialog(primaryAction.action)}>
                                    {primaryAction.label}
                                </Button>
                            )}

                            <Button data-testid="openGateway" title="Open Entrypoint" variant="outline" onClick={handleViewDetails}>
                                Open Entrypoint
                            </Button>
                        </div>
                    </div>

                    {gateway.type in ENDPOINT_DOCS && networkingDetails?.url && (
                        <div className="mb-4 flex flex-col gap-3">
                            <div className="border-t border-(--secondary-w20) py-2" />

                            <div className="flex items-center gap-2">
                                <Info className="size-6 text-(--secondary-text-wMain)" />
                                <span className="text-base leading-[150%] font-semibold">Entrypoint Endpoint</span>
                            </div>

                            {gateway.type === "whatsapp" && gateway.deploymentStatus !== "deployed" && (
                                <p className="text-sm leading-[150%]">To complete setup, deploy the entrypoint to enable the webhook URL, then paste it in Meta's WhatsApp configuration.</p>
                            )}

                            <TeamsWebhookUrlSection webhookUrl={resolveEntrypointEndpointUrl(gateway.type, networkingDetails)} onCopy={() => addNotification(GATEWAY_ENDPOINT_COPIED_MESSAGE, "success")} className="mb-3 bg-(--darkSurface-wMain)" />

                            {ENDPOINT_DOCS[gateway.type] && (
                                <a href={ENDPOINT_DOCS[gateway.type].url} target="_blank" rel="noopener noreferrer" className="flex items-center gap-1 text-sm leading-[150%] font-semibold text-(--primary-wMain) hover:underline">
                                    {ENDPOINT_DOCS[gateway.type].label} <ExternalLink className="size-4 text-(--primary-wMain)" />
                                </a>
                            )}
                        </div>
                    )}
                </div>

                {gateway.id && <Metadata metadata={gateway} className="mt-auto" />}
            </div>

            {gateway && deploymentAction && (
                <GatewayDeploymentDialog
                    open={deploymentDialogOpen}
                    gatewayName={gateway.name}
                    gateway={gateway}
                    action={deploymentAction}
                    onConfirm={() => {
                        handleDeploy();
                    }}
                    onClose={() => setDeploymentDialogOpen(false)}
                />
            )}

            {isComparisonOpen && gateway.deployed && gateway.type && schemaInfo && (
                <GatewayComparisonDialog
                    deployedGateway={{
                        name: gateway.deployed?.name || gateway.name,
                        description: gateway.deployed?.description || gateway.description,
                        type: gateway.type,
                        values: gateway.deployed?.values || {},
                        deploymentStatus: "deployed",
                    }}
                    draftGateway={gateway}
                    schemaInfo={schemaInfo!}
                    open={isComparisonOpen}
                    onClose={() => setIsComparisonOpen(false)}
                />
            )}

            <ErrorDialog open={!!error} title="Entrypoint Deployment Error" error={error ?? ""} errorSource="api" onOpenChange={open => !open && setError(null)} />
        </div>
    ) : null;
};
