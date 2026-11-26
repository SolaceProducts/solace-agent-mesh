// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import JSZip from "jszip";
import { afterEach, beforeEach, describe, expect, type Mock, test, vi } from "vitest";

import { api } from "@/api/client";
import { bundleHash, type CreateSkillFromConfigRequest, type FetchSkillArtifact, saveSkillFromConfig as saveWith, type SkillBundleConfig } from "@/api/skills/service";
import { skillsApi } from "@/api/skills/versioned";

import { isAgentFixableError } from "@/features/builder/builderUtils";

const saveSkillFromConfig = (payload: CreateSkillFromConfigRequest) => saveWith(skillsApi, payload);

type Reply = { status: number; body: unknown };
const toResponse = ({ status, body }: Reply) => new Response(JSON.stringify(body), { status });

const platformWrite: Mock = vi.fn();
const platformGet: Mock = vi.fn();
vi.spyOn(api.platform, "post").mockImplementation(async (...args) => toResponse(await platformWrite(...args)));
vi.spyOn(api.platform, "put").mockImplementation(async (...args) => toResponse(await platformWrite(...args)));
vi.spyOn(api.platform, "get").mockImplementation(async (...args) => toResponse(await platformGet(...args)));

const GOLDEN_BUNDLE_SHA256 = "65cfd4e7e63a38a7192e357267b27674f5a571bc95bd140c2194a24973800d16";

const readBytes = (file: Blob): Promise<ArrayBuffer> =>
    new Promise((resolve, reject) => {
        const reader = new FileReader();
        reader.onload = () => resolve(reader.result as ArrayBuffer);
        reader.onerror = () => reject(reader.error);
        reader.readAsArrayBuffer(file);
    });

const originalArrayBuffer = Blob.prototype.arrayBuffer;
beforeEach(() => {
    platformWrite.mockReset();
    platformGet.mockReset();
    Blob.prototype.arrayBuffer = function (this: Blob) {
        return readBytes(this);
    };
});
afterEach(() => {
    Blob.prototype.arrayBuffer = originalArrayBuffer;
    vi.useRealTimers();
    vi.unstubAllGlobals();
});

const config = (over: Partial<SkillBundleConfig> = {}): SkillBundleConfig => ({
    name: "haiku-style",
    description: "Writes haiku.",
    instruction_content: "# Haiku\n\nWrite 5-7-5.",
    ...over,
});

type Row = { id: string; name: string; contentHash?: string; discoveryStatus?: string };

const skillV2 = ({ id, name, contentHash, discoveryStatus = "ready" }: Row) => ({
    id,
    type: "skill",
    origin: "custom",
    name,
    label: null,
    description: null,
    tags: [],
    discoveryStatus,
    hasReferences: false,
    hasAssets: false,
    hasTools: false,
    resourceCount: 0,
    referenceCount: 0,
    assetCount: 0,
    bundledTools: [],
    toolsets: [],
    bundle: contentHash === undefined ? null : { contentHash },
    agents: [],
    configSummary: null,
    createdBy: null,
    updatedBy: null,
    createdTime: null,
    updatedTime: null,
});

const listPage = (rows: Row[], pageNumber = 1, nextPage: number | null = null): Reply => ({
    status: 200,
    body: { data: rows.map(skillV2), meta: { pagination: { pageNumber, count: rows.length, pageSize: 100, nextPage, totalPages: 1 } } },
});

const listPages = (...pages: Row[][]) => {
    platformGet.mockImplementation(async (url: string) => {
        const pageNumber = Number(new URL(url, "http://x").searchParams.get("pageNumber"));
        return listPage(pages[pageNumber - 1] ?? [], pageNumber, pageNumber < pages.length ? pageNumber + 1 : null);
    });
};

const skillReply = (id: string, name = "haiku-style"): Reply => ({ status: 200, body: { data: skillV2({ id, name }) } });

const httpError = (status: number, message: string): Reply => ({ status, body: { message, errorId: "e1" } });

