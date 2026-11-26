// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { A2UISurface } from "@/types/a2ui";

export interface NotificationItem {
    id: string;
    title: string;
    body: string;
    status: "unread" | "read";
    source?: string;
    category?: string;
    createdAt: string;
    readAt?: string;
    requestId?: string;
    surface?: A2UISurface;
    action?: { label: string; sessionId?: string; agentId?: string; shareId?: string; path?: string };
    promptState?: "pending" | "resolved";
    promptAnsweredBy?: string;
    promptOutcome?: "approved" | "declined" | "answered" | "cancelled" | "expired" | "withdrawn" | "failed";
}

export interface RespondRequest {
    status: "answered" | "cancelled";
    data: Record<string, unknown>;
}

export interface NotificationPreference {
    channelType: string;
    destinationKey?: string;
    enabled: boolean;
    destination?: string;
}

export interface UserChannel {
    channelType: string;
    confirmed: boolean;
    pending: boolean;
    locked: boolean;
    confirmedAt?: string;
}

export interface NotificationCategoryPreference {
    category: string;
    label: string;
    enabled: boolean;
}

export interface NotificationMatrixCategory {
    category: string;
    label: string;
    group: string;
    description: string;
}

export interface NotificationMatrixChannel {
    channelType: string;
    destinationKey: string;
    destination: string;
}

export interface NotificationMatrixCell {
    category: string;
    channelType: string;
    destinationKey: string;
    enabled: boolean;
    locked: boolean;
}

export interface NotificationMatrix {
    categories: NotificationMatrixCategory[];
    channels: NotificationMatrixChannel[];
    cells: NotificationMatrixCell[];
}

export interface NotificationMatrixCellUpdate {
    category: string;
    channelType: string;
    destinationKey: string;
    enabled: boolean;
}
