// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export interface Session {
    type?: "session";
    id: string;
    userId?: string;
    createdBy?: string;
    createdAt: string;
    updatedAt: string;
    name: string | null;
    agentId?: string | null;
    project?: { id: string; name?: string | null } | null;
    projectId?: string | null;
    projectName?: string | null;
    source?: string | null;
    hasRunningBackgroundTask?: boolean;
    ownerDisplayName?: string | null;
    ownerEmail?: string | null;
    scheduledTaskId?: string | null;
    scheduledTaskName?: string | null;
    lastViewedAt?: string | null;
    isEphemeralTest?: boolean;
}