const hashOfUpload = async (cfg: SkillBundleConfig, fetchArtifact?: FetchSkillArtifact): Promise<string> => {
    listPages([]);
    platformWrite.mockResolvedValueOnce(skillReply("new", cfg.name));
    await saveSkillFromConfig({ config: cfg, fetchArtifact });
    const [, form] = platformWrite.mock.calls.at(-1) as [string, FormData];
    platformWrite.mockReset();
    return (await bundleHash(form.get("file") as File))!;
};

const writes = () => platformWrite.mock.calls.map(([url]) => url as string);

const uploadedSkillMd = async (callIndex = 0) => {
    const [, form] = platformWrite.mock.calls[callIndex] as [string, FormData];
    const zip = await JSZip.loadAsync(await readBytes(form.get("file") as File));
    return zip.file("SKILL.md")!.async("string");
};

const apiError = (message: string, extra: object) => Object.assign(new Error(message), extra);

type Failure = Reply | Error;
const answerWith = (failure: Failure) => (failure instanceof Error ? Promise.reject(failure) : Promise.resolve(failure));

describe("saveSkillFromConfig — a skill that is not on the platform", () => {
    test.each([
        ["the platform has no skills", [[]]],
        ["only other skills", [[{ id: "s1", name: "web-search" }]]],
        ["a skill that differs only by case", [[{ id: "s1", name: "Haiku-Style" }]]],
        ["a skill whose name merely contains it", [[{ id: "s1", name: "haiku-style-2" }]]],
    ] as [string, Row[][]][])("is created when %s", async (_label, pages) => {
        listPages(...pages);
        platformWrite.mockResolvedValue(skillReply("created-id"));

        const saved = await saveSkillFromConfig({ config: config() });

        expect(saved).toEqual({ id: "created-id", name: "haiku-style", action: "created" });
        expect(writes()).toEqual(["/api/v2/platform/skills"]);
    });

    test("asks for the custom skills only, from the skills endpoint", async () => {
        listPages([]);
        platformWrite.mockResolvedValue(skillReply("created-id"));

        await saveSkillFromConfig({ config: config() });

        const [firstUrl] = platformGet.mock.calls[0] as [string];
        expect(firstUrl.startsWith("/api/v2/platform/skills?")).toBe(true);
        expect(firstUrl).toContain("origin=custom");
    });
});

