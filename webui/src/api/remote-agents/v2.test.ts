// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { ApiV2Error } from "../common";

import { createRemoteAgentV2, deleteRemoteAgentV2, deployRemoteAgentV2, fetchAgentCardV2, getAllRemoteAgentsV2, getRemoteAgentV2, updateRemoteAgentV2 } from "./v2";

const remoteAgent = (overrides: Record<string, unknown> = {}) => ({
    id: "r1",
    type: "remoteAgent",
    name: "Billing",
    instanceName: "agent_r1",
    description: "d",
    agentUrl: "https://billing.example.com",
    agentCardLocation: "well_known",
    agentCardUrl: null,
    agentCardHeaders: null,
    agentCardAuthType: null,
    agentCardAuthConfig: null,
    agentAuthType: "none",
    agentAuthConfig: null,
    taskHeaders: null,
    requestTimeoutSeconds: 60,
    skills: [],
    inputModes: ["text"],
    outputModes: ["text"],
    deploymentStatus: "not_deployed",
    syncStatus: null,
    runtimeStatus: { status: "unknown", problems: [] },
    deployed: null,
    cardData: null,
    cardFetchError: null,
    createdBy: "u1",
    updatedBy: "u2",
    createdTime: "2026-01-02T03:04:05.678Z",
    updatedTime: null,
    ...overrides,
});

const page = (data: unknown[], nextPage: number | null = null) => ({ data, meta: { pagination: { pageNumber: 1, count: data.length, pageSize: 100, nextPage, totalPages: 1 } } });

const request = { agentUrl: "https://billing.example.com", name: "Billing", agentCardLocation: "well_known" as const, agentAuthConfig: { type: "none" as const } };

