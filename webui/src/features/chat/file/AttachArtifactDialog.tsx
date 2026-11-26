// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useEffect, useMemo, useRef, useState } from "react";
import { MessageSquare } from "lucide-react";

import { type ArtifactWithSession, isProjectArtifact, useAllArtifacts, useArtifactVersions } from "@/api/artifacts";

import { formatBytes, formatRelativeTime } from "@/utils/format";

import { useDebounce } from "@/hooks";
import { Button, Checkbox, Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle, SearchInput, Select, SelectContent, SelectItem, SelectTrigger, SelectValue, Spinner } from "@/ui";
import { cn } from "@/utils";

import { getExtensionLabel } from "./attachmentUtils";
import { getFileTypeColor } from "./FileIcon";
import { ProjectBadge } from "./ProjectBadge";

const ArtifactVersionPicker: React.FC<{
    artifact: ArtifactWithSession;
    value: number;
    onValueChange: (value: number) => void;
}> = ({ artifact, value, onValueChange }) => {
    const [hasOpened, setHasOpened] = useState(false);
    const { data: lazyVersions, isLoading } = useArtifactVersions({
        sessionId: artifact.sessionId,
        projectId: artifact.projectId,
        filename: artifact.filename,
        enabled: hasOpened,
    });

    const versions = lazyVersions ?? (typeof artifact.version === "number" ? [artifact.version] : []);

    return (
        <Select
            value={value.toString()}
            onValueChange={v => onValueChange(parseInt(v, 10))}
            onOpenChange={open => {
                if (open) setHasOpened(true);
            }}
        >
            <SelectTrigger
                className="h-[16px] w-auto gap-1 py-0 text-xs shadow-none"
                onClick={e => {
                    e.stopPropagation();
                }}
            >
                <SelectValue />
            </SelectTrigger>
            <SelectContent
                onClick={e => {
                    e.stopPropagation();
                }}
            >
                {isLoading && (
                    <div className="flex items-center justify-center py-1">
                        <Spinner size="small" variant="muted" />
                    </div>
                )}
                {versions.map(v => (
                    <SelectItem key={v} value={v.toString()}>
                        Version {v}
                    </SelectItem>
                ))}
            </SelectContent>
        </Select>
    );
};

const resolveArtifactUri = (artifact: ArtifactWithSession): string | null => {
    return artifact.uri ? artifact.uri : null;
};

const applyVersionToUri = (uri: string, version: number): string => {
    try {
        const url = new URL(uri);
        url.search = "";
        url.searchParams.set("version", version.toString());
        return url.toString();
    } catch {
        return uri;
    }
};

interface AttachArtifactDialogProps {
    isOpen: boolean;
    onClose: () => void;
    onAttach: (artifacts: ArtifactWithSession[]) => void;
    alreadyAttachedUris?: string[];
}

const keyFor = (a: ArtifactWithSession) => `${a.sessionId}::${a.filename}`;

