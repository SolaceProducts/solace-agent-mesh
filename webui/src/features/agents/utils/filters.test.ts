// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import type { AgentCardInfo, AgentStatus } from "@/types";

import { agentManagementTabFor, agentManagementUrl, filterAgentsByDeployment, filterAgentsByKind, isDeployed, isRemoteNotRunning, kindOf, sortAgentsByDisplayName } from "./filters";

const agentWithStatus = (name: string, status: AgentStatus): AgentCardInfo =>
    ({
        name,
        displayName: name,
        status,
    }) as AgentCardInfo;

describe("isDeployed", () => {
    test("treats deployed and disconnected as deployed", () => {
        expect(isDeployed(agentWithStatus("a", "deployed"))).toBe(true);
        expect(isDeployed(agentWithStatus("b", "disconnected"))).toBe(true);
    });

    test("treats every other status as undeployed", () => {
        const undeployed: AgentStatus[] = ["not_deployed", "deploy_failed"];
        for (const status of undeployed) {
            expect(isDeployed(agentWithStatus("x", status))).toBe(false);
        }
    });

    test("treats degraded as deployed (still running, just reporting a problem)", () => {
        expect(isDeployed(agentWithStatus("d", "degraded"))).toBe(true);
    });

    test("treats a deployed-but-not-running remote agent (external + stopped) as deployed", () => {
        expect(isDeployed({ name: "r", displayName: "r", status: "stopped", type: "external" } as AgentCardInfo)).toBe(true);
    });

    test("treats a stopped user agent as undeployed (Redeploy can restart a local agent)", () => {
        expect(isDeployed({ name: "u", displayName: "u", status: "stopped", type: "user" } as AgentCardInfo)).toBe(false);
    });
});

describe("isRemoteNotRunning", () => {
    test("external agent with 'stopped' status is not-running", () => {
        expect(isRemoteNotRunning({ name: "r", displayName: "r", status: "stopped", type: "external" } as AgentCardInfo)).toBe(true);
    });

    test("registered no-card external agent ('deployed') is NOT treated as not-running", () => {
        expect(isRemoteNotRunning({ name: "r", displayName: "r", status: "deployed", type: "external", agentCardLocation: "none" } as AgentCardInfo)).toBe(false);
    });

    test("a stopped user agent is not a remote-not-running case", () => {
        expect(isRemoteNotRunning({ name: "u", displayName: "u", status: "stopped", type: "user" } as AgentCardInfo)).toBe(false);
    });
});

describe("filterAgentsByDeployment", () => {
    const agents: AgentCardInfo[] = [agentWithStatus("alpha", "deployed"), agentWithStatus("beta", "disconnected"), agentWithStatus("gamma", "not_deployed"), agentWithStatus("delta", "deploy_failed")];

    test("'deployed' keeps only deployed and disconnected agents", () => {
        const result = filterAgentsByDeployment(agents, "deployed");
        expect(result.map(a => a.name)).toEqual(["alpha", "beta"]);
    });

    test("'undeployed' keeps everything else, including failures", () => {
        const result = filterAgentsByDeployment(agents, "undeployed");
        expect(result.map(a => a.name)).toEqual(["gamma", "delta"]);
    });
});

describe("kindOf / filterAgentsByKind", () => {
    const card = (name: string, isWorkflow?: boolean): AgentCardInfo => ({ name, displayName: name, isWorkflow }) as AgentCardInfo;

    test("treats an absent isWorkflow as an agent", () => {
        expect(kindOf(card("plain"))).toBe("agent");
        expect(kindOf(card("explicit", false))).toBe("agent");
        expect(kindOf(card("flow", true))).toBe("workflow");
    });

    test("keeps only the requested kind", () => {
        const cards = [card("agent-a"), card("flow-a", true), card("agent-b", false), card("flow-b", true)];

        expect(filterAgentsByKind(cards, "agent").map(c => c.name)).toEqual(["agent-a", "agent-b"]);
        expect(filterAgentsByKind(cards, "workflow").map(c => c.name)).toEqual(["flow-a", "flow-b"]);
    });

    test("returns an empty list rather than everything when nothing matches", () => {
        expect(filterAgentsByKind([card("agent-a")], "workflow")).toEqual([]);
    });
});

