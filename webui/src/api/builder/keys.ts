// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export const builderKeys = {
    all: ["builder"] as const,
    manifests: () => [...builderKeys.all, "manifest"] as const,
    manifestVersions: (sessionId: string, filename: string) => [...builderKeys.manifests(), "versions", sessionId, filename] as const,
    manifestContent: (sessionId: string, filename: string, version: number | "latest") => [...builderKeys.manifests(), "content", sessionId, filename, version] as const,

    sessions: () => [...builderKeys.all, "sessions"] as const,
    sessionList: (queryString: string) => [...builderKeys.sessions(), "list", queryString] as const,
    sessionsInfinite: (pageSize: number, serverFilter: boolean) => [...builderKeys.sessions(), "infinite", pageSize, serverFilter] as const,
    sessionArtifacts: (sessionId: string) => [...builderKeys.sessions(), "artifacts", sessionId] as const,

    dropped: (sessionId: string, signature: string) => [...builderKeys.all, "dropped", sessionId, signature] as const,
    droppedAll: () => [...builderKeys.all, "dropped"] as const,

    testAgent: (sessionId: string) => [...builderKeys.all, "testAgent", sessionId] as const,
};
