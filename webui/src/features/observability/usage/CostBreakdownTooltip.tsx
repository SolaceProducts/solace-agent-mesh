// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { formatUsageMoneyWithFloor } from "@/utils";

import { type OutsideSpendConfig, WORKFLOW_OVERLAP_HELP } from "./usageKinds";
import { outsideSpend } from "./usageSpenders";

const COVERS_ALL = "This breakdown covers all of Agent Mesh Total Cost.";

interface CostBreakdownTooltipProps {
    config: OutsideSpendConfig;
    listCost: string;
    totalCost: string;
    rowsOverlap: boolean;
}

export function CostBreakdownTooltip({ config, listCost, totalCost, rowsOverlap }: Readonly<CostBreakdownTooltipProps>) {
    const outsideCost = outsideSpend(totalCost, listCost);
    return (
        <div className="flex flex-col gap-2">
            {outsideCost ? <p>{config.leftOut(formatUsageMoneyWithFloor(outsideCost, 2))}</p> : <p>{COVERS_ALL}</p>}
            {rowsOverlap && <p>{WORKFLOW_OVERLAP_HELP}</p>}
        </div>
    );
}
