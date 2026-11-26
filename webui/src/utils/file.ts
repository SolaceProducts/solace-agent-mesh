// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { api, getErrorFromResponse } from "../api";
import type { ArtifactInfo } from "../types";
import type { Project } from "../types/projects";

export const validIdOrUndefined = (id: string | null | undefined): string | undefined => (id && id.trim() && id !== "null" && id !== "undefined" ? id : undefined);

export const fileToBase64 = (file: File): Promise<string> =>
    new Promise((resolve, reject) => {
        const reader = new FileReader();
        reader.onload = () => resolve((reader.result as string).split(",")[1]);
        reader.onerror = error => reject(error);
        reader.readAsDataURL(file);
    });

export const blobToBase64 = (blob: Blob): Promise<string> => {
    return new Promise((resolve, reject) => {
        const reader = new FileReader();
        reader.onloadend = () => resolve(reader.result?.toString().split(",")[1] || "");
        reader.onerror = reject;
        reader.readAsDataURL(blob);
    });
};

export function base64ToArrayBuffer(b64: string): ArrayBuffer {
    const bin = atob(b64);
    const bytes = new Uint8Array(bin.length);
    for (let i = 0; i < bin.length; i++) bytes[i] = bin.codePointAt(i) ?? 0;
    return bytes.buffer;
}

export const getArtifactUrl = ({ filename, sessionId, projectId, version }: { filename: string; sessionId?: string; projectId?: string; version?: number | "latest" }): string => {
    const validSessionId = validIdOrUndefined(sessionId);
    const encodedFilename = encodeURIComponent(filename);

    const basePath = validSessionId ? `/api/v1/artifacts/${validSessionId}/${encodedFilename}/versions` : `/api/v1/artifacts/null/${encodedFilename}/versions`;
    const versionPath = version !== undefined ? `/${version}` : "";
    const url = `${basePath}${versionPath}`;

    if (!validSessionId && projectId) {
        return `${url}?projectId=${projectId}`;
    }

    if (!validSessionId && !projectId) {
        throw new Error("No valid context for artifact: either sessionId or projectId must be provided");
    }

    return url;
};

export const getArtifactContent = async ({ filename, sessionId, projectId, version }: { filename: string; sessionId?: string; projectId?: string; version?: number | "latest" }): Promise<{ content: string; mimeType: string }> => {
    const contentUrl = getArtifactUrl({
        filename,
        sessionId,
        projectId,
        version,
    });

    const contentResponse = await api.webui.get(contentUrl, { fullResponse: true });
    if (!contentResponse.ok) {
        throw new Error(await getErrorFromResponse(contentResponse));
    }

    const contentType = contentResponse.headers.get("Content-Type") || "application/octet-stream";
    const mimeType = contentType.split(";")[0].trim();
    const blob = await contentResponse.blob();
    const content = await blobToBase64(blob);

    return { content, mimeType };
};

export const parseArtifactUri = (uri: string): { sessionId: string | null; filename: string; version: string | null } | null => {
    try {
        const url = new URL(uri);
        if (url.protocol !== "artifact:") {
            return null;
        }

        const pathParts = url.pathname.split("/").filter(p => p);

        if (pathParts.length < 3) {
            const sessionId = url.hostname || null;
            const filename = pathParts.length > 0 ? decodeURIComponent(pathParts[pathParts.length - 1]) : "";
            if (!filename) {
                return null;
            }
            const version = url.searchParams.get("version");
            return { sessionId, filename, version };
        }

        const sessionId = pathParts[1];
        const filename = decodeURIComponent(pathParts[2]);

        const version = url.searchParams.get("version");
        return { sessionId, filename, version };
    } catch (e) {
        console.error("Invalid artifact URI:", e);
        return null;
    }
};

export const getSourceProjectName = (artifact: ArtifactInfo | undefined, activeProject: Project | null): string | undefined => {
    if (artifact?.sourceProjectId === activeProject?.id) return activeProject?.name;
    if (artifact?.source === "project" && !artifact?.sourceProjectId) return "Project";
    return undefined;
};
