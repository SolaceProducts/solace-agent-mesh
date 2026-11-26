// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { ApiV2Error } from "../common";

import { createToolsetV2, deleteToolsetV2, downloadToolsetBundleV2, getBuiltinToolsetsV2, getCustomToolsetsV2, getToolsetBuildTargetV2, getToolsetConfigV2, patchToolsetConfigV2, updateToolsetV2, uploadToolsetBundleV2 } from "./v2";

const tool = { toolName: "lookup", description: "Look up an order.", timeoutSeconds: 30, sandboxProfile: "standard", parameters: { type: "object" }, configSchema: [{ key: "region", type: "string" }] };

const toolset = (overrides: Record<string, unknown> = {}) => ({
    id: "ts-1",
    type: "toolset",
    origin: "custom",
    name: "order_tools",
    description: "Orders.",
    runtime: "python",
    discoveryStatus: "ready",
    discoveryErrors: [],
    bundle: { filename: "order_tools.zip", sizeBytes: 127, contentHash: "abc" },
    tools: [tool],
    agents: [{ id: "a1", name: "Support", description: null }],
    configSummary: null,
    capabilities: {},
    default: false,
    aliases: [],
    createdBy: "u1",
    updatedBy: "u2",
    createdTime: "2026-01-02T03:04:05.678Z",
    updatedTime: null,
    ...overrides,
});

const page = (data: unknown[], nextPage: number | null = null) => ({ data, meta: { pagination: { pageNumber: 1, count: data.length, pageSize: 100, nextPage, totalPages: 1 } } });

