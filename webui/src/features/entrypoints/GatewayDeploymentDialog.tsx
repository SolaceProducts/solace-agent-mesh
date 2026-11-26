// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect, useMemo, useState } from "react";
import { ExternalLink } from "lucide-react";

import { useChatContext, useConfigContext } from "@/providers/contexts";

import { isDeployFailed } from "@/utils/deployment";

import { Button } from "@/ui/button";
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@/ui/dialog";
import type { Gateway } from "@/types/models";

import { MessageBanner } from "@/common";
import { DOC_LINKS } from "@/utils";

import { googleChatPullsEvents } from "./customEndpoint";
import { googleChatWebhookUrl } from "./googleChat/endpoint";
import { TeamsWebhookUrlSection } from "./TeamsWebhookUrlSection";
import { DEPLOYMENT_TIME_MESSAGE, GATEWAY_ENDPOINT_COPIED_MESSAGE, TEAMS_CONFIG_DOCS_URL } from "./utils/teams";
import { WHATSAPP_CONFIG_DOCS_URL } from "./utils/whatsapp";

const BANNER_DISMISSED_KEY = "gatewayDeploymentBannerDismissed";

function GoogleChatDeployMessage({ gateway }: Readonly<{ gateway: Gateway }>) {
    const { googleChatEndpointBase } = useConfigContext();
    const { addNotification } = useChatContext();
    const webhookUrl = googleChatWebhookUrl(gateway, googleChatEndpointBase || globalThis.location.origin);
    return (
        <div className="flex flex-col gap-4 text-sm leading-relaxed text-(--primary-text-wMain)">
            <p>Ensure the webhook URL is configured in Google Chat before deploying.</p>
            <TeamsWebhookUrlSection webhookUrl={webhookUrl} onCopy={() => addNotification(GATEWAY_ENDPOINT_COPIED_MESSAGE, "success")} />
            <p>
                The entrypoint cannot receive messages until the webhook URL is configured in Google Chat.
                <br />
                <a href={DOC_LINKS.buildingEntrypointsGoogleChat} target="_blank" rel="noopener noreferrer" className="inline-flex items-center gap-1 font-semibold text-(--primary-wMain) hover:underline">
                    Learn about Google Chat configuration <ExternalLink className="size-3.5" aria-hidden="true" />
                </a>
            </p>
        </div>
    );
}

interface GatewayDeploymentDialogProps {
    open: boolean;
    gatewayName: string;
    gateway: Gateway;
    action: "deploy" | "undeploy" | "update";
    onConfirm: () => void;
    onClose: () => void;
}

export const GatewayDeploymentDialog = ({ open, gatewayName, gateway, action, onConfirm, onClose }: GatewayDeploymentDialogProps) => {
    const [isBannerDismissed, setIsBannerDismissed] = useState<boolean>(false);

    const isRecoveryScenario = useMemo(() => isDeployFailed(gateway.deploymentStatus), [gateway.deploymentStatus]);
    const isGoogleChatFirstDeploy = gateway.type === "google_chat" && action === "deploy" && !isRecoveryScenario && !googleChatPullsEvents(gateway.values);

    useEffect(() => {
        const stored = localStorage.getItem(BANNER_DISMISSED_KEY);
        if (stored === "true") {
            setIsBannerDismissed(true);
        }
    }, []);

    const handleDismissBanner = () => {
        localStorage.setItem(BANNER_DISMISSED_KEY, "true");
        setIsBannerDismissed(true);
    };

    const getTitle = () => {
        if (isRecoveryScenario && action === "update") {
            return `Redeploy ${gatewayName}`;
        }
        switch (action) {
            case "deploy":
                return `Deploy ${gatewayName}`;
            case "undeploy":
                return `Undeploy ${gatewayName}`;
            case "update":
                return `Deploy Updates for ${gatewayName}`;
        }
    };

    const getActionLabel = () => {
        switch (action) {
            case "deploy":
            case "update":
                return isRecoveryScenario ? "Redeploy" : "Deploy";
            case "undeploy":
                return "Undeploy";
        }
    };

    const getMessage = () => {
        if (isRecoveryScenario && action === "update") {
            return gateway.syncStatus === "out_of_sync" ? (
                <span>
                    Are you ready to redeploy <span className="font-semibold">{gatewayName}</span>? This will reinstall the entrypoint using the latest configuration, including any undeployed changes.
                </span>
            ) : (
                <span>
                    Are you ready to redeploy <span className="font-semibold">{gatewayName}</span>? This will reinstall the entrypoint using the latest configuration.
                </span>
            );
        }
        switch (action) {
            case "deploy":
                if (gateway.type === "teams") {
                    return (
                        <p className="text-sm leading-relaxed text-(--primary-text-wMain)">
                            {DEPLOYMENT_TIME_MESSAGE} After deployment, navigate to the entrypoint details to copy the entrypoint endpoint and configure it in Microsoft Teams.{" "}
                            <a href={TEAMS_CONFIG_DOCS_URL} target="_blank" rel="noopener noreferrer" className="inline-flex items-center gap-1 font-medium text-(--primary-wMain) hover:underline">
                                Learn more <ExternalLink className="size-3.5" aria-hidden="true" />
                            </a>
                        </p>
                    );
                }
                if (gateway.type === "whatsapp") {
                    return (
                        <p className="text-sm leading-relaxed text-(--primary-text-wMain)">
                            After deployment, the webhook URL must be pointed to Meta to enable external access.{" "}
                            <a href={WHATSAPP_CONFIG_DOCS_URL} target="_blank" rel="noopener noreferrer" className="inline-flex items-center gap-1 font-semibold text-(--primary-wMain) hover:underline">
                                Learn about WhatsApp configuration <ExternalLink className="size-3.5" aria-hidden="true" />
                            </a>
                        </p>
                    );
                }
                return (
                    <span>
                        Are you ready to deploy <span className="font-semibold">{gatewayName}</span>? This will make the entrypoint available for use.
                    </span>
                );
            case "undeploy":
                return gateway.syncStatus === "out_of_sync" ? (
                    <span>
                        Are you sure you want to undeploy <span className="font-semibold">{gatewayName}</span>? Any deployed configuration will be lost and only the draft configuration will remain.
                    </span>
                ) : (
                    <span>
                        Are you sure you want to undeploy <span className="font-semibold">{gatewayName}</span>?
                    </span>
                );
            case "update":
                return (
                    <span>
                        Are you ready to deploy updates for <span className="font-semibold">{gatewayName}</span>? This will update the running entrypoint with the latest changes.
                    </span>
                );
        }
    };

    return (
        <Dialog open={open} onOpenChange={isOpen => !isOpen && onClose()}>
            <DialogContent className="sm:max-w-[800px]">
                <DialogHeader className="pb-4">
                    <DialogTitle>{getTitle()}</DialogTitle>
                </DialogHeader>
                <div className="flex flex-col gap-4 pt-4">
                    {!isBannerDismissed && !isGoogleChatFirstDeploy && (
                        <MessageBanner variant="info" message="After submitting a deployment request, refresh your entrypoints to see changes in the entrypoint status." dismissible onDismiss={handleDismissBanner} />
                    )}
                    {isGoogleChatFirstDeploy ? <GoogleChatDeployMessage gateway={gateway} /> : getMessage()}
                </div>
                <DialogFooter>
                    <Button variant="ghost" onClick={onClose}>
                        {isGoogleChatFirstDeploy ? "Close" : "Cancel"}
                    </Button>
                    <Button variant="outline" onClick={onConfirm} data-testid="dialogConfirmButton">
                        {getActionLabel()}
                    </Button>
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
};
