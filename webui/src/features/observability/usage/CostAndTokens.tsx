// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { Fragment } from "react";

import type { UsageMoney, UsageTokens } from "@/api/usage";

import { formatCompactTokens } from "@/common/usage";

import { formatUsageMoney, totalTokens } from "@/utils";

import { tokenBreakdownParts } from "./usageFormat";

export function CostAndTokens({ cost, tokens }: Readonly<{ cost?: UsageMoney; tokens: UsageTokens }>) {
    return (
        <div className="flex flex-col">
            {cost !== undefined && (
                <span>
                    <span className="font-semibold">{formatUsageMoney(cost)}</span> token cost
                </span>
            )}
            <span>
                <span className="font-semibold">{formatCompactTokens(totalTokens(tokens))}</span> tokens
            </span>
            <span className="text-xs">
                {tokenBreakdownParts(tokens).map(({ label, value }, index) => (
                    <Fragment key={label}>
                        {index > 0 && " • "}
                        {label} <span className="font-semibold">{value}</span>
                    </Fragment>
                ))}
            </span>
        </div>
    );
}
