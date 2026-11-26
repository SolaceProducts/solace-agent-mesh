// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { AgentCardInfo } from "@/types";

import { selectInitialAgent } from "./selectInitialAgent";

const agent = (name: string, overrides: Partial<AgentCardInfo> = {}): AgentCardInfo =>
    ({
        name,
        displayName: name,
        description: "",
        skills: [],
        ...overrides,
    }) as AgentCardInfo;

describe("selectInitialAgent", () => {
    beforeEach(() => {
        vi.spyOn(console, "log").mockImplementation(() => {});
        vi.spyOn(console, "warn").mockImplementation(() => {});
    });

    afterEach(() => {
        vi.restoreAllMocks();
    });

    it("returns undefined when agents list is empty", () => {
        expect(selectInitialAgent({ agents: [], mode: "full" })).toBeUndefined();
    });

    it("URL agent wins over everything when present", () => {
        const manager = agent("Manager");
        const target = agent("Target");
        const project = agent("ProjectDefault");
        const result = selectInitialAgent({
            agents: [manager, target, project],
            urlAgentName: "Target",
            projectDefaultAgentId: "ProjectDefault",
            mode: "full",
        });
        expect(result).toBe(target);
    });

    it("unknown URL agent name logs a warning and falls back through priority order", () => {
        const manager = agent("Manager");
        const helper = agent("Helper", { priority: 5 });
        const result = selectInitialAgent({
            agents: [manager, helper],
            urlAgentName: "Nope",
            mode: "full",
        });
        expect(result).toBe(helper);
    });

    it("project default agent is preferred when no URL agent", () => {
        const helper = agent("Helper", { priority: 100 });
        const project = agent("ProjectDefault", { priority: 0 });
        const result = selectInitialAgent({
            agents: [helper, project],
            projectDefaultAgentId: "ProjectDefault",
            mode: "full",
        });
        expect(result).toBe(project);
    });

    it("missing project default agent logs a warning and falls back through priority order", () => {
        const helper = agent("Helper", { priority: 5 });
        const result = selectInitialAgent({
            agents: [helper],
            projectDefaultAgentId: "GoneAgent",
            mode: "full",
        });
        expect(result).toBe(helper);
    });

    it("last used agent is preferred over the priority-ordered default", () => {
        const high = agent("High", { priority: 100 });
        const remembered = agent("Remembered", { priority: 0 });
        const result = selectInitialAgent({
            agents: [high, remembered],
            lastUsedAgentName: "Remembered",
            mode: "full",
        });
        expect(result).toBe(remembered);
    });

    it("URL agent wins over the last used agent", () => {
        const target = agent("Target");
        const remembered = agent("Remembered");
        const result = selectInitialAgent({
            agents: [target, remembered],
            urlAgentName: "Target",
            lastUsedAgentName: "Remembered",
            mode: "full",
        });
        expect(result).toBe(target);
    });

    it("project default wins over the last used agent", () => {
        const project = agent("ProjectDefault");
        const remembered = agent("Remembered");
        const result = selectInitialAgent({
            agents: [project, remembered],
            projectDefaultAgentId: "ProjectDefault",
            lastUsedAgentName: "Remembered",
            mode: "full",
        });
        expect(result).toBe(project);
    });

    it("falls through to the last used agent when the project default is gone", () => {
        const high = agent("High", { priority: 100 });
        const remembered = agent("Remembered", { priority: 0 });
        const result = selectInitialAgent({
            agents: [high, remembered],
            projectDefaultAgentId: "GoneAgent",
            lastUsedAgentName: "Remembered",
            mode: "full",
        });
        expect(result).toBe(remembered);
    });

    it("ignores a last used agent that has left the mesh", () => {
        const high = agent("High", { priority: 10 });
        const result = selectInitialAgent({
            agents: [high],
            lastUsedAgentName: "GoneAgent",
            mode: "full",
        });
        expect(result).toBe(high);
    });

    it("ignores a last used agent that is now internal", () => {
        const high = agent("High", { priority: 10 });
        const remembered = agent("Remembered", { isInternal: true });
        const result = selectInitialAgent({
            agents: [high, remembered],
            lastUsedAgentName: "Remembered",
            mode: "full",
        });
        expect(result).toBe(high);
    });

    it("ignores a last used agent that is now a workflow", () => {
        const high = agent("High", { priority: 10 });
        const remembered = agent("Remembered", { isWorkflow: true });
        const result = selectInitialAgent({
            agents: [high, remembered],
            lastUsedAgentName: "Remembered",
            mode: "full",
        });
        expect(result).toBe(high);
    });

    it("resolves the last used agent from lastUsedCandidates when it is absent from agents", () => {
        const yamlOnly = agent("High", { priority: 10 });
        const userCreated = agent("agent_builder_uuid");
        const result = selectInitialAgent({
            agents: [yamlOnly],
            lastUsedCandidates: [yamlOnly, userCreated],
            lastUsedAgentName: "agent_builder_uuid",
            mode: "full",
        });
        expect(result).toBe(userCreated);
    });

    it("widens only the last used tier — the project default still resolves against agents", () => {
        const yamlOnly = agent("High", { priority: 10 });
        const userCreated = agent("agent_builder_uuid");
        const result = selectInitialAgent({
            agents: [yamlOnly],
            lastUsedCandidates: [yamlOnly, userCreated],
            projectDefaultAgentId: "agent_builder_uuid",
            mode: "full",
        });
        expect(result).toBe(yamlOnly);
    });

    it("still applies the internal/workflow predicate to lastUsedCandidates", () => {
        const yamlOnly = agent("High", { priority: 10 });
        const userInternal = agent("agent_builder_uuid", { isInternal: true });
        const result = selectInitialAgent({
            agents: [yamlOnly],
            lastUsedCandidates: [yamlOnly, userInternal],
            lastUsedAgentName: "agent_builder_uuid",
            mode: "full",
        });
        expect(result).toBe(yamlOnly);
    });

    it("an empty agents list still returns undefined even with candidates present", () => {
        const userCreated = agent("agent_builder_uuid");
        const result = selectInitialAgent({
            agents: [],
            lastUsedCandidates: [userCreated],
            lastUsedAgentName: "agent_builder_uuid",
            mode: "full",
        });
        expect(result).toBeUndefined();
    });

    it("defaults lastUsedCandidates to agents when not supplied", () => {
        const high = agent("High", { priority: 100 });
        const remembered = agent("Remembered", { priority: 0 });
        const result = selectInitialAgent({
            agents: [high, remembered],
            lastUsedAgentName: "Remembered",
            mode: "full",
        });
        expect(result).toBe(remembered);
    });

    it("falls back to pickDefaultChatAgent (highest priority non-internal/workflow) when nothing else applies", () => {
        const high = agent("High", { priority: 10 });
        const low = agent("Low", { priority: 0 });
        const internal = agent("Internal", { priority: 100, isInternal: true });
        const result = selectInitialAgent({
            agents: [internal, low, high],
            mode: "full",
        });
        expect(result).toBe(high);
    });

    it("falls back to the first agent when all are filtered out by pickDefaultChatAgent", () => {
        const onlyInternal = agent("Internal", { isInternal: true });
        const result = selectInitialAgent({
            agents: [onlyInternal],
            mode: "full",
        });
        expect(result).toBe(onlyInternal);
    });

    describe("agent mode", () => {
        it("pins to the agent matching the wire name", () => {
            const target = agent("Target");
            const other = agent("Other", { priority: 100 });
            const result = selectInitialAgent({ agents: [other, target], urlAgentName: "Target", mode: "agentMode" });
            expect(result).toBe(target);
        });

        it("pins by the unique wire name only — never the display name", () => {
            const target = agent("agent_abc", { displayName: "Support" });
            const result = selectInitialAgent({ agents: [agent("Other"), target], urlAgentName: "Support", mode: "agentMode" });
            expect(result).toBeUndefined();
        });

        it("bails (undefined) when the pinned agent is not found — no default fallback", () => {
            const other = agent("Other", { priority: 100 });
            const result = selectInitialAgent({ agents: [other], urlAgentName: "Missing", mode: "agentMode" });
            expect(result).toBeUndefined();
        });

        it("bails (undefined) when no agent is specified — never guesses a default", () => {
            const other = agent("Other", { priority: 100 });
            const result = selectInitialAgent({ agents: [other], urlAgentName: null, mode: "agentMode" });
            expect(result).toBeUndefined();
        });

        it("ignores the last used agent — agent mode honors only ?agent=", () => {
            const remembered = agent("Remembered");
            const result = selectInitialAgent({ agents: [remembered], urlAgentName: null, lastUsedAgentName: "Remembered", mode: "agentMode" });
            expect(result).toBeUndefined();
        });

        it("ignores lastUsedCandidates too — the widened list must not leak into agent mode", () => {
            const yamlOnly = agent("Other");
            const userCreated = agent("agent_builder_uuid");
            const result = selectInitialAgent({
                agents: [yamlOnly],
                lastUsedCandidates: [yamlOnly, userCreated],
                urlAgentName: "agent_builder_uuid",
                lastUsedAgentName: "agent_builder_uuid",
                mode: "agentMode",
            });
            expect(result).toBeUndefined();
        });
    });
});
