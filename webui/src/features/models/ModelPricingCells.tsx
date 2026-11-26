// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";

import type { ModelPricing } from "@/api/models/types";

import { HintedText } from "@/common";
import { TableCell } from "@/ui";

import { rateDisplay, REQUIRED_RATES } from "./modelPricing";

function cacheRatesHint(pricing: ModelPricing | null | undefined): string | null {
    const cacheRead = rateDisplay(pricing, "cacheRead");
    const cacheWrite = rateDisplay(pricing, "cacheWrite");
    if (!cacheRead || !cacheWrite) return null;
    if (cacheRead.resolved && cacheWrite.resolved) return "Cache read and cache write default to the input price.";
    return `Cache read: ${cacheRead.amount}\nCache write: ${cacheWrite.amount}`;
}

export const ModelPricingCells: React.FC<{ pricing: ModelPricing | null | undefined; offersCacheRates?: boolean }> = ({ pricing, offersCacheRates = true }) => (
    <>
        {REQUIRED_RATES.map(rate => {
            const display = rateDisplay(pricing, rate.key);

            if (!display) {
                return (
                    <TableCell key={rate.key} className="text-right">
                        —<span className="sr-only">Not priced</span>
                    </TableCell>
                );
            }

            const hint = rate.key === "input" && offersCacheRates ? cacheRatesHint(pricing) : null;
            return (
                <TableCell key={rate.key} className="text-right">
                    {hint ? <HintedText hint={hint}>{display.amount}</HintedText> : display.amount}
                </TableCell>
            );
        })}
    </>
);
