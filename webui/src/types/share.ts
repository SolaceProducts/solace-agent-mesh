// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { A2AEventSSEPayload } from "./sse";
import type { MessageBubble, TaskMetadata } from "./storage";

export type ShareAccessType = "public" | "authenticated" | "domain_restricted" | "user_specific";

export interface ShareLink {
    id: string;
    sessionId: string;
    title: string;
    isPublic: boolean;
    requireAuthentication: boolean;
    allowedDomains: string[];
    accessType: ShareAccessType;
    createdAt: string;
    snapshotTime: string;
    shareUrl: string;
}

export interface ShareLinkItem {
    id: string;
    sessionId: string;
    title: string;
    isPublic: boolean;
    requireAuthentication: boolean;
    allowedDomains: string[];
    accessType: ShareAccessType;
    createdAt: string;
    messageCount: number;
}

export interface CreateShareLinkRequest {
    requireAuthentication?: boolean;
    allowedDomains?: string[];
}

export interface UpdateShareLinkRequest {
    requireAuthentication?: boolean;
    allowedDomains?: string[];
}

export interface SharedTaskEvents {
    taskId: string;
    events: A2AEventSSEPayload[];
    initialRequestText?: string;
}

export interface SharedSessionView {
    id: string;
    title: string;
    createdAt: string;
    accessType: ShareAccessType;
    tasks: SharedTask[];
    artifacts: SharedArtifact[];
    taskEvents?: Record<string, SharedTaskEvents> | null;
    isOwner?: boolean;
    sessionId?: string | null;
    snapshotTime?: string | null;
}

export interface SharedTask {
    id: string;
    sessionId: string;
    userId: string;
    userMessage?: string;
    messageBubbles: MessageBubble[];
    taskMetadata?: TaskMetadata | null;
    createdAt: string;
    workflowTaskId?: string;
}

export interface SharedArtifact {
    filename: string;
    mimeType: string;
    size: number;
    lastModified?: string | null;
    version?: number | null;
    versionCount?: number | null;
    description?: string | null;
    source?: string | null;
}

export interface ShareLinksListResponse {
    data: ShareLinkItem[];
    meta: {
        pagination: {
            pageNumber: number;
            pageSize: number;
            totalPages: number;
            count: number;
            nextPage: number | null;
        };
    };
}

export interface SharedLinkUserInfo {
    userEmail: string;
    accessLevel: string;
    addedAt: string;
    originalAccessLevel?: string | null;
    originalAddedAt?: string | null;
}

export interface ShareUsersResponse {
    id: string;
    ownerEmail: string;
    users: SharedLinkUserInfo[];
}

export interface AddShareUserRequest {
    userEmail: string;
    accessLevel?: string;
}

export interface BatchAddShareUsersRequest {
    shares: AddShareUserRequest[];
}

export interface BatchAddShareUsersResponse {
    addedCount: number;
    users: SharedLinkUserInfo[];
}

export interface BatchDeleteShareUsersRequest {
    userEmails: string[];
}

export interface BatchDeleteShareUsersResponse {
    deletedCount: number;
}

export interface SharedWithMeItem {
    id: string;
    title: string;
    ownerEmail: string;
    accessLevel: string;
    sharedAt: string;
    shareUrl: string;
    sessionId?: string | null;
    viewedAt?: string | null;
}

export interface ForkSharedChatResponse {
    sessionId: string;
    sessionName: string;
    message: string;
}