export const AttachArtifactDialog: React.FC<AttachArtifactDialogProps> = ({ isOpen, onClose, onAttach, alreadyAttachedUris = [] }) => {
    const [searchQuery, setSearchQuery] = useState("");
    const debouncedSearch = useDebounce(searchQuery.trim(), 400);

    const { data: artifacts = [], isLoading, hasMore, loadMore, isLoadingMore, refetch } = useAllArtifacts(debouncedSearch || undefined, undefined, { enabled: isOpen });

    const wasOpenRef = useRef(false);
    useEffect(() => {
        if (isOpen && !wasOpenRef.current) {
            refetch().catch(() => {});
        }
        wasOpenRef.current = isOpen;
    }, [isOpen, refetch]);

    const [selectedKeys, setSelectedKeys] = useState<Set<string>>(new Set());
    const [versionByKey, setVersionByKey] = useState<Map<string, number>>(new Map());

    useEffect(() => {
        if (!isOpen) {
            setSelectedKeys(new Set());
            setVersionByKey(new Map());
            setSearchQuery("");
        }
    }, [isOpen]);

    const attachedUriSet = useMemo(() => new Set(alreadyAttachedUris.filter(Boolean)), [alreadyAttachedUris]);

    const visibleArtifacts = useMemo(() => artifacts.map(a => ({ artifact: a, resolvedUri: resolveArtifactUri(a) })).filter(({ resolvedUri }) => resolvedUri && !attachedUriSet.has(resolvedUri)), [artifacts, attachedUriSet]);

    const sentinelRef = useRef<HTMLLIElement>(null);
    useEffect(() => {
        const sentinel = sentinelRef.current;
        if (!sentinel || !hasMore || isLoadingMore) return;
        const observer = new IntersectionObserver(
            ([entry]) => {
                if (entry.isIntersecting) loadMore();
            },
            { rootMargin: "100px" }
        );
        observer.observe(sentinel);
        return () => observer.disconnect();
    }, [hasMore, isLoadingMore, loadMore]);

    const toggleSelect = (artifact: ArtifactWithSession) => {
        const k = keyFor(artifact);
        setSelectedKeys(prev => {
            const next = new Set(prev);
            if (next.has(k)) next.delete(k);
            else next.add(k);
            return next;
        });
    };

    const selectedArtifacts = useMemo(
        () =>
            visibleArtifacts
                .filter(({ artifact }) => selectedKeys.has(keyFor(artifact)))
                .map(({ artifact, resolvedUri }) => {
                    const baseUri = resolvedUri ?? artifact.uri;
                    const versionChoice = versionByKey.get(keyFor(artifact)) ?? artifact.version;
                    const finalUri = baseUri && typeof versionChoice === "number" ? applyVersionToUri(baseUri, versionChoice) : baseUri;
                    return { ...artifact, uri: finalUri };
                }),
        [visibleArtifacts, selectedKeys, versionByKey]
    );

    const handleAttach = () => {
        if (selectedArtifacts.length === 0) return;
        onAttach(selectedArtifacts);
        onClose();
    };

    const selectedCount = selectedArtifacts.length;

    return (
        <Dialog
            open={isOpen}
            onOpenChange={open => {
                if (!open) onClose();
            }}
        >
            <DialogContent showCloseButton className="flex max-h-[70vh] flex-col rounded-lg sm:max-w-2xl">
                <DialogHeader>
                    <DialogTitle>Attach existing artifact</DialogTitle>
                </DialogHeader>

                <div className="mt-4 flex min-h-0 flex-1 flex-col gap-3 overflow-hidden">
                    <SearchInput className="w-full" value={searchQuery} onChange={setSearchQuery} placeholder="Search artifacts by name, type, session, or project…" testid="attach-artifact-search" />

                    <div className="min-h-0 flex-1 overflow-y-auto rounded-md border bg-(--background-w10)">
                        {isLoading ? (
                            <div className="flex h-32 items-center justify-center">
                                <Spinner />
                            </div>
                        ) : visibleArtifacts.length === 0 ? (
                            <div className="flex h-32 items-center justify-center px-4 text-center text-sm text-(--secondary-text-wMain)">{searchQuery ? "No artifacts match your search." : "No artifacts available to attach."}</div>
                        ) : (
                            <ul role="listbox" aria-multiselectable="true" className="divide-y">
                                {visibleArtifacts.map(({ artifact }) => {
                                    const k = keyFor(artifact);
                                    const isSelected = selectedKeys.has(k);
                                    return (
                                        <li
                                            key={k}
                                            role="option"
                                            aria-selected={isSelected}
                                            onClick={() => toggleSelect(artifact)}
                                            onKeyDown={event => {
                                                if (event.key === "Enter" || event.key === " ") {
                                                    event.preventDefault();
                                                    toggleSelect(artifact);
                                                }
                                            }}
                                            tabIndex={0}
                                            className={cn("flex cursor-pointer items-center gap-3 px-3 py-2 transition-colors hover:bg-(--primary-w10) focus:bg-(--primary-w10) focus:outline-none", isSelected && "bg-(--primary-w10)")}
                                        >
                                            <Checkbox checked={isSelected} />
                                            <span className={cn("w-12 flex-shrink-0 rounded px-2 py-0.5 text-center text-[10px] font-bold text-(--darkSurface-text)", getFileTypeColor(artifact.mimeType, artifact.filename))}>
                                                {getExtensionLabel(artifact.filename)}
                                            </span>
                                            <div className="min-w-0 flex-1">
                                                <div className="flex items-center gap-2">
                                                    <div className="min-w-0 flex-1 truncate text-sm font-medium text-(--primary-text-wMain)" title={artifact.filename}>
                                                        {artifact.filename}
                                                    </div>
                                                    {!isProjectArtifact(artifact) && typeof artifact.version === "number" && artifact.version > 0 && (
                                                        <ArtifactVersionPicker
                                                            artifact={artifact}
                                                            value={versionByKey.get(k) ?? artifact.version}
                                                            onValueChange={v => {
                                                                setVersionByKey(prev => {
                                                                    const next = new Map(prev);
                                                                    next.set(k, v);
                                                                    return next;
                                                                });
                                                            }}
                                                        />
                                                    )}
                                                </div>
                                                <div className="flex items-center gap-2 overflow-hidden text-xs whitespace-nowrap text-(--secondary-text-wMain)">
                                                    <span className="flex-shrink-0">{formatBytes(artifact.size)}</span>
                                                    {artifact.lastModified && (
                                                        <>
                                                            <span aria-hidden>·</span>
                                                            <span className="flex-shrink-0">{formatRelativeTime(artifact.lastModified)}</span>
                                                        </>
                                                    )}
                                                    {artifact.projectName ? (
                                                        <>
                                                            <span aria-hidden>·</span>
                                                            <ProjectBadge text={artifact.projectName} className="flex-shrink-0" />
                                                        </>
                                                    ) : (
                                                        artifact.sessionName && (
                                                            <>
                                                                <span aria-hidden>·</span>
                                                                <span className="flex min-w-0 items-center gap-1">
                                                                    <MessageSquare className="size-3 flex-shrink-0" />
                                                                    <span className="truncate" title={artifact.sessionName}>
                                                                        {artifact.sessionName}
                                                                    </span>
                                                                </span>
                                                            </>
                                                        )
                                                    )}
                                                </div>
                                            </div>
                                        </li>
                                    );
                                })}
                                {hasMore && (
                                    <li ref={sentinelRef} className="flex h-12 items-center justify-center">
                                        {isLoadingMore && <Spinner />}
                                    </li>
                                )}
                            </ul>
                        )}
                    </div>
                </div>

                <DialogFooter>
                    <Button variant="outline" onClick={onClose}>
                        Cancel
                    </Button>
                    <Button onClick={handleAttach} disabled={selectedCount === 0}>
                        {selectedCount === 0 ? "Attach" : `Attach ${selectedCount}`}
                    </Button>
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
};
