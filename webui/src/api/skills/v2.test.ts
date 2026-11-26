// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { ApiV2Error } from "../common";

import { bundleHash, saveSkillFromConfig } from "./service";
import { createSkillV2, deleteSkillV2, getBuiltinSkillsV2, getCustomSkillsV2, getSkillFileContentV2, getSkillFilesV2, getSkillV2, listBuiltinSkillsV2, listCustomSkillsV2, uploadSkillZipV2 } from "./v2";
import { skillsApi } from "./versioned";

const bundledTool = {
    toolName: "order-lookup__find_order",
    description: "Find an order.",
    instructions: null,
    parameters: { type: "object" },
    artifactParams: { report: { isList: false, isOptional: true, filenameKey: null } },
    configSchema: null,
};

const skill = (overrides: Record<string, unknown> = {}) => ({
    id: "sk-1",
    type: "skill",
    origin: "custom",
    name: "order-lookup",
    label: null,
    description: "Look up orders.",
    tags: ["orders"],
    discoveryStatus: "ready",
    hasReferences: true,
    hasAssets: false,
    hasTools: true,
    resourceCount: 1,
    referenceCount: 1,
    assetCount: 0,
    bundledTools: [bundledTool],
    toolsets: [],
    bundle: { contentHash: "abc" },
    agents: [{ id: "a1", name: "Support", description: null }],
    configSummary: null,
    createdBy: "u1",
    updatedBy: null,
    createdTime: "2026-01-02T03:04:05.678Z",
    updatedTime: null,
    ...overrides,
});

const page = (data: unknown[], nextPage: number | null = null) => ({ data, meta: { pagination: { pageNumber: 1, count: data.length, pageSize: 100, nextPage, totalPages: 1 } } });

