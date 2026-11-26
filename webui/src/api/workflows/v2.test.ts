// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import type { CreateWorkflowRequest } from "@/types/workflow";

import { createWorkflowV2, deleteWorkflowV2, getAllWorkflowsV2, postWorkflowDeploymentV2, updateWorkflowV2 } from "./v2";

const workflow = (overrides: Record<string, unknown> = {}) => ({
    id: "w1",
    type: "workflow",
    name: "orders",
    description: "Routes orders",
    appConfig: { nodes: [] },
    deploymentStatus: "not_deployed",
    runtimeStatus: { status: "unknown", problems: [] },
    createdBy: "u1",
    updatedBy: "u2",
    createdTime: "2026-01-02T03:04:05.678Z",
    updatedTime: null,
    ...overrides,
});

const deployment = (status: string, message?: string) => ({
    id: "d1",
    type: "workflowDeployment",
    workflowId: "w1",
    action: "deploy",
    status,
    failure: message ? { reason: "runtime_error", message, errorId: "e0" } : null,
    createdBy: "u1",
    createdTime: "2026-01-02T03:04:05.678Z",
    updatedTime: null,
});

const page = (data: unknown[], nextPage: number | null = null) => ({ data, meta: { pagination: { pageNumber: 1, count: data.length, pageSize: 100, nextPage, totalPages: 1 } } });

const fields = { name: "orders", description: "Routes orders", appConfig: { nodes: [] } };

describe("workflows on /api/v2", () => {
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

    test("reads every page and maps instants to the epoch ms the pages render", async () => {
        respond(200, page([workflow({ id: "w1" })], 2));
        respond(200, page([workflow({ id: "w2" })]));

        const workflows = await getAllWorkflowsV2();

        expect(call(1).url).toContain("/api/v2/platform/workflows?pageNumber=2");
        expect(workflows[0]).toMatchObject({ id: "w1", displayName: "orders", createdTime: Date.parse("2026-01-02T03:04:05.678Z"), updatedTime: 0, runtimeStatus: { status: "unknown" } });
        expect(workflows).toHaveLength(2);
    });

    test("create sends only name, description and appConfig", async () => {
        respond(201, { data: workflow() });

        await createWorkflowV2({ ...fields, displayName: "Orders" });

        expect(call(0)).toEqual({ url: "/api/v2/platform/workflows", method: "POST", body: fields });
    });

    test("update replaces the workflow with PUT and sends no displayName", async () => {
        respond(200, { data: workflow() });

        const resave: CreateWorkflowRequest = { ...fields, displayName: "Orders" };
        await expect(updateWorkflowV2("w1", resave)).resolves.toMatchObject({ id: "w1" });
        expect(call(0)).toEqual({ url: "/api/v2/platform/workflows/w1", method: "PUT", body: fields });
    });

    test("delete is a DELETE on the workflow", async () => {
        respond(204);

        await deleteWorkflowV2("w1");

        expect(call(0)).toMatchObject({ url: "/api/v2/platform/workflows/w1", method: "DELETE" });
    });

    describe("deploy", () => {
        test("posts workflowId and action", async () => {
            respond(201, { data: deployment("success") });

            await expect(postWorkflowDeploymentV2({ workflowId: "w1", action: "undeploy" })).resolves.toMatchObject({ workflowId: "w1", status: "success", createdAt: "2026-01-02T03:04:05.678Z" });
            expect(call(0)).toEqual({ url: "/api/v2/platform/workflowDeployments", method: "POST", body: { workflowId: "w1", action: "undeploy" } });
        });

        test("a 502 resolves with the failed record it points at", async () => {
            respond(502, { message: "Unable to deploy workflow", errorId: "e1" }, { Location: "/api/v2/platform/workflowDeployments/d1" });
            respond(200, { data: deployment("failed", "The workflow runtime did not complete the change. Try again, and contact your administrator with the errorId if it fails again.") });

            await expect(postWorkflowDeploymentV2({ workflowId: "w1", action: "deploy" })).resolves.toMatchObject({
                status: "failed",
                errorMessage: "The workflow runtime did not complete the change. Try again, and contact your administrator with the errorId if it fails again.",
            });
            expect(call(1).url).toBe("/api/v2/platform/workflowDeployments/d1");
        });
    });
});
