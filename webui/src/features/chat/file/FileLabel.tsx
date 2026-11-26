// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { File } from "lucide-react";

import { formatBytes } from "@/utils";

export const FileLabel = ({ fileName, fileSize }: { fileName: string; fileSize: number }) => {
    return (
        <div className="flex min-w-0 items-center gap-3">
            <File className="size-5 shrink-0 text-(--secondary-text-wMain)" />
            <div className="min-w-0 flex-1 overflow-hidden">
                <div className="truncate" title={fileName}>
                    {fileName}
                </div>
                <div className="text-xs text-(--secondary-text-wMain)">{formatBytes(fileSize)}</div>
            </div>
        </div>
    );
};
