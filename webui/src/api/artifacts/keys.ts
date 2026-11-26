// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export const artifactKeys = {
    all: ["artifacts"] as const,
    lists: () => [...artifactKeys.all, "list", "paginated"] as const,
    session: (sessionId: string) => [...artifactKeys.all, "session", sessionId] as const,
    project: (projectId: string) => [...artifactKeys.all, "project", projectId] as const,
    sessionWithWorking: (sessionId: string) => [...artifactKeys.all, "session-with-working", sessionId] as const,
    projectWithWorking: (projectId: string) => [...artifactKeys.all, "project-with-working", projectId] as const,
    content: (sessionId: string | null, projectId: string | null, filename: string, version?: number) => [...artifactKeys.all, "content", sessionId, projectId, filename, version] as const,
    versions: (sessionId: string | null, projectId: string | null, filename: string) => [...artifactKeys.all, "versions", sessionId, projectId, filename] as const,
    pdfBlob: (url: string) => [...artifactKeys.all, "pdf-blob", url] as const,
};
