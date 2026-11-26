// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { ArtifactPart, FilePart, PartFE } from "@/types";

export const ARTIFACT_TAG_USER_UPLOADED = "__user_uploaded";
export const ARTIFACT_TAG_WORKING = "__working";

export const SHOW_WORKING_ARTIFACTS_STORAGE_KEY = "sam_show_working_artifacts";

export function hasWorkingTag(tags: string[] | undefined): boolean {
    return tags?.some(t => t.toLowerCase() === ARTIFACT_TAG_WORKING.toLowerCase()) ?? false;
}

export function partTags(part: PartFE): string[] | undefined {
    if (part.kind === "artifact") {
        return stringTags((part as ArtifactPart).tags);
    }
    if (part.kind === "file") {
        return stringTags((part as FilePart).metadata?.tags);
    }
    return undefined;
}

function stringTags(tags: unknown): string[] | undefined {
    return Array.isArray(tags) ? tags.filter((tag): tag is string => typeof tag === "string") : undefined;
}

export function isHiddenWorkingPart(part: PartFE): boolean {
    if (part.kind === "artifact" && (part as ArtifactPart).status === "failed") {
        return false;
    }
    return hasWorkingTag(partTags(part));
}

export function isFileShownAsCard(part: PartFE, parts: PartFE[]): boolean {
    if (part.kind !== "file") {
        return false;
    }
    const name = (part as FilePart).file?.name;
    return !!name && parts.some(p => p.kind === "artifact" && (p as ArtifactPart).name === name && (p as ArtifactPart).status !== "failed");
}

export function partArtifactName(part: PartFE): string | undefined {
    if (part.kind === "artifact") {
        return (part as ArtifactPart).name;
    }
    if (part.kind === "file") {
        return (part as FilePart).file?.name;
    }
    return undefined;
}