describe("skills on /api/v2", () => {
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

    test("maps a v2 skill onto the Skill the pages render, with bundled tools back in v1's snake_case shape", async () => {
        respond(200, page([skill()]));
        const [got] = await getCustomSkillsV2();
        expect(call(0).url).toContain("/api/v2/platform/skills?origin=custom&pageNumber=1&pageSize=100");
        expect(got).toEqual({
            id: "sk-1",
            name: "order-lookup",
            label: undefined,
            description: "Look up orders.",
            tags: ["orders"],
            origin: "platform",
            discoveryStatus: "ready",
            hasReferences: true,
            hasAssets: false,
            hasTools: true,
            resourceCount: 1,
            referenceCount: 1,
            assetCount: 0,
            bundledTools: [
                {
                    tool_name: "order-lookup__find_order",
                    description: "Find an order.",
                    parameters: { type: "object" },
                    artifact_params: { report: { is_list: false, is_optional: true } },
                },
            ],
            contentHash: "abc",
            agents: [{ id: "a1", name: "Support", description: undefined }],
            createdBy: "u1",
            updatedBy: "",
            createdAt: "2026-01-02T03:04:05.678Z",
            updatedAt: "",
        });
    });

    test("a bundled tool keeps a filename key, and one without artifact params sends none", async () => {
        const withFilenameKey = { ...bundledTool, artifactParams: { report: { isList: true, isOptional: false, filenameKey: "report_name" } } };
        const withoutArtifacts = { ...bundledTool, toolName: "order-lookup__ping", artifactParams: {} };
        respond(200, page([skill({ bundledTools: [withFilenameKey, withoutArtifacts] })]));
        const [got] = await getCustomSkillsV2();
        expect(got.bundledTools).toEqual([
            {
                tool_name: "order-lookup__find_order",
                description: "Find an order.",
                parameters: { type: "object" },
                artifact_params: { report: { is_list: true, is_optional: false, filename_key: "report_name" } },
            },
            { tool_name: "order-lookup__ping", description: "Find an order.", parameters: { type: "object" } },
        ]);
    });

    test("a skill that breaks the v2 contract rejects as the server's fault, not the network's", async () => {
        respond(200, { data: skill({ createdTime: "2026-01-02" }) });
        await expect(getSkillV2("sk-1")).rejects.toMatchObject({ status: 502, message: expect.stringContaining("not a v2 date-time") });

        respond(200, page([]));
        respond(201, { data: skill({ createdTime: "2026-01-02" }) });
        const saving = saveSkillFromConfig(skillsApi, { config: { name: "order-lookup", description: "d" } });
        await expect(saving).rejects.toMatchObject({ status: 502 });
        await expect(saving).rejects.not.toMatchObject({ code: "ERR_NETWORK" });
    });

    test("listBuiltinSkills asks for built-in skills only", async () => {
        respond(200, page([skill({ id: "sam-docs", origin: "builtin", name: "sam-docs" })]));
        expect((await listBuiltinSkillsV2()).map(s => s.id)).toEqual(["sam-docs"]);
        expect(call(0).url).toContain("/api/v2/platform/skills?origin=builtin&pageNumber=1&pageSize=100");
    });

    test("an id is encoded, so it cannot walk to another path", async () => {
        const id = "../agents/a1";
        const encoded = "/api/v2/platform/skills/..%2Fagents%2Fa1";
        respond(200, { data: skill() });
        await getSkillV2(id);
        respond(204);
        await deleteSkillV2(id);
        respond(200, { data: skill() });
        await uploadSkillZipV2(id, new File(["zip"], "s.zip"));
        respond(200, page([]));
        await getSkillFilesV2(id);
        fetchMock.mockResolvedValueOnce(new Response("# x", { status: 200, headers: { "Content-Type": "text/markdown" } }));
        await getSkillFileContentV2(id, "references/x.md");
        const paths = fetchMock.mock.calls.map(([input]) => new URL(String(input), "http://localhost").pathname);
        expect(paths).toEqual([encoded, encoded, `${encoded}/bundle`, `${encoded}/files`, `${encoded}/fileContent`]);
    });

    test("a built-in skill keeps its origin and label", async () => {
        respond(200, page([skill({ id: "sam-docs", origin: "builtin", name: "sam-docs", label: "SAM documentation", description: null, bundle: null })]));
        const [got] = await getBuiltinSkillsV2();
        expect(call(0).url).toContain("/api/v2/platform/skills?origin=builtin&pageNumber=1&pageSize=100");
        expect(got).toMatchObject({ origin: "builtin", label: "SAM documentation", description: "", contentHash: undefined });
    });

    test("a bundle uploaded before SAM stored its hash has no contentHash", async () => {
        respond(200, page([skill({ bundle: { contentHash: null } })]));
        const [got] = await getBuiltinSkillsV2();
        expect(got.contentHash).toBeUndefined();
    });

    test("the custom list reads every page, sorted by name", async () => {
        respond(200, page([skill({ id: "b", name: "beta" })], 2));
        respond(200, page([skill({ id: "a", name: "alpha" })]));
        const got = await getCustomSkillsV2();
        expect(call(1).url).toContain("origin=custom&pageNumber=2&pageSize=100");
        expect(got.map(s => s.name)).toEqual(["alpha", "beta"]);
    });

    test("listCustomSkills asks for custom skills only and rejects a malformed page", async () => {
        respond(200, page([skill()]));
        expect(await listCustomSkillsV2()).toHaveLength(1);
        expect(call(0).url).toContain("origin=custom");

        respond(200, { meta: {} });
        await expect(listCustomSkillsV2()).rejects.toMatchObject({ status: 502 });
    });

    test("getSkill reads one skill, and createSkill posts a multipart body", async () => {
        respond(200, { data: skill() });
        expect((await getSkillV2("sk-1")).id).toBe("sk-1");
        expect(call(0).url).toContain("/api/v2/platform/skills/sk-1");

        respond(201, { data: skill() });
        const file = new File(["zip"], "order-lookup.zip");
        await createSkillV2("Look up orders.", file);
        expect(call(1)).toMatchObject({ method: "POST" });
        expect(call(1).url).toContain("/api/v2/platform/skills");
        const form = call(1).body as FormData;
        expect(form.get("description")).toBe("Look up orders.");
        expect((form.get("file") as File).name).toBe("order-lookup.zip");
    });

    test("deleteSkill deletes, uploadSkillZip puts the bundle", async () => {
        respond(204);
        await deleteSkillV2("sk-1");
        expect(call(0)).toMatchObject({ method: "DELETE" });

        respond(200, { data: skill() });
        await uploadSkillZipV2("sk-1", new File(["zip"], "s.zip"));
        expect(call(1)).toMatchObject({ method: "PUT" });
        expect(call(1).url).toContain("/api/v2/platform/skills/sk-1/bundle");
        expect((call(1).body as FormData).get("file")).toBeInstanceOf(File);
    });

    test("files read every page and split back into references and assets without the category prefix", async () => {
        respond(200, page([{ path: "references/guide.md", category: "reference", sizeBytes: 10 }], 2));
        respond(
            200,
            page([
                { path: "references/sub/more.md", category: "reference", sizeBytes: 20 },
                { path: "assets/logo.png", category: "asset", sizeBytes: 30 },
            ])
        );
        const files = await getSkillFilesV2("sk-1");
        expect(call(0).url).toContain("/api/v2/platform/skills/sk-1/files?pageNumber=1&pageSize=100");
        expect(call(1).url).toContain("pageNumber=2");
        expect(files).toEqual({
            references: [
                { path: "guide.md", sizeBytes: 10 },
                { path: "sub/more.md", sizeBytes: 20 },
            ],
            assets: [{ path: "logo.png", sizeBytes: 30 }],
        });
    });

    test("file content is text for a text type, a blob for an image, and bare otherwise", async () => {
        fetchMock.mockResolvedValueOnce(new Response("# Guide", { status: 200, headers: { "Content-Type": "text/markdown" } }));
        expect(await getSkillFileContentV2("sk-1", "references/guide.md")).toEqual({ contentType: "text/markdown", text: "# Guide" });
        expect(call(0).url).toContain("/api/v2/platform/skills/sk-1/fileContent?path=references%2Fguide.md");

        fetchMock.mockResolvedValueOnce(new Response("png", { status: 200, headers: { "Content-Type": "image/png" } }));
        const image = await getSkillFileContentV2("sk-1", "assets/logo.png");
        expect(image.contentType).toBe("image/png");
        expect(image.blob).toBeInstanceOf(Blob);

        fetchMock.mockResolvedValueOnce(new Response("bin", { status: 200, headers: { "Content-Type": "application/pdf" } }));
        expect(await getSkillFileContentV2("sk-1", "assets/a.pdf")).toEqual({ contentType: "application/pdf" });
    });

    test("a missing file rejects with the v2 error", async () => {
        respond(404, { message: "No such file", errorId: "e1" });
        await expect(getSkillFileContentV2("sk-1", "references/x.md")).rejects.toBeInstanceOf(ApiV2Error);
    });

    describe("saveSkillFromConfig on v2", () => {
        const request = { config: { name: "order-lookup", description: "d" } };

        test("a 409 on create reaches the conflict branch with its status", async () => {
            respond(200, page([]));
            respond(409, { message: "A skill with that name exists", errorId: "e1" });
            respond(200, page([]));
            await expect(saveSkillFromConfig(skillsApi, request)).rejects.toMatchObject({ status: 409, name: "ApiV2Error" });
        });

        test("a 403 on the lookup then a 409 on create is reported as a refusal", async () => {
            respond(403, { message: "Forbidden", errorId: "e1" });
            respond(409, { message: "exists", errorId: "e2" });
            await expect(saveSkillFromConfig(skillsApi, request)).rejects.toMatchObject({ status: 403, errorId: "e2" });
        });

        describe("a skill already on the platform", () => {
            const originalArrayBuffer = Blob.prototype.arrayBuffer;
            beforeEach(() => {
                Blob.prototype.arrayBuffer = function (this: Blob) {
                    return new Promise<ArrayBuffer>((resolve, reject) => {
                        const reader = new FileReader();
                        reader.onload = () => resolve(reader.result as ArrayBuffer);
                        reader.onerror = () => reject(reader.error);
                        reader.readAsArrayBuffer(this);
                    });
                };
            });
            afterEach(() => {
                Blob.prototype.arrayBuffer = originalArrayBuffer;
            });

            const hashOfUpload = async () => {
                respond(200, page([]));
                respond(201, { data: skill() });
                await saveSkillFromConfig(skillsApi, request);
                const form = fetchMock.mock.calls.at(-1)![1]!.body as FormData;
                const hash = await bundleHash(form.get("file") as File);
                fetchMock.mockReset();
                return hash!;
            };
            const writes = () => fetchMock.mock.calls.filter(([, init]) => (init?.method ?? "GET") !== "GET").map(([input, init]) => `${init?.method} ${new URL(String(input), "http://localhost").pathname}`);

            test("an unchanged bundle, read from bundle.contentHash, is left alone and nobody is asked", async () => {
                respond(200, page([skill({ bundle: { contentHash: await hashOfUpload() } })]));
                const confirmReplace = vi.fn();

                expect(await saveSkillFromConfig(skillsApi, { ...request, confirmReplace })).toEqual({ id: "sk-1", name: "order-lookup", action: "unchanged" });
                expect(writes()).toEqual([]);
                expect(confirmReplace).not.toHaveBeenCalled();
            });

            test.each([
                ["a skill uploaded before SAM stored its hash", { bundle: { contentHash: null } }],
                ["a skill with no bundle", { bundle: null }],
            ])("%s counts as edited, and is replaced through PUT /bundle once confirmed", async (_label, overrides) => {
                respond(200, page([skill(overrides)]));
                respond(200, { data: skill() });
                const confirmReplace = vi.fn().mockResolvedValue(undefined);

                expect(await saveSkillFromConfig(skillsApi, { ...request, confirmReplace })).toEqual({ id: "sk-1", name: "order-lookup", action: "updated" });
                expect(confirmReplace).toHaveBeenCalledWith({ id: "sk-1", name: "order-lookup" });
                expect(writes()).toEqual(["PUT /api/v2/platform/skills/sk-1/bundle"]);
            });

            test("an identical bundle whose discovery failed is replaced, not trusted", async () => {
                respond(200, page([skill({ bundle: { contentHash: await hashOfUpload() }, discoveryStatus: "failed" })]));
                respond(200, { data: skill() });

                expect((await saveSkillFromConfig(skillsApi, { ...request, confirmReplace: async () => {} })).action).toBe("updated");
                expect(writes()).toEqual(["PUT /api/v2/platform/skills/sk-1/bundle"]);
            });

            test("different contents with no way to confirm are a conflict, and nothing is written", async () => {
                respond(200, page([skill({ bundle: { contentHash: "stale" } })]));

                await expect(saveSkillFromConfig(skillsApi, request)).rejects.toMatchObject({ status: 409 });
                expect(writes()).toEqual([]);
            });

            test("a deferred replacement uploads only when committed, without asking", async () => {
                respond(200, page([skill({ bundle: { contentHash: "stale" } })]));
                const confirmReplace = vi.fn();

                const saved = await saveSkillFromConfig(skillsApi, { ...request, deferReplace: true, confirmReplace });

                expect(saved).toMatchObject({ id: "sk-1", name: "order-lookup", action: "replace_deferred" });
                expect(writes()).toEqual([]);
                expect(confirmReplace).not.toHaveBeenCalled();

                respond(200, { data: skill() });
                await (saved as { commit: () => Promise<void> }).commit();
                expect(writes()).toEqual(["PUT /api/v2/platform/skills/sk-1/bundle"]);
            });

            test("a deferred upload that SAM refuses says which skill, and keeps the status", async () => {
                respond(200, page([skill({ bundle: { contentHash: "stale" } })]));
                const saved = await saveSkillFromConfig(skillsApi, { ...request, deferReplace: true });

                respond(403, { message: "Forbidden", errorId: "e5" });
                await expect((saved as { commit: () => Promise<void> }).commit()).rejects.toMatchObject({ status: 403, errorId: "e5", message: expect.stringContaining('Could not update skill "order-lookup"') });
            });
        });

        test("a skill that does not exist yet is created through /api/v2", async () => {
            respond(200, page([]));
            respond(201, { data: skill() });
            expect(await saveSkillFromConfig(skillsApi, request)).toEqual({ id: "sk-1", name: "order-lookup", action: "created" });
            expect(call(0).url).toContain("/api/v2/platform/skills?origin=custom");
            expect(call(1)).toMatchObject({ method: "POST" });
        });
    });
});