describe("sortAgentsByDisplayName", () => {
    const named = (displayName: string | undefined): AgentCardInfo => ({ name: displayName ?? "", displayName }) as AgentCardInfo;

    test("sorts alphabetically, case-insensitively", () => {
        const result = sortAgentsByDisplayName([named("NewsSummarizer"), named("data analyst"), named("Celebrate"), named("BusinessAdvisor")]);
        expect(result.map(a => a.displayName)).toEqual(["BusinessAdvisor", "Celebrate", "data analyst", "NewsSummarizer"]);
    });

    test("does not mutate the source array", () => {
        const source = [named("beta"), named("alpha")];
        sortAgentsByDisplayName(source);
        expect(source.map(a => a.displayName)).toEqual(["beta", "alpha"]);
    });

    test("tolerates undefined display names", () => {
        const result = sortAgentsByDisplayName([named("beta"), named(undefined), named("alpha")]);
        expect(result.map(a => a.displayName)).toEqual([undefined, "alpha", "beta"]);
    });
});

describe("agentManagementTabFor", () => {
    test("deployed + running → 'deployed' tab", () => {
        expect(agentManagementTabFor("deployed", { status: "running" })).toBe("deployed");
    });

    test("deployed + degraded → 'deployed' tab (still running, not routed as stopped)", () => {
        expect(agentManagementTabFor("deployed", { status: "degraded", problems: [{ id: "queue:x", severity: "degraded", detail: "boom", since: "" }] })).toBe("deployed");
    });

    test("deployed + runtimeStatus absent → 'undeployed' tab (stopped row lives in undeployed bucket)", () => {
        expect(agentManagementTabFor("deployed")).toBe("undeployed");
        expect(agentManagementTabFor("deployed", { status: "stopped" })).toBe("undeployed");
        expect(agentManagementTabFor("deployed", { status: "starting" })).toBe("undeployed");
    });

    test("deployed + not running stays 'undeployed' for a user agent even when agentType is passed", () => {
        expect(agentManagementTabFor("deployed", { status: "stopped" }, "user")).toBe("undeployed");
        expect(agentManagementTabFor("deployed", { status: "disconnected" }, "user")).toBe("undeployed");
    });

    test("remote (external) deployed + not running → 'deployed' tab", () => {
        expect(agentManagementTabFor("deployed", { status: "disconnected" }, "external")).toBe("deployed");
        expect(agentManagementTabFor("deployed", { status: "stopped" }, "external")).toBe("deployed");
        expect(agentManagementTabFor("deployed", undefined, "external")).toBe("deployed");
    });

    test("remote (external) deployed + running → 'deployed' tab", () => {
        expect(agentManagementTabFor("deployed", { status: "running" }, "external")).toBe("deployed");
    });

    test("disconnected → 'deployed' tab (row is configured for deployment)", () => {
        expect(agentManagementTabFor("disconnected")).toBe("deployed");
    });

    test("not_deployed → 'undeployed' tab", () => {
        expect(agentManagementTabFor("not_deployed")).toBe("undeployed");
    });

    test("deploy_failed → 'undeployed' tab", () => {
        expect(agentManagementTabFor("deploy_failed")).toBe("undeployed");
    });

    test("undefined (new/never-deployed agent) → 'undeployed' tab", () => {
        expect(agentManagementTabFor(undefined)).toBe("undeployed");
    });
});

describe("agentManagementUrl", () => {
    test("includes id and tab when agentId is provided", () => {
        expect(agentManagementUrl("abc-123", "deployed")).toBe("/agents?id=abc-123&tab=deployed");
        expect(agentManagementUrl("abc-123", "undeployed")).toBe("/agents?id=abc-123&tab=undeployed");
    });

    test("omits id param when agentId is undefined", () => {
        expect(agentManagementUrl(undefined, "deployed")).toBe("/agents?tab=deployed");
    });

    test("omits id param when agentId is empty string", () => {
        expect(agentManagementUrl("", "undeployed")).toBe("/agents?tab=undeployed");
    });
});
