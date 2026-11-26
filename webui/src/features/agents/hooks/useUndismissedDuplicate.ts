// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useMemo } from "react";

import { createCombinedDismissalStorageKey } from "./useDuplicateBannerDismissal";
import { useDuplicateDisplayName } from "./useDuplicateDisplayName";

type DuplicateGroup = { name: string; count: number; duplicateAgentIds: string[] };

export function selectUndismissedDuplicates(hasDuplicates: boolean, allDuplicateGroups: DuplicateGroup[], storage: Pick<Storage, "getItem"> = localStorage): DuplicateGroup[] {
    if (!hasDuplicates || allDuplicateGroups.length === 0) {
        return [];
    }
    const storageKey = createCombinedDismissalStorageKey(allDuplicateGroups);
    const isDismissed = storage.getItem(storageKey) === "true";
    return isDismissed ? [] : allDuplicateGroups;
}

export function useUndismissedDuplicate(): {
    undismissedDuplicates: DuplicateGroup[];
    hasAnyDuplicates: boolean;
} {
    const { hasDuplicates, allDuplicateGroups } = useDuplicateDisplayName();

    const undismissedDuplicates = useMemo(() => selectUndismissedDuplicates(hasDuplicates, allDuplicateGroups), [hasDuplicates, allDuplicateGroups]);

    return {
        undismissedDuplicates,
        hasAnyDuplicates: hasDuplicates,
    };
}
