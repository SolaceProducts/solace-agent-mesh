// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useQueryClient } from "@tanstack/react-query";
import { RefreshCcw } from "lucide-react";

import { connectorKeys } from "@/api/connectors";

import { useUserContext } from "@/providers/contexts";

import { Button } from "@/ui/button";

import { Header } from "@/common";

import { ConnectorsAvailableDialog } from "./ConnectorsAvailableDialog";
import { ConnectorsView } from "./ConnectorsView";

export function ConnectorsPage() {
    const queryClient = useQueryClient();
    const { userHasCapability, CAPABILITIES } = useUserContext();
    const userCanCreateConnectors = userHasCapability(CAPABILITIES.createConnectors);

    const buttons = [];
    if (userCanCreateConnectors) {
        buttons.push(<ConnectorsAvailableDialog key="connectors-dialog" />);
    }
    const handleRefresh = () => {
        queryClient.invalidateQueries({ queryKey: connectorKeys.lists() });
    };
    buttons.push(
        <Button key="refresh" variant="ghost" title="Refresh Connectors" onClick={handleRefresh}>
            <RefreshCcw />
            Refresh
        </Button>
    );

    return (
        <div className="flex h-full w-full flex-col overflow-x-auto">
            <div className="flex h-full min-w-0 flex-col">
                <Header title="Connectors" buttons={buttons} />
                <div className="relative min-h-0 flex-1">
                    <ConnectorsView />
                </div>
            </div>
        </div>
    );
}
