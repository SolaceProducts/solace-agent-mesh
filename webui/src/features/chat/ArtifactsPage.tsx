// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { memo, useCallback, useContext, useEffect, useMemo, useRef, useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { AlertTriangle, ArrowDown, ArrowUp, Download, Eye, EyeOff, File, FileCode, FileImage, FileText, FolderOpen, MessageCircle, MoreHorizontal, Presentation, Trash2 } from "lucide-react";
import { useNavigate } from "react-router-dom";

import { type ArtifactWithSession, getArtifactApiUrl, isProjectArtifact, useAllArtifacts } from "@/api/artifacts";
import { api } from "@/api/client";

import { useChatContext } from "@/providers/contexts";
import { ConfigContext } from "@/providers/contexts/ConfigContext";
import { useProjectContext } from "@/providers/contexts/ProjectContext";

import { useIsArtifactsPageEnabled } from "@/hooks/useIsArtifactsPageEnabled";
import { useIsBinaryArtifactPreviewEnabled } from "@/hooks/useIsBinaryArtifactPreviewEnabled";

import { acquireFetchSlotOrAbort, releaseFetchSlot } from "@/utils/artifactFetchSemaphore";
import { formatBytes } from "@/utils/format";

import { ARTIFACT_TAG_WORKING } from "@/features/chat/artifactTags";
import { DocumentThumbnail, supportsThumbnail } from "@/features/chat/file/DocumentThumbnail";
import { getFileTypeColor } from "@/features/chat/file/FileIcon";
import { ProjectBadge } from "@/features/chat/file/ProjectBadge";
import { StandaloneArtifactPreview } from "@/features/chat/file/StandaloneArtifactPreview";
import { goToArtifactSession } from "@/features/chat/goToArtifactSession";
import { NO_PROJECT_LABEL, NO_PROJECT_VALUE } from "@/features/chat/projectFilter";

import { ConfirmationDialog, Header, PageLayout } from "@/common";
import { useDebounce } from "@/hooks";
import {
    Button,
    Card,
    DropdownMenu,
    DropdownMenuContent,
    DropdownMenuItem,
    DropdownMenuSeparator,
    DropdownMenuTrigger,
    SearchInput,
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
    Spinner,
    Tooltip,
    TooltipContent,
    TooltipTrigger,
} from "@/ui";
import { cn, createPersistentCache, formatTimestamp } from "@/utils";

const documentContentCache = createPersistentCache<string>({
    dbName: "sam-document-thumbnails",
    storeName: "document-thumbnails",
    maxEntries: 100,
    memoryMaxEntries: 50,
    ttlMs: 7 * 24 * 60 * 60 * 1000,
});

const textPreviewCache = createPersistentCache<string>({
    dbName: "sam-text-snippets",
    storeName: "text-snippets",
    maxEntries: 500,
    memoryMaxEntries: 200,
    ttlMs: 7 * 24 * 60 * 60 * 1000,
});

function getDocumentCacheKey(sessionId: string, filename: string, lastModified?: string | null): string {
    return `${sessionId}:${filename}:${lastModified ?? ""}`;
}

function getFileExtension(filename: string): string {
    const parts = filename.split(".");
    return parts.length > 1 ? parts[parts.length - 1].toUpperCase() : "FILE";
}

const TEXT_PREVIEW_MAX_BYTES = 2048;

function supportsTextPreview(mimeType: string): boolean {
    return (
        mimeType.startsWith("text/") ||
        mimeType.includes("json") ||
        mimeType.includes("xml") ||
        mimeType.includes("javascript") ||
        mimeType.includes("typescript") ||
        mimeType.includes("markdown") ||
        mimeType.includes("yaml") ||
        mimeType.includes("yml")
    );
}

function isImageType(mimeType: string): boolean {
    return mimeType.startsWith("image/");
}

const artifactCardKey = (artifact: ArtifactWithSession) => `${artifact.sessionId}-${artifact.filename}-${artifact.version || 0}`;

interface ArtifactGridCardProps {
    artifact: ArtifactWithSession;
    downloading: boolean;
    onDownload: (artifact: ArtifactWithSession) => Promise<void>;
    onDelete: (artifact: ArtifactWithSession) => void;
    onPreview: (artifact: ArtifactWithSession) => void;
    onGoToChat: (artifact: ArtifactWithSession) => void;
    onGoToProject: (artifact: ArtifactWithSession) => void;
    isSelected?: boolean;
    binaryArtifactPreviewEnabled: boolean;
}

