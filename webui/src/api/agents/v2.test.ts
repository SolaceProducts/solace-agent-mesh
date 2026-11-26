// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { createAgentV2, deleteAgentV2, getAgentV2, getAllAgentsV2, getConnectorAgentsV2, updateAgentV2 } from "./v2";

const agent = (overrides: Record<string, unknown> = {}) => ({
    id: "a1",
    type: "agent",
    agentType: "standard",
    name: "Orders",
    instanceName: "agent_a1",
    description: "d",
    systemPrompt: "You handle orders.",
    toolsets: [],
    toolsetExcludedTools: null,
    toolsetConfigs: [],
    skillConfigs: [],
    skills: [],
    skillRefs: [],
    skillIds: [],
    inputModes: ["text"],
    outputModes: ["text"],
    connectorIds: ["c1"],
    modelProvider: [],
    additionalConfigurations: null,
    deploymentStatus: "not_deployed",
    syncStatus: null,
    deployed: null,
    runtimeStatus: { status: "unknown", problems: [] },
    toolRegistrations: [],
    isEphemeral: false,
    sourceSessionId: null,
    sourceComponentName: null,
    sourceComponentKind: null,
    expiresTime: null,
    createdBy: "u1",
    updatedBy: "u2",
    createdTime: "2026-01-02T03:04:05.678Z",
    updatedTime: null,
    ...overrides,
});

const deployment = (status: string, message?: string) => ({
    id: "d1",
    type: "agentDeployment",
    agentId: "a1",
    action: "deploy",
    status,
    failure: message ? { reason: "runtime_error", message, errorId: "e0" } : null,
    createdBy: "u1",
    createdTime: "2026-01-02T03:04:05.678Z",
    updatedTime: null,
});

const page = (data: unknown[], nextPage: number | null = null) => ({ data, meta: { pagination: { pageNumber: 1, count: data.length, pageSize: 100, nextPage, totalPages: 1 } } });

const fields = { name: "Orders", description: "d", systemPrompt: "You handle orders.", type: "standard", connectors: ["c1"], toolsets: [], deploy: true };

