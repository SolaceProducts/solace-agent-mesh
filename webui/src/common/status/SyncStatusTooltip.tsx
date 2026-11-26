// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useMemo } from "react";
import { Info } from "lucide-react";

import { Tooltip, TooltipContent, TooltipTrigger } from "@/ui/tooltip";

export interface DeployableEntity {
    syncStatus?: string;
    deploymentStatus?: string;
    status?: string;
}

export const SyncStatusTooltip = ({ entity }: { entity?: DeployableEntity }) => {
    const badge = useMemo(() => {
        if (!entity) return null;

        if (entity.syncStatus !== "out_of_sync") {
            return null;
        }

        return (
            <Tooltip>
                <TooltipTrigger asChild className="text-(--secondary-text-wMain)">
                    <Info className="size-5" data-testid="syncStatusInfo" />
                </TooltipTrigger>
                <TooltipContent>Undeployed changes</TooltipContent>
            </Tooltip>
        );
    }, [entity]);

    return <>{badge}</>;
};
