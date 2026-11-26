// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { PaginatedResponse } from "@/api/common";

import type {
    BatchAddShareUsersRequest,
    BatchAddShareUsersResponse,
    BatchDeleteShareUsersRequest,
    BatchDeleteShareUsersResponse,
    CreateShareLinkRequest,
    ForkSharedChatResponse,
    SharedSessionView,
    SharedWithMeItem,
    ShareLink,
    ShareLinksListResponse,
    ShareUsersResponse,
    UpdateShareLinkRequest,
} from "../../types/share";
import { api } from "../client";

const SHARE_BASE = "/api/v1/share";

export async function createShareLink(sessionId: string, options: CreateShareLinkRequest = {}): Promise<ShareLink> {
    return api.webui.post(`${SHARE_BASE}/${sessionId}`, options) as Promise<ShareLink>;
}

export async function getShareLinkForSession(sessionId: string): Promise<ShareLink | null> {
    const response = await api.webui.get(`${SHARE_BASE}/link/${sessionId}`, { fullResponse: true });
    if (response.status === 404) {
        return null;
    }
    if (!response.ok) {
        const error = await response.json().catch(() => ({ message: "Failed to get share link" }));
        throw new Error(error.message || "Failed to get share link");
    }
    return response.json();
}

export async function listShareLinks(
    params: {
        pageNumber?: number;
        pageSize?: number;
        search?: string;
    } = {}
): Promise<ShareLinksListResponse> {
    const queryParams = new URLSearchParams();
    if (params.pageNumber != null) queryParams.append("pageNumber", params.pageNumber.toString());
    if (params.pageSize != null) queryParams.append("pageSize", params.pageSize.toString());
    if (params.search) queryParams.append("search", params.search);

    const qs = queryParams.toString();
    return api.webui.get(`${SHARE_BASE}${qs ? `?${qs}` : ""}`) as Promise<ShareLinksListResponse>;
}

export async function viewSharedSession(shareId: string): Promise<SharedSessionView> {
    return api.webui.get(`${SHARE_BASE}/${shareId}`, { credentials: "include" }) as Promise<SharedSessionView>;
}

export async function updateShareLink(shareId: string, options: UpdateShareLinkRequest): Promise<ShareLink> {
    return api.webui.patch(`${SHARE_BASE}/${shareId}`, options) as Promise<ShareLink>;
}

export async function deleteShareLink(shareId: string): Promise<void> {
    await api.webui.delete(`${SHARE_BASE}/${shareId}`);
}

export async function getSharedArtifactContent(shareId: string, filename: string): Promise<{ content: string; mimeType: string }> {
    const encodedFilename = encodeURIComponent(filename);
    const response = await api.webui.get(`${SHARE_BASE}/${shareId}/artifacts/${encodedFilename}`, { fullResponse: true, credentials: "include" });

    if (!response.ok) {
        throw new Error(`Failed to fetch shared artifact content: ${response.statusText}`);
    }

    const contentType = response.headers.get("Content-Type") || "application/octet-stream";
    const mimeType = contentType.split(";")[0].trim();
    const blob = await response.blob();

    const content = await new Promise<string>((resolve, reject) => {
        const reader = new FileReader();
        reader.onloadend = () => resolve(reader.result?.toString().split(",")[1] || "");
        reader.onerror = reject;
        reader.readAsDataURL(blob);
    });

    return { content, mimeType };
}

export async function downloadSharedArtifact(shareId: string, filename: string): Promise<Blob> {
    const encodedFilename = encodeURIComponent(filename);
    const response = await api.webui.get(`${SHARE_BASE}/${shareId}/artifacts/${encodedFilename}`, { fullResponse: true, credentials: "include" });

    if (!response.ok) {
        throw new Error(`Failed to download: ${response.statusText}`);
    }

    return response.blob();
}

export async function getShareUsers(shareId: string): Promise<ShareUsersResponse> {
    return api.webui.get(`${SHARE_BASE}/${shareId}/users`) as Promise<ShareUsersResponse>;
}

export async function addShareUsers(shareId: string, data: BatchAddShareUsersRequest): Promise<BatchAddShareUsersResponse> {
    return api.webui.post(`${SHARE_BASE}/${shareId}/users`, data) as Promise<BatchAddShareUsersResponse>;
}

export async function deleteShareUsers(shareId: string, data: BatchDeleteShareUsersRequest): Promise<BatchDeleteShareUsersResponse> {
    const opts = {
        fullResponse: true as const,
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(data),
    };
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const response: Response = await (api.webui.delete as any)(`${SHARE_BASE}/${shareId}/users`, opts);
    if (!response.ok) {
        const error = await response.json().catch(() => ({ message: "Failed to delete share users" }));
        throw new Error(error.message || "Failed to delete share users");
    }
    return response.json();
}

export async function listSharedWithMe(): Promise<SharedWithMeItem[]> {
    const resp = (await api.webui.get(`${SHARE_BASE}/sharedWithMe`)) as PaginatedResponse<SharedWithMeItem>;
    return resp.data;
}

export async function markShareViewed(shareId: string): Promise<{ viewedAt: string }> {
    return api.webui.post<{ viewedAt: string }>(`${SHARE_BASE}/${shareId}/viewed`);
}

export async function updateShareSnapshot(shareId: string, userEmail?: string): Promise<{ snapshotTime: string }> {
    const body = userEmail ? { userEmail } : undefined;
    return api.webui.post<{ snapshotTime: string }>(`${SHARE_BASE}/${shareId}/updateSnapshot`, body);
}

export async function forkSharedChat(shareId: string): Promise<ForkSharedChatResponse> {
    return api.webui.post(`${SHARE_BASE}/${shareId}/fork`) as Promise<ForkSharedChatResponse>;
}
