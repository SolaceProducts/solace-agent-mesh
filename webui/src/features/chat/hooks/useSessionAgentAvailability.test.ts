// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import type { AgentCardInfo } from "@/types";

import { deriveSessionAgentAvailability } from "./useSessionAgentAvailability";

const card = (name: string): AgentCardInfo => ({ name }) as AgentCardInfo;

const base = {
    active: true,
    selectedAgentName: "deleted-agent",
    agents: [card("live-agent")],
    knownAgentNames: new Set<string>(),
    agentDefinitionsKnown: false,
};

describe("deriveSessionAgentAvailability", () => {
    test("not unavailable when the selector is inactive (builder/hidden)", () => {
        expect(deriveSessionAgentAvailability({ ...base, active: false })).toEqual({ unavailable: false, bannerMessage: null });
    });

    test("not unavailable when no agent is selected", () => {
        expect(deriveSessionAgentAvailability({ ...base, selectedAgentName: "" }).unavailable).toBe(false);
    });

    test("not unavailable while the agent list is still empty (loading guard)", () => {
        expect(deriveSessionAgentAvailability({ ...base, agents: [] }).unavailable).toBe(false);
    });

    test("not unavailable when the selected agent is discoverable", () => {
        expect(deriveSessionAgentAvailability({ ...base, selectedAgentName: "live-agent" }).unavailable).toBe(false);
    });

    test('"has been deleted" only when the definition tables confirm absence', () => {
        const r = deriveSessionAgentAvailability({ ...base, agentDefinitionsKnown: true, knownAgentNames: new Set(["live-agent"]) });
        expect(r.unavailable).toBe(true);
        expect(r.bannerMessage).toContain("has been deleted");
    });

    test('"is not available" when the definition still exists (offline, not deleted)', () => {
        const r = deriveSessionAgentAvailability({ ...base, agentDefinitionsKnown: true, knownAgentNames: new Set(["deleted-agent"]) });
        expect(r.unavailable).toBe(true);
        expect(r.bannerMessage).toContain("is not available");
        expect(r.bannerMessage).not.toContain("deleted");
    });

    test('falls back to "is not available" when the definition tables are unreadable', () => {
        const r = deriveSessionAgentAvailability({ ...base, agentDefinitionsKnown: false });
        expect(r.unavailable).toBe(true);
        expect(r.bannerMessage).toContain("is not available");
        expect(r.bannerMessage).not.toContain("deleted");
    });
});