describe("saveSkillFromConfig — a skill already on the platform", () => {
    test("an unchanged bundle is left alone, and nobody is asked", async () => {
        const stored = await hashOfUpload(config());
        listPages([{ id: "s1", name: "haiku-style", contentHash: stored }]);
        const confirmReplace = vi.fn().mockResolvedValue(undefined);

        const saved = await saveSkillFromConfig({ config: config(), confirmReplace });

        expect(saved).toEqual({ id: "s1", name: "haiku-style", action: "unchanged" });
        expect(platformWrite).not.toHaveBeenCalled();
        expect(confirmReplace).not.toHaveBeenCalled();
    });

    test.each(["created", "pending", "ready"])("an identical bundle whose discovery is %s is left alone", async discoveryStatus => {
        const stored = await hashOfUpload(config());
        listPages([{ id: "s1", name: "haiku-style", contentHash: stored, discoveryStatus }]);

        const saved = await saveSkillFromConfig({ config: config(), confirmReplace: vi.fn() });

        expect(saved.action).toBe("unchanged");
        expect(platformWrite).not.toHaveBeenCalled();
    });

    test("an identical bundle whose discovery failed is replaced, not trusted", async () => {
        const stored = await hashOfUpload(config());
        listPages([{ id: "s1", name: "haiku-style", contentHash: stored, discoveryStatus: "failed" }]);
        platformWrite.mockResolvedValue(skillReply("s1"));

        const saved = await saveSkillFromConfig({ config: config(), confirmReplace: async () => {} });

        expect(saved.action).toBe("updated");
    });

    test("an edited bundle replaces the existing skill by id, carrying the new contents", async () => {
        const stored = await hashOfUpload(config());
        listPages([{ id: "s1", name: "haiku-style", contentHash: stored }]);
        platformWrite.mockResolvedValue(skillReply("s1"));
        const confirmReplace = vi.fn().mockResolvedValue(undefined);

        const saved = await saveSkillFromConfig({ config: config({ instruction_content: "# Haiku\n\nAlways end with Enjoy!" }), confirmReplace });

        expect(saved).toEqual({ id: "s1", name: "haiku-style", action: "updated" });
        expect(confirmReplace).toHaveBeenCalledWith({ id: "s1", name: "haiku-style" });
        expect(writes()).toEqual(["/api/v2/platform/skills/s1/bundle"]);
        expect(await uploadedSkillMd()).toContain("Always end with Enjoy!");
    });

    test("a bundle with a different reference counts as edited", async () => {
        const stored = await hashOfUpload(config({ references: [{ name: "a.md", content: "one" }] }));
        listPages([{ id: "s1", name: "haiku-style", contentHash: stored }]);
        platformWrite.mockResolvedValue(skillReply("s1"));

        const saved = await saveSkillFromConfig({ config: config({ references: [{ name: "a.md", content: "two" }] }), confirmReplace: async () => {} });

        expect(saved.action).toBe("updated");
    });

    test("a name with surrounding whitespace finds its own row, and uploads the trimmed name", async () => {
        listPages([{ id: "s1", name: "haiku-style", contentHash: "stale" }]);
        platformWrite.mockResolvedValue(skillReply("s1"));

        const saved = await saveSkillFromConfig({ config: config({ name: "  haiku-style " }), confirmReplace: async () => {} });

        expect(saved).toEqual({ id: "s1", name: "haiku-style", action: "updated" });
        expect(await uploadedSkillMd()).toMatch(/^---\nname: haiku-style\n/);
    });

    test("is found on a later page of the list", async () => {
        listPages([{ id: "a", name: "a" }], [{ id: "b", name: "b" }], [{ id: "s3", name: "haiku-style", contentHash: "stale" }]);
        platformWrite.mockResolvedValue(skillReply("s3"));

        const saved = await saveSkillFromConfig({ config: config(), confirmReplace: async () => {} });

        expect(saved).toMatchObject({ id: "s3", action: "updated" });
    });

    test("with no way to ask, an existing skill with different contents is a conflict, not a quiet success", async () => {
        listPages([{ id: "s1", name: "haiku-style", contentHash: "stale" }]);

        await expect(saveSkillFromConfig({ config: config() })).rejects.toMatchObject({ status: 409, message: expect.stringContaining('"haiku-style" already exists') });

        expect(platformWrite).not.toHaveBeenCalled();
    });

    test("writes nothing when the confirmation is rejected", async () => {
        listPages([{ id: "s1", name: "haiku-style", contentHash: "stale" }]);

        await expect(saveSkillFromConfig({ config: config(), confirmReplace: () => Promise.reject(new Error("User cancelled")) })).rejects.toThrow("User cancelled");

        expect(platformWrite).not.toHaveBeenCalled();
    });

    test.each([
        ["the platform recorded no hash", { id: "s1", name: "haiku-style", contentHash: "" }],
        ["the platform omits the hash", { id: "s1", name: "haiku-style" }],
    ] as [string, Row][])("counts as edited when %s", async (_label, row) => {
        listPages([row]);
        const confirmReplace = vi.fn().mockRejectedValue(new Error("User cancelled"));

        await expect(saveSkillFromConfig({ config: config(), confirmReplace })).rejects.toThrow("User cancelled");

        expect(confirmReplace).toHaveBeenCalledTimes(1);
    });

    test("counts as edited where nothing can be compared: no recorded hash and a browser that cannot hash", async () => {
        listPages([{ id: "s1", name: "haiku-style" }]);
        vi.stubGlobal("crypto", {});
        const confirmReplace = vi.fn().mockRejectedValue(new Error("User cancelled"));

        await expect(saveSkillFromConfig({ config: config(), confirmReplace })).rejects.toThrow("User cancelled");

        expect(confirmReplace).toHaveBeenCalledTimes(1);
    });

    test("counts as edited where the browser cannot hash", async () => {
        const stored = await hashOfUpload(config());
        listPages([{ id: "s1", name: "haiku-style", contentHash: stored }]);
        vi.stubGlobal("crypto", {});
        const confirmReplace = vi.fn().mockRejectedValue(new Error("User cancelled"));

        await expect(saveSkillFromConfig({ config: config(), confirmReplace })).rejects.toThrow("User cancelled");

        expect(confirmReplace).toHaveBeenCalledTimes(1);
    });
});

