// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useLocalStorage } from "@/hooks/useLocalStorage";

function simpleHash(str: string): string {
    let hash = 0;
    for (let i = 0; i < str.length; i++) {
        const char = str.charCodeAt(i);
        hash = (hash << 5) - hash + char;
        hash = hash & hash;
    }
    return Math.abs(hash).toString(36);
}

export function generateDuplicateSignature(agentIds: string[]): string {
    if (!agentIds || agentIds.length === 0) {
        return "no-agents";
    }

    const sorted = [...agentIds].sort();
    const combined = sorted.join(":");
    return simpleHash(combined).substring(0, 8);
}

export function createDismissalStorageKey(agentName: string | null | undefined, duplicateAgentIds: string[]): string {
    const signature = generateDuplicateSignature(duplicateAgentIds);
    return `sam-duplicate-banner-dismissed-${agentName ?? "unknown"}-${signature}`;
}

function generateCombinedDuplicateSignature(duplicateGroups: Array<{ name: string; duplicateAgentIds: string[] }>): string {
    if (!duplicateGroups || duplicateGroups.length === 0) {
        return "no-duplicates";
    }

    const sortedGroups = [...duplicateGroups].sort((a, b) => a.name.localeCompare(b.name));

    const groupSignatures = sortedGroups.map(group => {
        const signature = generateDuplicateSignature(group.duplicateAgentIds);
        return `${group.name}:${signature}`;
    });

    const combined = groupSignatures.join("|");
    return simpleHash(combined).substring(0, 8);
}

export function createCombinedDismissalStorageKey(duplicateGroups: Array<{ name: string; duplicateAgentIds: string[] }>): string {
    const signature = generateCombinedDuplicateSignature(duplicateGroups);
    return `sam-duplicate-banner-dismissed-combined-${signature}`;
}

export function useDuplicateBannerDismissal(
    agentName: string | null | undefined,
    duplicateAgentIds: string[]
): {
    isDismissed: boolean;
    dismiss: () => void;
} {
    const storageKey = createDismissalStorageKey(agentName, duplicateAgentIds);

    const [isDismissed, setIsDismissed] = useLocalStorage(storageKey, false);

    const dismiss = () => {
        setIsDismissed(true);
    };

    return {
        isDismissed,
        dismiss,
    };
}

export function useCombinedDuplicateBannerDismissal(duplicateGroups: Array<{ name: string; duplicateAgentIds: string[] }>): {
    isDismissed: boolean;
    dismiss: () => void;
} {
    const storageKey = createCombinedDismissalStorageKey(duplicateGroups);

    const [isDismissed, setIsDismissed] = useLocalStorage(storageKey, false);

    const dismiss = () => {
        setIsDismissed(true);
    };

    return {
        isDismissed,
        dismiss,
    };
}
