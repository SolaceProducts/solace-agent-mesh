// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useQuery } from "@tanstack/react-query";

import { capabilityKeys } from "./keys";
import { fetchCapabilities } from "./service";
import type { CapabilityStatus } from "./types";

export function useCapabilities() {
    return useQuery({
        queryKey: capabilityKeys.list(),
        queryFn: fetchCapabilities,
        retry: 0,
        staleTime: Infinity,
    });
}

export function useIsCapabilitySupported(key: string): boolean {
    const { data } = useCapabilities();
    if (!data) return true;
    return (data.find(c => c.key === key)?.status ?? "unavailable") !== "unavailable";
}

export function useCapabilityBadge(key: string): string | null {
    const { data } = useCapabilities();
    if (!data) return null;
    const status = data.find(c => c.key === key)?.status;
    return statusBadgeLabel(status);
}

export function findCapabilityBadge(capabilities: { key: string; status: CapabilityStatus }[] | undefined, key: string): string | null {
    if (!capabilities) return null;
    const status = capabilities.find(c => c.key === key)?.status;
    return statusBadgeLabel(status);
}

export function findConnectorBadge(capabilities: { key: string; status: CapabilityStatus }[] | undefined, type: string, experimental: boolean, subtype?: string): string | null {
    const subtypeBadge = subtype ? findCapabilityBadge(capabilities, `${type}_${subtype}_connector`) : null;
    return subtypeBadge ?? findCapabilityBadge(capabilities, `${type}_connector`) ?? (experimental ? "EXPERIMENTAL" : null);
}

export function findGatewayBadge(capabilities: { key: string; status: CapabilityStatus }[] | undefined, type: string): string | null {
    return findCapabilityBadge(capabilities, `${type}_gateway`);
}

function statusBadgeLabel(status: CapabilityStatus | undefined): string | null {
    switch (status) {
        case "experimental":
            return "EXPERIMENTAL";
        case "early_access":
            return "EARLY ACCESS";
        case "beta":
            return "BETA";
        default:
            return null;
    }
}