describe("saveSkillFromConfig — deferring the replacement", () => {
    test("hands the upload back without performing it, and without asking", async () => {
        listPages([{ id: "s1", name: "haiku-style", contentHash: "stale" }]);
        platformWrite.mockResolvedValue(skillReply("s1"));
        const confirmReplace = vi.fn();

        const saved = await saveSkillFromConfig({ config: config({ instruction_content: "# New" }), deferReplace: true, confirmReplace });

        expect(saved).toMatchObject({ id: "s1", name: "haiku-style", action: "replace_deferred" });
        expect(platformWrite).not.toHaveBeenCalled();
        expect(confirmReplace).not.toHaveBeenCalled();

        await (saved as { commit: () => Promise<void> }).commit();

        expect(writes()).toEqual(["/api/v2/platform/skills/s1/bundle"]);
        expect(await uploadedSkillMd()).toContain("# New");
    });

    test("a deferred upload that fails says which skill, and keeps the status", async () => {
        listPages([{ id: "s1", name: "haiku-style", contentHash: "stale" }]);
        platformWrite.mockResolvedValue(httpError(403, "forbidden"));

        const saved = await saveSkillFromConfig({ config: config(), deferReplace: true });

        await expect((saved as { commit: () => Promise<void> }).commit()).rejects.toMatchObject({ status: 403, message: expect.stringContaining('Could not update skill "haiku-style"') });
    });

    test("a skill that is not on the platform is created at once, not deferred", async () => {
        listPages([]);
        platformWrite.mockResolvedValue(skillReply("created-id"));

        const saved = await saveSkillFromConfig({ config: config(), deferReplace: true });

        expect(saved.action).toBe("created");
    });

    test("an unchanged skill is left alone, not deferred", async () => {
        const stored = await hashOfUpload(config());
        listPages([{ id: "s1", name: "haiku-style", contentHash: stored }]);

        const saved = await saveSkillFromConfig({ config: config(), deferReplace: true });

        expect(saved.action).toBe("unchanged");
    });
});

