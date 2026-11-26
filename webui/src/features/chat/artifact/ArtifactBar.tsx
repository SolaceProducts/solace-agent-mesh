// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useEffect, useRef, useState } from "react";
import { ChevronDown, ChevronUp, CircleAlert, Download, Info, Pencil, Trash } from "lucide-react";

import { clickableNodeProps } from "@/utils/nodeInteraction";

import { Button, Spinner } from "@/ui";
import { cn, formatBytes } from "@/utils";

import { FileIcon } from "../file/FileIcon";
import { ProjectBadge } from "../file/ProjectBadge";

const ErrorState: React.FC<{ message: string }> = ({ message }) => (
    <div className="w-full rounded-lg border border-(--error-w100) bg-(--error-wMain-50) p-3">
        <div className="text-sm text-(--error-wMain)">Error: {message}</div>
    </div>
);

export interface ArtifactBarProps {
    filename: string;
    description?: string;
    mimeType?: string;
    size?: number;
    status: "in-progress" | "completed" | "failed";
    expandable?: boolean;
    expanded?: boolean;
    onToggleExpand?: () => void;
    actions?: {
        onDownload?: () => void;
        onPreview?: () => void;
        onDelete?: () => void;
        onInfo?: () => void;
        onExpand?: () => void;
        onEdit?: () => void;
    };
    bytesTransferred?: number;
    error?: string;
    expandedContent?: React.ReactNode;
    expandedContentSelectable?: boolean;
    context?: "chat" | "list";
    isDeleted?: boolean;
    version?: number;
    source?: string;
    sourceProjectName?: string;
    downloading?: boolean;
}

