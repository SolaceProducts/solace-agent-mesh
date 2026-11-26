// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export interface CollaborativeUser {
    id: string;
    name: string;
    email: string;
    avatar?: string;
    role: "owner" | "collaborator";
    isOnline: boolean;
    lastSeen?: Date;
}

export interface CollaborativeSessionInfo {
    isSharedSession: boolean;
    wasSharedSession: boolean;
    collaborators: CollaborativeUser[];
    currentUserId: string;
    ownerId: string;
    sharedAt?: number;
    sharedByName?: string;
    sharedWithNames?: string[];
}

export interface ShareEvent {
    type: "share_notification";
    timestamp: number;
    sharedBy: string;
    sharedWith: string[];
}
