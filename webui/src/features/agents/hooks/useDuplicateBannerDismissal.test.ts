// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, it } from "vitest";

import { createCombinedDismissalStorageKey, createDismissalStorageKey, generateDuplicateSignature } from "./useDuplicateBannerDismissal";

describe("generateDuplicateSignature", () => {
    it("returns 'no-agents' sentinel for empty input", () => {
        expect(generateDuplicateSignature([])).toBe("no-agents");
    });

    it("is order-independent", () => {
        const a = generateDuplicateSignature(["agent-1", "agent-2", "agent-3"]);
        const b = generateDuplicateSignature(["agent-3", "agent-1", "agent-2"]);
        expect(a).toBe(b);
    });

    it("produces different signatures for different agent sets", () => {
        const a = generateDuplicateSignature(["agent-1", "agent-2"]);
        const b = generateDuplicateSignature(["agent-1", "agent-3"]);
        expect(a).not.toBe(b);
    });
});

describe("createDismissalStorageKey", () => {
    it("includes the agent name and a stable signature", () => {
        const key = createDismissalStorageKey("MyAgent", ["agent-1", "agent-2"]);
        expect(key).toMatch(/^sam-duplicate-banner-dismissed-MyAgent-[a-z0-9]{1,8}$/);
    });

    it("falls back to 'unknown' when agent name is null or undefined", () => {
        const nullKey = createDismissalStorageKey(null, ["agent-1"]);
        const undefKey = createDismissalStorageKey(undefined, ["agent-1"]);
        expect(nullKey).toMatch(/^sam-duplicate-banner-dismissed-unknown-/);
        expect(undefKey).toMatch(/^sam-duplicate-banner-dismissed-unknown-/);
        expect(nullKey).toBe(undefKey);
    });

    it("produces different keys for different agent names with the same IDs", () => {
        const a = createDismissalStorageKey("AgentA", ["agent-1", "agent-2"]);
        const b = createDismissalStorageKey("AgentB", ["agent-1", "agent-2"]);
        expect(a).not.toBe(b);
    });
});

describe("createCombinedDismissalStorageKey", () => {
    it("returns a stable 'no-duplicates' key for empty input", () => {
        const key = createCombinedDismissalStorageKey([]);
        expect(key).toMatch(/^sam-duplicate-banner-dismissed-combined-/);
        expect(createCombinedDismissalStorageKey([])).toBe(key);
    });

    it("is order-independent across groups", () => {
        const groups1 = [
            { name: "AgentA", duplicateAgentIds: ["a-1", "a-2"] },
            { name: "AgentB", duplicateAgentIds: ["b-1", "b-2"] },
        ];
        const groups2 = [
            { name: "AgentB", duplicateAgentIds: ["b-2", "b-1"] },
            { name: "AgentA", duplicateAgentIds: ["a-2", "a-1"] },
        ];
        expect(createCombinedDismissalStorageKey(groups1)).toBe(createCombinedDismissalStorageKey(groups2));
    });

    it("changes when one group gains a new duplicate", () => {
        const before = createCombinedDismissalStorageKey([
            { name: "AgentA", duplicateAgentIds: ["a-1", "a-2"] },
            { name: "AgentB", duplicateAgentIds: ["b-1", "b-2"] },
        ]);
        const after = createCombinedDismissalStorageKey([
            { name: "AgentA", duplicateAgentIds: ["a-1", "a-2", "a-3"] },
            { name: "AgentB", duplicateAgentIds: ["b-1", "b-2"] },
        ]);
        expect(before).not.toBe(after);
    });

    it("changes when a new duplicate group appears", () => {
        const before = createCombinedDismissalStorageKey([{ name: "AgentA", duplicateAgentIds: ["a-1", "a-2"] }]);
        const after = createCombinedDismissalStorageKey([
            { name: "AgentA", duplicateAgentIds: ["a-1", "a-2"] },
            { name: "AgentB", duplicateAgentIds: ["b-1", "b-2"] },
        ]);
        expect(before).not.toBe(after);
    });
});
