// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { Plus, RefreshCcw } from "lucide-react";

import { useGateways } from "@/api/gateways";

import { useUserContext } from "@/providers/contexts";

import { Button } from "@/ui/button";

import { Header } from "@/common";

import { GatewaysAvailableDialog } from "./GatewaysAvailableDialog";
import { GatewaysView } from "./GatewaysView";

export function GatewaysPage() {
    const { refetch: gatewaysRefetch } = useGateways();
    const { userHasCapability, CAPABILITIES } = useUserContext();
    const userCanCreateGateways = userHasCapability(CAPABILITIES.createGateways);

    const buttons = [];
    if (userCanCreateGateways) {
        buttons.push(
            <GatewaysAvailableDialog
                key="create-gateway"
                triggerButton={
                    <Button variant="outline" testid="createGateway" tooltip="Create Entrypoint">
                        <Plus />
                        Create<span className="hidden sm:inline">Entrypoint</span>
                    </Button>
                }
            />
        );
    }
    buttons.push(
        <Button key="refresh" variant="ghost" title="Refresh Entrypoints" tooltip="Refresh Entrypoints" onClick={() => gatewaysRefetch()}>
            <RefreshCcw />
            <span className="hidden sm:inline">Refresh</span>
        </Button>
    );

    return (
        <div className="flex h-full w-full flex-col overflow-x-auto">
            <div className="flex h-full min-w-0 flex-col">
                <Header title="Entrypoints" buttons={buttons} />
                <div className="relative min-h-0 flex-1">
                    <GatewaysView />
                </div>
            </div>
        </div>
    );
}
