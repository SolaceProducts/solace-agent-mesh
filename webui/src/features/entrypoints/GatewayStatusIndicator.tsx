// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useMemo } from "react";

import { HealthProblemsIndicator, STATUS_COLOUR_MAP, STATUS_LABEL_MAP } from "@/common/status";

import { Badge } from "@/ui/badge";

import type { Gateway } from "@/types";

interface GatewayStatusIndicatorProps {
    gateway: Gateway;
    badgeClassName?: string;
    showConnectionStatus?: boolean;
}

export const GatewayStatusIndicator = ({ gateway, badgeClassName, showConnectionStatus = true }: GatewayStatusIndicatorProps) => {
    return useMemo(() => {
        const isDeployed = gateway.deploymentStatus === "deployed";
        const displayBadge = showConnectionStatus && isDeployed;
        const displayedStatus = gateway.deploymentStatus || "unknown";

        let runtimeBadgeLabel = "Inactive";
        if (gateway.runtimeStatus?.status === "running") runtimeBadgeLabel = "Running";
        else if (gateway.runtimeStatus?.status === "degraded") runtimeBadgeLabel = "Degraded";

        return (
            <div className="flex items-center gap-2">
                <div className={`h-2 w-2 rounded-full ${STATUS_COLOUR_MAP[displayedStatus]}`}></div>
                <div>{STATUS_LABEL_MAP[displayedStatus]}</div>
                {displayBadge && <Badge className={badgeClassName}>{runtimeBadgeLabel}</Badge>}
                {isDeployed && <HealthProblemsIndicator problems={gateway.runtimeStatus?.problems} testIdPrefix="gatewayHealth" />}
            </div>
        );
    }, [gateway, badgeClassName, showConnectionStatus]);
};
