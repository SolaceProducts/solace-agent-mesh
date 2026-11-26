// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { getArtifactContent as getArtifactContentUtil, validIdOrUndefined } from "@/utils/file";

import type { ArtifactInfo } from "@/types";

import { api, getErrorFromResponse } from "../client";

import type { ArtifactWithSession, BulkArtifactsResponse } from "./types";

interface ListArtifactsOptions {
    includeWorking?: boolean;
    signal?: AbortSignal;
}

function listQuery(params: Record<string, string>, options: ListArtifactsOptions): string {
    const query = new URLSearchParams(params);
    if (options.includeWorking) {
        query.set("visibility", "all");
    }
    const encoded = query.toString();
    return encoded ? `?${encoded}` : "";
}

export async function getSessionArtifacts(sessionId: string, options: ListArtifactsOptions = {}): Promise<ArtifactInfo[]> {
    const response = await api.webui.get<{ data: ArtifactInfo[] }>(`/api/v1/artifacts/${sessionId}${listQuery({}, options)}`, { signal: options.signal });
    const items = response?.data ?? [];
    return items.map(artifact => ({
        ...artifact,
        uri: artifact.uri || `artifact://${sessionId}/${artifact.filename}`,
    }));
}

export async function getProjectArtifacts(projectId: string, options: ListArtifactsOptions = {}): Promise<ArtifactInfo[]> {
    const response = await api.webui.get<{ data: ArtifactInfo[] }>(`/api/v1/artifacts/null${listQuery({ projectId }, options)}`, { signal: options.signal });
    return response?.data ?? [];
}

export function isProjectArtifact(artifact: ArtifactWithSession): boolean {
    return artifact.sessionId.startsWith("project:") || artifact.sessionId.startsWith("project-") || artifact.source === "project";
}

export function getArtifactApiUrl(artifact: ArtifactWithSession): string {
    if (isProjectArtifact(artifact) && artifact.projectId) {
        return `/api/v1/artifacts/null/${encodeURIComponent(artifact.filename)}?projectId=${encodeURIComponent(artifact.projectId)}`;
    }
    return `/api/v1/artifacts/${encodeURIComponent(artifact.sessionId)}/${encodeURIComponent(artifact.filename)}`;
}

export async function getArtifactContentWithValidation({ sessionId, projectId, filename, version }: { sessionId?: string; projectId?: string; filename: string; version?: number }): Promise<{ content: string; mimeType: string }> {
    const validSession = validIdOrUndefined(sessionId);
    const validProject = validIdOrUndefined(projectId);
    return getArtifactContentUtil({
        filename,
        ...(validSession ? { sessionId: validSession } : {}),
        ...(validProject ? { projectId: validProject } : {}),
        version: version ?? "latest",
    });
}

function convertDataUrlToBlobUrlForPdfJs(url: string): string {
    try {
        const [header, base64Data] = url.split(",");
        const mimeMatch = header.match(/data:([^;]+)/);
        const mimeType = mimeMatch ? mimeMatch[1] : "application/pdf";
        const binaryString = atob(base64Data);
        const bytes = new Uint8Array(binaryString.length);
        for (let i = 0; i < binaryString.length; i++) {
            bytes[i] = binaryString.charCodeAt(i);
        }
        const blob = new Blob([bytes], { type: mimeType });
        return URL.createObjectURL(blob);
    } catch (e) {
        throw new Error(`Failed to decode data URL: ${e instanceof Error ? e.message : String(e)}`, { cause: e });
    }
}

export async function fetchPdfBlob(url: string): Promise<string> {
    if (url.startsWith("data:")) {
        return convertDataUrlToBlobUrlForPdfJs(url);
    }

    if (url.startsWith("blob:")) {
        return url;
    }

    const response = await api.webui.get(url, { fullResponse: true });
    if (!response.ok) {
        throw new Error(await getErrorFromResponse(response));
    }
    const blob = await response.blob();
    return URL.createObjectURL(blob);
}

const LIST_ALL_ARTIFACTS_PAGE_SIZE = 50;

export async function getAllArtifacts(pageNumber: number = 1, pageSize: number = LIST_ALL_ARTIFACTS_PAGE_SIZE, search?: string, projectId?: string): Promise<BulkArtifactsResponse> {
    const params = new URLSearchParams({
        pageNumber: String(pageNumber),
        pageSize: String(pageSize),
    });
    if (search) {
        params.set("search", search);
    }
    if (projectId) {
        params.set("projectId", projectId);
    }
    type AllItem = BulkArtifactsResponse["artifacts"][number];
    const envelope = await api.webui.get<{
        data: AllItem[];
        meta: { pagination: { count: number; nextPage: number | null } };
    }>(`/api/v1/artifacts/all?${params.toString()}`);
    const items = envelope?.data ?? [];
    const pagination = envelope?.meta?.pagination;
    return {
        artifacts: items,
        totalCount: pagination?.count ?? items.length,
        hasMore: pagination?.nextPage != null,
        nextPage: pagination?.nextPage ?? null,
    };
}