const ArtifactGridCard = memo(function ArtifactGridCard({ artifact, downloading, onDownload, onDelete, onPreview, onGoToChat, onGoToProject, isSelected, binaryArtifactPreviewEnabled }: ArtifactGridCardProps) {
    const [contentPreview, setContentPreview] = useState<string | null>(() => {
        const cacheKey = getDocumentCacheKey(artifact.sessionId, artifact.filename, artifact.lastModified);
        return textPreviewCache.getSync(cacheKey) ?? null;
    });
    const [imagePreviewUrl, setImagePreviewUrl] = useState<string | null>(null);
    const [documentContent, setDocumentContent] = useState<string | null>(() => {
        const cacheKey = getDocumentCacheKey(artifact.sessionId, artifact.filename, artifact.lastModified);
        return documentContentCache.getSync(cacheKey) ?? null;
    });
    const [isLoadingPreview, setIsLoadingPreview] = useState(false);
    const [documentThumbnailFailed, setDocumentThumbnailFailed] = useState(false);
    const [dropdownOpen, setDropdownOpen] = useState(false);
    const [isVisible, setIsVisible] = useState(false);
    const cardRef = useRef<HTMLDivElement>(null);

    const isDocumentThumbnailSupported = supportsThumbnail(artifact.filename, artifact.mimeType);

    const isPdfFile = artifact.mimeType === "application/pdf" || artifact.filename.toLowerCase().endsWith(".pdf");

    const canAttemptDocumentThumbnail = isDocumentThumbnailSupported && (isPdfFile || binaryArtifactPreviewEnabled);

    const canShowTextPreview = !isDocumentThumbnailSupported && supportsTextPreview(artifact.mimeType);

    const needsPreviewFetch = isImageType(artifact.mimeType) || canAttemptDocumentThumbnail || canShowTextPreview;

    useEffect(() => {
        const el = cardRef.current;
        if (!el) return;

        if (!needsPreviewFetch) {
            setIsVisible(true);
            return;
        }

        const cacheKey = getDocumentCacheKey(artifact.sessionId, artifact.filename, artifact.lastModified);
        if (documentContentCache.getSync(cacheKey) || textPreviewCache.getSync(cacheKey)) {
            setIsVisible(true);
            return;
        }

        const observer = new IntersectionObserver(
            entries => {
                if (entries[0].isIntersecting) {
                    setIsVisible(true);
                    observer.disconnect();
                }
            },
            { rootMargin: "200px" }
        );
        observer.observe(el);
        return () => observer.disconnect();
    }, [needsPreviewFetch, artifact.sessionId, artifact.filename, artifact.lastModified]);

    useEffect(() => {
        if (!isVisible) return;

        let isMounted = true;
        const abortController = new AbortController();
        let imageBlobUrl: string | null = null;

        async function fetchWithSlot<T>(signal: AbortSignal, fn: () => Promise<T>): Promise<T | undefined> {
            const acquired = await acquireFetchSlotOrAbort(signal);
            if (!acquired) return undefined;
            try {
                return await fn();
            } finally {
                releaseFetchSlot();
            }
        }

        const loadPreview = async () => {
            const artifactApiUrl = getArtifactApiUrl(artifact);

            if (isImageType(artifact.mimeType)) {
                await fetchWithSlot(abortController.signal, async () => {
                    const response = await api.webui.get(artifactApiUrl, { fullResponse: true, signal: abortController.signal });
                    if (abortController.signal.aborted) return;
                    if (!response.ok) {
                        throw new Error(`Image fetch failed with status ${response.status}`);
                    }
                    const blob = await response.blob();
                    if (abortController.signal.aborted) return;
                    imageBlobUrl = URL.createObjectURL(blob);
                    if (isMounted) setImagePreviewUrl(imageBlobUrl);
                });
            } else if (canAttemptDocumentThumbnail) {
                const cacheKey = getDocumentCacheKey(artifact.sessionId, artifact.filename, artifact.lastModified);
                const cachedContent = await documentContentCache.get(cacheKey);

                if (cachedContent) {
                    if (isMounted) setDocumentContent(cachedContent);
                    return;
                }

                if (isMounted) setIsLoadingPreview(true);

                const blob = await fetchWithSlot(abortController.signal, async () => {
                    const response = await api.webui.get(artifactApiUrl, { fullResponse: true, signal: abortController.signal });
                    if (abortController.signal.aborted) return undefined;

                    const b = await response.blob();
                    if (abortController.signal.aborted) return undefined;
                    return b;
                });

                if (!blob) {
                    if (isMounted) setIsLoadingPreview(false);
                    return;
                }

                try {
                    const base64data = await new Promise<string>((resolve, reject) => {
                        const reader = new FileReader();
                        reader.onloadend = () => {
                            if (typeof reader.result === "string") {
                                resolve(reader.result.split(",")[1]);
                            } else {
                                reject(new Error("Failed to read content as data URL"));
                            }
                        };
                        reader.onerror = () => {
                            reject(reader.error || new Error("Unknown error reading file"));
                        };
                        reader.readAsDataURL(blob);
                    });

                    if (abortController.signal.aborted) return;

                    documentContentCache.set(cacheKey, base64data);

                    if (isMounted) {
                        setDocumentContent(base64data);
                        setIsLoadingPreview(false);
                    }
                } catch (error) {
                    if (!abortController.signal.aborted) {
                        console.error("Error loading document content:", error);
                        if (isMounted) setIsLoadingPreview(false);
                    }
                }
            } else if (canShowTextPreview) {
                const cacheKey = getDocumentCacheKey(artifact.sessionId, artifact.filename, artifact.lastModified);
                const cachedPreview = await textPreviewCache.get(cacheKey);

                if (cachedPreview) {
                    if (isMounted) setContentPreview(cachedPreview);
                    return;
                }

                if (isMounted) setIsLoadingPreview(true);

                await fetchWithSlot(abortController.signal, async () => {
                    const separator = artifactApiUrl.includes("?") ? "&" : "?";
                    const previewUrl = `${artifactApiUrl}${separator}maxBytes=${TEXT_PREVIEW_MAX_BYTES}`;
                    const response = await api.webui.get(previewUrl, { fullResponse: true, signal: abortController.signal });
                    if (abortController.signal.aborted) return;

                    const text = await response.text();
                    if (abortController.signal.aborted) return;

                    const preview = text
                        .substring(0, 500)
                        .split("\n")
                        .slice(0, 8)
                        .map(line => {
                            return line.length > 50 ? line.substring(0, 50) + "..." : line;
                        })
                        .join("\n");

                    textPreviewCache.set(cacheKey, preview).catch(err => console.warn("[ArtifactsPage] Failed to cache text preview:", err));

                    if (isMounted) {
                        setContentPreview(preview);
                        setIsLoadingPreview(false);
                    }
                });
            }
        };

        loadPreview().catch(error => {
            if (!abortController.signal.aborted) {
                console.error("Error loading preview:", error);
                if (isMounted) setIsLoadingPreview(false);
            }
        });

        return () => {
            isMounted = false;
            abortController.abort();
            if (imageBlobUrl) {
                URL.revokeObjectURL(imageBlobUrl);
            }
        };
    }, [artifact.sessionId, artifact.filename, artifact.lastModified, artifact.mimeType, canAttemptDocumentThumbnail, isVisible]);

    const handleDocumentThumbnailError = useCallback(() => {
        setDocumentThumbnailFailed(true);
    }, []);

    const canShowDocumentThumbnail = canAttemptDocumentThumbnail && !documentThumbnailFailed;

    const handleCardClick = () => {
        onPreview(artifact);
    };

    return (
        <Card noPadding isCardSelected={isSelected} onCardSelect={handleCardClick} className="group relative flex h-55 w-full shrink-0 flex-col gap-0 overflow-hidden sm:w-[320px]">
            <div className="flex items-center justify-between gap-2 border-b px-3 py-2">
                <div className="flex min-w-0 flex-1 items-center gap-2">
                    <h3 className="min-w-0 flex-1 truncate text-sm font-semibold" title={artifact.filename}>
                        {artifact.filename}
                    </h3>
                    {artifact.projectName && <ProjectBadge text={artifact.projectName} className="flex-shrink-0" />}
                </div>
                <DropdownMenu open={dropdownOpen} onOpenChange={setDropdownOpen}>
                    <DropdownMenuTrigger asChild>
                        <Button variant="ghost" size="icon" className="h-6 w-6 flex-shrink-0 p-0" onClick={e => e.stopPropagation()}>
                            <MoreHorizontal size={14} />
                        </Button>
                    </DropdownMenuTrigger>
                    <DropdownMenuContent align="end" className="w-48" onClick={e => e.stopPropagation()}>
                        {isProjectArtifact(artifact) ? (
                            <DropdownMenuItem
                                onClick={e => {
                                    e.stopPropagation();
                                    setDropdownOpen(false);
                                    onGoToProject(artifact);
                                }}
                            >
                                <FolderOpen size={14} className="mr-2" />
                                Go to Project
                            </DropdownMenuItem>
                        ) : (
                            <DropdownMenuItem
                                onClick={e => {
                                    e.stopPropagation();
                                    setDropdownOpen(false);
                                    onGoToChat(artifact);
                                }}
                            >
                                <MessageCircle size={14} className="mr-2" />
                                Go to Chat
                            </DropdownMenuItem>
                        )}
                        <DropdownMenuSeparator />
                        <DropdownMenuItem
                            disabled={downloading}
                            className={downloading ? "data-[disabled]:opacity-100" : undefined}
                            onSelect={e => e.preventDefault()}
                            onClick={e => {
                                e.stopPropagation();
                                if (downloading) return;
                                onDownload(artifact).finally(() => setDropdownOpen(false));
                            }}
                        >
                            {downloading ? <Spinner size="small" className="mr-2 size-3.5" /> : <Download size={14} className="mr-2" />}
                            Download
                        </DropdownMenuItem>
                        {!isProjectArtifact(artifact) && (
                            <DropdownMenuItem
                                onClick={e => {
                                    e.stopPropagation();
                                    setDropdownOpen(false);
                                    onDelete(artifact);
                                }}
                            >
                                <Trash2 size={14} className="mr-2" />
                                Delete
                            </DropdownMenuItem>
                        )}
                    </DropdownMenuContent>
                </DropdownMenu>
            </div>

            <div ref={cardRef} className="relative flex flex-1 items-center justify-center overflow-hidden bg-(--secondary-w10)">
                {imagePreviewUrl ? (
                    <img src={imagePreviewUrl} alt={artifact.filename} className="h-full w-full object-cover" onError={() => setImagePreviewUrl(null)} />
                ) : canShowDocumentThumbnail && documentContent ? (
                    <DocumentThumbnail content={documentContent} filename={artifact.filename} mimeType={artifact.mimeType} width={320} height={130} onError={handleDocumentThumbnailError} className="absolute inset-0 h-full w-full" />
                ) : contentPreview ? (
                    <div className="h-full w-full overflow-hidden px-3 py-2 font-mono text-[11px] leading-relaxed text-(--secondary-text-wMain)">
                        {contentPreview.split("\n").map((line, index) => (
                            <div key={index} className="truncate">
                                {line || "\u00A0"}
                            </div>
                        ))}
                    </div>
                ) : isLoadingPreview ? (
                    <Spinner size="small" variant="muted" />
                ) : (
                    <div className="flex flex-col items-center justify-center gap-2">
                        {isImageType(artifact.mimeType) ? (
                            <FileImage className="h-12 w-12 text-(--secondary-text-wMain)" />
                        ) : canShowTextPreview ? (
                            <FileCode className="h-12 w-12 text-(--secondary-text-wMain)" />
                        ) : isDocumentThumbnailSupported ? (
                            artifact.mimeType.includes("pdf") ? (
                                <FileText className="h-12 w-12 text-(--secondary-text-wMain)" />
                            ) : artifact.mimeType.includes("presentation") || artifact.filename.toLowerCase().endsWith(".pptx") || artifact.filename.toLowerCase().endsWith(".ppt") ? (
                                <Presentation className="h-12 w-12 text-(--secondary-text-wMain)" />
                            ) : (
                                <FileText className="h-12 w-12 text-(--secondary-text-wMain)" />
                            )
                        ) : (
                            <File className="h-12 w-12 text-(--secondary-text-wMain)" />
                        )}
                        {artifact.description && <span className="px-4 text-center text-xs text-(--secondary-text-wMain)">{artifact.description}</span>}
                    </div>
                )}

                <div className="overlay-backdrop absolute inset-0 flex items-center justify-center opacity-0 transition-opacity group-hover:opacity-100">
                    <Tooltip>
                        <TooltipTrigger asChild>
                            <Button
                                variant="secondary"
                                size="sm"
                                className="h-8 w-8 rounded-full p-0"
                                onClick={e => {
                                    e.stopPropagation();
                                    onPreview(artifact);
                                }}
                            >
                                <Eye className="h-4 w-4" />
                            </Button>
                        </TooltipTrigger>
                        <TooltipContent>Preview</TooltipContent>
                    </Tooltip>
                </div>
            </div>

            <div className="flex items-center justify-between border-t px-3 py-2">
                <div className="flex items-center gap-2">
                    <span className="text-xs text-(--secondary-text-wMain)">{formatBytes(artifact.size)}</span>
                    <span className="text-xs text-(--secondary-text-wMain)">•</span>
                    <span className="text-xs text-(--secondary-text-wMain)">{formatTimestamp(artifact.lastModified)}</span>
                </div>
                <span className={cn("ml-3 flex-shrink-0 rounded px-2 py-0.5 text-[10px] font-bold text-(--darkSurface-text)", getFileTypeColor(artifact.mimeType, artifact.filename))}>
                    {getFileExtension(artifact.filename).length > 4 ? getFileExtension(artifact.filename).substring(0, 4) : getFileExtension(artifact.filename)}
                </span>
            </div>
        </Card>
    );
});

