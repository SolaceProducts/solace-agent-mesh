// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useGateways } from "@/api/gateways";

import { useChatContext, useUserContext } from "@/providers/contexts";

import { Button } from "@/ui/button";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/ui/dialog";

import { MessageBanner } from "@/common";
import type { DeploymentStatus, Gateway, GatewaySchema } from "@/types";

import { GatewayConfigurationRenderer } from "./common";
import { GatewayStatusIndicator } from "./GatewayStatusIndicator";
import { GatewayTypeChip } from "./GatewayTypeChip";
import { useGatewayDeployment } from "./hooks";

interface GatewayComparisonDialogProps {
    deployedGateway?: Gateway;
    draftGateway?: Gateway;
    schemaInfo: GatewaySchema;
    open: boolean;
    onClose: (e?: React.MouseEvent) => void;
}

export const GatewayComparisonDialog = ({ deployedGateway, draftGateway, schemaInfo, open, onClose }: GatewayComparisonDialogProps) => {
    const { addNotification, navigate } = useChatContext();
    const { refetch: gatewaysRefetch } = useGateways();
    const { userHasCapability, CAPABILITIES } = useUserContext();
    const canDeployGateways = userHasCapability(CAPABILITIES.deployGateways, true);
    const { isLoading, error, setError, deploy } = useGatewayDeployment();

    if (!deployedGateway || !draftGateway) {
        return null;
    }

    const onOpen = (e: React.MouseEvent) => {
        e.stopPropagation();
        onClose(e);
        if (draftGateway.id) {
            navigate(`/entrypoints/${draftGateway.id}`);
        }
    };

    const onDeploy = async (e: React.MouseEvent) => {
        e.stopPropagation();
        if (draftGateway?.id) {
            await deploy({
                gatewayId: draftGateway.id,
                action: "update",
                onSuccess: async () => {
                    setError(null);
                    addNotification("Update started.", "success");
                    onClose(e);
                    gatewaysRefetch();
                },
            });
        }
    };

    const getGatewayContent = (gateway: Gateway, isDeployed: boolean) => {
        const configValues = isDeployed && gateway.deployed ? gateway.deployed.values : gateway.values;
        const statusGateway = { ...gateway, deploymentStatus: isDeployed ? "deployed" : ("not_deployed" as DeploymentStatus) };

        return (
            <div className="flex-1 overflow-y-auto p-4">
                <div className="mb-4 flex flex-col gap-2 rounded-sm bg-(--secondary-w10) p-4">
                    <div className="line-clamp-3">{gateway.description ?? "N/A"}</div>
                    <div className="grid grid-cols-2 gap-4 p-4">
                        <div>
                            <div className="mb-2 text-sm font-medium text-(--secondary-text-wMain)">Status</div>
                            <GatewayStatusIndicator gateway={statusGateway} badgeClassName="" showConnectionStatus={false} />
                        </div>
                        <div>
                            <div className="mb-2 text-sm font-medium text-(--secondary-text-wMain)">Type</div>
                            <GatewayTypeChip type={gateway.type} badgeClassName="" />
                        </div>
                    </div>
                </div>

                <div className="py-2 font-semibold">Configuration</div>
                <div className="flex flex-col gap-2">
                    <GatewayConfigurationRenderer schemaInfo={schemaInfo} values={configValues} />
                </div>
            </div>
        );
    };

    return (
        <Dialog
            open={open}
            onOpenChange={isOpen => {
                if (!isOpen) onClose();
            }}
        >
            <DialogContent className="flex max-h-[80vh] flex-col sm:max-w-6xl">
                <DialogHeader>
                    <DialogTitle>Preview Updates</DialogTitle>
                </DialogHeader>
                {error && <MessageBanner variant="error" message={`Entrypoint Update Failed: ${error}`} dismissible onDismiss={() => setError(null)} />}

                <div className="grid min-h-0 flex-1 grid-cols-2 gap-0">
                    <div className="flex min-h-0 flex-col border-r">
                        <div className="flex justify-between gap-2 border-b px-4 py-3">
                            <div className="truncate font-semibold" title={deployedGateway.name}>
                                {deployedGateway.name}
                            </div>
                            <div className="text-(--info-wMain)">Deployed</div>
                        </div>
                        {getGatewayContent(deployedGateway, true)}
                    </div>

                    <div className="flex min-h-0 flex-col">
                        <div className="flex justify-between gap-2 border-b px-4 py-3">
                            <div className="truncate font-semibold" title={draftGateway.name}>
                                {draftGateway.name}
                            </div>
                            <div className="text-(--info-wMain)">Not Deployed</div>
                        </div>
                        {getGatewayContent(draftGateway, false)}
                    </div>
                </div>

                <div className="flex justify-end gap-2 pt-6">
                    <Button variant="ghost" testid="closeButton" title="Close" onClick={onClose}>
                        Close
                    </Button>
                    <Button variant="outline" testid="openGateway" title="Open" onClick={onOpen} disabled={isLoading}>
                        Open Entrypoint
                    </Button>
                    {canDeployGateways && (
                        <Button testid="deployUpdates" title="Deploy Updates" onClick={onDeploy} disabled={isLoading}>
                            Deploy Updates
                        </Button>
                    )}
                </div>
            </DialogContent>
        </Dialog>
    );
};