export const ArtifactBar: React.FC<ArtifactBarProps> = ({
    filename,
    description,
    mimeType,
    size,
    status,
    expandable = false,
    expanded = false,
    onToggleExpand,
    actions,
    bytesTransferred,
    error,
    expandedContent,
    expandedContentSelectable = true,
    context = "chat",
    isDeleted = false,
    version,
    source,
    sourceProjectName,
    downloading = false,
}) => {
    const [contentForAnimation, setContentForAnimation] = useState(expandedContent);
    const expandedContentRef = useRef<HTMLDivElement>(null);

    useEffect(() => {
        if (expandedContent) {
            setContentForAnimation(expandedContent);
        } else {
            const timer = setTimeout(() => {
                setContentForAnimation(undefined);
            }, 300);
            return () => clearTimeout(timer);
        }
    }, [expandedContent]);

    if (!filename || typeof filename !== "string") {
        console.error("ArtifactBar: filename is required and must be a string");
        return <ErrorState message="Invalid artifact filename" />;
    }

    if (!status || !["in-progress", "completed", "failed"].includes(status)) {
        console.error("ArtifactBar: status must be one of: in-progress, completed, failed");
        return <ErrorState message="Invalid artifact status" />;
    }

    const getStatusDisplay = () => {
        if (isDeleted) {
            return {
                text: "Deleted",
                className: "text-(--secondary-text-wMain)",
            };
        }

        switch (status) {
            case "in-progress":
                return {
                    text: bytesTransferred ? `Creating... ${formatBytes(bytesTransferred)}` : "Creating...",
                    className: "text-(--info-wMain)",
                };
            case "failed":
                return {
                    text: error || "Failed to create",
                    className: "text-(--error-wMain)",
                };
            case "completed":
                return {
                    text: size ? formatBytes(size) : "",
                };
            default:
                return {
                    text: "Unknown",
                    className: "text-(--secondary-text-wMain)",
                };
        }
    };

    const statusDisplay = getStatusDisplay();

    const getDisplayDescription = (desc?: string, maxLength: number = 100): string => {
        if (!desc || typeof desc !== "string") {
            return "";
        }

        const cleaned = desc.replace(/\s+/g, " ").trim();

        if (cleaned.length <= maxLength) {
            return cleaned;
        }

        const truncated = cleaned.substring(0, maxLength);
        const lastSpaceIndex = truncated.lastIndexOf(" ");

        if (lastSpaceIndex > maxLength * 0.7) {
            return truncated.substring(0, lastSpaceIndex) + "...";
        }

        return truncated + "...";
    };

    const displayDescription = getDisplayDescription(description);
    const hasDescription = description?.trim();
    let primaryLabel: string;
    if (hasDescription) {
        primaryLabel = displayDescription;
    } else if (filename.length > 50) {
        primaryLabel = `${filename.substring(0, 47)}...`;
    } else {
        primaryLabel = filename;
    }

    const handleBarClick = (e?: React.MouseEvent | React.KeyboardEvent) => {
        const target = e?.target as Node | null;
        if (expandedContentSelectable && target && expandedContentRef.current?.contains(target)) {
            return;
        }
        if (status === "completed" && actions?.onPreview) {
            try {
                actions.onPreview();
            } catch (error) {
                console.error("Preview failed:", error);
            }
        }
    };

    const isClickable = status === "completed" && actions?.onPreview && !isDeleted;
    const showShadow = context === "chat" && !isDeleted;
    const interactiveProps = isClickable ? clickableNodeProps(handleBarClick) : {};

    return (
        <div
            className={cn(
                "w-full bg-(--background-w10) transition-shadow duration-200 ease-in-out",
                context === "list" ? "border-b" : "rounded",
                isClickable && "cursor-pointer outline-none focus-visible:border-(--brand-wMain)",
                isDeleted && "opacity-60",
                showShadow && "card-surface",
                isClickable && "card-surface-hover"
            )}
            aria-label={isClickable ? `Preview ${filename}` : undefined}
            {...interactiveProps}
        >
            <div className="flex min-h-15 items-center gap-3 p-3">
                <FileIcon filename={filename} mimeType={mimeType} size={size} className="shrink-0" />

                <div className="min-w-0 flex-1 py-1">
                    <div className="flex items-center gap-2">
                        <div className="truncate text-sm leading-tight font-semibold" title={hasDescription ? description : filename}>
                            {primaryLabel}
                        </div>
                        {source === "project" && sourceProjectName && <ProjectBadge text={sourceProjectName} className="max-w-90" />}
                    </div>

                    <div className="mt-1 flex items-center gap-2 text-xs leading-tight text-(--secondary-text-wMain)" title={hasDescription ? filename : statusDisplay.text}>
                        {hasDescription ? (
                            <div className="truncate">
                                {filename.length > 60 ? `${filename.substring(0, 57)}...` : filename}
                                {version !== undefined && context === "chat" && <span className="ml-1.5">(v{version})</span>}
                            </div>
                        ) : (
                            <>
                                {status === "in-progress" && <Spinner size="small" variant="primary" />}
                                <span className={statusDisplay.className}>{statusDisplay.text}</span>
                            </>
                        )}
                    </div>

                    {hasDescription && (
                        <div className={cn("mt-0.5 flex items-center gap-2 text-xs leading-tight", statusDisplay.className)}>
                            {status === "in-progress" && <Spinner size="small" variant="primary" />}
                            <span>{statusDisplay.text}</span>
                        </div>
                    )}
                </div>

                <div className="flex shrink-0 items-center gap-1">
                    {status === "completed" && actions?.onInfo && !isDeleted && (
                        <Button
                            variant="ghost"
                            size="icon"
                            onClick={e => {
                                e.stopPropagation();
                                try {
                                    actions.onInfo?.();
                                } catch (error) {
                                    console.error("Info failed:", error);
                                }
                            }}
                            tooltip="Info"
                        >
                            <Info className="h-4 w-4" />
                        </Button>
                    )}

                    {status === "completed" && actions?.onDownload && !isDeleted && (
                        <Button
                            variant="ghost"
                            size="icon"
                            loading={downloading}
                            onClick={e => {
                                e.stopPropagation();
                                try {
                                    actions.onDownload?.();
                                } catch (error) {
                                    console.error("Download failed:", error);
                                }
                            }}
                            tooltip="Download"
                        >
                            <Download className="h-4 w-4" />
                        </Button>
                    )}

                    {status === "completed" && actions?.onExpand && !isDeleted && (
                        <Button
                            variant="ghost"
                            size="icon"
                            onClick={e => {
                                e.stopPropagation();
                                try {
                                    actions.onExpand?.();
                                } catch (error) {
                                    console.error("Expand failed:", error);
                                }
                            }}
                            tooltip={expanded ? "Collapse" : "Expand"}
                        >
                            {expanded ? <ChevronUp className="h-4 w-4" /> : <ChevronDown className="h-4 w-4" />}
                        </Button>
                    )}

                    {status === "completed" && actions?.onEdit && !isDeleted && (
                        <Button
                            variant="ghost"
                            size="icon"
                            onClick={e => {
                                e.stopPropagation();
                                try {
                                    actions.onEdit?.();
                                } catch (error) {
                                    console.error("Edit failed:", error);
                                }
                            }}
                            tooltip="Edit Description"
                        >
                            <Pencil className="h-4 w-4" />
                        </Button>
                    )}

                    {status === "completed" && actions?.onDelete && !isDeleted && (
                        <Button
                            variant="ghost"
                            size="icon"
                            onClick={e => {
                                e.stopPropagation();
                                try {
                                    actions.onDelete?.();
                                } catch (error) {
                                    console.error("Delete failed:", error);
                                }
                            }}
                            tooltip="Delete"
                        >
                            <Trash className="h-4 w-4" />
                        </Button>
                    )}

                    {status === "failed" && (
                        <div className="pr-4" title="Artifact action failed">
                            <CircleAlert className="h-4 w-4 text-(--error-wMain)" />
                        </div>
                    )}
                </div>

                {expandable && onToggleExpand && !isDeleted && (
                    <Button
                        variant="ghost"
                        size="icon"
                        onClick={e => {
                            e.stopPropagation();
                            try {
                                onToggleExpand();
                            } catch (error) {
                                console.error("Toggle expand failed:", error);
                            }
                        }}
                        tooltip={expanded ? "Collapse" : "Expand"}
                    >
                        {expanded ? <ChevronUp className="h-4 w-4 transition-transform duration-200" /> : <ChevronDown className="h-4 w-4 transition-transform duration-200" />}
                    </Button>
                )}
            </div>

            <div ref={expandedContentRef} className={cn("grid grid-rows-[0fr] transition-[grid-template-rows] duration-300 ease-in-out", expanded && expandedContent && "grid-rows-[1fr]", expandedContentSelectable && "cursor-auto")}>
                <div className="overflow-hidden">
                    {contentForAnimation && (
                        <>
                            <hr className="border-t" />
                            <div className="p-3">{contentForAnimation}</div>
                        </>
                    )}
                </div>
            </div>
        </div>
    );
};
