// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { ConfigSummary, Skill, SkillFilesResponse } from "@/types";

import { fetchAllPages, instantToIso, pageQuery, platformV2, withoutNulls } from "../common";

import { type SkillFileContent, skillFileContentFrom } from "./service";

interface SkillArtifactParamV2 {
    isList: boolean;
    isOptional: boolean;
    filenameKey: string | null;
}

interface SkillBundledToolV2 {
    toolName: string;
    description: string | null;
    instructions: string | null;
    parameters: unknown;
    artifactParams: Record<string, SkillArtifactParamV2>;
    configSchema: unknown;
}

interface SkillV2 {
    id: string;
    type: "skill";
    origin: "custom" | "builtin";
    name: string;
    label: string | null;
    description: string | null;
    tags: string[];
    discoveryStatus: Skill["discoveryStatus"];
    hasReferences: boolean;
    hasAssets: boolean;
    hasTools: boolean;
    resourceCount: number;
    referenceCount: number;
    assetCount: number;
    bundledTools: SkillBundledToolV2[];
    toolsets: string[];
    bundle: { contentHash: string | null } | null;
    agents: { id: string; name: string; description: string | null }[];
    configSummary: ConfigSummary | null;
    createdBy: string | null;
    updatedBy: string | null;
    createdTime: string | null;
    updatedTime: string | null;
}

interface SkillFileV2 {
    path: string;
    category: "reference" | "asset";
    sizeBytes: number;
}

const CUSTOM_ORIGIN_V1 = "platform";
const REFERENCES_PREFIX = "references/";

const skillPath = (id: string) => `/skills/${encodeURIComponent(id)}`;
const ASSETS_PREFIX = "assets/";

function toBundledTool(tool: SkillBundledToolV2): Record<string, unknown> {
    const artifactParams = Object.fromEntries(Object.entries(tool.artifactParams).map(([name, param]) => [name, { is_list: param.isList, is_optional: param.isOptional, ...(param.filenameKey !== null && { filename_key: param.filenameKey }) }]));
    const v1Shape = {
        tool_name: tool.toolName,
        description: tool.description,
        instructions: tool.instructions,
        parameters: tool.parameters,
        artifact_params: Object.keys(artifactParams).length > 0 ? artifactParams : null,
        config_schema: tool.configSchema,
    };
    return withoutNulls(v1Shape);
}

function toSkill(s: SkillV2): Skill {
    try {
        return mapSkill(s);
    } catch (err) {
        throw Object.assign(new Error(`The skill came back in an unexpected shape: ${err instanceof Error ? err.message : String(err)}`, { cause: err }), { status: 502 });
    }
}

function mapSkill(s: SkillV2): Skill {
    return {
        id: s.id,
        name: s.name,
        label: s.label ?? undefined,
        description: s.description ?? "",
        tags: s.tags,
        origin: s.origin === "builtin" ? "builtin" : CUSTOM_ORIGIN_V1,
        discoveryStatus: s.discoveryStatus,
        hasReferences: s.hasReferences,
        hasAssets: s.hasAssets,
        hasTools: s.hasTools,
        resourceCount: s.resourceCount,
        referenceCount: s.referenceCount,
        assetCount: s.assetCount,
        bundledTools: s.bundledTools.map(toBundledTool),
        contentHash: s.bundle?.contentHash ?? undefined,
        agents: s.agents.map(a => withoutNulls(a)),
        createdBy: s.createdBy ?? "",
        updatedBy: s.updatedBy ?? "",
        createdAt: instantToIso(s.createdTime),
        updatedAt: instantToIso(s.updatedTime),
    };
}

async function fetchSkillsV2(origin: SkillV2["origin"]): Promise<Skill[]> {
    const all = await fetchAllPages(async params => {
        const page = await platformV2.list<SkillV2>(`/skills?origin=${origin}&${pageQuery(params)}`);
        if (!Array.isArray(page?.data)) throw Object.assign(new Error("The skill list came back in an unexpected shape."), { status: 502 });
        return page;
    });
    return all.map(toSkill);
}

const byName = (a: Skill, b: Skill) => a.name.localeCompare(b.name);

export const getCustomSkillsV2 = async () => (await fetchSkillsV2("custom")).toSorted(byName);

export const getBuiltinSkillsV2 = async () => (await fetchSkillsV2("builtin")).toSorted(byName);

export const listCustomSkillsV2 = () => fetchSkillsV2("custom");

export const listBuiltinSkillsV2 = () => fetchSkillsV2("builtin");

export async function getSkillV2(id: string): Promise<Skill> {
    return toSkill(await platformV2.get<SkillV2>(skillPath(id)));
}

export async function createSkillV2(description: string, file: File): Promise<Skill> {
    const form = new FormData();
    form.append("description", description);
    form.append("file", file);
    return toSkill(await platformV2.post<SkillV2>("/skills", form));
}

export async function deleteSkillV2(id: string): Promise<void> {
    await platformV2.delete(skillPath(id));
}

export async function uploadSkillZipV2(id: string, file: File): Promise<Skill> {
    const form = new FormData();
    form.append("file", file);
    return toSkill(await platformV2.put<SkillV2>(`${skillPath(id)}/bundle`, form));
}

export async function getSkillFilesV2(id: string): Promise<SkillFilesResponse> {
    const all = await fetchAllPages(params => platformV2.list<SkillFileV2>(`${skillPath(id)}/files?${pageQuery(params)}`));
    const inCategory = (category: SkillFileV2["category"], prefix: string) => all.filter(file => file.category === category).map(file => ({ path: file.path.replace(prefix, ""), sizeBytes: file.sizeBytes }));
    return { references: inCategory("reference", REFERENCES_PREFIX), assets: inCategory("asset", ASSETS_PREFIX) };
}

export async function getSkillFileContentV2(id: string, path: string): Promise<SkillFileContent> {
    const response = await platformV2.getResponse(`${skillPath(id)}/fileContent?path=${encodeURIComponent(path)}`);
    return await skillFileContentFrom(response);
}
