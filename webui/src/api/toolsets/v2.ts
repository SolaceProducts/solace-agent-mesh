// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { ConfigSchemaField, ConfigSummary, Toolset, ToolsetConfigResponse } from "@/types";

import { fetchAllPages, instantToIso, pageQuery, platformV2 } from "../common";

import type { CreateToolsetRequest, ToolsetBuildTarget, UpdateToolsetRequest } from "./service";

interface ToolsetToolV2 {
    toolName: string;
    description: string | null;
    timeoutSeconds: number | null;
    sandboxProfile: string | null;
    parameters: Record<string, unknown> | null;
    configSchema: ConfigSchemaField[] | null;
}

interface ToolsetV2 {
    id: string;
    type: "toolset";
    origin: Toolset["type"];
    name: string;
    description: string | null;
    runtime: string | null;
    discoveryStatus: NonNullable<Toolset["discoveryStatus"]> | null;
    discoveryErrors: string[];
    bundle: { filename: string; sizeBytes: number | null; contentHash: string | null } | null;
    tools: ToolsetToolV2[];
    agents: { id: string; name: string; description: string | null }[];
    configSummary: ConfigSummary | null;
    capabilities: Record<string, unknown>;
    default: boolean;
    aliases: string[];
    createdBy: string | null;
    updatedBy: string | null;
    createdTime: string | null;
    updatedTime: string | null;
}

interface ToolsetConfigurationV2 {
    values: Record<string, Record<string, unknown>>;
    updatedBy: string | null;
    updatedTime: string | null;
}

interface ToolsetBuildTargetV2 {
    os: string;
    arch: string;
    source: "str_init" | "fallback";
    pythonVersion: string | null;
}

function toToolset(t: ToolsetV2): Toolset {
    const createdAt = instantToIso(t.createdTime);
    return {
        id: t.id,
        name: t.name,
        description: t.description ?? "",
        type: t.origin,
        default: t.default,
        aliases: t.aliases,
        runtime: t.runtime ?? undefined,
        discoveryStatus: t.discoveryStatus ?? undefined,
        discoveryErrors: t.discoveryErrors,
        zipFilename: t.bundle?.filename ?? null,
        zipFileSize: t.bundle?.sizeBytes ?? null,
        contentHash: t.bundle?.contentHash ?? undefined,
        files:
            t.origin === "builtin"
                ? []
                : t.tools.map(tool => ({
                      id: tool.toolName,
                      filename: "",
                      toolName: tool.toolName,
                      toolFunction: null,
                      toolDescription: tool.description ?? "",
                      timeoutSeconds: tool.timeoutSeconds ?? 0,
                      sandboxProfile: tool.sandboxProfile ?? "",
                      modulePath: null,
                      fileSize: 0,
                      configSchema: tool.configSchema,
                      createdAt,
                  })),
        tools: t.tools.map(tool => ({
            toolName: tool.toolName,
            toolDescription: tool.description ?? undefined,
            timeoutSeconds: tool.timeoutSeconds ?? undefined,
            sandboxProfile: tool.sandboxProfile ?? undefined,
            configSchema: tool.configSchema ?? undefined,
            parameters: tool.parameters ?? undefined,
        })),
        agents: t.agents.map(a => ({ id: a.id, name: a.name, description: a.description ?? undefined })),
        configSummary: t.configSummary,
        createdBy: t.createdBy ?? "",
        updatedBy: t.updatedBy ?? "",
        createdAt,
        updatedAt: instantToIso(t.updatedTime),
    };
}

const toolsetPath = (toolsetId: string) => `/toolsets/${encodeURIComponent(toolsetId)}`;

const toToolsetConfig = (c: ToolsetConfigurationV2): ToolsetConfigResponse => ({ values: c.values, updatedBy: c.updatedBy ?? "", updatedAt: instantToIso(c.updatedTime) });

async function getToolsetsV2(origin: Toolset["type"]): Promise<Toolset[]> {
    const all = await fetchAllPages(params => platformV2.list<ToolsetV2>(`/toolsets?origin=${origin}&${pageQuery(params)}`));
    return all.map(toToolset).toSorted((a, b) => a.name.localeCompare(b.name));
}

export const getBuiltinToolsetsV2 = () => getToolsetsV2("builtin");

export const getCustomToolsetsV2 = () => getToolsetsV2("custom");

export async function getToolsetConfigV2(toolsetId: string, signal?: AbortSignal): Promise<ToolsetConfigResponse> {
    return toToolsetConfig(await platformV2.get<ToolsetConfigurationV2>(`${toolsetPath(toolsetId)}/config`, { signal }));
}

export async function patchToolsetConfigV2(toolsetId: string, values: Record<string, Record<string, unknown>>): Promise<ToolsetConfigResponse> {
    return toToolsetConfig(await platformV2.patch<ToolsetConfigurationV2>(`${toolsetPath(toolsetId)}/config`, { values }));
}

export async function getToolsetBuildTargetV2(): Promise<ToolsetBuildTarget> {
    const { os, arch, source, pythonVersion } = await platformV2.get<ToolsetBuildTargetV2>("/toolsetBuildTarget");
    return { os, arch, source: source === "str_init" ? "str-init" : source, pythonVersion: pythonVersion ?? undefined };
}

export async function createToolsetV2(request: CreateToolsetRequest): Promise<{ id: string }> {
    const created = await platformV2.post<ToolsetV2>("/toolsets", request);
    return { id: created.id };
}

export async function updateToolsetV2(toolsetId: string, request: UpdateToolsetRequest): Promise<void> {
    await platformV2.patch<ToolsetV2>(toolsetPath(toolsetId), request);
}

export async function deleteToolsetV2(toolsetId: string): Promise<void> {
    await platformV2.delete(toolsetPath(toolsetId));
}

export async function uploadToolsetBundleV2(toolsetId: string, file: File): Promise<void> {
    const formData = new FormData();
    formData.append("file", file);
    await platformV2.put<ToolsetV2>(`${toolsetPath(toolsetId)}/bundle`, formData);
}

export async function downloadToolsetBundleV2(toolsetId: string): Promise<Blob> {
    const response = await platformV2.getResponse(`${toolsetPath(toolsetId)}/bundle`);
    return await response.blob();
}