describe("agents on /api/v2", () => {
    const fetchMock = vi.fn<typeof fetch>();
    const respond = (status: number, body?: unknown, headers?: HeadersInit) => fetchMock.mockResolvedValueOnce(new Response(body === undefined ? null : JSON.stringify(body), { status, headers }));
    const call = (n: number) => ({ url: fetchMock.mock.calls[n][0], method: fetchMock.mock.calls[n][1]?.method, body: JSON.parse((fetchMock.mock.calls[n][1]?.body as string) ?? "null") });

    beforeEach(() => {
        vi.stubGlobal("fetch", fetchMock);
    });

    afterEach(() => {
        fetchMock.mockReset();
        vi.unstubAllGlobals();
    });

    test("maps a v2 agent onto the Agent the pages render", async () => {
        respond(200, {
            data: agent({
                syncStatus: "in_sync",
                deployed: {
                    name: "Orders",
                    description: "d",
                    systemPrompt: "p",
                    toolsets: [],
                    inputModes: [],
                    outputModes: [],
                    connectorIds: ["c1"],
                    additionalConfigurations: null,
                    skillIds: [],
                    skillRefs: [],
                    skills: [],
                    deployedTime: "2026-01-03T00:00:00.000Z",
                    deployedBy: "u1",
                },
            }),
        });

        const got = await getAgentV2("a1");

        expect(call(0).url).toBe("/api/v2/platform/agents/a1");
        expect(got).toMatchObject({ id: "a1", type: "standard", connectors: ["c1"], syncStatus: "in_sync", expiresAt: undefined, createdAt: "2026-01-02T03:04:05.678Z", updatedAt: "" });
        expect(got.deployed).toMatchObject({ connectors: ["c1"], connectorSnapshots: [{ id: "c1" }], deployedAt: "2026-01-03T00:00:00.000Z" });
    });

    test("a member v2 sends as null is left out, as v1 leaves it out", async () => {
        respond(200, {
            data: agent({
                skills: [{ id: null, name: "Track", description: "d", tags: [], examples: [], inputModes: [], outputModes: [] }],
                toolsetConfigs: [{ toolsetName: "web", configValues: null, volumeBindings: null, networkBindings: null }],
                skillConfigs: [{ skillId: "s1", skillName: null, configValues: null }],
                toolRegistrations: [{ connectorId: null, name: null, status: "registered", error: null }],
            }),
        });

        const got = await getAgentV2("a1");

        for (const key of ["toolsetExcludedTools", "additionalConfigurations", "syncStatus", "deployed", "sourceSessionId", "sourceComponentName", "sourceComponentKind"]) expect(got[key as keyof typeof got]).toBeUndefined();
        expect(got.skills?.[0]).not.toHaveProperty("id");
        expect(got.toolsetConfigs?.[0]).toEqual({ toolsetName: "web", configValues: {} });
        expect(got.skillConfigs?.[0]).toEqual({ skillId: "s1", configValues: {} });
        expect(got.toolRegistrations?.[0]).toEqual({ status: "registered" });
    });

    test("reads every page of the list and leaves out ephemeral agents", async () => {
        respond(200, page([agent({ id: "a1" })], 2));
        respond(200, page([agent({ id: "a2", isEphemeral: true })]));

        const agents = await getAllAgentsV2();

        expect(agents.map(a => a.id)).toEqual(["a1"]);
        expect(call(1).url).toContain("/api/v2/platform/agents?pageNumber=2");
    });

    test("connector agents filter by connectorId across every page", async () => {
        respond(200, page([agent({ id: "a1" })], 2));
        respond(200, page([agent({ id: "a2" })]));

        await expect(getConnectorAgentsV2("c 1")).resolves.toHaveLength(2);
        expect(call(0).url).toContain("/api/v2/platform/agents?connectorId=c%201&pageNumber=1");
    });

    describe("create", () => {
        test("sends agentType and connectorIds, and no deploy field", async () => {
            respond(201, { data: agent() });

            await createAgentV2({ ...fields, deploy: false });

            expect(fetchMock).toHaveBeenCalledTimes(1);
            expect(call(0)).toEqual({ url: "/api/v2/platform/agents", method: "POST", body: { name: "Orders", description: "d", systemPrompt: "You handle orders.", agentType: "standard", connectorIds: ["c1"], toolsets: [] } });
        });

        test("deploys the saved agent", async () => {
            respond(201, { data: agent() });
            respond(201, { data: deployment("success") });

            await expect(createAgentV2(fields)).resolves.toMatchObject({ id: "a1", deploymentStatus: "deployed" });
            expect(call(1)).toEqual({ url: "/api/v2/platform/agentDeployments", method: "POST", body: { agentId: "a1", action: "deploy" } });
            expect(fetchMock).toHaveBeenCalledTimes(2);
        });

        test("an agent without instructions is saved but not deployed, as on v1", async () => {
            respond(201, { data: agent({ systemPrompt: "" }) });

            await createAgentV2({ ...fields, systemPrompt: "" });

            expect(fetchMock).toHaveBeenCalledTimes(1);
        });

        test("a failed deploy is not a failed save", async () => {
            respond(201, { data: agent() });
            respond(502, { message: "Unable to deploy agent", errorId: "e1" }, { Location: "/api/v2/platform/agentDeployments/d1" });
            respond(200, { data: deployment("failed", "The agent runtime did not complete the change. Try again, and contact your administrator with the errorId if it fails again.") });

            await expect(createAgentV2(fields)).resolves.toMatchObject({
                id: "a1",
                deploymentStatus: "deploy_failed",
                deployError: "The agent runtime did not complete the change. Try again, and contact your administrator with the errorId if it fails again.",
            });
        });

        test("a deploy that cannot be reached is not a failed save", async () => {
            respond(201, { data: agent() });
            respond(503, { message: "Deployment service not available", errorId: "e2" });

            await expect(createAgentV2(fields)).resolves.toMatchObject({ id: "a1", deploymentStatus: "deploy_failed", deployError: "Deployment service not available" });
        });
    });

    describe("update", () => {
        test("replaces the agent with PUT and sends no agentType", async () => {
            respond(200, { data: agent() });

            await updateAgentV2("a1", { ...fields, deploy: false });

            expect(call(0)).toEqual({ url: "/api/v2/platform/agents/a1", method: "PUT", body: { name: "Orders", description: "d", systemPrompt: "You handle orders.", connectorIds: ["c1"], toolsets: [] } });
        });

        test("a deployed agent is redeployed as an update", async () => {
            respond(200, { data: agent({ deploymentStatus: "deployed" }) });
            respond(201, { data: deployment("success") });

            await updateAgentV2("a1", fields);

            expect(call(1).body).toEqual({ agentId: "a1", action: "update" });
            expect(fetchMock).toHaveBeenCalledTimes(2);
        });

        test("a failed save rejects without deploying", async () => {
            respond(422, { message: "Validation failed", errorId: "e3", validationDetails: { name: ["is required"] } });

            await expect(updateAgentV2("a1", fields)).rejects.toThrow("Validation failed: name: is required");
            expect(fetchMock).toHaveBeenCalledTimes(1);
        });
    });

    test("delete is a DELETE on the agent", async () => {
        respond(204);

        await deleteAgentV2("a1");

        expect(call(0)).toMatchObject({ url: "/api/v2/platform/agents/a1", method: "DELETE" });
    });
});
