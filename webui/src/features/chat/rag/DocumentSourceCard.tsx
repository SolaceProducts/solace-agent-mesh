// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useMemo, useState } from "react";
import { Loader2 } from "lucide-react";

import { useSessionArtifactContent } from "@/api/artifacts";

import { useChatContext } from "@/providers/contexts";

import type { GroupedDocument, LocationCitation } from "@/utils/documentSourceUtils";

import { AccordionCard } from "@/ui/accordion-card";

import { ArtifactPreviewDownload } from "@/features/chat/artifact/ArtifactPreviewDownload";
import { FileIcon } from "@/features/chat/file/FileIcon";
import { canPreviewArtifact } from "@/features/chat/preview/previewUtils";

import type { ArtifactInfo } from "@/types";

import { CitationPreviewModal } from "./CitationPreviewModal";
import { LocationCitationItem } from "./PageCitationItem";

export interface DocumentSourceCardProps {
    document: GroupedDocument;
    sourceIndex: number;
    readOnly?: boolean;
}

export const DocumentSourceCard: React.FC<DocumentSourceCardProps> = ({ document, sourceIndex, readOnly = false }) => {
    const { totalCitations, locations, fileExtension, filename } = document;
    const [selectedLocation, setSelectedLocation] = useState<LocationCitation | null>(null);

    const { sessionId } = useChatContext();

    const needsPreviewCheck = !readOnly && (fileExtension.toLowerCase() === "pptx" || fileExtension.toLowerCase() === "docx");

    const fileVersion: number | undefined = locations[0]?.citations[0]?.metadata?.file_version ?? undefined;

    const { data: artifactData, isLoading: isLoadingArtifact, error: artifactError } = useSessionArtifactContent(needsPreviewCheck ? sessionId : null, needsPreviewCheck ? filename : null, fileVersion);

    const previewCheck = useMemo(() => {
        if (!needsPreviewCheck) {
            return { canPreview: true };
        }

        if (artifactError) {
            return { canPreview: false, reason: "Unable to check preview availability" };
        }

        if (!artifactData) {
            return null;
        }

        const estimatedSize = artifactData.content ? Math.floor(artifactData.content.length * 0.75) : 0;

        const mockArtifact: ArtifactInfo = {
            filename,
            mimeType: artifactData.mimeType,
            size: estimatedSize,
            lastModified: "",
        };

        return canPreviewArtifact(mockArtifact);
    }, [artifactData, artifactError, filename, needsPreviewCheck]);

    return (
        <>
            <AccordionCard
                value={`document-${sourceIndex}`}
                standalone={false}
                icon={<FileIcon filename={filename} variant="compact" />}
                trigger={
                    <div className="flex min-w-0 flex-1 flex-col items-start gap-0.5">
                        <span className="truncate text-sm font-medium text-(--primary-text-wMain)">{filename}</span>
                        <span className="truncate text-sm text-(--secondary-text-wMain)">
                            {fileExtension.toUpperCase()} file | {totalCitations} citation
                            {totalCitations !== 1 ? "s" : ""}
                        </span>
                    </div>
                }
            >
                {isLoadingArtifact || !previewCheck ? (
                    <div className="flex items-center justify-center py-8 text-(--secondary-text-wMain)">
                        <Loader2 className="h-5 w-5 animate-spin" />
                    </div>
                ) : !previewCheck.canPreview ? (
                    <div className="py-4">
                        <ArtifactPreviewDownload
                            artifact={
                                {
                                    filename,
                                    mimeType: artifactData?.mimeType || "",
                                    size: artifactData?.content ? Math.floor(artifactData.content.length * 0.75) : 0,
                                    lastModified: "",
                                } as ArtifactInfo
                            }
                            message={previewCheck.reason || "Preview not available"}
                        />
                    </div>
                ) : (
                    <div className="pt-4">
                        {locations.map(location => (
                            <LocationCitationItem key={location.sortKey} locationLabel={location.locationLabel} citationCount={location.citationCount} onView={readOnly ? undefined : () => setSelectedLocation(location)} />
                        ))}
                    </div>
                )}
            </AccordionCard>

            <CitationPreviewModal
                isOpen={!!selectedLocation}
                onClose={() => setSelectedLocation(null)}
                filename={filename}
                locationLabel={selectedLocation?.locationLabel ?? ""}
                initialLocation={selectedLocation?.sortKey ?? 1}
                fileExtension={fileExtension}
                citations={selectedLocation?.citations ?? []}
            />
        </>
    );
};
