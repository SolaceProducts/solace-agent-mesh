// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useQueryClient } from "@tanstack/react-query";
import { Plus, RefreshCcw } from "lucide-react";

import { activationKeys } from "@/api/activation";

import { useChatContext, useUserContext } from "@/providers/contexts";

import { Button } from "@/ui/button";

import { Header } from "@/common";

import { ModelsView } from "./ModelsView";

export function ModelsPage() {
    const { navigate } = useChatContext();
    const { userHasCapability, CAPABILITIES } = useUserContext();
    const queryClient = useQueryClient();
    const userCanManageModels = userHasCapability(CAPABILITIES.createModelConfig);

    const refresh = () => {
        queryClient.invalidateQueries({ queryKey: ["models"] });
        queryClient.invalidateQueries({ queryKey: activationKeys.freeModel() });
    };

    const buttons = [];
    if (userCanManageModels) {
        buttons.push(
            <Button key="add-model" variant="outline" title="Add Model" onClick={() => navigate("/models/new/edit")}>
                <Plus />
                Add Model
            </Button>
        );
    }
    buttons.push(
        <Button key="refresh" variant="ghost" title="Refresh Models" onClick={refresh}>
            <RefreshCcw />
            Refresh
        </Button>
    );

    return (
        <div className="flex h-full w-full flex-col overflow-x-auto">
            <div className="flex h-full min-w-250 flex-col">
                <Header title="Models" buttons={buttons} />
                <div className="relative min-h-0 flex-1">
                    <ModelsView />
                </div>
            </div>
        </div>
    );
}
