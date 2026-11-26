// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { Plus, RefreshCcw } from "lucide-react";

import { useCustomToolsets } from "@/api/toolsets";

import { useChatContext, useUserContext } from "@/providers/contexts";

import { Button } from "@/ui/button";

import { Header } from "@/common";

import { ToolsetsView } from "./ToolsetsView";

export function ToolsetsPage() {
    const { handleCreateCustomToolset } = useChatContext();
    const { refetch: customToolsetsRefetch } = useCustomToolsets();
    const { userHasCapability, CAPABILITIES } = useUserContext();
    const userCanCreateToolsets = userHasCapability(CAPABILITIES.createToolsets);

    const buttons = [];
    if (userCanCreateToolsets) {
        buttons.push(
            <Button key="add-toolset" variant="outline" onClick={handleCreateCustomToolset}>
                <Plus />
                Create Toolset
            </Button>
        );
    }
    buttons.push(
        <Button
            key="refresh-toolsets"
            variant="ghost"
            title="Refresh Toolsets"
            onClick={() => {
                customToolsetsRefetch();
                window.dispatchEvent(new CustomEvent("toolsets-refetch"));
            }}
        >
            <RefreshCcw />
            Refresh
        </Button>
    );

    return (
        <div className="flex h-full w-full flex-col overflow-x-auto">
            <div className="flex h-full min-w-0 flex-col">
                <Header title="Toolsets" buttons={buttons} />
                <div className="relative min-h-0 flex-1">
                    <ToolsetsView />
                </div>
            </div>
        </div>
    );
}