describe("toolsets on /api/v2", () => {
    const fetchMock = vi.fn<typeof fetch>();
    const respond = (status: number, body?: unknown, headers?: HeadersInit) => fetchMock.mockResolvedValueOnce(new Response(body === undefined ? null : JSON.stringify(body), { status, headers }));
    const call = (n: number) => ({ url: fetchMock.mock.calls[n][0], method: fetchMock.mock.calls[n][1]?.method, body: fetchMock.mock.calls[n][1]?.body });

    beforeEach(() => {
        vi.stubGlobal("fetch", fetchMock);
    });

    afterEach(() => {
        fetchMock.mockReset();
        vi.unstubAllGlobals();
    });

    test("maps a v2 toolset onto the Toolset the pages render, rebuilding files from tools", async () => {
        respond(200, page([toolset()]));
        const [got] = await getCustomToolsetsV2();
        expect(call(0).url).toContain("/api/v2/platform/toolsets?origin=custom&pageNumber=1&pageSize=100");
        expect(got).toMatchObject({
            id: "ts-1",
            type: "custom",
            description: "Orders.",
            zipFilename: "order_tools.zip",
            zipFileSize: 127,
            contentHash: "abc",
            createdAt: "2026-01-02T03:04:05.678Z",
            updatedAt: "",
            agents: [{ id: "a1", name: "Support", description: undefined }],
            tools: [{ toolName: "lookup", toolDescription: "Look up an order.", timeoutSeconds: 30 }],
        });
        expect(got.files).toEqual([
            {
                id: "lookup",
                filename: "",
                toolName: "lookup",
                toolFunction: null,
                toolDescription: "Look up an order.",
                timeoutSeconds: 30,
                sandboxProfile: "standard",
                modulePath: null,
                fileSize: 0,
                configSchema: [{ key: "region", type: "string" }],
                createdAt: "2026-01-02T03:04:05.678Z",
            },
        ]);
    });

    test("a custom tool v2 sends without a description, timeout or sandbox profile gets v1's file defaults", async () => {
        respond(200, page([toolset({ tools: [{ toolName: "ping", description: null, timeoutSeconds: null, sandboxProfile: null, parameters: null, configSchema: null }] })]));
        const [got] = await getCustomToolsetsV2();
        expect(got.files).toEqual([
            {
                id: "ping",
                filename: "",
                toolName: "ping",
                toolFunction: null,
                toolDescription: "",
                timeoutSeconds: 0,
                sandboxProfile: "",
                modulePath: null,
                fileSize: 0,
                configSchema: null,
                createdAt: "2026-01-02T03:04:05.678Z",
            },
        ]);
    });

    test("a built-in has no files, no bundle and empty audit fields, as on v1", async () => {
        respond(
            200,
            page([
                toolset({
                    id: "builtin_artifact_tools",
                    origin: "builtin",
                    name: "Artifact Tools",
                    runtime: null,
                    discoveryStatus: null,
                    bundle: null,
                    tools: [{ toolName: "load_artifact", description: null, timeoutSeconds: null, sandboxProfile: null, parameters: null, configSchema: null }],
                    createdBy: null,
                    updatedBy: null,
                    createdTime: null,
                }),
            ])
        );
        const [got] = await getBuiltinToolsetsV2();
        expect(call(0).url).toContain("origin=builtin");
        expect(got).toMatchObject({ type: "builtin", files: [], zipFilename: null, zipFileSize: null, discoveryStatus: undefined, runtime: undefined, createdBy: "", createdAt: "" });
        expect(got.tools).toEqual([{ toolName: "load_artifact", toolDescription: undefined, timeoutSeconds: undefined, sandboxProfile: undefined, configSchema: undefined, parameters: undefined }]);
    });

    test("reads every page and sorts by name", async () => {
        respond(200, page([toolset({ id: "b", name: "zeta" })], 2));
        respond(200, page([toolset({ id: "a", name: "alpha" })]));
        expect((await getCustomToolsetsV2()).map(t => t.name)).toEqual(["alpha", "zeta"]);
        expect(call(1).url).toContain("pageNumber=2");
    });

    test("configuration round-trips through config and maps its audit fields", async () => {
        respond(200, { data: { values: { lookup: { api_key: "<REDACTED>" } }, updatedBy: null, updatedTime: null } });
        expect(await getToolsetConfigV2("ts-1")).toEqual({ values: { lookup: { api_key: "<REDACTED>" } }, updatedBy: "", updatedAt: "" });
        expect(call(0).url).toContain("/api/v2/platform/toolsets/ts-1/config");

        respond(200, { data: { values: { lookup: { region: "eu" } }, updatedBy: "u1", updatedTime: "2026-01-02T03:04:05.678Z" } });
        expect(await patchToolsetConfigV2("ts-1", { lookup: { region: "eu" } })).toEqual({ values: { lookup: { region: "eu" } }, updatedBy: "u1", updatedAt: "2026-01-02T03:04:05.678Z" });
        expect(call(1)).toMatchObject({ method: "PATCH", body: JSON.stringify({ values: { lookup: { region: "eu" } } }) });
    });

    test.each([
        ["str_init", "str-init", "3.12", "3.12"],
        ["fallback", "fallback", null, undefined],
    ])("the build target's %s source reads as the UI's %s", async (source, uiSource, pythonVersion, uiPythonVersion) => {
        respond(200, { data: { os: "linux", arch: "arm64", source, pythonVersion } });
        expect(await getToolsetBuildTargetV2()).toEqual({ os: "linux", arch: "arm64", source: uiSource, pythonVersion: uiPythonVersion });
        expect(call(0).url).toContain("/api/v2/platform/toolsetBuildTarget");
    });

    test("create, update and delete send the v2 requests", async () => {
        respond(201, { data: toolset() }, { Location: "/api/v2/platform/toolsets/ts-1" });
        expect(await createToolsetV2({ name: "order_tools", description: "Orders." })).toEqual({ id: "ts-1" });
        expect(call(0)).toMatchObject({ method: "POST", body: JSON.stringify({ name: "order_tools", description: "Orders." }) });

        respond(200, { data: toolset() });
        await updateToolsetV2("ts-1", { name: "order_tools", description: "" });
        expect(call(1)).toMatchObject({ method: "PATCH", body: JSON.stringify({ name: "order_tools", description: "" }) });
        expect(call(1).url).toContain("/api/v2/platform/toolsets/ts-1");

        respond(204);
        await deleteToolsetV2("ts-1");
        expect(call(2).method).toBe("DELETE");
    });

    test("a bundle uploads as the file part of a PUT and downloads as a blob", async () => {
        respond(200, { data: toolset() });
        const file = new File(["zip"], "order_tools.zip", { type: "application/zip" });
        await uploadToolsetBundleV2("ts-1", file);
        expect(call(0).url).toContain("/api/v2/platform/toolsets/ts-1/bundle");
        expect(call(0).method).toBe("PUT");
        expect((call(0).body as FormData).get("file")).toBeInstanceOf(File);

        fetchMock.mockResolvedValueOnce(new Response("PK-bytes", { status: 200, headers: { "Content-Type": "application/zip" } }));
        const bundle = await downloadToolsetBundleV2("ts-1");
        expect(bundle.size).toBe("PK-bytes".length);
        expect(call(1).url).toContain("/api/v2/platform/toolsets/ts-1/bundle");
    });

    test("a refused download rejects with the v2 error, not the error body as a file", async () => {
        respond(409, { message: "The toolset has no bundle yet; upload one first", errorId: "e1" });
        await expect(downloadToolsetBundleV2("ts-1")).rejects.toMatchObject({ status: 409, message: "The toolset has no bundle yet; upload one first" });
    });

    test("a rejected bundle names the file field in the message", async () => {
        respond(422, { message: "Unable to upload toolset bundle", errorId: "e2", validationDetails: { file: ["invalid ZIP file"] } });
        const rejection = uploadToolsetBundleV2("ts-1", new File(["x"], "x.zip"));
        await expect(rejection).rejects.toBeInstanceOf(ApiV2Error);
        await expect(rejection).rejects.toThrow("Unable to upload toolset bundle: file: invalid ZIP file");
    });

    test("an id is encoded, so it cannot walk to another path", async () => {
        const id = "../agents/a1";
        const encoded = "/api/v2/platform/toolsets/..%2Fagents%2Fa1";
        respond(204);
        await deleteToolsetV2(id);
        respond(200, { data: toolset() });
        await updateToolsetV2(id, { name: "n" });
        respond(200, { data: toolset() });
        await uploadToolsetBundleV2(id, new File(["zip"], "t.zip"));
        respond(200, { data: { values: {}, updatedBy: null, updatedTime: null } });
        await patchToolsetConfigV2(id, {});
        fetchMock.mockResolvedValueOnce(new Response("zip", { status: 200 }));
        await downloadToolsetBundleV2(id);
        const paths = fetchMock.mock.calls.map(([input]) => new URL(String(input), "http://localhost").pathname);
        expect(paths).toEqual([encoded, encoded, `${encoded}/bundle`, `${encoded}/config`, `${encoded}/bundle`]);
    });

    test("the config read carries the caller's abort signal", async () => {
        respond(200, { data: { values: {}, updatedBy: null, updatedTime: null } });
        const controller = new AbortController();
        await getToolsetConfigV2("ts-1", controller.signal);
        expect(fetchMock.mock.calls[0][1]?.signal).toBe(controller.signal);
    });
});
