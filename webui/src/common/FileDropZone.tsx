// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { ReactNode } from "react";

import { useChatContext } from "@/providers/contexts";

import { ZIP_ONLY_ERROR } from "@/utils/fileValidation";

import { useDragAndDrop } from "@/hooks";

interface FileDropZoneProps {
    overlayLabel: string;
    onZipDropped: (file: File) => void;
    children: ReactNode;
    disabled?: boolean;
}

export function FileDropZone({ overlayLabel, onZipDropped, children, disabled = false }: Readonly<FileDropZoneProps>) {
    const { addNotification } = useChatContext();
    const { isDragging, handleDragEnter, handleDragOver, handleDragLeave, handleDrop } = useDragAndDrop({
        onFilesDropped: files => {
            const zip = files[0];
            if (!zip) return;
            if (!zip.name.toLowerCase().endsWith(".zip")) {
                addNotification(ZIP_ONLY_ERROR, "warning");
                return;
            }
            onZipDropped(zip);
        },
    });

    if (disabled) {
        return <div className="relative flex h-full w-full">{children}</div>;
    }

    return (
        <div className="relative flex h-full w-full" onDragEnter={handleDragEnter} onDragOver={handleDragOver} onDragLeave={handleDragLeave} onDrop={handleDrop} role="presentation">
            {isDragging && (
                <div className="pointer-events-none absolute inset-0 z-20 flex items-center justify-center bg-(--background-w20)/80">
                    <div className="rounded-md border-2 border-dashed border-(--primary-wMain) px-6 py-4 text-lg font-semibold text-(--primary-text-wMain)">{overlayLabel}</div>
                </div>
            )}
            {children}
        </div>
    );
}
