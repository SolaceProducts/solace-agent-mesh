// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, it } from "vitest";

import type { AgentCardInfo } from "@/types";

import { EPHEMERAL_TEST_AGENT_NAME_PREFIX, isEphemeralTestAgentName, isEphemeralTestSession, pickDefaultChatAgent, resolveAgentSessionRoute, withWorkflowBodyFields, workflowBody } from "./agentUtils";

const agent = (overrides: Partial<AgentCardInfo>): AgentCardInfo => ({ name: "a", ...overrides }) as AgentCardInfo;

describe("EPHEMERAL_TEST_AGENT_NAME_PREFIX", () => {
    it("matches the backend constant", () => {
        expect(EPHEMERAL_TEST_AGENT_NAME_PREFIX).toBe("__test__");
    });
});

describe("isEphemeralTestAgentName", () => {
    it("matches names that start with __test__", () => {
        expect(isEphemeralTestAgentName("__test__019e07b3__sam_dev_")).toBe(true);
    });
    it("rejects names that merely contain __test__ mid-string", () => {
        expect(isEphemeralTestAgentName("real__test__variant")).toBe(false);
    });
    it("rejects nullish + empty + non-test names", () => {
        expect(isEphemeralTestAgentName(null)).toBe(false);
        expect(isEphemeralTestAgentName(undefined)).toBe(false);
        expect(isEphemeralTestAgentName("")).toBe(false);
        expect(isEphemeralTestAgentName("Builder")).toBe(false);
        expect(isEphemeralTestAgentName("agent_019e07b3_1491_7ba0_9611_717bd7cc595e")).toBe(false);
    });
});

describe("isEphemeralTestSession", () => {
    it("trusts the server flag even when agentId is the broker-safe instance name", () => {
        expect(
            isEphemeralTestSession({
                agentId: "agent_019e07b3_1491_7ba0_9611_717bd7cc595e",
                isEphemeralTest: true,
            })
        ).toBe(true);
    });
    it("falls back to the __test__ prefix when the flag is absent (legacy cached row)", () => {
        expect(isEphemeralTestSession({ agentId: "__test__abc__sam_dev_" })).toBe(true);
    });
    it("ignores the prefix-fallback when the server flag is explicitly false", () => {
        expect(isEphemeralTestSession({ agentId: "__test__abc__sam_dev_", isEphemeralTest: false })).toBe(true);
        expect(isEphemeralTestSession({ agentId: "Builder", isEphemeralTest: false })).toBe(false);
    });
    it("returns false for non-test agents", () => {
        expect(isEphemeralTestSession({ agentId: "Builder" })).toBe(false);
        expect(isEphemeralTestSession({ agentId: "agent_019e07b3_1491_7ba0_9611_717bd7cc595e" })).toBe(false);
    });
    it("returns false for null / undefined sessions", () => {
        expect(isEphemeralTestSession(null)).toBe(false);
        expect(isEphemeralTestSession(undefined)).toBe(false);
    });
});

describe("resolveAgentSessionRoute", () => {
    const routes = { Builder: "/builder", "agent-known": "/somewhere" };

    it("routes server-flagged instance-name test sessions to the Builder page", () => {
        expect(resolveAgentSessionRoute({ agentId: "agent_019e07b3_1491_7ba0_9611_717bd7cc595e", isEphemeralTest: true }, routes)).toBe("/builder");
    });

    it("keeps resolving the legacy string call signature that the project sidebar still uses", () => {
        expect(resolveAgentSessionRoute("__test__abc", routes)).toBe("/builder");
        expect(resolveAgentSessionRoute("agent-known", routes)).toBe("/somewhere");
    });

    it("returns the direct mapping when agentId is a literal key", () => {
        expect(resolveAgentSessionRoute({ agentId: "Builder" }, routes)).toBe("/builder");
    });

    it("returns undefined for unmapped regular agents", () => {
        expect(resolveAgentSessionRoute({ agentId: "random_agent" }, routes)).toBeUndefined();
    });

    it("never routes an ephemeral session to a Builder route the host did not register", () => {
        expect(resolveAgentSessionRoute({ agentId: "agent_019e07b3_1491_7ba0_9611_717bd7cc595e", isEphemeralTest: true }, {})).toBeUndefined();
    });

    it("returns undefined for empty / null / undefined input", () => {
        expect(resolveAgentSessionRoute(null, routes)).toBeUndefined();
        expect(resolveAgentSessionRoute(undefined, routes)).toBeUndefined();
        expect(resolveAgentSessionRoute("", routes)).toBeUndefined();
        expect(resolveAgentSessionRoute({ agentId: "" }, routes)).toBeUndefined();
        expect(resolveAgentSessionRoute({ agentId: null }, routes)).toBeUndefined();
    });
});

