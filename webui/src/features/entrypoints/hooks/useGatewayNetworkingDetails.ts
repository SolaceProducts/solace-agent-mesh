// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useState } from "react";

import { gatewaysApi } from "@/api/gateways";

import { getErrorMessage } from "@/utils/api";

import type { GatewayNetworkingDetails } from "@/types/models";

interface UseGatewayNetworkingDetailsReturn {
    networkingDetails: GatewayNetworkingDetails | null;
    isLoading: boolean;
    error: string | null;
    refetch: () => void;
}

export const useGatewayNetworkingDetails = (gatewayId: string | undefined): UseGatewayNetworkingDetailsReturn => {
    const [networkingDetails, setNetworkingDetails] = useState<GatewayNetworkingDetails | null>(null);
    const [isLoading, setIsLoading] = useState(false);
    const [error, setError] = useState<string | null>(null);
    const [refetchTrigger, setRefetchTrigger] = useState(0);

    useEffect(() => {
        if (!gatewayId) return;

        let isCancelled = false;

        setIsLoading(true);
        setError(null);

        gatewaysApi
            .getNetworkingDetails(gatewayId)
            .then(details => {
                if (!isCancelled) {
                    setNetworkingDetails(details);
                    setError(null);
                }
            })
            .catch((err: unknown) => {
                if (!isCancelled) {
                    setError(getErrorMessage(err, "Failed to load networking details"));
                }
            })
            .finally(() => {
                if (!isCancelled) {
                    setIsLoading(false);
                }
            });

        return () => {
            isCancelled = true;
        };
    }, [gatewayId, refetchTrigger]);

    const refetch = useCallback(() => {
        setRefetchTrigger(prev => prev + 1);
    }, []);

    return { networkingDetails, isLoading, error, refetch };
};
