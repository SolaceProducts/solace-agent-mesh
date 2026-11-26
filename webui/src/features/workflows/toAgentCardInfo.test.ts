// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import { getWorkflowConfig } from "@/utils/agentUtils";

import type { Workflow } from "@/types/workflow";

import { toAgentCardInfo } from "./toAgentCardInfo";

const base: Workflow = {
    id: "wf-1",
    name: "order_flow",
    displayName: "Order Flow",
    description: "",
    appConfig: {},
    deploymentStatus: "not_deployed",
    createdBy: "user",
    updatedBy: "user",
    createdTime: 0,
    updatedTime: 0,
};

describe("toAgentCardInfo version mapping", () => {
    test("preserves an authored appConfig.version", () => {
        expect(toAgentCardInfo({ ...base, appConfig: { version: "2.3.0" } }).version).toBe("2.3.0");
    });

    test("defaults to the runtime version when appConfig has none or an empty version", () => {
        expect(toAgentCardInfo({ ...base, appConfig: {} }).version).toBe("1.0.0");
        expect(toAgentCardInfo({ ...base, appConfig: { version: "" } }).version).toBe("1.0.0");
    });

    test("preserves an authored version nested under 'workflow'", () => {
        expect(toAgentCardInfo({ ...base, appConfig: { workflow: { version: "2.3.0" } } }).version).toBe("2.3.0");
    });
});

describe("toAgentCardInfo visualization extension", () => {
    test("embeds a readable workflow config only when appConfig has a nodes array", () => {
        const withNodes = toAgentCardInfo({ ...base, appConfig: { nodes: [{ id: "n1", type: "agent" }] } });
        expect(getWorkflowConfig(withNodes)).not.toBeNull();
        expect(getWorkflowConfig(toAgentCardInfo({ ...base, appConfig: {} }))).toBeNull();
    });

    test("reads the workflow body when appConfig nests it under 'workflow'", () => {
        const nested = toAgentCardInfo({
            ...base,
            appConfig: { tools: [], toolsets: [], workflow: { nodes: [{ id: "n1", type: "agent" }] } },
        });
        expect(getWorkflowConfig(nested)).not.toBeNull();
    });

    test("exposes the nested body's nodes directly on workflow_config", () => {
        const nested = toAgentCardInfo({
            ...base,
            appConfig: {
                workflow: {
                    nodes: [
                        { id: "n1", type: "agent" },
                        { id: "n2", type: "tool" },
                    ],
                },
            },
        });
        expect(getWorkflowConfig(nested)?.nodes).toHaveLength(2);
    });

    test("attaches no extension when the nested body has no nodes", () => {
        expect(getWorkflowConfig(toAgentCardInfo({ ...base, appConfig: { workflow: {} } }))).toBeNull();
    });
});

describe("toAgentCardInfo status mapping", () => {
    test.each([
        ["not_deployed passthrough", { deploymentStatus: "not_deployed" }, "not_deployed"],
        ["empty deploymentStatus falls back to not_deployed", { deploymentStatus: "" }, "not_deployed"],
        ["deployed + running maps to active", { deploymentStatus: "deployed", runtimeStatus: { status: "running" } }, "active"],
        ["deployed + degraded maps to degraded, not active", { deploymentStatus: "deployed", runtimeStatus: { status: "degraded" } }, "degraded"],
        ["deployed + disconnected", { deploymentStatus: "deployed", runtimeStatus: { status: "disconnected" } }, "disconnected"],
        ["deployed with no runtime status", { deploymentStatus: "deployed" }, "deployed"],
    ] as [string, Partial<Workflow>, string][])("%s", (_label, over, expected) => {
        expect(toAgentCardInfo({ ...base, ...over }).status).toBe(expected);
    });

    test("carries runtimeProblems through onto the AgentCardInfo", () => {
        const problems = [{ id: "queue:x", severity: "degraded" as const, detail: "boom", since: "" }];
        const info = toAgentCardInfo({ ...base, deploymentStatus: "deployed", runtimeStatus: { status: "degraded", problems } });
        expect(info.runtimeProblems).toEqual(problems);
    });

    test("not deployed + stale degraded runtimeStatus → status follows deploymentStatus, runtimeProblems is not carried over", () => {
        const problems = [{ id: "queue:x", severity: "degraded" as const, detail: "boom", since: "" }];
        const info = toAgentCardInfo({ ...base, deploymentStatus: "not_deployed", runtimeStatus: { status: "degraded", problems } });
        expect(info.status).toBe("not_deployed");
        expect(info.runtimeProblems).toBeUndefined();
    });
});
