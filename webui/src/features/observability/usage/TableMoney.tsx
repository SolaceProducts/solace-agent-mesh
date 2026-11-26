// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { Tooltip, TooltipContent, TooltipTrigger } from "@/ui/tooltip";

import { formatExactUsageMoney, formatUsageMoneyWithFloor, isRoundedUsageMoney } from "@/utils";

const TABLE_MONEY_DECIMALS = 3;

export function TableMoney({ amount }: Readonly<{ amount: string }>) {
    const shown = formatUsageMoneyWithFloor(amount, TABLE_MONEY_DECIMALS);
    if (!isRoundedUsageMoney(amount, TABLE_MONEY_DECIMALS)) return shown;
    return (
        <Tooltip>
            <TooltipTrigger asChild>
                <span>{shown}</span>
            </TooltipTrigger>
            <TooltipContent>{formatExactUsageMoney(amount)}</TooltipContent>
        </Tooltip>
    );
}
