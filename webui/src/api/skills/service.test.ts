// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import yaml from "js-yaml";
import JSZip from "jszip";
import { beforeEach, describe, expect, type Mock, test, vi } from "vitest";

import { api } from "@/api/client";
import { buildSkillMd, type CreateSkillFromConfigRequest, type FetchSkillArtifact, isTextContentType, saveSkillFromConfig as saveWith, type SkillBundleConfig } from "@/api/skills/service";
import { skillsApi } from "@/api/skills/versioned";

const saveSkillFromConfig = (payload: CreateSkillFromConfigRequest) => saveWith(skillsApi, payload);

const platformPost: Mock = vi.fn();
vi.spyOn(api.platform, "post").mockImplementation((...args) => platformPost(...args));
vi.spyOn(api.platform, "get").mockImplementation(async () => new Response(JSON.stringify({ data: [], meta: { pagination: { nextPage: null } } })));

describe("isTextContentType", () => {
    test.each(["text/plain", "text/markdown; charset=utf-8", "application/json", "application/yaml", "text/xml", "application/xml"])("treats %s as text", ct => {
        expect(isTextContentType(ct)).toBe(true);
    });

    test.each(["image/png", "image/jpeg", "application/pdf", "application/octet-stream", ""])("treats %s as non-text", ct => {
        expect(isTextContentType(ct)).toBe(false);
    });

    test("is case-insensitive", () => {
        expect(isTextContentType("TEXT/PLAIN")).toBe(true);
        expect(isTextContentType("Application/JSON")).toBe(true);
    });
});

const parseFrontmatter = (skillMd: string): Record<string, unknown> => {
    const match = skillMd.match(/^---\n([\s\S]*?)\n---\n/);
    if (!match) throw new Error("no frontmatter delimiters");
    return yaml.load(match[1]) as Record<string, unknown>;
};

const bodyOf = (skillMd: string): string => skillMd.replace(/^---\n[\s\S]*?\n---\n/, "");

describe("buildSkillMd", () => {
    test("writes the description into the frontmatter so the STR reconcile preserves it", () => {
        const description = 'Translates text into authentic cowboy: yeehaw "speech"';
        const skillMd = buildSkillMd({ name: "cowboy-tone", description });

        const parsed = parseFrontmatter(skillMd);
        expect(parsed.name).toBe("cowboy-tone");
        expect(parsed.description).toBe(description);
    });

    test("round-trips a multi-line description", () => {
        const description = "Line one.\nLine two with special: chars # and more.";
        const skillMd = buildSkillMd({ name: "multi", description });
        expect(parseFrontmatter(skillMd).description).toBe(description);
    });

    test("round-trips a description that ends with a newline (block-scalar chomping preserved)", () => {
        const description = "Ends with a newline\n";
        const skillMd = buildSkillMd({ name: "nl", description });
        expect(parseFrontmatter(skillMd).description).toBe(description);
    });

    test("emits a parseable empty description", () => {
        const skillMd = buildSkillMd({ name: "empty", description: "" });
        const parsed = parseFrontmatter(skillMd);
        expect(parsed.name).toBe("empty");
        expect(parsed.description).toBe("");
    });

    test("writes the instruction content as the markdown body", () => {
        const skillMd = buildSkillMd({
            name: "with-body",
            description: "A skill with a body.",
            instruction_content: "# Heading\n\nInstruction text.",
        });
        expect(bodyOf(skillMd)).toBe("\n# Heading\n\nInstruction text.\n");
        expect(parseFrontmatter(skillMd).name).toBe("with-body");
    });

    test("mirrors discovery-reconciled metadata fields into the frontmatter", () => {
        const skillMd = buildSkillMd({
            name: "meta",
            description: "A skill with metadata.",
            tags: ["reporting", "finance"],
            examples: ["Render the Q3 report"],
            required_scopes: ["tool:reports:read"],
            required_builtins: ["instantiate_template"],
            tools: [{ tool_type: "builtin", tool_name: "instantiate_template" }],
        });
        const parsed = parseFrontmatter(skillMd);
        expect(parsed.tags).toEqual(["reporting", "finance"]);
        expect(parsed.examples).toEqual(["Render the Q3 report"]);
        expect(parsed.required_scopes).toEqual(["tool:reports:read"]);
        expect(parsed.required_builtins).toEqual(["instantiate_template"]);
        expect(parsed.tools).toEqual([{ tool_type: "builtin", tool_name: "instantiate_template" }]);
    });

    test("omits empty metadata arrays from the frontmatter", () => {
        const skillMd = buildSkillMd({ name: "lean", description: "No metadata.", tags: [] });
        const parsed = parseFrontmatter(skillMd);
        expect(parsed).not.toHaveProperty("tags");
        expect(parsed).not.toHaveProperty("examples");
        expect(parsed).not.toHaveProperty("tools");
    });
});

