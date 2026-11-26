// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect, useLayoutEffect } from "react";

import { isCollecting, useProductAnalyticsConsent } from "@/api/product-analytics";

import { useIsProductAnalyticsEnabled } from "@/hooks/useIsProductAnalyticsEnabled";

import { productAnalyticsClient } from "./client";
import { RouteChangeTracker } from "./RouteChangeTracker";

export function ProductAnalyticsGate() {
    const featureFlagOn = useIsProductAnalyticsEnabled();
    const { data: consent, error: consentError } = useProductAnalyticsConsent({ enabled: featureFlagOn });
    const collecting = isCollecting(consent);

    useEffect(() => {
        if (consentError) console.error("Failed to read the usage analytics consent state:", consentError);
    }, [consentError]);

    useLayoutEffect(() => {
        productAnalyticsClient.setFeatureEnabled(featureFlagOn);
    }, [featureFlagOn]);

    useLayoutEffect(() => {
        productAnalyticsClient.setConsent(collecting);
    }, [collecting]);

    useEffect(() => {
        if (collecting) productAnalyticsClient.trackOnce("app:launched");
    }, [collecting]);

    return null;
}

export function ProductAnalyticsPageViews() {
    const enabled = useIsProductAnalyticsEnabled();
    const { data: consent } = useProductAnalyticsConsent({ enabled });
    return enabled && isCollecting(consent) ? <RouteChangeTracker /> : null;
}
