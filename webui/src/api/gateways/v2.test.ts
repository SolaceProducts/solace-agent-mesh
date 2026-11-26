// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { ApiV2Error } from "../common";

import { createGatewayV2, deployGatewayV2, getAllGatewaysV2, getGatewayDeploymentsV2, getGatewayNetworkingDetailsV2, getGatewaySchemasV2, getGatewayV2, getWhatsAppMessageTemplatesV2, saveGatewayV2, testGatewayConnectionV2 } from "./v2";

const entrypoint = (overrides: Record<string, unknown> = {}) => ({
    id: "ep-1",
    type: "entrypoint",
    entrypointType: "slack",
    slug: "orders",
    name: "Orders",
    description: "d",
    publicUrl: null,
    values: { bot_token: "********" },
    deploymentStatus: "not_deployed",
    syncStatus: null,
    deployed: null,
    runtimeStatus: { status: "unknown", problems: [] },
    createdBy: "u1",
    updatedBy: "u2",
    createdTime: "2026-01-02T03:04:05.678Z",
    updatedTime: null,
    ...overrides,
});

const failedDeploy = "The entrypoint runtime did not complete the change.";

const deployment = (status: string) => ({
    id: "d1",
    type: "entrypointDeployment",
    entrypointId: "ep-1",
    action: "deploy",
    status,
    failure: status === "failed" ? { reason: "runtime_error", message: failedDeploy, errorId: "e0" } : null,
    createdBy: "u1",
    createdTime: "2026-01-02T03:04:05.678Z",
    updatedTime: null,
});

const page = (data: unknown[], nextPage: number | null = null) => ({ data, meta: { pagination: { pageNumber: 1, count: data.length, pageSize: 100, nextPage, totalPages: 1 } } });

const fields = { name: "Orders", description: "d", type: "slack", values: {}, deploy: true };

