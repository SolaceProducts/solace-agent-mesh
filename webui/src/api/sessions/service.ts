// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { api } from "@/api/client";
import type { PaginatedResponse } from "@/api/common";

import type { CompactSessionRequest, CompactSessionResponse, ContextUsage, Session } from "@/types";

function normalizeSession(raw: Record<string, unknown>): Session {
    return {
        ...raw,
        agentId: (raw.agentId ?? raw.agent_id ?? null) as Session["agentId"],
        projectId: (raw.projectId ?? raw.project_id ?? null) as Session["projectId"],
        projectName: (raw.projectName ?? raw.project_name ?? null) as Session["projectName"],
        createdAt: (raw.createdAt ?? raw.created_time ?? raw.createdTime ?? "") as string,
        updatedAt: (raw.updatedAt ?? raw.updated_time ?? raw.updatedTime ?? "") as string,
        userId: (raw.userId ?? raw.user_id) as Session["userId"],
        hasRunningBackgroundTask: (raw.hasRunningBackgroundTask ?? raw.has_running_background_task) as Session["hasRunningBackgroundTask"],
        ownerDisplayName: (raw.ownerDisplayName ?? raw.owner_display_name ?? null) as Session["ownerDisplayName"],
        ownerEmail: (raw.ownerEmail ?? raw.owner_email ?? null) as Session["ownerEmail"],
        isEphemeralTest: (raw.isEphemeralTest ?? raw.is_ephemeral_test ?? false) as Session["isEphemeralTest"],
    } as Session;
}

export const getRecentSessions = async (maxItems: number, agentId?: string): Promise<Session[]> => {
    let url = `/api/v1/sessions?pageNumber=1&pageSize=${maxItems}`;
    if (agentId) url += `&includeAgentIds=${encodeURIComponent(agentId)}`;
    const result = await api.webui.get<PaginatedResponse<Session>>(url);
    return (result.data || []).map(s => normalizeSession(s as unknown as Record<string, unknown>));
};

export const getPaginatedSessions = async (pageNumber: number = 1, pageSize: number = 20, source?: string, agentId?: string): Promise<PaginatedResponse<Session>> => {
    let url = `/api/v1/sessions?pageNumber=${pageNumber}&pageSize=${pageSize}`;
    if (source) url += `&source=${source}`;
    if (agentId) url += `&includeAgentIds=${encodeURIComponent(agentId)}`;
    const result = await api.webui.get<PaginatedResponse<Session>>(url);
    return { ...result, data: (result.data || []).map(s => normalizeSession(s as unknown as Record<string, unknown>)) };
};

export const getSession = async (sessionId: string): Promise<Session> => {
    const raw = await api.webui.get<Record<string, unknown>>(`/api/v1/sessions/${encodeURIComponent(sessionId)}`);
    return normalizeSession(raw);
};

export const getSessionChatTasks = async (sessionId: string) => {
    return api.webui.get(`/api/v1/sessions/${sessionId}/chatTasks`);
};

export const markSessionViewed = async (sessionId: string): Promise<{ lastViewedAt: string }> => {
    return api.webui.post(`/api/v1/sessions/${sessionId}/viewed`, {});
};

export async function getSessionContextUsage(sessionId: string, model?: string, agentName?: string): Promise<ContextUsage> {
    const params = new URLSearchParams();
    if (model) params.append("model", model);
    if (agentName) params.append("agentName", agentName);
    const query = params.toString() ? `?${params}` : "";
    return api.webui.get<ContextUsage>(`/api/v1/sessions/${sessionId}/contextUsage${query}`);
}

export async function compactSession(sessionId: string, request?: CompactSessionRequest): Promise<CompactSessionResponse> {
    return api.webui.post<CompactSessionResponse>(`/api/v1/sessions/${sessionId}/compact`, request ?? {});
}
