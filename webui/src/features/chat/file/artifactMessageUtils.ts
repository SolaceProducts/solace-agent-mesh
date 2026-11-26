// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { ArtifactInfo, FileAttachment, MessageFE } from "@/types";

export function isArtifactDeleted({
    status,
    artifactInfo,
    fileAttachment,
    message,
}: {
    status: "in-progress" | "completed" | "failed";
    artifactInfo: ArtifactInfo | undefined;
    fileAttachment: FileAttachment | undefined;
    message: MessageFE | undefined;
}): boolean {
    if (status !== "completed") return false;
    if (artifactInfo) return false;
    if (fileAttachment?.uri) return false;
    if (message && !message.isComplete) return false;
    return true;
}
