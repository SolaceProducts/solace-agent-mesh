// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { productAnalyticsKeys } from "./keys";
import { getConsent, updateConsent } from "./service";
import type { ConsentResponse } from "./types";

const CONSENT_STALE_MS = 60_000;

export const CONSENT_CHANNEL_NAME = "product-analytics-consent";
const consentChannel = __SAM_PRODUCT_ANALYTICS__ && typeof BroadcastChannel !== "undefined" ? new BroadcastChannel(CONSENT_CHANNEL_NAME) : null;

let lastBroadcastAt = 0;

export function useProductAnalyticsConsent(options?: { enabled?: boolean }) {
    const queryClient = useQueryClient();

    useEffect(() => {
        if (!consentChannel) return;
        const onMessage = (event: MessageEvent<ConsentResponse>) => {
            lastBroadcastAt = Date.now();
            queryClient.setQueryData(productAnalyticsKeys.consent(), event.data);
        };
        consentChannel.addEventListener("message", onMessage);
        return () => consentChannel.removeEventListener("message", onMessage);
    }, [queryClient]);

    return useQuery({
        queryKey: productAnalyticsKeys.consent(),
        queryFn: async () => {
            const fetchStartedAt = Date.now();
            const consent = await getConsent();
            if (lastBroadcastAt >= fetchStartedAt) {
                return queryClient.getQueryData<ConsentResponse>(productAnalyticsKeys.consent()) ?? consent;
            }
            return consent;
        },
        enabled: options?.enabled ?? true,
        staleTime: CONSENT_STALE_MS,
    });
}

export function useUpdateProductAnalyticsConsent() {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: (granted: boolean) => updateConsent(granted),
        onSuccess: consent => {
            queryClient.setQueryData(productAnalyticsKeys.consent(), consent);
            consentChannel?.postMessage(consent);
        },
    });
}