describe("saveSkillFromConfig — a lookup that fails", () => {
    test.each([
        ["a server error", httpError(500, "failed"), { status: 500 }],
        ["a not-found", httpError(404, "failed"), { status: 404 }],
        ["an expired session", httpError(401, "failed"), { status: 401 }],
        ["a dropped connection", new TypeError("Failed to fetch"), { code: "ERR_NETWORK" }],
    ] as [string, Failure, object][])("is not read as a free name: %s", async (_label, failure, fields) => {
        platformGet.mockImplementation(() => answerWith(failure));

        await expect(saveSkillFromConfig({ config: config() })).rejects.toMatchObject({ ...fields, message: expect.stringContaining('Could not check whether skill "haiku-style" already exists') });

        expect(platformWrite).not.toHaveBeenCalled();
    });

    test("a failure on a later page is not read as a free name either", async () => {
        platformGet.mockImplementation(async (url: string) => {
            if (url.includes("pageNumber=2")) return httpError(502, "boom");
            return listPage([{ id: "a", name: "a" }], 1, 2);
        });

        await expect(saveSkillFromConfig({ config: config() })).rejects.toMatchObject({ status: 502 });

        expect(platformWrite).not.toHaveBeenCalled();
    });

    test("a reply that is not a skill list is not read as a free name", async () => {
        platformGet.mockResolvedValue({ status: 200, body: "<html>proxy error</html>" });

        await expect(saveSkillFromConfig({ config: config() })).rejects.toThrow(/unexpected shape/);

        expect(platformWrite).not.toHaveBeenCalled();
    });

    test("a refusal to read the list falls through to create, whose conflict answer is the guard", async () => {
        platformGet.mockResolvedValue(httpError(403, "forbidden"));
        platformWrite.mockResolvedValue(skillReply("created-id"));

        const saved = await saveSkillFromConfig({ config: config() });

        expect(saved.action).toBe("created");
        expect(writes()).toEqual(["/api/v2/platform/skills"]);
    });

    test("a refusal to read the list, then a conflict from create, is reported as a refusal and not as a name to change", async () => {
        platformGet.mockResolvedValue(httpError(403, "forbidden"));
        platformWrite.mockResolvedValue(httpError(409, "skill with name already exists"));

        await expect(saveSkillFromConfig({ config: config() })).rejects.toMatchObject({ status: 403, message: expect.stringContaining('Skill "haiku-style" already exists, and this role may not read skills') });
    });

    test("a refusal to read the list, then another failure from create, is that failure unchanged", async () => {
        platformGet.mockResolvedValue(httpError(403, "forbidden"));
        platformWrite.mockResolvedValue(httpError(500, "boom"));

        await expect(saveSkillFromConfig({ config: config() })).rejects.toMatchObject({ name: "ApiV2Error", status: 500, message: "boom" });
    });

    test("a reply that is not a skill list is an infrastructure failure, not something for the builder to fix", async () => {
        platformGet.mockResolvedValue({ status: 200, body: "<html>proxy error</html>" });

        await expect(saveSkillFromConfig({ config: config() })).rejects.toMatchObject({ status: 502 });
    });

    test("a bundle that cannot be assembled fails before the platform is asked anything", async () => {
        await expect(saveSkillFromConfig({ config: config({ references: [{ name: "empty.md" }] }) })).rejects.toThrow(/neither inline content nor an artifact/);

        expect(platformGet).not.toHaveBeenCalled();
        expect(platformWrite).not.toHaveBeenCalled();
    });
});

describe("saveSkillFromConfig — a skill that appears between the lookup and the create", () => {
    const racedList = (second: Row[]) => {
        let reads = 0;
        platformGet.mockImplementation(async () => listPage(reads++ === 0 ? [] : second));
    };
    const conflict = () => httpError(409, "skill with name already exists");

    test("is looked up again and left alone when it holds the same bundle", async () => {
        const hash = await hashOfUpload(config());
        racedList([{ id: "s9", name: "haiku-style", contentHash: hash, discoveryStatus: "ready" }]);
        platformWrite.mockResolvedValueOnce(conflict());

        const saved = await saveSkillFromConfig({ config: config() });

        expect(saved).toEqual({ id: "s9", name: "haiku-style", action: "unchanged" });
        expect(writes()).toEqual(["/api/v2/platform/skills"]);
    });

    test("is looked up again and replaced when it holds a different bundle", async () => {
        racedList([{ id: "s9", name: "haiku-style", contentHash: "0".repeat(64), discoveryStatus: "ready" }]);
        platformWrite.mockResolvedValueOnce(conflict()).mockResolvedValueOnce(skillReply("s9"));

        const saved = await saveSkillFromConfig({ config: config(), confirmReplace: async () => {} });

        expect(saved).toEqual({ id: "s9", name: "haiku-style", action: "updated" });
        expect(writes()).toEqual(["/api/v2/platform/skills", "/api/v2/platform/skills/s9/bundle"]);
    });

    test("is the conflict itself when the second lookup still finds nothing", async () => {
        racedList([]);
        platformWrite.mockResolvedValueOnce(conflict());

        await expect(saveSkillFromConfig({ config: config() })).rejects.toMatchObject({ name: "ApiV2Error", status: 409, message: "skill with name already exists" });
    });

    test("is that failure, not a second lookup, when the create fails any other way", async () => {
        racedList([{ id: "s9", name: "haiku-style" }]);
        platformWrite.mockResolvedValueOnce(httpError(500, "boom"));

        await expect(saveSkillFromConfig({ config: config() })).rejects.toMatchObject({ name: "ApiV2Error", status: 500, message: "boom" });
        expect(platformGet).toHaveBeenCalledTimes(1);
    });
});

