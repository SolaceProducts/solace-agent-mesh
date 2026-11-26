// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { Download } from "lucide-react";

import { useDownload } from "@/hooks/useDownload";

import type { ArtifactInfo } from "@/types";
import { Button } from "@/ui";

interface ArtifactPreviewDownloadProps {
    artifact: ArtifactInfo;
    message: string;
}

export const ArtifactPreviewDownload: React.FC<ArtifactPreviewDownloadProps> = ({ artifact, message }) => {
    const { onDownload, isDownloading } = useDownload();

    return (
        <div className="flex h-full w-full flex-col items-center justify-center gap-2 p-4">
            <div className="mb-1 font-semibold">Preview Unavailable</div>
            <div>{message}</div>
            <Button loading={isDownloading} onClick={() => onDownload(artifact)}>
                <Download className="h-4 w-4" />
                Download
            </Button>
        </div>
    );
};