describe("remote agents on /api/v2", () => {
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

    test("maps a v2 remote agent onto the model the pages render", async () => {
        respond(200, {
            data: remoteAgent({
                syncStatus: "in_sync",
                deployed: { name: "Billing", description: "d", agentUrl: "https://billing.example.com", deployedTime: "2026-01-03T00:00:00.000Z", deployedBy: "u1" },
            }),
        });

        const got = await getRemoteAgentV2("r1", true);

        expect(call(0).url).toBe("/api/v2/platform/remoteAgents/r1?expand=card");
        expect(got).toMatchObject({ id: "r1", syncStatus: "in_sync", createdAt: "2026-01-02T03:04:05.678Z", updatedAt: "" });
        expect(got.deployed).toMatchObject({ deployedAt: "2026-01-03T00:00:00.000Z" });
        expect(got).not.toHaveProperty("createdTime");
    });

    test("a member v2 sends as null is left out, as v1 leaves it out", async () => {
        respond(200, {
            data: remoteAgent({
                description: null,
                agentCardAuthConfig: { type: "bearer", token: null, apiKey: null, headerName: null, tokenEndpoint: null, clientId: null, clientSecret: null, scopes: null, username: null, password: null },
                deployed: { name: "Billing", description: null, agentUrl: "https://billing.example.com", agentCardUrl: null, agentCardAuthType: null, deployedTime: null, deployedBy: "u1" },
            }),
        });

        const got = await getRemoteAgentV2("r1", false);

        for (const key of ["description", "agentCardUrl", "agentCardHeaders", "agentCardAuthType", "taskHeaders", "syncStatus", "cardData", "cardFetchError"]) expect(got).not.toHaveProperty(key);
        expect(got.agentAuthConfig).toBeUndefined();
        expect(got.agentCardAuthConfig).toEqual({ type: "bearer" });
        expect(got.deployed).toEqual({ name: "Billing", agentUrl: "https://billing.example.com", deployedAt: "", deployedBy: "u1" });
    });

    test("a remote agent that has not reported its A2A protocol maps to no remoteProtocol", async () => {
        respond(200, { data: remoteAgent({ remoteProtocol: null }) });

        expect(await getRemoteAgentV2("r1", false)).not.toHaveProperty("remoteProtocol");
    });

    test("a reported A2A protocol, pin and card trust map through unchanged, in the list and the single GET", async () => {
        const remoteProtocol = { selectedVersion: "v0_3", binding: "http_json", deprecated: true, cardSignature: "refused" };
        const trust = { protocolVersion: "v0_3", trustedCardJwksUrls: ["https://keys.example.com/jwks.json"], requireSignedCard: true };
        respond(200, { data: remoteAgent({ remoteProtocol, ...trust }) });
        respond(200, page([remoteAgent({ remoteProtocol, ...trust })]));

        expect(await getRemoteAgentV2("r1", false)).toMatchObject({ remoteProtocol, ...trust });
        expect((await getAllRemoteAgentsV2())[0]).toMatchObject({ remoteProtocol, ...trust });
    });

    test("reads every page of the list", async () => {
        respond(200, page([remoteAgent({ id: "r1" })], 2));
        respond(200, page([remoteAgent({ id: "r2" })]));

        await expect(getAllRemoteAgentsV2()).resolves.toHaveLength(2);
        expect(call(1).url).toContain("/api/v2/platform/remoteAgents?pageNumber=2");
    });

    test.each([404, 501])("a %i list reads as no remote agents, as on v1", async status => {
        respond(status, { message: "Not found", errorId: "e1" });

        await expect(getAllRemoteAgentsV2()).resolves.toEqual([]);
    });

    test("any other list failure rejects", async () => {
        respond(500, { message: "Unable to list remote agents", errorId: "e1" });

        await expect(getAllRemoteAgentsV2()).rejects.toBeInstanceOf(ApiV2Error);
    });

    test("create posts the request as it is", async () => {
        respond(201, { data: remoteAgent() });

        await createRemoteAgentV2(request);

        expect(call(0)).toEqual({ url: "/api/v2/platform/remoteAgents", method: "POST", body: request });
    });

    test("update replaces the remote agent with PUT", async () => {
        respond(200, { data: remoteAgent() });

        await updateRemoteAgentV2("r1", request);

        expect(call(0)).toEqual({ url: "/api/v2/platform/remoteAgents/r1", method: "PUT", body: request });
    });

    test("delete is a DELETE on the remote agent", async () => {
        respond(204);

        await deleteRemoteAgentV2("r1");

        expect(call(0)).toMatchObject({ url: "/api/v2/platform/remoteAgents/r1", method: "DELETE" });
    });

    test("deploy and undeploy go through agentDeployments", async () => {
        respond(201, { data: { id: "d1", agentId: "r1", action: "undeploy", status: "success", createdBy: "u1", createdTime: null, updatedTime: null } });

        await expect(deployRemoteAgentV2("r1", "undeploy")).resolves.toMatchObject({ agentId: "r1", status: "success" });
        expect(call(0)).toEqual({ url: "/api/v2/platform/agentDeployments", method: "POST", body: { agentId: "r1", action: "undeploy" } });
    });

    test("fetching a card is a PUT action", async () => {
        const card = { name: "Billing", description: "d", url: "https://billing.example.com", skills: [] };
        respond(200, { data: card });
        const agentCardUrl = "https://billing.example.com/.well-known/agent-card.json";

        await expect(fetchAgentCardV2({ agentCardUrl, authConfig: null, customHeaders: null })).resolves.toEqual(card);
        expect(call(0)).toEqual({ url: "/api/v2/platform/remoteAgents/fetchCard", method: "PUT", body: { agentCardUrl, customHeaders: null } });
    });

    test("a card fetch's auth errors go in the message, since the connect form has no field to mark", async () => {
        respond(422, { message: "Validation failed", errorId: "e1", validationDetails: { "authConfig.token": ["is required"], agentCardUrl: ["must be an http:// or https:// URL"] } });

        const error = await fetchAgentCardV2({ agentCardUrl: "ftp://billing.example.com/card", authenticationType: "bearer", authConfig: null, customHeaders: null }).catch(e => e);

        expect(error).toBeInstanceOf(ApiV2Error);
        expect(error.validationDetails).toBeUndefined();
        expect(error.message).toContain("authConfig.token");
        expect(error.message).toContain("agentCardUrl");
    });

    test("a card fetch's URL error stays on the field, so the connect form can mark it", async () => {
        const validationDetails = { agentCardUrl: ["must be an http:// or https:// URL"] };
        respond(422, { message: "Validation failed", errorId: "e1", validationDetails });

        const error = await fetchAgentCardV2({ agentCardUrl: "ftp://billing.example.com/card", authConfig: null, customHeaders: null }).catch(e => e);

        expect(error).toBeInstanceOf(ApiV2Error);
        expect(error.validationDetails).toEqual(validationDetails);
    });

    test("a card fetch sends its credentials as a remote agent's auth object", async () => {
        respond(200, { data: { name: "Billing", description: "d", skills: [] } });

        await fetchAgentCardV2({
            agentCardUrl: "https://billing.example.com/card",
            authenticationType: "oauth2",
            authConfig: { oauth2TokenUrl: "https://idp.example.com/token", oauth2ClientId: "id", oauth2ClientSecret: "secret", oauth2Scope: "read write" },
            customHeaders: null,
        });
        expect(call(0).body).toEqual({
            agentCardUrl: "https://billing.example.com/card",
            authConfig: { type: "oauth2", tokenEndpoint: "https://idp.example.com/token", clientId: "id", clientSecret: "secret", scopes: ["read", "write"] },
            customHeaders: null,
        });
    });
});
