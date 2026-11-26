// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { Download, X } from "lucide-react";

import { useChatContext } from "@/providers/contexts";

import { Button } from "@/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "@/ui/dialog";

import { ArtifactPreviewContent } from "@/features/chat/artifact/ArtifactPreviewContent";

import { isBuilderConfigMime } from "./builderUtils";
import { SAM_BUILD_MANIFEST_MIME_TYPE } from "./hooks/useBuilderManifest";

export function BuilderArtifactPreviewModal() {
    const { previewArtifact, setPreviewArtifact } = useChatContext();

    const isBuilderOwned = !!previewArtifact && (previewArtifact.mimeType === SAM_BUILD_MANIFEST_MIME_TYPE || isBuilderConfigMime(previewArtifact.mimeType));
    const isOpen = !!previewArtifact && !isBuilderOwned;

    const handleClose = () => setPreviewArtifact(null);

    const handleDownload = () => {
        if (!previewArtifact?.uri) return;
        try {
            const url = new URL(previewArtifact.uri, window.location.origin);
            if (url.origin !== window.location.origin) return;
            window.open(`${url.href}?download=true`, "_blank");
        } catch {}
    };

    return (
        <Dialog open={isOpen} onOpenChange={open => !open && handleClose()}>
            <DialogContent className="flex h-[85vh] !max-w-[min(1100px,90vw)] flex-col gap-0 rounded-lg bg-(--background-w10) p-0" onInteractOutside={() => handleClose()}>
                {previewArtifact && (
                    <>
                        <div className="flex items-center justify-between border-b px-4 py-3">
                            <div className="min-w-0 flex-1">
                                <DialogTitle className="truncate text-sm leading-normal" title={previewArtifact.filename}>
                                    {previewArtifact.filename}
                                </DialogTitle>
                                <DialogDescription className="text-xs">{previewArtifact.mimeType || "Unknown"}</DialogDescription>
                            </div>
                            <div className="flex items-center gap-1">
                                <Button variant="ghost" size="sm" onClick={handleDownload}>
                                    <Download className="mr-1 h-4 w-4" />
                                    Download
                                </Button>
                                <Button variant="ghost" size="sm" onClick={handleClose} tooltip="Close">
                                    <X className="h-4 w-4" />
                                </Button>
                            </div>
                        </div>

                        <div className="min-h-0 flex-1 overflow-auto">
                            <ArtifactPreviewContent artifact={previewArtifact} />
                        </div>
                    </>
                )}
            </DialogContent>
        </Dialog>
    );
}
