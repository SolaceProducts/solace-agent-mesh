// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { getManifestContent, getManifestContentAt } from "@/api/builder";

import { type ArtifactInfo } from "@/types/chat";

import { infrastructureError } from "../builderUtils";

export function decodeBase64Content(content: string): string {
    const binary = atob(content);
    const bytes = Uint8Array.from(binary, c => c.charCodeAt(0));
    return new TextDecoder().decode(bytes);
}

export function decodeBase64ContentSafe(content: string): string | null {
    try {
        return decodeBase64Content(content);
    } catch {
        return null;
    }
}

export async function fetchArtifactDirect(sessionId: string, filename: string): Promise<string | null> {
    try {
        return await getManifestContent(sessionId, filename, "latest");
    } catch {
        return null;
    }
}

export async function fetchArtifactAtVersion(sessionId: string, filename: string, version: number): Promise<string> {
    try {
        return await getManifestContentAt(sessionId, filename, version);
    } catch (err) {
        throw unreadableAtVersion(filename, version, err);
    }
}

function unreadableAtVersion(filename: string, version: number, cause: unknown): Error {
    console.warn("[builder] could not read a config at the version a save recorded", { filename, version, status: (cause as { status?: number } | null)?.status });
    return infrastructureError(`Could not read "${filename}" at version ${version}. Try again.`, cause);
}

export async function resolveArtifactContent(artifact: ArtifactInfo, downloadAndResolveArtifact: (filename: string) => Promise<unknown>, sessionId?: string, options?: { skipAccumulated?: boolean }): Promise<string | null> {
    if (!options?.skipAccumulated && artifact.accumulatedContent) return artifact.accumulatedContent;

    try {
        const fileAttachment = (await downloadAndResolveArtifact(artifact.filename)) as { content?: string } | null;
        if (fileAttachment?.content) {
            try {
                return decodeBase64Content(fileAttachment.content);
            } catch {
                return fileAttachment.content;
            }
        }
    } catch {}

    if (sessionId) {
        return fetchArtifactDirect(sessionId, artifact.filename);
    }
    return null;
}
