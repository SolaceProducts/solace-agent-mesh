// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useMemo, useState } from "react";
import { ArrowDown, ArrowLeft, Ellipsis, EyeOff, FileText, Loader2 } from "lucide-react";

import { useArtifactsWithWorking } from "@/api/artifacts";

import { useChatContext } from "@/providers/contexts";

import { formatBytes } from "@/utils/format";

import { Button } from "@/ui/button";

import { hasWorkingTag, isHiddenWorkingPart, partArtifactName } from "@/features/chat/artifactTags";

import { useDownload } from "@/hooks";
import type { ArtifactInfo, MessageFE } from "@/types";

import { ArtifactCard } from "./ArtifactCard";
import { ArtifactDeleteAllDialog } from "./ArtifactDeleteAllDialog";
import { ArtifactDeleteDialog } from "./ArtifactDeleteDialog";
import { ArtifactDetails } from "./ArtifactDetails";
import { ArtifactMorePopover } from "./ArtifactMorePopover";
import { ArtifactPreviewContent } from "./ArtifactPreviewContent";
import { SortOption, type SortOptionType, SortPopover } from "./ArtifactSortPopover";

const workingFilesLabel = (count: number) => `${count} working ${count === 1 ? "file" : "files"}`;

function countWorkingArtifacts(allArtifacts: ArtifactInfo[], messages: MessageFE[]): number {
    const names = new Set(allArtifacts.filter(artifact => hasWorkingTag(artifact.tags)).map(artifact => artifact.filename));
    for (const message of messages) {
        for (const part of message.parts ?? []) {
            const name = partArtifactName(part);
            if (name && isHiddenWorkingPart(part)) {
                names.add(name);
            }
        }
    }
    return names.size;
}

const sortFunctions: Record<SortOptionType, (a1: ArtifactInfo, a2: ArtifactInfo) => number> = {
    [SortOption.NameAsc]: (a1, a2) => a1.filename.localeCompare(a2.filename),
    [SortOption.NameDesc]: (a1, a2) => a2.filename.localeCompare(a1.filename),
    [SortOption.DateAsc]: (a1, a2) => (a1.lastModified > a2.lastModified ? 1 : -1),
    [SortOption.DateDesc]: (a1, a2) => (a1.lastModified < a2.lastModified ? 1 : -1),
};

interface ArtifactPanelProps {
    readOnly?: boolean;
    onDownloadOverride?: (artifact: ArtifactInfo) => Promise<void>;
}