describe("entrypoints on /api/v2", () => {
    const fetchMock = vi.fn<typeof fetch>();
    const respond = (status: number, body?: unknown, headers?: HeadersInit) => fetchMock.mockResolvedValueOnce(new Response(body === undefined ? null : JSON.stringify(body), { status, headers }));
    const call = (n: number) => ({ url: fetchMock.mock.calls[n][0], method: fetchMock.mock.calls[n][1]?.method, body: JSON.parse((fetchMock.mock.calls[n][1]?.body as string) ?? "null") });
    const deployFailed = (status = 502) => {
        respond(status, { message: "Deployment failed", errorId: "e1" }, { Location: "/api/v2/platform/entrypointDeployments/d1" });
        respond(200, { data: deployment("failed") });
    };

    beforeEach(() => {
        vi.stubGlobal("fetch", fetchMock);
    });

    afterEach(() => {
        fetchMock.mockReset();
        vi.unstubAllGlobals();
    });

    test("maps a v2 entrypoint onto the Gateway the pages render", async () => {
        respond(200, {
            data: entrypoint({
                deploymentStatus: "deployed",
                deployed: { name: "Orders", description: "d", entrypointType: "slack", values: {}, deployedTime: "2026-01-03T00:00:00.000Z", deployedBy: "u1" },
                runtimeStatus: { status: "degraded", problems: [{ key: "p1", severity: "error", detail: "down", sinceTime: "2026-01-04T00:00:00.000Z" }] },
            }),
        });

        const gateway = await getGatewayV2("ep-1");

        expect(call(0).url).toBe("/api/v2/platform/entrypoints/ep-1");
        expect(gateway).toMatchObject({ id: "ep-1", type: "slack", createdAt: "2026-01-02T03:04:05.678Z", updatedAt: "", syncStatus: undefined, publicUrl: undefined });
        expect(gateway.deployed).toMatchObject({ type: "slack", deployedAt: "2026-01-03T00:00:00.000Z" });
        expect(gateway.runtimeStatus).toEqual({ status: "degraded", problems: [{ id: "p1", severity: "error", detail: "down", since: "2026-01-04T00:00:00.000Z" }] });
    });

    test("reads every page of the list, sorted by name", async () => {
        respond(200, page([entrypoint({ id: "b", name: "b" })], 2));
        respond(200, page([entrypoint({ id: "a", name: "a" })]));

        const gateways = await getAllGatewaysV2();

        expect(gateways.map(g => g.id)).toEqual(["a", "b"]);
        expect(call(1).url).toContain("/api/v2/platform/entrypoints?pageNumber=2");
    });

    test("create sends entrypointType and no deploy field", async () => {
        respond(201, { data: entrypoint() });

        await createGatewayV2({ ...fields, slug: "orders", publicUrl: "https://x" });

        expect(call(0)).toEqual({ url: "/api/v2/platform/entrypoints", method: "POST", body: { name: "Orders", description: "d", entrypointType: "slack", values: {}, slug: "orders", publicUrl: "https://x" } });
    });

    test("create omits publicUrl and slug when not set", async () => {
        respond(201, { data: entrypoint() });

        await createGatewayV2(fields);

        expect(call(0).body).toEqual({ name: "Orders", description: "d", entrypointType: "slack", values: {} });
    });

    test("update sends only the mutable fields", async () => {
        respond(200, { data: entrypoint() });

        await saveGatewayV2("ep-1", { ...fields, deploy: false, publicUrl: "https://x" });

        expect(call(0)).toEqual({ url: "/api/v2/platform/entrypoints/ep-1", method: "PATCH", body: { name: "Orders", description: "d", publicUrl: "https://x", values: {} } });
    });

    describe("deploy", () => {
        test("posts entrypointId", async () => {
            respond(201, { data: deployment("success") });

            await expect(deployGatewayV2({ gatewayId: "ep-1", action: "deploy" })).resolves.toMatchObject({ gatewayId: "ep-1", status: "success" });
            expect(call(0)).toEqual({ url: "/api/v2/platform/entrypointDeployments", method: "POST", body: { entrypointId: "ep-1", action: "deploy" } });
        });

        test.each([502, 422])("a %i rejects with the reason from the record it points at", async status => {
            deployFailed(status);

            await expect(deployGatewayV2({ gatewayId: "ep-1", action: "deploy" })).rejects.toMatchObject({ status, message: failedDeploy, errorId: "e1" });
            expect(call(1).url).toBe("/api/v2/platform/entrypointDeployments/d1");
        });

        test("a record that cannot be read rejects with the deploy's error", async () => {
            respond(502, { message: "Deployment failed", errorId: "e1" }, { Location: "/api/v2/platform/entrypointDeployments/d1" });
            respond(404, { message: "Not found", errorId: "e2" });

            await expect(deployGatewayV2({ gatewayId: "ep-1", action: "deploy" })).rejects.toMatchObject({ status: 502, errorId: "e1" });
        });

        test("a 502 without a record rejects", async () => {
            respond(502, { message: "Deployment failed", errorId: "e1" });

            await expect(deployGatewayV2({ gatewayId: "ep-1", action: "deploy" })).rejects.toBeInstanceOf(ApiV2Error);
        });
    });

    describe("save", () => {
        test("without deploy it only saves", async () => {
            respond(201, { data: entrypoint() });

            await expect(saveGatewayV2(undefined, { ...fields, deploy: false })).resolves.toMatchObject({ id: "ep-1", deploymentStatus: "not_deployed" });
            expect(fetchMock).toHaveBeenCalledTimes(1);
        });

        test("a new entrypoint is created, then deployed", async () => {
            respond(201, { data: entrypoint() });
            respond(201, { data: deployment("success") });

            await expect(saveGatewayV2(undefined, fields)).resolves.toMatchObject({ deploymentStatus: "deployed" });
            expect(call(1).body).toEqual({ entrypointId: "ep-1", action: "deploy" });
        });

        test("a deployed entrypoint is updated, then redeployed", async () => {
            respond(200, { data: entrypoint({ deploymentStatus: "deployed" }) });
            respond(201, { data: deployment("success") });

            await saveGatewayV2("ep-1", fields);

            expect(call(0).method).toBe("PATCH");
            expect(call(1).body).toEqual({ entrypointId: "ep-1", action: "update" });
        });

        test("a failed deploy is not a failed save", async () => {
            respond(201, { data: entrypoint() });
            deployFailed();

            await expect(saveGatewayV2(undefined, fields)).resolves.toMatchObject({ id: "ep-1", deploymentStatus: "deploy_failed", deployError: failedDeploy });
        });

        test("a deploy that cannot be reached is not a failed save", async () => {
            respond(201, { data: entrypoint() });
            respond(503, { message: "Service unavailable", errorId: "e2" });

            await expect(saveGatewayV2(undefined, fields)).resolves.toMatchObject({ deploymentStatus: "deploy_failed", deployError: "Service unavailable" });
        });

        test("a failed save rejects", async () => {
            respond(422, { message: "Validation failed", errorId: "e3", validationDetails: { name: ["is required"] } });

            await expect(saveGatewayV2(undefined, fields)).rejects.toThrow("Validation failed: name: is required");
            expect(fetchMock).toHaveBeenCalledTimes(1);
        });
    });

    test("lists deployments for one entrypoint", async () => {
        respond(200, page([deployment("success")]));

        await expect(getGatewayDeploymentsV2("ep-1")).resolves.toMatchObject([{ id: "d1", gatewayId: "ep-1" }]);
        expect(call(0).url).toContain("/api/v2/platform/entrypointDeployments?entrypointId=ep-1&");
    });

    test("networking details of a type with no ingress are null", async () => {
        respond(200, { data: { dnsName: null, port: null, url: null, webhookUrl: null, apiPaths: [], publicUrl: null } });

        await expect(getGatewayNetworkingDetailsV2("ep-1")).resolves.toBeNull();
        expect(call(0).url).toBe("/api/v2/platform/entrypoints/ep-1/networkingDetails");
    });

    test("networking details on a shared ingress leave out what is null", async () => {
        const apiPaths = [{ path: "/api/messages", description: "Bot Framework messages" }];
        respond(200, { data: { dnsName: null, port: null, url: "/gw/ep-1", webhookUrl: "/gw/ep-1/api/messages", apiPaths, publicUrl: null } });

        await expect(getGatewayNetworkingDetailsV2("ep-1")).resolves.toEqual({ dnsName: undefined, port: undefined, url: "/gw/ep-1", webhookUrl: "/gw/ep-1/api/messages", apiPaths, publicUrl: null });
    });

    test("schemas are keyed by type, as v1's were", async () => {
        respond(200, page([{ id: "slack", type: "entrypointSchema", entrypointType: "slack", name: "Slack", description: "d", warning: null, schema: {}, requiredEnvVars: [] }]));

        await expect(getGatewaySchemasV2()).resolves.toEqual([{ type: "slack", name: "Slack", description: "d", warning: undefined, schema: {}, requiredEnvVars: [] }]);
        expect(call(0).url).toContain("/api/v2/platform/entrypointSchemas?");
    });

    test("template statuses come back in Meta's capitals, as v1's did", async () => {
        respond(200, { data: { templates: [{ name: "share_contact", language: "en_US", status: "pending_deletion", body: "b", usable: false, blockers: ["not_approved"] }], hasMore: false } });

        await expect(getWhatsAppMessageTemplatesV2({ businessAccountId: "1234567890", gatewayId: "ep-1" })).resolves.toEqual({
            data: [{ name: "share_contact", language: "en_US", status: "PENDING_DELETION", body: "b", usable: false, blockers: ["not_approved"] }],
            hasMore: false,
        });
    });

    test("a connection test is a PUT action and always carries a message", async () => {
        respond(200, { data: { success: true, message: null, errorClass: null, stage: null } });

        await expect(testGatewayConnectionV2({ type: "slack", values: { bot_token: "t" }, gatewayId: "ep-1" })).resolves.toEqual({ success: true, message: "" });
        expect(call(0)).toEqual({ url: "/api/v2/platform/entrypoints/testConnection", method: "PUT", body: { entrypointType: "slack", values: { bot_token: "t" }, entrypointId: "ep-1" } });
    });
});
