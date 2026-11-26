// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { api } from "@/api/client";

import type { FileAttachment } from "@/types";

import { parseArtifactUri } from "./file";

export const downloadBlob = (blob: Blob, filename?: string) => {
    try {
        const url = URL.createObjectURL(blob);
        const a = document.createElement("a");
        a.href = url;
        a.download = filename || "download";
        document.body.appendChild(a);
        a.click();
        document.body.removeChild(a);
        URL.revokeObjectURL(url);
    } catch (error) {
        console.error("Error downloading blob:", error);
    }
};

export const downloadAuthenticatedFile = async (url: string, filename: string): Promise<boolean> => {
    try {
        const response = await api.platform.get(url, { fullResponse: true });
        if (!response.ok) {
            throw new Error(`Failed to download file: ${response.statusText}`);
        }
        const blob = await response.blob();
        downloadBlob(blob, filename);
        return true;
    } catch (error) {
        console.error("Error downloading authenticated file:", error);
        return false;
    }
};

export const downloadFile = async (file: FileAttachment, sessionId?: string, projectId?: string): Promise<boolean> => {
    try {
        let blob: Blob;
        let filename = file.name;

        if (file.content) {
            const byteCharacters = atob(file.content);
            const byteNumbers = new Array(byteCharacters.length);
            for (let i = 0; i < byteCharacters.length; i++) {
                byteNumbers[i] = byteCharacters.charCodeAt(i);
            }
            const byteArray = new Uint8Array(byteNumbers);
            blob = new Blob([byteArray], { type: file.mimeType || "application/octet-stream" });
        } else if (file.uri) {
            const parsedUri = parseArtifactUri(file.uri);
            if (!parsedUri) {
                throw new Error(`Invalid or unhandled URI format: ${file.uri}`);
            }

            const { filename: parsedFilename, version: parsedVersion } = parsedUri;
            filename = parsedFilename;
            const version = parsedVersion || "latest";

            let endpoint: string;
            if (sessionId && sessionId.trim() && sessionId !== "null" && sessionId !== "undefined") {
                endpoint = `/api/v1/artifacts/${encodeURIComponent(sessionId)}/${encodeURIComponent(filename)}/versions/${version}`;
            } else if (projectId) {
                endpoint = `/api/v1/artifacts/null/${encodeURIComponent(filename)}/versions/${version}?projectId=${projectId}`;
            } else {
                endpoint = `/api/v1/artifacts/null/${encodeURIComponent(filename)}/versions/${version}`;
            }

            const response = await api.webui.get(endpoint, { fullResponse: true });
            if (!response.ok) {
                throw new Error(`Failed to download file: ${response.statusText}`);
            }
            blob = await response.blob();
        } else {
            throw new Error("File has no content or URI to download.");
        }

        downloadBlob(blob, filename);
        return true;
    } catch (error) {
        console.error("Error creating download link:", error);
        return false;
    }
};
