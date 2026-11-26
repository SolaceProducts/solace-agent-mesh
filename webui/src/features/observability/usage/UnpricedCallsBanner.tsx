// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { Button } from "@/ui/button";

import { MessageBanner } from "@/common";

import { formatCount } from "../feedbackFormat";

interface UnpricedCallsBannerProps {
    unpricedCalls: number;
    totalCalls: number;
    onGoToModels?: () => void;
}

export function UnpricedCallsBanner({ unpricedCalls, totalCalls, onGoToModels }: Readonly<UnpricedCallsBannerProps>) {
    if (unpricedCalls <= 0) return null;
    const text = `${formatCount(unpricedCalls)} out of ${formatCount(totalCalls)} ${totalCalls === 1 ? "call" : "calls"} in the selected period used a model with no price set. ${unpricedCalls === 1 ? "Its" : "Their"} cost is not included.`;
    return (
        <MessageBanner
            variant="info"
            message={
                <span className="flex flex-1 flex-wrap items-center justify-between gap-x-4 gap-y-2">
                    <span>{text}</span>
                    {onGoToModels && (
                        <Button variant="ghost" size="sm" className="h-auto p-0" onClick={onGoToModels}>
                            Go to Models
                        </Button>
                    )}
                </span>
            }
        />
    );
}
