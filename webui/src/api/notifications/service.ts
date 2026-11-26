// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { api } from "@/api/client";
import type { PaginatedResponse } from "@/api/common";

import type { NotificationCategoryPreference, NotificationItem, NotificationMatrix, NotificationMatrixCellUpdate, NotificationPreference, RespondRequest, UserChannel } from "./types";

export async function fetchInbox(pageNumber = 1, pageSize = 50, unreadOnly = false, category = ""): Promise<PaginatedResponse<NotificationItem>> {
    const params = new URLSearchParams({ pageNumber: String(pageNumber), pageSize: String(pageSize) });
    if (unreadOnly) {
        params.set("status", "unread");
    }
    if (category) {
        params.set("category", category);
    }
    return api.webui.get(`/api/v1/notify/inbox?${params.toString()}`);
}

export async function markRead(id: string): Promise<void> {
    await api.webui.post(`/api/v1/notify/inbox/${id}/read`);
}

export async function markUnread(id: string): Promise<void> {
    await api.webui.post(`/api/v1/notify/inbox/${id}/unread`);
}

export async function markAllRead(category = ""): Promise<void> {
    const query = category ? `?${new URLSearchParams({ category }).toString()}` : "";
    await api.webui.post(`/api/v1/notify/inbox/read-all${query}`);
}

export async function deleteNotification(id: string): Promise<void> {
    await api.webui.delete(`/api/v1/notify/inbox/${id}`);
}

export async function clearAllNotifications(): Promise<void> {
    await api.webui.delete(`/api/v1/notify/inbox`);
}

export async function respondToPrompt(id: string, body: RespondRequest): Promise<void> {
    await api.webui.post(`/api/v1/notify/inbox/${id}/respond`, body);
}

export async function fetchPreferences(): Promise<PaginatedResponse<NotificationPreference>> {
    return api.webui.get(`/api/v1/notify/preferences`);
}

export async function setPreference(channelType: string, destinationKey: string, enabled: boolean): Promise<NotificationPreference> {
    return api.webui.put(`/api/v1/notify/preferences`, { channelType, destinationKey, enabled });
}

export async function fetchCategoryPreferences(): Promise<PaginatedResponse<NotificationCategoryPreference>> {
    return api.webui.get(`/api/v1/notify/preferences/categories`);
}

export async function setCategoryPreference(category: string, enabled: boolean): Promise<NotificationCategoryPreference> {
    return api.webui.put(`/api/v1/notify/preferences/categories`, { category, enabled });
}

export async function fetchChannels(): Promise<PaginatedResponse<UserChannel>> {
    return api.webui.get(`/api/v1/notify/channels`);
}

export async function turnOnChannel(channelType: string): Promise<UserChannel> {
    return api.webui.post(`/api/v1/notify/channels/${encodeURIComponent(channelType)}/confirm`);
}

export async function turnOffChannel(channelType: string): Promise<UserChannel> {
    return api.webui.post(`/api/v1/notify/channels/${encodeURIComponent(channelType)}/disable`);
}

export async function verifyChannelCode(channelType: string, code: string): Promise<UserChannel> {
    return api.webui.post(`/api/v1/notify/channels/${encodeURIComponent(channelType)}/confirm/verify`, { code });
}

export async function fetchNotificationMatrix(): Promise<NotificationMatrix> {
    return api.webui.get(`/api/v1/notify/preferences/matrix`);
}

export async function updateNotificationMatrix(cells: NotificationMatrixCellUpdate[], signal?: AbortSignal): Promise<NotificationMatrix> {
    return api.webui.put(`/api/v1/notify/preferences/matrix`, { cells }, { signal });
}