describe("saveSkillFromConfig bundle assembly", () => {
    const created = {
        id: "skill-id-1",
        type: "skill",
        origin: "custom",
        name: "report-skill",
        label: null,
        description: null,
        tags: [],
        discoveryStatus: "created",
        hasReferences: false,
        hasAssets: false,
        hasTools: false,
        resourceCount: 0,
        referenceCount: 0,
        assetCount: 0,
        bundledTools: [],
        toolsets: [],
        bundle: null,
        agents: [],
        configSummary: null,
        createdBy: null,
        updatedBy: null,
        createdTime: null,
        updatedTime: null,
    };

    beforeEach(() => {
        platformPost.mockReset();
        platformPost.mockImplementation(async () => new Response(JSON.stringify({ data: created }), { status: 201 }));
    });

    const readFileBytes = (file: File): Promise<ArrayBuffer> =>
        new Promise((resolve, reject) => {
            const reader = new FileReader();
            reader.onload = () => resolve(reader.result as ArrayBuffer);
            reader.onerror = () => reject(reader.error);
            reader.readAsArrayBuffer(file);
        });

    const uploadedZip = async (): Promise<JSZip> => {
        expect(platformPost).toHaveBeenCalledTimes(1);
        const [url, form] = platformPost.mock.calls[0] as [string, FormData];
        expect(url).toBe("/api/v2/platform/skills");
        const file = form.get("file") as File;
        return JSZip.loadAsync(await readFileBytes(file));
    };

    const config = (over: Partial<SkillBundleConfig> = {}): SkillBundleConfig => ({
        name: "report-skill",
        description: "Renders the quarterly report.",
        instruction_content: "# Report Skill\n\nUse instantiate_template.",
        ...over,
    });

    const base64Of = (text: string) => btoa(text);

    test("uploads SKILL.md with frontmatter and instruction body", async () => {
        await saveSkillFromConfig({ config: config() });

        const zip = await uploadedZip();
        const skillMd = await zip.file("SKILL.md")!.async("string");
        expect(parseFrontmatter(skillMd)).toEqual({ name: "report-skill", description: "Renders the quarterly report." });
        expect(bodyOf(skillMd)).toContain("# Report Skill");
        const [, form] = platformPost.mock.calls[0] as [string, FormData];
        expect(form.get("description")).toBe("Renders the quarterly report.");
    });

    test("writes inline references into references/", async () => {
        await saveSkillFromConfig({
            config: config({ references: [{ name: "endpoints.md", content: "# Endpoints\nGET /users" }] }),
        });

        const zip = await uploadedZip();
        expect(await zip.file("references/endpoints.md")!.async("string")).toBe("# Endpoints\nGET /users");
    });

    test("fetches artifact-backed references from the session store", async () => {
        const fetchArtifact: FetchSkillArtifact = vi.fn().mockResolvedValue({ content: base64Of("# Uploaded spec"), mimeType: "text/markdown" });

        await saveSkillFromConfig({
            config: config({ references: [{ name: "spec.md", artifact: "uploaded_spec" }] }),
            fetchArtifact,
        });

        expect(fetchArtifact).toHaveBeenCalledWith("uploaded_spec");
        const zip = await uploadedZip();
        expect(await zip.file("references/spec.md")!.async("string")).toBe("# Uploaded spec");
    });

    test("packages binary assets byte-for-byte under assets/", async () => {
        const bytes = new Uint8Array([0x50, 0x4b, 0x03, 0x04, 0x00, 0xff, 0x80, 0x01]);
        const fetchArtifact: FetchSkillArtifact = vi.fn().mockResolvedValue({
            content: btoa(String.fromCharCode(...bytes)),
            mimeType: "application/vnd.sam-template+zip",
        });

        await saveSkillFromConfig({
            config: config({ assets: [{ name: "quarterly_report.samt", artifact: "packaged_report" }] }),
            fetchArtifact,
        });

        expect(fetchArtifact).toHaveBeenCalledWith("packaged_report");
        const zip = await uploadedZip();
        expect(await zip.file("assets/quarterly_report.samt")!.async("uint8array")).toEqual(bytes);
    });

    test("fails with a clear error when a referenced artifact is missing from the session", async () => {
        const fetchArtifact: FetchSkillArtifact = vi.fn().mockRejectedValue(new Error("404 not found"));

        await expect(
            saveSkillFromConfig({
                config: config({ assets: [{ name: "report.samt", artifact: "gone_artifact" }] }),
                fetchArtifact,
            })
        ).rejects.toThrow(/artifact "gone_artifact" which could not be loaded from the session/);
        expect(platformPost).not.toHaveBeenCalled();
    });

    test("fails when an artifact reference is present but no fetcher is available", async () => {
        await expect(
            saveSkillFromConfig({
                config: config({ references: [{ name: "spec.md", artifact: "uploaded_spec" }] }),
            })
        ).rejects.toThrow(/no session is available/);
        expect(platformPost).not.toHaveBeenCalled();
    });

    test("rejects a reference with neither content nor artifact", async () => {
        await expect(
            saveSkillFromConfig({
                config: config({ references: [{ name: "empty.md" }] }),
            })
        ).rejects.toThrow(/neither inline content nor an artifact/);
        expect(platformPost).not.toHaveBeenCalled();
    });

    test("rejects a reference with both content and artifact", async () => {
        await expect(
            saveSkillFromConfig({
                config: config({ references: [{ name: "dual.md", content: "x", artifact: "uploaded_dual" }] }),
            })
        ).rejects.toThrow(/both inline content and an artifact/);
        expect(platformPost).not.toHaveBeenCalled();
    });

    test("rejects bundle filenames that would escape the bundle directories", async () => {
        await expect(
            saveSkillFromConfig({
                config: config({ references: [{ name: "../evil.md", content: "x" }] }),
            })
        ).rejects.toThrow(/invalid filename/);
        await expect(
            saveSkillFromConfig({
                config: config({ assets: [{ name: "nested/asset.samt", artifact: "a" }] }),
            })
        ).rejects.toThrow(/invalid filename/);
        expect(platformPost).not.toHaveBeenCalled();
    });
});