describe("pickDefaultChatAgent", () => {
    it("returns undefined for an empty list", () => {
        expect(pickDefaultChatAgent([])).toBeUndefined();
    });

    it("picks the highest-priority non-internal agent", () => {
        const result = pickDefaultChatAgent([agent({ name: "low", priority: 1 }), agent({ name: "high", priority: 100 }), agent({ name: "mid", priority: 50 })]);
        expect(result?.name).toBe("high");
    });

    it("breaks ties alphabetically by displayName", () => {
        const result = pickDefaultChatAgent([agent({ name: "z", displayName: "Zebra", priority: 10 }), agent({ name: "a", displayName: "Alpha", priority: 10 })]);
        expect(result?.name).toBe("a");
    });

    it("falls back to name when displayName is missing", () => {
        const result = pickDefaultChatAgent([agent({ name: "zeta", priority: 10 }), agent({ name: "alpha", priority: 10 })]);
        expect(result?.name).toBe("alpha");
    });

    it("skips internal agents", () => {
        const result = pickDefaultChatAgent([agent({ name: "internal", priority: 100, isInternal: true }), agent({ name: "public", priority: 1 })]);
        expect(result?.name).toBe("public");
    });

    it("skips workflow agents", () => {
        const result = pickDefaultChatAgent([agent({ name: "workflow", priority: 100, isWorkflow: true }), agent({ name: "regular", priority: 1 })]);
        expect(result?.name).toBe("regular");
    });

    it("falls back to first agent when every candidate is internal/workflow", () => {
        const result = pickDefaultChatAgent([agent({ name: "only-internal", isInternal: true })]);
        expect(result?.name).toBe("only-internal");
    });

    it("treats absent priority as 0", () => {
        const result = pickDefaultChatAgent([agent({ name: "no-priority" }), agent({ name: "explicit", priority: 5 })]);
        expect(result?.name).toBe("explicit");
    });
});

describe("workflowBody", () => {
    it("returns the flat appConfig when there is no nested workflow key", () => {
        const appConfig = { nodes: [{ id: "n1" }], version: "1.2.0" };
        expect(workflowBody(appConfig)).toBe(appConfig);
    });

    it("returns the nested workflow map when appConfig carries sibling tools/toolsets", () => {
        const nested = { nodes: [{ id: "n1" }], version: "2.3.0" };
        expect(workflowBody({ tools: [], toolsets: [], workflow: nested })).toBe(nested);
    });

    it("falls back to flat when the nested workflow key is an array, not a map", () => {
        const appConfig = { workflow: [1, 2, 3], nodes: [{ id: "n1" }] };
        expect(workflowBody(appConfig)).toBe(appConfig);
    });

    it("returns an empty object for undefined/null appConfig", () => {
        expect(workflowBody(undefined)).toEqual({});
        expect(workflowBody(null)).toEqual({});
    });
});

describe("withWorkflowBodyFields", () => {
    it("merges fields flat when there is no nested workflow key", () => {
        const result = withWorkflowBodyFields({ nodes: [{ id: "n1" }] }, { version: "3.0.0" });
        expect(result).toEqual({ nodes: [{ id: "n1" }], version: "3.0.0" });
    });

    it("merges fields into the nested workflow map, preserving sibling tools/toolsets and other nested fields", () => {
        const appConfig = { tools: ["t1"], toolsets: ["ts1"], workflow: { nodes: [{ id: "n1" }], version: "1.0.0" } };
        const result = withWorkflowBodyFields(appConfig, { version: "2.0.0" });
        expect(result).toEqual({ tools: ["t1"], toolsets: ["ts1"], workflow: { nodes: [{ id: "n1" }], version: "2.0.0" } });
    });

    it("round-trips through workflowBody so a write is visible to every reader", () => {
        const appConfig = { tools: [], workflow: { nodes: [] } };
        const updated = withWorkflowBodyFields(appConfig, { description: "updated" });
        expect(workflowBody(updated).description).toBe("updated");
    });
});
