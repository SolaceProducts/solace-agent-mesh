// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, it } from "vitest";

import type { AgentCardInfo } from "@/types";

import { mergeAgentCards } from "./mergeAgentCards";

const yaml = (name: string, overrides: Partial<AgentCardInfo> = {}): AgentCardInfo =>
    ({
        name,
        description: `${name} desc`,
        skills: [{ id: `${name}-skill`, name: `${name} skill`, description: "", tags: [] }],
        ...overrides,
    }) as AgentCardInfo;

const user = (name: string, overrides: Partial<AgentCardInfo> = {}): AgentCardInfo =>
    ({
        name,
        description: `user ${name}`,
        skills: [],
        type: "user",
        createdBy: "alice",
        ...overrides,
    }) as AgentCardInfo;

const remote = (name: string, overrides: Partial<AgentCardInfo> = {}): AgentCardInfo =>
    ({
        name,
        description: `remote ${name}`,
        skills: [],
        agentCardLocation: "well_known",
        status: "deployed",
        createdBy: "bob",
        ...overrides,
    }) as AgentCardInfo;

describe("mergeAgentCards", () => {
    it("returns [] for empty inputs (including nullish)", () => {
        expect(mergeAgentCards([], [], [])).toEqual([]);
        expect(mergeAgentCards(null, undefined, null)).toEqual([]);
    });

    it("returns yaml agents unchanged when there are no user or remote matches", () => {
        const a = yaml("A");
        const b = yaml("B");
        const result = mergeAgentCards([a, b], [], []);
        expect(result).toEqual([a, b]);
    });

    it("user agent wins by name over yaml entry", () => {
        const yamlA = yaml("Shared", { description: "yaml" });
        const userA = user("Shared", { description: "user" });
        const result = mergeAgentCards([yamlA], [userA], []);
        expect(result).toEqual([userA]);
        expect(result[0]).toBe(userA);
    });

    it("enriches yaml entry with remote-agent fields when remote matches by name (no user override)", () => {
        const yamlA = yaml("Mesh", { skills: [{ id: "s1", name: "live", description: "", tags: [] }], description: "live desc" });
        const remoteA = remote("Mesh", {
            createdBy: "carol",
            updatedBy: "dave",
            createdAt: "2026-05-01T00:00:00.000+00:00",
            updatedAt: "2026-05-10T00:00:00.000+00:00",
            agentCardLocation: "custom",
            status: "deployed",
            deployed: { name: "Mesh", description: "stale", skills: [{ id: "old", name: "stale", description: "", tags: [] }] } as unknown as AgentCardInfo,
        });
        const [merged] = mergeAgentCards([yamlA], [], [remoteA]);
        expect(merged.type).toBe("external");
        expect(merged.agentCardLocation).toBe("custom");
        expect(merged.status).toBe("deployed");
        expect(merged.createdBy).toBe("carol");
        expect(merged.updatedBy).toBe("dave");
        expect(merged.createdAt).toBe("2026-05-01T00:00:00.000+00:00");
        expect(merged.updatedAt).toBe("2026-05-10T00:00:00.000+00:00");
        expect(merged.deployed?.skills).toEqual(yamlA.skills);
        expect(merged.deployed?.description).toBe("live desc");
    });

    it("preserves remote.deployed=undefined when remote has no deployed snapshot", () => {
        const yamlA = yaml("Mesh");
        const remoteA = remote("Mesh", { deployed: undefined });
        const [merged] = mergeAgentCards([yamlA], [], [remoteA]);
        expect(merged.deployed).toBeUndefined();
        expect(merged.type).toBe("external");
    });

    it("user wins over remote when both match the same name", () => {
        const yamlA = yaml("Triple");
        const userA = user("Triple");
        const remoteA = remote("Triple", { createdBy: "carol" });
        const result = mergeAgentCards([yamlA], [userA], [remoteA]);
        expect(result).toEqual([userA]);
    });

    it("does NOT surface a registered card-less agent absent from the discovery spine", () => {
        const yamlA = yaml("A");
        const registered = remote("Registered", { agentCardLocation: "none", status: "deployed" });
        const result = mergeAgentCards([yamlA], [], [registered]);
        expect(result).toEqual([yamlA]);
    });

    it("surfaces an invokable card-less agent exactly once, enriched via the spine (no duplicate)", () => {
        const spineEntry = yaml("proxy_uuid");
        const registered = remote("proxy_uuid", { agentCardLocation: "none", status: "deployed", createdBy: "carol" });
        const result = mergeAgentCards([spineEntry], [], [registered]);
        expect(result).toHaveLength(1);
        expect(result[0].name).toBe("proxy_uuid");
        expect(result[0].type).toBe("external");
        expect(result[0].createdBy).toBe("carol");
    });

    it("never adds a remote agent with no matching spine entry, regardless of location or status", () => {
        const offMesh = remote("Off", { agentCardLocation: "well_known", status: "deployed" });
        const pending = remote("Pending", { agentCardLocation: "none", status: "not_deployed" });
        const result = mergeAgentCards([], [], [offMesh, pending]);
        expect(result).toEqual([]);
    });

    it("winning user agent keeps the spine's live welcome when its own is absent", () => {
        const welcome = { welcome_message: "Ask me anything", suggestions: [{ label: "Plan", prompt: "Help me plan " }] };
        const yamlA = yaml("Orchestrator", { welcome });
        const userA = user("Orchestrator", { welcome: undefined });
        const [merged] = mergeAgentCards([yamlA], [userA], []);
        expect(merged.welcome).toBe(yamlA.welcome);
        expect(merged.createdBy).toBe("alice");
    });

    it("winning user agent keeps its own welcome when it fully covers both fields", () => {
        const userWelcome = { welcome_message: "User configured", suggestions: [{ label: "U", prompt: "u" }] };
        const yamlA = yaml("Orchestrator", { welcome: { welcome_message: "Spine", suggestions: [{ label: "S", prompt: "s" }] } });
        const userA = user("Orchestrator", { welcome: userWelcome });
        const [merged] = mergeAgentCards([yamlA], [userA], []);
        expect(merged.welcome).toBe(userWelcome);
    });

    it("backfills spine suggestions when the user welcome has a message but no suggestions", () => {
        const spine = { welcome_message: "Spine msg", suggestions: [{ label: "Plan", prompt: "Help me plan " }] };
        const yamlA = yaml("Orchestrator", { welcome: spine });
        const userA = user("Orchestrator", { welcome: { welcome_message: "User msg", suggestions: undefined } });
        const [merged] = mergeAgentCards([yamlA], [userA], []);
        expect(merged.welcome?.welcome_message).toBe("User msg");
        expect(merged.welcome?.suggestions).toEqual(spine.suggestions);
        expect(merged.createdBy).toBe("alice");
    });

    it("treats an empty user suggestion list as absent and backfills the spine's", () => {
        const spine = { welcome_message: "Spine", suggestions: [{ label: "Plan", prompt: "p" }] };
        const yamlA = yaml("Orchestrator", { welcome: spine });
        const userA = user("Orchestrator", { welcome: { welcome_message: "User", suggestions: [] } });
        const [merged] = mergeAgentCards([yamlA], [userA], []);
        expect(merged.welcome?.suggestions).toEqual(spine.suggestions);
    });

    it("backfills the spine message when the user welcome has suggestions but no message", () => {
        const userSuggestions = [{ label: "U", prompt: "u" }];
        const yamlA = yaml("Orchestrator", { welcome: { welcome_message: "Spine msg", suggestions: [{ label: "S", prompt: "s" }] } });
        const userA = user("Orchestrator", { welcome: { welcome_message: undefined, suggestions: userSuggestions } });
        const [merged] = mergeAgentCards([yamlA], [userA], []);
        expect(merged.welcome?.welcome_message).toBe("Spine msg");
        expect(merged.welcome?.suggestions).toBe(userSuggestions);
    });

    it("keeps the user welcome by reference when the spine has none to backfill", () => {
        const userWelcome = { welcome_message: "User only", suggestions: [{ label: "U", prompt: "u" }] };
        const yamlA = yaml("Orchestrator", { welcome: undefined });
        const userA = user("Orchestrator", { welcome: userWelcome });
        const [merged] = mergeAgentCards([yamlA], [userA], []);
        expect(merged.welcome).toBe(userWelcome);
    });
});
