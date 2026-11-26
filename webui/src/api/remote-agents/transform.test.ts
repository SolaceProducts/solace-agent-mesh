// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import type { RemoteAgent } from "@/types";

import { transformRemoteAgent } from "./transform";

const baseRemoteAgent = (overrides: Partial<RemoteAgent>): RemoteAgent =>
    ({
        id: "ra-1",
        name: "MyRemoteAgent",
        agentUrl: "https://example.com/agent",
        description: "desc",
        agentAuthType: "none",
        createdBy: "user@example.com",
        updatedBy: "system",
        createdAt: "2026-01-29T10:00:00.000000+00:00",
        updatedAt: "2026-01-29T10:00:00.000000+00:00",
        ...overrides,
    }) as RemoteAgent;

const deployedSnapshot: RemoteAgent["deployed"] = {
    agentUrl: "https://example.com/agent",
    name: "MyRemoteAgent",
    description: "deployed snapshot",
    agentCardLocation: "well_known",
    agentAuthType: "none",
    requestTimeoutSeconds: 60,
    deployedAt: "2026-01-29T08:00:00.000+00:00",
    deployedBy: "user@example.com",
};

describe("transformRemoteAgent", () => {
    test("deployed + running → status 'deployed'", () => {
        const info = transformRemoteAgent(baseRemoteAgent({ deploymentStatus: "deployed", runtimeStatus: { status: "running" } }));
        expect(info.status).toBe("deployed");
        expect(info.type).toBe("external");
    });

    test("deployed + degraded → status 'degraded', not folded into 'deployed' or 'stopped'", () => {
        const problems = [{ id: "queue:x", severity: "degraded" as const, detail: "boom", since: "" }];
        const info = transformRemoteAgent(baseRemoteAgent({ deploymentStatus: "deployed", runtimeStatus: { status: "degraded", problems } }));
        expect(info.status).toBe("degraded");
        expect(info.runtimeProblems).toEqual(problems);
    });

    test("deployed + not running → status 'stopped' (Not Running badge)", () => {
        const info = transformRemoteAgent(baseRemoteAgent({ deploymentStatus: "deployed", runtimeStatus: { status: "disconnected" } }));
        expect(info.status).toBe("stopped");
    });

    test("deployed-but-not-running deployed snapshot also surfaces 'stopped' so the active view keeps the Not Running badge", () => {
        const info = transformRemoteAgent(baseRemoteAgent({ deploymentStatus: "deployed", runtimeStatus: { status: "disconnected" }, deployed: deployedSnapshot }));
        expect(info.deployed?.status).toBe("stopped");
    });

    test("deployed + running keeps the deployed snapshot status as 'deployed'", () => {
        const info = transformRemoteAgent(baseRemoteAgent({ deploymentStatus: "deployed", runtimeStatus: { status: "running" }, deployed: deployedSnapshot }));
        expect(info.deployed?.status).toBe("deployed");
    });

    test("no-card (agentCardLocation 'none') + not running stays 'deployed' — Registered, no heartbeat expected", () => {
        const info = transformRemoteAgent(baseRemoteAgent({ deploymentStatus: "deployed", runtimeStatus: { status: "disconnected" }, agentCardLocation: "none" }));
        expect(info.status).toBe("deployed");
    });

    test("no-card + running also stays 'deployed'", () => {
        const info = transformRemoteAgent(baseRemoteAgent({ deploymentStatus: "deployed", runtimeStatus: { status: "running" }, agentCardLocation: "none" }));
        expect(info.status).toBe("deployed");
    });

    test("no-card deployed snapshot also stays 'deployed' so the comparison view keeps the Registered badge", () => {
        const info = transformRemoteAgent(baseRemoteAgent({ deploymentStatus: "deployed", runtimeStatus: { status: "disconnected" }, agentCardLocation: "none", deployed: { ...deployedSnapshot, agentCardLocation: "none" } }));
        expect(info.deployed?.status).toBe("deployed");
    });

    test("no-card + deploy_failed still surfaces 'deploy_failed' (exemption only skips the running downgrade)", () => {
        const info = transformRemoteAgent(baseRemoteAgent({ deploymentStatus: "deploy_failed", runtimeStatus: { status: "disconnected" }, agentCardLocation: "none" }));
        expect(info.status).toBe("deploy_failed");
    });

    test("no-card + degraded still surfaces 'degraded' — the proxy's own health, not the remote's, so the exemption must not swallow it", () => {
        const problems = [{ id: "queue:x", severity: "degraded" as const, detail: "boom", since: "" }];
        const info = transformRemoteAgent(baseRemoteAgent({ deploymentStatus: "deployed", runtimeStatus: { status: "degraded", problems }, agentCardLocation: "none" }));
        expect(info.status).toBe("degraded");
        expect(info.runtimeProblems).toEqual(problems);
    });

    test("not_deployed → status 'not_deployed'", () => {
        const info = transformRemoteAgent(baseRemoteAgent({ deploymentStatus: "not_deployed" }));
        expect(info.status).toBe("not_deployed");
    });

    test("not_deployed + stale degraded runtimeStatus → status follows deploymentStatus, runtimeProblems is not carried over", () => {
        const info = transformRemoteAgent(baseRemoteAgent({ deploymentStatus: "not_deployed", runtimeStatus: { status: "degraded", problems: [{ id: "queue:x", severity: "degraded", detail: "boom", since: "" }] } }));
        expect(info.status).toBe("not_deployed");
        expect(info.runtimeProblems).toBeUndefined();
    });
});

describe("transformRemoteAgent — remote protocol", () => {
    test("marks an agent reached over the older A2A 0.3, in both the current and the deployed view", () => {
        const remoteProtocol = { selectedVersion: "v0_3", binding: "jsonrpc", deprecated: true, cardSignature: "unchecked" } as const;
        const agent = transformRemoteAgent(baseRemoteAgent({ agentCardLocation: "well_known", deploymentStatus: "deployed", syncStatus: "in_sync", deployed: deployedSnapshot, remoteProtocol }));
        expect(agent.remoteProtocol).toEqual(remoteProtocol);
        expect(agent.deployed?.remoteProtocol).toEqual(remoteProtocol);
    });

    test("leaves an undeployed agent unmarked even when a stale report remains", () => {
        const remoteProtocol = { selectedVersion: "v0_3", binding: "jsonrpc", deprecated: true, cardSignature: "unchecked" } as const;
        const agent = transformRemoteAgent(baseRemoteAgent({ agentCardLocation: "well_known", deploymentStatus: "not_deployed", deployed: deployedSnapshot, remoteProtocol }));
        expect(agent.remoteProtocol).toBeUndefined();
        expect(agent.deployed?.remoteProtocol).toBeUndefined();
    });

    test("marks only the deployed view of an agent whose draft differs from what is deployed", () => {
        const remoteProtocol = { selectedVersion: "v0_3", binding: "jsonrpc", deprecated: true, cardSignature: "unchecked" } as const;
        const agent = transformRemoteAgent(baseRemoteAgent({ agentCardLocation: "well_known", deploymentStatus: "deployed", syncStatus: "out_of_sync", deployed: deployedSnapshot, remoteProtocol }));
        expect(agent.remoteProtocol).toBeUndefined();
        expect(agent.deployed?.remoteProtocol).toEqual(remoteProtocol);
    });

    test("leaves an agent that has not reported unmarked", () => {
        expect(transformRemoteAgent(baseRemoteAgent({ agentCardLocation: "well_known" })).remoteProtocol).toBeUndefined();
    });
});
