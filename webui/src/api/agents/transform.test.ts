// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, it } from "vitest";

import { createAgent, createDeployedAgent } from "@/testing/data/agents";
import { mockToolsets } from "@/testing/data/toolsets";
import type { Connector } from "@/types";

import { transformAgent } from "./transform";

describe("transformAgent", () => {
    it("marks an agent with additionalConfigurations.internal=true as internal", () => {
        const agent = createAgent("1", { additionalConfigurations: { internal: true } });
        expect(transformAgent(agent, [], []).isInternal).toBe(true);
    });

    it("leaves isInternal false when the internal flag is absent or not true", () => {
        expect(transformAgent(createAgent("2"), [], []).isInternal).toBe(false);
        expect(transformAgent(createAgent("3", { additionalConfigurations: {} }), [], []).isInternal).toBe(false);
        expect(transformAgent(createAgent("4", { additionalConfigurations: { internal: false } }), [], []).isInternal).toBe(false);
    });

    it("marks a workflow-type agent as isWorkflow", () => {
        const agent = createAgent("5", { type: "workflow" });
        expect(transformAgent(agent, [], []).isWorkflow).toBe(true);
    });

    it("leaves isWorkflow false for standard agents", () => {
        expect(transformAgent(createAgent("6", { type: "standard" }), [], []).isWorkflow).toBe(false);
        expect(transformAgent(createAgent("7"), [], []).isWorkflow).toBe(false);
    });

    it("carries the running agent's tool registrations through", () => {
        const agent = createAgent("8", { toolRegistrations: [{ toolType: "openapi", connectorId: "conn-1", status: "ready", toolCount: 2, toolNames: ["a", "b"] }] });
        expect(transformAgent(agent, [], []).toolRegistrations).toEqual([{ toolType: "openapi", connectorId: "conn-1", status: "ready", toolCount: 2, toolNames: ["a", "b"] }]);
    });

    describe("status derivation", () => {
        it("deployed + running → status 'deployed'", () => {
            const agent = createAgent("status-1", { deploymentStatus: "deployed", runtimeStatus: { status: "running" } });
            expect(transformAgent(agent, [], []).status).toBe("deployed");
        });

        it("deployed + degraded → status 'degraded', not folded into 'stopped' or 'deployed'", () => {
            const agent = createAgent("status-2", { deploymentStatus: "deployed", runtimeStatus: { status: "degraded", problems: [{ id: "queue:x", severity: "degraded", detail: "boom", since: "" }] } });
            expect(transformAgent(agent, [], []).status).toBe("degraded");
        });

        it("deployed + disconnected → status 'stopped'", () => {
            const agent = createAgent("status-3", { deploymentStatus: "deployed", runtimeStatus: { status: "disconnected" } });
            expect(transformAgent(agent, [], []).status).toBe("stopped");
        });

        it("deployed + no runtimeStatus → status 'stopped'", () => {
            const agent = createAgent("status-4", { deploymentStatus: "deployed" });
            expect(transformAgent(agent, [], []).status).toBe("stopped");
        });

        it("not deployed → status passes deploymentStatus through unchanged", () => {
            const agent = createAgent("status-5", { deploymentStatus: "not_deployed" });
            expect(transformAgent(agent, [], []).status).toBe("not_deployed");
        });

        it("carries runtimeProblems onto the deployed snapshot too", () => {
            const problems = [{ id: "queue:x", severity: "degraded" as const, detail: "boom", since: "" }];
            const agent = createDeployedAgent("status-6", { runtimeStatus: { status: "degraded", problems } });
            expect(transformAgent(agent, [], []).deployed?.runtimeProblems).toEqual(problems);
        });

        it("degraded + running deployed snapshot → deployed.status is also 'degraded', not flattened to 'deployed'", () => {
            const agent = createDeployedAgent("status-7", { runtimeStatus: { status: "degraded", problems: [{ id: "queue:x", severity: "degraded", detail: "boom", since: "" }] } });
            expect(transformAgent(agent, [], []).deployed?.status).toBe("degraded");
        });

        it("disconnected deployed snapshot → deployed.status is also 'stopped', not flattened to 'deployed'", () => {
            const agent = createDeployedAgent("status-8", { runtimeStatus: { status: "disconnected" } });
            expect(transformAgent(agent, [], []).deployed?.status).toBe("stopped");
        });

        it("healthy deployed snapshot → deployed.status is 'deployed'", () => {
            const agent = createDeployedAgent("status-9", { runtimeStatus: { status: "running" } });
            expect(transformAgent(agent, [], []).deployed?.status).toBe("deployed");
        });

        it("not deployed + stale degraded runtimeStatus → status follows deploymentStatus, runtimeProblems is not carried over", () => {
            const agent = createAgent("status-10", {
                deploymentStatus: "not_deployed",
                runtimeStatus: { status: "degraded", problems: [{ id: "queue:x", severity: "degraded", detail: "boom", since: "" }] },
            });
            const info = transformAgent(agent, [], []);
            expect(info.status).toBe("not_deployed");
            expect(info.runtimeProblems).toBeUndefined();
        });
    });

    describe("deployed connectorObjects", () => {
        const apiConnectorValues = {
            specification_url: "https://example.com/openapi.json",
            allow_list: "getPets,createPet",
        };
        const apiConnector = {
            id: "conn-api-1",
            name: "Petstore API",
            description: "Petstore OpenAPI connector",
            type: "api",
            subtype: "openapi",
            values: apiConnectorValues,
        } as Connector;
        const snapshot = { id: "conn-api-1", name: "Petstore API", type: "api" } as Connector;

        it("merges the live connector's values into the deployed snapshot so api/mcp tools can load", () => {
            const agent = createDeployedAgent("8", { connectors: ["conn-api-1"] }, { connectorSnapshots: [snapshot] });

            const deployedObjects = transformAgent(agent, [], [apiConnector]).deployed?.connectorObjects;

            expect(deployedObjects).toHaveLength(1);
            expect(deployedObjects?.[0].values).toEqual(apiConnectorValues);
            expect(deployedObjects?.[0].name).toBe("Petstore API");
        });

        it("leaves values empty and marks the name when the connector no longer exists", () => {
            const agent = createDeployedAgent("9", { connectors: ["conn-api-1"] }, { connectorSnapshots: [snapshot] });

            const deployedObjects = transformAgent(agent, [], []).deployed?.connectorObjects;

            expect(deployedObjects).toHaveLength(1);
            expect(deployedObjects?.[0].values).toEqual({});
            expect(deployedObjects?.[0].name).toBe("Petstore API (NOT FOUND)");
        });

        it("takes the name, type and subtype from the live connector when v2 sends only the id", () => {
            const agent = createDeployedAgent("11", { connectors: ["conn-api-1"] }, { connectorSnapshots: [{ id: "conn-api-1" } as Connector] });

            const deployedObjects = transformAgent(agent, [], [apiConnector]).deployed?.connectorObjects;

            expect(deployedObjects?.[0]).toMatchObject({ name: "Petstore API", type: "api", subtype: "openapi", values: apiConnectorValues });
        });

        it("carries the top-level agent's tool registrations onto the deployed version", () => {
            const regs = [{ toolType: "openapi", connectorId: "conn-api-1", status: "ready" as const, toolCount: 2, toolNames: ["getPets", "createPet"] }];
            const agent = createDeployedAgent("10", { connectors: ["conn-api-1"], toolRegistrations: regs }, { connectorSnapshots: [snapshot], toolRegistrations: [] });

            expect(agent.deployed?.toolRegistrations).toEqual([]);
            expect(transformAgent(agent, [], [apiConnector]).deployed?.toolRegistrations).toEqual(regs);
        });
    });

    describe("toolset resolution", () => {
        it("falls back to a humanized label when a toolset ref matches neither catalog", () => {
            const agent = createAgent("11", { toolsets: ["activity_reporting"] });

            const result = transformAgent(agent, [], [], []);

            expect(result.tools).toHaveLength(1);
            expect(result.tools?.[0]).toEqual({ id: "activity_reporting", name: "Activity Reporting", description: "", tags: [] });
        });

        it("resolves a catalog toolset alongside an unresolved one, sorted together", () => {
            const agent = createAgent("12", { toolsets: ["web", "activity_reporting"] });

            const result = transformAgent(agent, mockToolsets, [], []);

            const webToolset = mockToolsets.find(ts => ts.id === "web")!;
            expect(result.tools).toEqual([
                { id: "activity_reporting", name: "Activity Reporting", description: "", tags: [] },
                { id: webToolset.id, name: webToolset.name, description: webToolset.description, tags: [] },
            ]);
        });
    });
});
