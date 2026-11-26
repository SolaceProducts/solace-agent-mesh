// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useMutation } from "@tanstack/react-query";

import { api } from "@/api/client";

import { useChatContext, useProjectContext } from "@/providers/contexts";

import type { ArtifactInfo } from "@/types";

import { getErrorMessage } from "../utils/api";
import { downloadBlob } from "../utils/download";

interface DownloadScope {
    sessionId: string | null;
    projectId: string | null;
    explicitProjectScope: boolean;
}

export const resolveArtifactDownloadEndpoint = (artifact: ArtifactInfo, scope: DownloadScope): string => {
    const { sessionId, projectId, explicitProjectScope } = scope;
    const trimmedSession = sessionId?.trim();
    const hasSessionContext = trimmedSession && trimmedSession !== "null" && trimmedSession !== "undefined";
    const projectScoped = explicitProjectScope || artifact.source === "project";
    const file = encodeURIComponent(artifact.filename);

    if (projectScoped && projectId) {
        return `/api/v1/artifacts/null/${file}?projectId=${encodeURIComponent(projectId)}`;
    }
    if (hasSessionContext) {
        return `/api/v1/artifacts/${trimmedSession}/${file}`;
    }
    if (projectId) {
        return `/api/v1/artifacts/null/${file}?projectId=${encodeURIComponent(projectId)}`;
    }
    throw new Error("No valid session or project context for downloading artifact.");
};

const downloadArtifactFile = async (artifact: ArtifactInfo, scope: DownloadScope) => {
    const endpoint = resolveArtifactDownloadEndpoint(artifact, scope);

    const response = await api.webui.get(endpoint, { fullResponse: true });
    if (!response.ok) {
        throw new Error(`Failed to download artifact: ${response.statusText}`);
    }
    const blob = await response.blob();
    downloadBlob(blob, artifact.filename);
};

export const useDownload = (projectIdOverride?: string | null) => {
    const { addNotification, sessionId, displayError } = useChatContext();
    const { activeProject } = useProjectContext();

    const mutation = useMutation({
        mutationFn: (artifact: ArtifactInfo) => {
            const projectId = projectIdOverride || activeProject?.id || null;
            return downloadArtifactFile(artifact, { sessionId, projectId, explicitProjectScope: !!projectIdOverride });
        },
        onSuccess: (_result, artifact) => addNotification(`Downloaded artifact: ${artifact.filename}.`, "success"),
        onError: error => displayError({ title: "Failed to Download Artifact", error: getErrorMessage(error, "An unknown error occurred while downloading the artifact."), errorSource: "api" }),
    });

    const onDownload = async (artifact: ArtifactInfo) => {
        try {
            await mutation.mutateAsync(artifact);
        } catch {}
    };

    return { onDownload, isDownloading: mutation.isPending };
};
