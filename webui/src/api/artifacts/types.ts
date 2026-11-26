// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { ArtifactInfo } from "@/types";

export interface BulkArtifactsResponse {
    artifacts: Array<{
        filename: string;
        size: number;
        mimeType: string | null;
        lastModified: string | null;
        uri: string | null;
        version: number | null;
        sessionId: string;
        sessionName: string | null;
        projectId: string | null;
        projectName: string | null;
        agentId?: string | null;
        source: string | null;
        tags: string[] | null;
    }>;
    totalCount: number;
    hasMore: boolean;
    nextPage: number | null;
}

export interface ArtifactWithSession extends ArtifactInfo {
    sessionId: string;
    sessionName: string | null;
    projectId?: string;
    projectName?: string | null;
    agentId?: string;
    source?: string;
}
