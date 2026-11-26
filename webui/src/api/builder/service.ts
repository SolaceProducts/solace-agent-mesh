// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { ApplyBuildRequest, ApplyBuildResponse, RemoveDroppedRequest, RemoveDroppedResponse } from "@/api/builder/applyTypes";
import { api } from "@/api/client";
import type { PaginatedResponse } from "@/api/common";

import { type ArtifactInfo } from "@/types/chat";
import type { Agent } from "@/types/models";
import { type Session } from "@/types/session";

export async function listManifestVersions(sessionId: string, filename: string): Promise<number[]> {
    const url = `/api/v1/artifacts/${sessionId}/${encodeURIComponent(filename)}/versions`;
    const { data = [] } = await api.webui.get<{ data: number[] }>(url);
    return data;
}

export async function getManifestContent(sessionId: string, filename: string, version: number | "latest" = "latest"): Promise<string | null> {
    let resolvedVersion: number;
    if (version === "latest") {
        const versions = await listManifestVersions(sessionId, filename);
        if (versions.length === 0) return null;
        resolvedVersion = Math.max(...versions);
    } else {
        resolvedVersion = version;
    }
    const res = await api.webui.get(manifestVersionUrl(sessionId, filename, resolvedVersion), { fullResponse: true });
    if (!res.ok) return null;
    return decodeBody(res);
}

export async function getManifestContentAt(sessionId: string, filename: string, version: number): Promise<string> {
    const res = await api.webui.get(manifestVersionUrl(sessionId, filename, version), { fullResponse: true });
    if (!res.ok) throw Object.assign(new Error(`The platform answered ${res.status} for "${filename}" at version ${version}.`), { status: res.status });
    return decodeBody(res);
}

const manifestVersionUrl = (sessionId: string, filename: string, version: number) => `/api/v1/artifacts/${sessionId}/${encodeURIComponent(filename)}/versions/${version}`;

async function decodeBody(res: { blob: () => Promise<Blob> }): Promise<string> {
    const blob = await res.blob();
    const buffer = await blob.arrayBuffer();
    return new TextDecoder().decode(buffer);
}

export async function uploadManifest(sessionId: string, filename: string, yamlText: string, mimeType: string): Promise<{ ok: boolean }> {
    const file = new File([yamlText], filename, { type: mimeType });
    const formData = new FormData();
    formData.append("uploadFile", file);
    formData.append("filename", filename);
    formData.append("sessionId", sessionId);
    const res = await api.webui.post("/api/v1/artifacts/upload", formData, { fullResponse: true });
    return { ok: res.ok };
}

export async function uploadArtifact(sessionId: string, filename: string, content: string, mimeType: string): Promise<{ ok: boolean }> {
    return uploadManifest(sessionId, filename, content, mimeType);
}

export async function patchSessionName(sessionId: string, name: string, agentId: string): Promise<void> {
    await api.webui.patch(`/api/v1/sessions/${sessionId}`, { name, agentId });
}

export async function listBuilderSessions(queryString: string): Promise<PaginatedResponse<Session>> {
    return api.webui.get<PaginatedResponse<Session>>(`/api/v1/sessions?${queryString}`);
}

export async function listSessionArtifacts(sessionId: string): Promise<ArtifactInfo[]> {
    const result = await api.webui.get<ArtifactInfo[]>(`/api/v1/artifacts/${sessionId}`);
    return Array.isArray(result) ? result : [];
}

export async function deleteSession(sessionId: string): Promise<void> {
    await api.webui.delete(`/api/v1/sessions/${sessionId}`);
}

export interface CreateTestAgentRequest {
    componentName: string;
    componentKind: "agent" | "workflow" | "skill" | "a2a_agent";
}

export async function createTestAgent(sessionId: string, request: CreateTestAgentRequest): Promise<Agent> {
    return api.webui.post<Agent>(`/api/internal/platform/builder/sessions/${sessionId}/test-agent`, request);
}

export async function getTestAgent(sessionId: string): Promise<Agent | null> {
    const res = await api.webui.get(`/api/internal/platform/builder/sessions/${sessionId}/test-agent`, { fullResponse: true });
    if (res.status === 404) return null;
    if (!res.ok) {
        throw new Error(`get test agent: ${res.status}`);
    }
    return (await res.json()) as Agent;
}

export async function deleteTestAgent(sessionId: string): Promise<void> {
    await api.webui.delete(`/api/internal/platform/builder/sessions/${sessionId}/test-agent`);
}

export interface TestAgentParentLookup {
    builderSessionId: string;
    componentName: string;
    componentKind?: string;
    testAgentName: string;
    testAgentId: string;
}

export async function lookupTestAgentParent(testAgentName: string): Promise<TestAgentParentLookup | null> {
    const url = `/api/internal/platform/builder/test-agents/lookup?name=${encodeURIComponent(testAgentName)}`;
    const res = await api.webui.get(url, { fullResponse: true });
    if (res.status === 404) return null;
    if (!res.ok) {
        throw new Error(`lookup test agent parent: ${res.status}`);
    }
    return (await res.json()) as TestAgentParentLookup;
}

export async function applyBuild(request: ApplyBuildRequest): Promise<ApplyBuildResponse> {
    return api.platform.post<ApplyBuildResponse>("/api/internal/platform/builder/builds", request);
}

export async function previewBuild(request: ApplyBuildRequest, options?: { signal?: AbortSignal }): Promise<ApplyBuildResponse> {
    const dryRun = { ...request, dryRun: true };
    if (!options) return applyBuild(dryRun);
    return api.platform.post<ApplyBuildResponse>("/api/internal/platform/builder/builds", dryRun, options);
}

export async function removeDropped(request: RemoveDroppedRequest): Promise<RemoveDroppedResponse> {
    return api.platform.post<RemoveDroppedResponse>("/api/internal/platform/builder/removals", request);
}
