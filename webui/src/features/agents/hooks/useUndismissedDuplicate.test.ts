// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, it } from "vitest";

import { createCombinedDismissalStorageKey } from "./useDuplicateBannerDismissal";
import { selectUndismissedDuplicates } from "./useUndismissedDuplicate";

type Group = { name: string; count: number; duplicateAgentIds: string[] };

function makeStorage(initial: Record<string, string> = {}): Pick<Storage, "getItem"> {
    return { getItem: (key: string) => initial[key] ?? null };
}

describe("selectUndismissedDuplicates", () => {
    it("returns empty when hasDuplicates is false", () => {
        expect(selectUndismissedDuplicates(false, [], makeStorage())).toEqual([]);
    });

    it("returns empty when the group list is empty even if hasDuplicates is true", () => {
        expect(selectUndismissedDuplicates(true, [], makeStorage())).toEqual([]);
    });

    it("returns all groups when none have been dismissed", () => {
        const groups: Group[] = [
            { name: "AgentA", count: 2, duplicateAgentIds: ["a-1", "a-2"] },
            { name: "AgentB", count: 2, duplicateAgentIds: ["b-1", "b-2"] },
        ];
        expect(selectUndismissedDuplicates(true, groups, makeStorage())).toEqual(groups);
    });

    it("returns empty when the combined dismissal key is present in storage", () => {
        const groups: Group[] = [{ name: "AgentA", count: 2, duplicateAgentIds: ["a-1", "a-2"] }];
        const storage = makeStorage({ [createCombinedDismissalStorageKey(groups)]: "true" });
        expect(selectUndismissedDuplicates(true, groups, storage)).toEqual([]);
    });

    it("ignores a stored dismissal for a different group composition (banner reappears UX)", () => {
        const originalGroups: Group[] = [{ name: "AgentA", count: 2, duplicateAgentIds: ["a-1", "a-2"] }];
        const storage = makeStorage({ [createCombinedDismissalStorageKey(originalGroups)]: "true" });

        const changedGroups: Group[] = [{ name: "AgentA", count: 3, duplicateAgentIds: ["a-1", "a-2", "a-3"] }];
        expect(selectUndismissedDuplicates(true, changedGroups, storage)).toEqual(changedGroups);
    });

    it("treats anything other than the literal string 'true' as not-dismissed", () => {
        const groups: Group[] = [{ name: "AgentA", count: 2, duplicateAgentIds: ["a-1", "a-2"] }];
        const key = createCombinedDismissalStorageKey(groups);
        expect(selectUndismissedDuplicates(true, groups, makeStorage({ [key]: "false" }))).toEqual(groups);
        expect(selectUndismissedDuplicates(true, groups, makeStorage({ [key]: "1" }))).toEqual(groups);
        expect(selectUndismissedDuplicates(true, groups, makeStorage({}))).toEqual(groups);
    });
});