describe("saveSkillFromConfig — which failures the builder is sent to fix", () => {
    type Step = { label: string; arrange: (failure: Failure) => void; run: () => Promise<unknown> };
    const differing: Row = { id: "s1", name: "haiku-style", contentHash: "0".repeat(64), discoveryStatus: "ready" };
    const STEPS: Step[] = [
        {
            label: "the lookup",
            arrange: failure => {
                platformGet.mockImplementation(() => answerWith(failure));
                platformWrite.mockResolvedValue(httpError(403, "forbidden"));
            },
            run: () => saveSkillFromConfig({ config: config() }),
        },
        {
            label: "the create",
            arrange: failure => {
                listPages([]);
                platformWrite.mockImplementation(() => answerWith(failure));
            },
            run: () => saveSkillFromConfig({ config: config() }),
        },
        {
            label: "the create by a role that cannot read the list",
            arrange: failure => {
                platformGet.mockResolvedValue(httpError(403, "forbidden"));
                platformWrite.mockImplementation(() => answerWith(failure));
            },
            run: () => saveSkillFromConfig({ config: config() }),
        },
        {
            label: "the second lookup after a create conflict",
            arrange: failure => {
                let reads = 0;
                platformGet.mockImplementation(async () => (reads++ === 0 ? listPage([]) : answerWith(failure)));
                platformWrite.mockResolvedValue(httpError(409, "already exists"));
            },
            run: () => saveSkillFromConfig({ config: config() }),
        },
        {
            label: "the upload that replaces a skill",
            arrange: failure => {
                listPages([differing]);
                platformWrite.mockImplementation(() => answerWith(failure));
            },
            run: () => saveSkillFromConfig({ config: config(), confirmReplace: async () => {} }),
        },
        {
            label: "the upload deferred to the caller",
            arrange: failure => {
                listPages([differing]);
                platformWrite.mockImplementation(() => answerWith(failure));
            },
            run: async () => {
                const saved = await saveSkillFromConfig({ config: config(), deferReplace: true });
                if (saved.action !== "replace_deferred") throw new Error("expected a deferred replacement");
                return saved.commit();
            },
        },
    ];

    const FAILURES: Array<[string, () => Failure, boolean]> = [
        ["no answer at all", () => new TypeError("Failed to fetch"), false],
        ["a timeout code", () => apiError("timed out", { code: "ECONNABORTED" }), false],
        ["a 500", () => httpError(500, "boom"), false],
        ["a 503", () => httpError(503, "unavailable"), false],
        ["a 401", () => httpError(401, "expired"), false],
        ["a 403", () => httpError(403, "forbidden"), false],
        ["a 400 on the bundle", () => httpError(400, "invalid bundle"), true],
        ["a 422 on the bundle", () => httpError(422, "invalid bundle"), true],
    ];

    test.each(STEPS.flatMap(step => FAILURES.map(([label, make, fixable]) => [step.label, label, fixable, step, make] as const)))("%s failing with %s: fixable by the builder is %s", async (_step, _shape, fixable, step, make) => {
        step.arrange(make());

        const thrown = await step.run().catch((error: unknown) => error);

        expect(thrown).toBeInstanceOf(Error);
        expect(isAgentFixableError(thrown)).toBe(fixable);
    });

    test("a failure that already carries a code keeps it, rather than being relabelled as the network's", async () => {
        platformGet.mockRejectedValue(apiError("timed out", { code: "ECONNABORTED" }));

        await expect(saveSkillFromConfig({ config: config() })).rejects.toMatchObject({ code: "ECONNABORTED" });
    });

    test("an artifact that cannot be loaded is for the builder to look at", async () => {
        const fetchArtifact: FetchSkillArtifact = async () => {
            throw new Error("Artifact not found");
        };

        const thrown = await saveSkillFromConfig({ config: config({ references: [{ name: "r.md", artifact: "notes.md" }] }), fetchArtifact }).catch((error: unknown) => error);

        expect((thrown as Error).message).toContain("which could not be loaded from the session: Artifact not found");
        expect(isAgentFixableError(thrown)).toBe(true);
    });
});