type SortField = "date" | "name" | "type" | "size";
type SortDirection = "asc" | "desc";

const SORT_OPTIONS: { value: SortField; label: string }[] = [
    { value: "date", label: "Date" },
    { value: "name", label: "Name" },
    { value: "type", label: "Type" },
    { value: "size", label: "Size" },
];

const SHOW_INTERNAL_ARTIFACTS_KEY = "sam_show_internal_artifacts";

function isInternalArtifact(artifact: ArtifactWithSession): boolean {
    return artifact.tags?.some(t => t.toLowerCase() === ARTIFACT_TAG_WORKING.toLowerCase()) ?? false;
}

export function ArtifactsPage() {
    const navigate = useNavigate();
    const { addNotification, displayError, handleSwitchSession, agentSessionRoutes } = useChatContext();
    const [searchQuery, setSearchQuery] = useState<string>("");
    const debouncedSearch = useDebounce(searchQuery.trim(), 400);

    const [selectedProject, setSelectedProject] = useState<string>("all");
    const projectFilter = selectedProject === "all" ? undefined : selectedProject;
    const { data: artifacts = [], isLoading, error: fetchError, refetch, hasMore, loadMore, isLoadingMore } = useAllArtifacts(debouncedSearch || undefined, projectFilter);
    const [sortBy, setSortBy] = useState<SortField>("date");
    const [sortDirection, setSortDirection] = useState<SortDirection>("desc");
    const [previewArtifact, setPreviewArtifact] = useState<ArtifactWithSession | null>(null);
    const [deleteConfirmArtifact, setDeleteConfirmArtifact] = useState<ArtifactWithSession | null>(null);
    const [showInternalArtifacts, setShowInternalArtifacts] = useState<boolean>(() => {
        try {
            return localStorage.getItem(SHOW_INTERNAL_ARTIFACTS_KEY) === "true";
        } catch {
            return false;
        }
    });

    const toggleShowInternalArtifacts = useCallback(() => {
        setShowInternalArtifacts(prev => {
            const next = !prev;
            try {
                localStorage.setItem(SHOW_INTERNAL_ARTIFACTS_KEY, String(next));
            } catch {}
            return next;
        });
    }, []);

    const sentinelRef = useRef<HTMLDivElement>(null);
    useEffect(() => {
        const sentinel = sentinelRef.current;
        if (!sentinel || !hasMore || isLoadingMore) return;

        const observer = new IntersectionObserver(
            ([entry]) => {
                if (entry.isIntersecting) {
                    loadMore();
                }
            },
            { rootMargin: "200px" }
        );
        observer.observe(sentinel);
        return () => observer.disconnect();
    }, [hasMore, isLoadingMore, loadMore]);

    const config = useContext(ConfigContext);
    const artifactsPageEnabled = useIsArtifactsPageEnabled();
    const binaryArtifactPreviewEnabled = useIsBinaryArtifactPreviewEnabled();

    useEffect(() => {
        if (!artifactsPageEnabled && config !== null) {
            navigate("/chat", { replace: true });
        }
    }, [artifactsPageEnabled, navigate, config]);

    const { projects } = useProjectContext();
    const projectOptions = useMemo(() => {
        if (projects.length === 0) return [];
        return [{ value: NO_PROJECT_VALUE, label: NO_PROJECT_LABEL }, ...projects.map(project => ({ value: project.id, label: project.name }))];
    }, [projects]);

    const internalArtifactCount = useMemo(() => {
        return artifacts.filter(isInternalArtifact).length;
    }, [artifacts]);

    const filteredArtifacts = useMemo(() => {
        const trimmedQuery = searchQuery.trim().toLowerCase();

        const filtered = artifacts.filter(artifact => {
            if (!showInternalArtifacts && isInternalArtifact(artifact)) return false;

            if (trimmedQuery) {
                const filename = artifact.filename.toLowerCase();
                const mimeType = artifact.mimeType.toLowerCase();
                const sessionName = artifact.sessionName?.toLowerCase() || "";
                const projectName = artifact.projectName?.toLowerCase() || "";
                if (!(filename.includes(trimmedQuery) || mimeType.includes(trimmedQuery) || sessionName.includes(trimmedQuery) || projectName.includes(trimmedQuery))) {
                    return false;
                }
            }

            return true;
        });

        filtered.sort((a, b) => {
            let comparison = 0;

            switch (sortBy) {
                case "date": {
                    const dateA = a.lastModified ? new Date(a.lastModified).getTime() : 0;
                    const dateB = b.lastModified ? new Date(b.lastModified).getTime() : 0;
                    comparison = dateA - dateB;
                    break;
                }
                case "name":
                    comparison = a.filename.toLowerCase().localeCompare(b.filename.toLowerCase());
                    break;
                case "type": {
                    const extA = a.filename.split(".").pop()?.toLowerCase() || "";
                    const extB = b.filename.split(".").pop()?.toLowerCase() || "";
                    comparison = extA.localeCompare(extB);
                    break;
                }
                case "size":
                    comparison = a.size - b.size;
                    break;
            }

            return sortDirection === "asc" ? comparison : -comparison;
        });

        return filtered;
    }, [artifacts, searchQuery, sortBy, sortDirection, showInternalArtifacts]);

    const handleSortChange = useCallback(
        (field: SortField) => {
            if (field === sortBy) {
                setSortDirection(prev => (prev === "asc" ? "desc" : "asc"));
            } else {
                setSortBy(field);
                setSortDirection(field === "date" || field === "size" ? "desc" : "asc");
            }
        },
        [sortBy]
    );

    const downloadMutation = useMutation({
        mutationFn: async (artifact: ArtifactWithSession) => {
            const apiUrl = getArtifactApiUrl(artifact);
            const response = await api.webui.get(apiUrl, { fullResponse: true });
            const blob = await response.blob();

            const downloadUrl = window.URL.createObjectURL(blob);
            const link = document.createElement("a");
            link.href = downloadUrl;
            link.download = artifact.filename;
            document.body.appendChild(link);
            link.click();
            document.body.removeChild(link);
            window.URL.revokeObjectURL(downloadUrl);
        },
        onSuccess: (_result, artifact) => addNotification?.(`Downloaded ${artifact.filename}`, "success"),
        onError: error => displayError?.({ title: "Download Failed", error: error instanceof Error ? error.message : "Failed to download artifact" }),
    });

    const handleDownload = useCallback((artifact: ArtifactWithSession) => downloadMutation.mutateAsync(artifact).catch(() => {}), [downloadMutation]);

    const downloadingCardKey = downloadMutation.isPending && downloadMutation.variables ? artifactCardKey(downloadMutation.variables) : null;

    const handleDeleteRequest = useCallback(
        (artifact: ArtifactWithSession) => {
            if (isProjectArtifact(artifact)) {
                displayError?.({
                    title: "Cannot Delete",
                    error: "Project artifacts must be deleted from the project page",
                });
                return;
            }
            setDeleteConfirmArtifact(artifact);
        },
        [displayError]
    );

    const handleDeleteConfirm = useCallback(async () => {
        if (!deleteConfirmArtifact) return;

        try {
            await api.webui.delete(`/api/v1/artifacts/${deleteConfirmArtifact.sessionId}/${encodeURIComponent(deleteConfirmArtifact.filename)}`);
            addNotification?.(`Deleted ${deleteConfirmArtifact.filename}`, "success");
            setDeleteConfirmArtifact(null);
            setPreviewArtifact(current => (current?.filename === deleteConfirmArtifact.filename && current.sessionId === deleteConfirmArtifact.sessionId ? null : current));
            refetch();
        } catch (error) {
            console.error("Error deleting artifact:", error);
            displayError?.({
                title: "Delete Failed",
                error: error instanceof Error ? error.message : "Failed to delete artifact",
            });
            setDeleteConfirmArtifact(null);
        }
    }, [deleteConfirmArtifact, addNotification, displayError, refetch]);

    const handlePreview = useCallback((artifact: ArtifactWithSession) => {
        setPreviewArtifact(artifact);
    }, []);

    const handleClosePreview = useCallback(() => setPreviewArtifact(null), []);

    const handleGoToChat = useCallback((artifact: ArtifactWithSession) => goToArtifactSession(artifact, agentSessionRoutes, { switchSession: handleSwitchSession, navigate, chatRoute: "/chat" }), [navigate, handleSwitchSession, agentSessionRoutes]);

    const handleGoToProject = useCallback(
        (artifact: ArtifactWithSession) => {
            navigate(`/projects/${artifact.projectId}`);
        },
        [navigate]
    );

    return (
        <PageLayout>
            <Header title="Artifacts" />

            <div className="flex min-h-0 flex-1">
                <div className={cn("flex min-h-0 flex-col transition-all", previewArtifact ? "w-1/2" : "w-full")}>
                    <div className="flex h-full flex-col gap-4 px-4 py-4 sm:py-6 sm:pr-0 sm:pl-6">
                        <div className="flex flex-wrap items-center gap-3 sm:gap-4 sm:pr-4">
                            <SearchInput className="w-64" placeholder="Search artifacts..." value={searchQuery} onChange={setSearchQuery} />

                            {projectOptions.length > 0 && (
                                <div className="flex items-center gap-2">
                                    <label className="text-sm font-medium whitespace-nowrap">Project:</label>
                                    <Select value={selectedProject} onValueChange={setSelectedProject}>
                                        <SelectTrigger className="w-40 rounded-md">
                                            <SelectValue />
                                        </SelectTrigger>
                                        <SelectContent>
                                            <SelectItem value="all">All Artifacts</SelectItem>
                                            {projectOptions.map(option => (
                                                <SelectItem key={option.value} value={option.value}>
                                                    {option.label}
                                                </SelectItem>
                                            ))}
                                        </SelectContent>
                                    </Select>
                                </div>
                            )}

                            <div className="flex items-center gap-2">
                                <label className="text-sm font-medium whitespace-nowrap">Sort:</label>
                                <div className="flex items-center">
                                    <Select value={sortBy} onValueChange={(value: SortField) => handleSortChange(value)}>
                                        <SelectTrigger className="h-9 w-24 rounded-r-none border-r-0">
                                            <SelectValue />
                                        </SelectTrigger>
                                        <SelectContent>
                                            {SORT_OPTIONS.map(option => (
                                                <SelectItem key={option.value} value={option.value}>
                                                    {option.label}
                                                </SelectItem>
                                            ))}
                                        </SelectContent>
                                    </Select>
                                    <Button variant="outline" size="icon" className="h-9 w-9 rounded-l-none" onClick={() => setSortDirection(prev => (prev === "asc" ? "desc" : "asc"))} title={sortDirection === "asc" ? "Ascending" : "Descending"}>
                                        {sortDirection === "asc" ? <ArrowUp className="h-4 w-4" /> : <ArrowDown className="h-4 w-4" />}
                                    </Button>
                                </div>
                            </div>

                            {!isLoading && artifacts.length > 0 && (
                                <span className="text-sm text-(--secondary-text-wMain)">
                                    {filteredArtifacts.length} artifact{filteredArtifacts.length !== 1 ? "s" : ""}
                                    {(searchQuery || selectedProject !== "all" || !showInternalArtifacts) && filteredArtifacts.length !== artifacts.length && ` (of ${artifacts.length})`}
                                </span>
                            )}

                            {!isLoading && internalArtifactCount > 0 && (
                                <Tooltip>
                                    <TooltipTrigger asChild>
                                        <Button variant={showInternalArtifacts ? "secondary" : "ghost"} size="sm" onClick={toggleShowInternalArtifacts} className="flex items-center gap-1.5">
                                            {showInternalArtifacts ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
                                            {showInternalArtifacts ? "Hide Internal" : `Show Internal (${internalArtifactCount})`}
                                        </Button>
                                    </TooltipTrigger>
                                    <TooltipContent>
                                        {showInternalArtifacts ? "Hide internal/working files generated by agents" : `Show ${internalArtifactCount} internal/working file${internalArtifactCount !== 1 ? "s" : ""} generated by agents`}
                                    </TooltipContent>
                                </Tooltip>
                            )}
                        </div>

                        <div className="flex-1 overflow-y-auto sm:pr-4">
                            {isLoading && (
                                <div className="flex h-full items-center justify-center">
                                    <Spinner size="large" variant="muted" />
                                </div>
                            )}

                            {!isLoading && filteredArtifacts.length > 0 && (
                                <div className="flex flex-col gap-4">
                                    <div className="flex flex-wrap gap-4">
                                        {filteredArtifacts.map(artifact => (
                                            <ArtifactGridCard
                                                key={artifactCardKey(artifact)}
                                                artifact={artifact}
                                                downloading={downloadingCardKey === artifactCardKey(artifact)}
                                                onDownload={handleDownload}
                                                onDelete={handleDeleteRequest}
                                                onPreview={handlePreview}
                                                onGoToChat={handleGoToChat}
                                                onGoToProject={handleGoToProject}
                                                isSelected={previewArtifact?.filename === artifact.filename && previewArtifact?.sessionId === artifact.sessionId}
                                                binaryArtifactPreviewEnabled={binaryArtifactPreviewEnabled}
                                            />
                                        ))}
                                    </div>

                                    {hasMore && (
                                        <div ref={sentinelRef} className="flex justify-center py-4">
                                            {isLoadingMore && <Spinner size="small" variant="muted" />}
                                        </div>
                                    )}
                                </div>
                            )}

                            {!isLoading && !fetchError && filteredArtifacts.length === 0 && (artifacts.length > 0 || searchQuery.trim() !== "" || selectedProject !== "all") && (
                                <div className="flex h-full flex-col items-center justify-center text-sm text-(--secondary-text-wMain)">
                                    <File className="mx-auto mb-4 h-12 w-12" />
                                    No artifacts found matching your {searchQuery && selectedProject !== "all" ? "search and filter" : searchQuery ? "search" : "filter"}
                                </div>
                            )}

                            {!isLoading && fetchError && (
                                <div className="flex h-full flex-col items-center justify-center text-sm text-(--secondary-text-wMain)">
                                    <AlertTriangle className="mx-auto mb-4 h-12 w-12 text-(--error-wMain)" />
                                    <p className="text-(--error-wMain)">Failed to load artifacts</p>
                                    <p className="mt-2 text-xs">{fetchError?.message}</p>
                                    <Button variant="outline" className="mt-4" onClick={() => refetch()}>
                                        Refresh
                                    </Button>
                                </div>
                            )}

                            {!isLoading && !fetchError && filteredArtifacts.length === 0 && artifacts.length === 0 && searchQuery.trim() === "" && selectedProject === "all" && (
                                <div className="flex h-full flex-col items-center justify-center text-sm text-(--secondary-text-wMain)">
                                    <File className="mx-auto mb-4 h-12 w-12" />
                                    <p>No artifacts available</p>
                                    <p className="mt-2 text-xs">Upload files in chat or generate artifacts with AI</p>
                                </div>
                            )}
                        </div>
                    </div>
                </div>

                {previewArtifact && (
                    <div className="w-1/2 border-l">
                        <StandaloneArtifactPreview artifact={previewArtifact} onClose={handleClosePreview} onDownload={handleDownload} onGoToChat={handleGoToChat} onGoToProject={handleGoToProject} />
                    </div>
                )}
            </div>

            <ConfirmationDialog
                open={!!deleteConfirmArtifact}
                onOpenChange={open => !open && setDeleteConfirmArtifact(null)}
                title="Delete Artifact"
                content={
                    <>
                        This action cannot be undone. This artifact will be permanently deleted: <strong>{deleteConfirmArtifact?.filename}</strong>
                    </>
                }
                actionLabels={{
                    confirm: "Delete",
                }}
                onConfirm={handleDeleteConfirm}
            />
        </PageLayout>
    );
}
