// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useState } from "react";
import { BarChart3 } from "lucide-react";

import { isCollecting, isConsentWriteRefused, isKnownConsentState, useProductAnalyticsConsent, useUpdateProductAnalyticsConsent } from "@/api/product-analytics";

import { useIsProductAnalyticsEnabled } from "@/hooks/useIsProductAnalyticsEnabled";

import { useIsActivationGateEnabled } from "@/hooks";
import { Button, Label, Switch } from "@/ui";

const ANONYMOUS_COPY = "To let us improve your experience, we collect anonymous usage data about which features are opened and where errors occur. We never collect personal data, prompts, message content, or any sensitive information.";

const SOLACE_ID_COPY =
    "To let us improve your experience, we collect usage data about which features are opened and where errors occur. Until you accept the license agreement, it is anonymous. After that, it is linked to your Solace ID account and email address. Turning this off stops all collection; turning it on again links new data to the same account. We never collect prompts, message content, or any sensitive information.";

export const PrivacySettings = () => {
    const productAnalyticsEnabled = useIsProductAnalyticsEnabled();
    const signsInWithSolaceId = useIsActivationGateEnabled();
    const { data: consent, isError, isFetching, refetch } = useProductAnalyticsConsent({ enabled: productAnalyticsEnabled });
    const updateConsent = useUpdateProductAnalyticsConsent();
    const [writeRefused, setWriteRefused] = useState<boolean | null>(null);

    const collecting = isCollecting(consent);
    const stateUnknown = !isKnownConsentState(consent);

    const changeConsent = (next: boolean) => {
        setWriteRefused(null);
        updateConsent.mutate(next, {
            onError: error => {
                console.error("Failed to save usage analytics consent:", error);
                setWriteRefused(isConsentWriteRefused(error));
            },
        });
    };

    if (!productAnalyticsEnabled) return null;

    return (
        <div className="space-y-8">
            <p className="text-sm text-(--secondary-text-wMain)">{signsInWithSolaceId ? SOLACE_ID_COPY : ANONYMOUS_COPY}</p>

            <div className="flex items-center justify-between gap-4">
                <div className="flex items-center gap-2">
                    <BarChart3 className="size-5 shrink-0" />
                    <Label htmlFor="product-analytics-switch" className="font-semibold">
                        Usage analytics
                    </Label>
                </div>
                <Switch id="product-analytics-switch" checked={collecting} disabled={stateUnknown || writeRefused === true || updateConsent.isPending} onCheckedChange={changeConsent} aria-label="Toggle usage analytics" />
            </div>
            {isError && (
                <div className="flex items-center justify-between gap-4">
                    <p className="text-sm text-(--error-wMain)">{stateUnknown ? "Could not read your usage analytics setting." : "Could not confirm your usage analytics setting, so the value shown may be out of date."}</p>
                    <Button variant="outline" onClick={() => refetch()} disabled={isFetching}>
                        Retry
                    </Button>
                </div>
            )}
            {writeRefused !== null && <p className="text-sm text-(--error-wMain)">{writeRefused ? "You do not have permission to change this setting for this installation." : "Could not save your choice. Please try again."}</p>}
        </div>
    );
};