describe("saveSkillFromConfig — the bundle hash matches what the platform records", () => {
    const ABC_SHA256 = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad";

    test("is the lowercase hex SHA-256 of the bytes", async () => {
        expect(await bundleHash(new Blob(["abc"]))).toBe(ABC_SHA256);
    });

    test("keeps the leading zeros of each byte", async () => {
        expect(await bundleHash(new Blob([new Uint8Array([0, 1, 2])]))).toBe("ae4b3280e56e2faf83f414a6e3dabe9d5fbe18976544c05fed121accb85b53fc");
    });

    test.each(["UTC", "Asia/Tokyo", "America/Los_Angeles", "Pacific/Chatham"])("is stable for a fixed config, whatever the clock, in %s", async zone => {
        const originalZone = process.env.TZ;
        process.env.TZ = zone;
        try {
            vi.resetModules();
            const { api: freshApi } = await import("@/api/client");
            const fresh = await import("@/api/skills/service");
            const { skillsApi: freshSkillsApi } = await import("@/api/skills/versioned");
            vi.spyOn(freshApi.platform, "get").mockImplementation(async () => toResponse(listPage([])));
            const post = vi.spyOn(freshApi.platform, "post").mockImplementation(async () => toResponse(skillReply("new", "golden")));
            vi.useFakeTimers({ toFake: ["Date"] });
            vi.setSystemTime(new Date("2031-07-15T23:59:59Z"));

            await fresh.saveSkillFromConfig(freshSkillsApi, { config: config({ name: "golden", description: "A golden bundle.", references: [{ name: "a.md", content: "x" }] }) });

            const form = post.mock.calls.at(-1)![1] as FormData;
            expect(await fresh.bundleHash(form.get("file") as File)).toBe(GOLDEN_BUNDLE_SHA256);
        } finally {
            if (originalZone === undefined) delete process.env.TZ;
            else process.env.TZ = originalZone;
        }
    });
});

describe("saveSkillFromConfig — an unchanged bundle is recognised whatever it contains", () => {
    const artifacts: FetchSkillArtifact = async name => ({ content: btoa(`bytes of ${name}`), mimeType: "application/octet-stream" });

    test.each([
        ["inline references", config({ references: [{ name: "a.md", content: "x" }] })],
        ["an artifact-backed reference", config({ references: [{ name: "b.md", artifact: "session-b" }] })],
        ["a binary asset", config({ assets: [{ name: "c.bin", artifact: "session-c" }] })],
        [
            "all of them",
            config({
                references: [
                    { name: "a.md", content: "x" },
                    { name: "b.md", artifact: "session-b" },
                ],
                assets: [{ name: "c.bin", artifact: "session-c" }],
            }),
        ],
    ])("when rebuilt months later: %s", async (_label, cfg) => {
        vi.useFakeTimers({ toFake: ["Date"] });
        vi.setSystemTime(new Date("2026-01-01T00:00:00Z"));
        const stored = await hashOfUpload(cfg, artifacts);
        vi.setSystemTime(new Date("2026-06-01T12:34:56Z"));
        listPages([{ id: "s1", name: "haiku-style", contentHash: stored }]);

        const saved = await saveSkillFromConfig({ config: cfg, fetchArtifact: artifacts });

        expect(saved.action).toBe("unchanged");
    });

    test("an asset whose bytes changed counts as edited", async () => {
        const cfg = config({ assets: [{ name: "c.bin", artifact: "session-c" }] });
        const stored = await hashOfUpload(cfg, artifacts);
        listPages([{ id: "s1", name: "haiku-style", contentHash: stored }]);
        const changed: FetchSkillArtifact = async () => ({ content: btoa("other bytes"), mimeType: "application/octet-stream" });

        const saved = await saveSkillFromConfig({ config: cfg, fetchArtifact: changed, deferReplace: true });

        expect(saved.action).toBe("replace_deferred");
    });
});
