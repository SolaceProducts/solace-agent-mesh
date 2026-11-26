// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { Loader2 } from "lucide-react";

interface DiscoveryIndicatorProps {
    status?: string;
    isBuiltin?: boolean;
    showReady?: boolean;
    testIdPrefix?: string;
    inProgressLabel?: string;
}

export const DiscoveryIndicator = ({ status, isBuiltin = false, showReady = false, testIdPrefix = "discoveryStatus", inProgressLabel = "In Progress" }: DiscoveryIndicatorProps) => {
    if (isBuiltin || !status) {
        return null;
    }

    if (status === "ready") {
        if (!showReady) return null;
        return (
            <span data-testid={`${testIdPrefix}-ready`} className="inline-flex items-center gap-1.5 self-start rounded-full bg-(--success-wMain)/30 px-2 py-0.5 text-xs font-semibold text-(--success-wMain)">
                Ready
            </span>
        );
    }

    if (status === "failed" || status === "removed") {
        return (
            <span data-testid={`${testIdPrefix}-failed`} className="inline-flex items-center gap-1.5 self-start rounded-full bg-(--error-wMain)/30 px-2 py-0.5 text-xs font-semibold text-(--error-wMain)">
                Failed
            </span>
        );
    }

    return (
        <div data-testid={`${testIdPrefix}-inProgress`} className="flex items-center gap-1.5">
            <Loader2 className="h-4 w-4 animate-spin text-(--success-wMain)" />
            <span className="text-sm">{inProgressLabel}</span>
        </div>
    );
};
