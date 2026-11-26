// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, it } from "vitest";

import type { VisualizedTask, VisualizerStep } from "@/types/activities";

import { rootTestAgentName, TEST_RUNNER_LABEL, testRunnerNameOverrides } from "./testActivityAgentLabel";

function step(partial: Partial<VisualizerStep>): VisualizerStep {
    return {
        id: "s",
        type: "USER_REQUEST",
        timestamp: "",
        title: "",
        data: {},
        rawEventIds: [],
        nestingLevel: 0,
        owningTaskId: "t",
        ...partial,
    };
}

function task(steps: VisualizerStep[]): VisualizedTask {
    return { taskId: "t", initialRequestText: "", status: "completed", startTime: "", steps };
}

describe("rootTestAgentName", () => {
    it("returns the root agent the user's request targets (the ephemeral wrapper)", () => {
        const t = task([
            step({ source: "User", target: "agent_019f3921_935a_7246", nestingLevel: 0 }),
            step({ source: "agent_019f3921_935a_7246", target: "LLM", nestingLevel: 0, type: "AGENT_LLM_CALL" }),
            step({ source: "agent_019f3921_935a_7246", target: "HelloGreeterAgent", nestingLevel: 1, type: "AGENT_TOOL_INVOCATION_START" }),
        ]);
        expect(rootTestAgentName(t)).toBe("agent_019f3921_935a_7246");
    });

    it("ignores the final agent→User response step (target 'User')", () => {
        const t = task([step({ source: "User", target: "agent_abc", nestingLevel: 0 }), step({ source: "agent_abc", target: "User", nestingLevel: 0, type: "AGENT_RESPONSE_TEXT" })]);
        expect(rootTestAgentName(t)).toBe("agent_abc");
    });

    it("ignores nested (nestingLevel > 0) user-sourced steps", () => {
        expect(rootTestAgentName(task([step({ source: "User", target: "peer_agent", nestingLevel: 1 })]))).toBeUndefined();
    });

    it("returns undefined for a null task or a task with no user-entry step", () => {
        expect(rootTestAgentName(null)).toBeUndefined();
        expect(rootTestAgentName(task([step({ source: "agent_x", target: "LLM", type: "AGENT_LLM_CALL" })]))).toBeUndefined();
    });
});

describe("testRunnerNameOverrides", () => {
    it("maps the wrapper's instance name to the Test Runner label", () => {
        const t = task([step({ source: "User", target: "agent_xyz", nestingLevel: 0 })]);
        expect(testRunnerNameOverrides(t)).toEqual({ agent_xyz: TEST_RUNNER_LABEL });
    });

    it("returns undefined when no root agent is found, so the name map is untouched", () => {
        expect(testRunnerNameOverrides(null)).toBeUndefined();
    });
});
