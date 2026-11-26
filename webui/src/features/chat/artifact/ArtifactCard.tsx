// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useMemo } from "react";

import { useChatContext } from "@/providers/contexts";

import type { ArtifactInfo } from "@/types";

import { ArtifactMessage } from "../file/ArtifactMessage";

interface ArtifactCardProps {
    artifact: ArtifactInfo;
    isPreview?: boolean;
    readOnly?: boolean;
    onDownloadOverride?: () => Promise<void>;
}

export const ArtifactCard: React.FC<ArtifactCardProps> = React.memo(({ artifact, isPreview, readOnly = false, onDownloadOverride }) => {
    const { setPreviewArtifact } = useChatContext();

    const fileAttachment = useMemo(
        () => ({
            name: artifact.filename,
            mimeType: artifact.mimeType,
            size: artifact.size,
            uri: artifact.uri,
        }),
        [artifact.filename, artifact.mimeType, artifact.size, artifact.uri]
    );

    let card: React.ReactNode;
    if (artifact.creationStatus === "in-progress") {
        card = <ArtifactMessage status="in-progress" name={artifact.filename} bytesTransferred={artifact.size ?? 0} context="list" readOnly={readOnly} onDownloadOverride={onDownloadOverride} />;
    } else if (artifact.creationStatus === "failed") {
        card = <ArtifactMessage status="failed" name={artifact.filename} context="list" readOnly={readOnly} onDownloadOverride={onDownloadOverride} />;
    } else {
        card = <ArtifactMessage status="completed" name={artifact.filename} fileAttachment={fileAttachment} context="list" readOnly={readOnly} onDownloadOverride={onDownloadOverride} />;
    }

    const handleClick = (e: React.MouseEvent) => {
        if (!isPreview) {
            e.stopPropagation();
            setPreviewArtifact(artifact);
        }
    };

    return (
        <div
            className={`${isPreview ? "" : "cursor-pointer transition-all duration-150 hover:bg-(--background-w20)"}`}
            onClick={handleClick}
            onKeyDown={
                !isPreview
                    ? e => {
                          if (e.key === "Enter" || e.key === " ") {
                              e.preventDefault();
                              setPreviewArtifact(artifact);
                          }
                      }
                    : undefined
            }
            role={!isPreview ? "button" : undefined}
            tabIndex={!isPreview ? 0 : undefined}
        >
            {card}
        </div>
    );
});

ArtifactCard.displayName = "ArtifactCard";