export const ArtifactPanel = ({ readOnly = false, onDownloadOverride }: ArtifactPanelProps) => {
    const { previewArtifact, setPreviewArtifact, openDeleteModal, isDeleteModalOpen, isBatchDeleteModalOpen, messages } = useChatContext();
    const { artifacts, allArtifacts, isLoading: artifactsLoading, refetch: artifactsRefetch, showWorkingArtifacts, toggleShowWorkingArtifacts } = useArtifactsWithWorking();
    const workingArtifactCount = useMemo(() => countWorkingArtifacts(allArtifacts, messages ?? []), [allArtifacts, messages]);

    const { onDownload: defaultOnDownload } = useDownload();

    const onDownload = onDownloadOverride || defaultOnDownload;

    const [sortOption, setSortOption] = useState<SortOptionType>(SortOption.DateDesc);
    const [isPreviewInfoExpanded, setIsPreviewInfoExpanded] = useState(false);
    const sortedArtifacts = useMemo(() => {
        if (artifactsLoading) return [];

        return artifacts ? [...artifacts].sort(sortFunctions[sortOption]) : [];
    }, [artifacts, artifactsLoading, sortOption]);

    const hasDeletableArtifacts = useMemo(() => {
        return sortedArtifacts.some(artifact => artifact.source !== "project");
    }, [sortedArtifacts]);

    const header = useMemo(() => {
        if (previewArtifact) {
            return (
                <div className="flex items-center gap-2 border-b p-2">
                    <Button variant="ghost" onClick={() => setPreviewArtifact(null)}>
                        <ArrowLeft />
                    </Button>
                    <div className="text-md font-semibold">Preview</div>
                </div>
            );
        }

        const hasArtifactsOrWorking = sortedArtifacts.length > 0 || workingArtifactCount > 0;
        if (!hasArtifactsOrWorking) return null;

        return (
            <div className="flex items-center justify-end border-b p-2">
                {sortedArtifacts.length > 0 && (
                    <SortPopover key="sort-popover" currentSortOption={sortOption} onSortChange={setSortOption}>
                        <Button variant="ghost" title="Sort By">
                            <ArrowDown className="h-5 w-5" />
                            <div>Sort By</div>
                        </Button>
                    </SortPopover>
                )}
                {!readOnly && (
                    <ArtifactMorePopover key="more-popover" hideDeleteAll={!hasDeletableArtifacts} showWorkingArtifacts={showWorkingArtifacts} onToggleWorkingArtifacts={toggleShowWorkingArtifacts} workingArtifactCount={workingArtifactCount}>
                        <Button variant="ghost" tooltip="More">
                            <Ellipsis className="h-5 w-5" />
                        </Button>
                    </ArtifactMorePopover>
                )}
            </div>
        );
    }, [previewArtifact, sortedArtifacts.length, sortOption, setPreviewArtifact, hasDeletableArtifacts, readOnly, showWorkingArtifacts, toggleShowWorkingArtifacts, workingArtifactCount]);

    return (
        <div className="flex h-full flex-col">
            {header}
            <div className="flex min-h-0 flex-1">
                {!previewArtifact && (
                    <div className="flex flex-1 flex-col overflow-hidden">
                        <div className="flex-1 overflow-y-auto">
                            {sortedArtifacts.map(artifact => (
                                <ArtifactCard key={artifact.filename} artifact={artifact} readOnly={readOnly} onDownloadOverride={onDownloadOverride ? () => onDownloadOverride(artifact) : undefined} />
                            ))}
                            {sortedArtifacts.length === 0 && (
                                <div className="flex h-full items-center justify-center p-4">
                                    <div className="text-center text-(--secondary-text-wMain)">
                                        {artifactsLoading && <Loader2 className="size-6 animate-spin" />}
                                        {!artifactsLoading && (
                                            <>
                                                <FileText className="mx-auto mb-4 h-12 w-12" />
                                                <div className="text-lg font-medium">Files</div>
                                                {!showWorkingArtifacts && workingArtifactCount > 0 ? (
                                                    <div className="mt-2 text-sm">{workingFilesLabel(workingArtifactCount)} hidden</div>
                                                ) : (
                                                    <>
                                                        <div className="mt-2 text-sm">No files available</div>
                                                        {!readOnly && (
                                                            <Button className="mt-4" variant="default" onClick={() => artifactsRefetch()} data-testid="refreshFiles" title="Refresh Files">
                                                                Refresh
                                                            </Button>
                                                        )}
                                                    </>
                                                )}
                                            </>
                                        )}
                                    </div>
                                </div>
                            )}
                        </div>
                        {!showWorkingArtifacts && workingArtifactCount > 0 && sortedArtifacts.length > 0 && (
                            <Button variant="ghost" onClick={toggleShowWorkingArtifacts} className="h-auto w-full rounded-none border-t px-3 py-2 text-xs text-(--secondary-text-wMain) hover:text-(--primary-text-wMain)">
                                <EyeOff className="h-3 w-3" />
                                <span>{workingFilesLabel(workingArtifactCount)} hidden</span>
                            </Button>
                        )}
                    </div>
                )}
                {previewArtifact && (
                    <div className="flex min-h-0 min-w-0 flex-1 flex-col gap-2">
                        <div className="border-b px-4 py-3">
                            <ArtifactDetails
                                artifactInfo={previewArtifact}
                                isPreview={true}
                                isExpanded={isPreviewInfoExpanded}
                                setIsExpanded={setIsPreviewInfoExpanded}
                                onDelete={readOnly || previewArtifact.source === "project" ? undefined : () => openDeleteModal(previewArtifact)}
                                onDownload={() => onDownload(previewArtifact)}
                            />
                        </div>
                        {isPreviewInfoExpanded && (
                            <div className="border-b px-4 py-3">
                                <div className="space-y-2 text-sm">
                                    {previewArtifact.description && (
                                        <div>
                                            <span className="text-(--secondary-text-wMain)">Description:</span>
                                            <div className="mt-1">{previewArtifact.description}</div>
                                        </div>
                                    )}
                                    <div className="grid grid-cols-2 gap-2">
                                        <div>
                                            <span className="text-(--secondary-text-wMain)">Size:</span>
                                            <div>{formatBytes(previewArtifact.size)}</div>
                                        </div>
                                        <div>
                                            <span className="text-(--secondary-text-wMain)">Type:</span>
                                            <div>{previewArtifact.mimeType || "Unknown"}</div>
                                        </div>
                                    </div>
                                </div>
                            </div>
                        )}
                        <div className="min-h-0 min-w-0 flex-1 overflow-y-auto">
                            <ArtifactPreviewContent artifact={previewArtifact} />
                        </div>
                    </div>
                )}
            </div>
            {(isDeleteModalOpen || isBatchDeleteModalOpen) && (
                <>
                    <ArtifactDeleteDialog />
                    <ArtifactDeleteAllDialog artifacts={artifacts} />
                </>
            )}
        </div>
    );
};
