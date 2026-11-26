// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useEffect, useRef, useState } from "react";
import { Download, Eye, FolderOpen, MoreHorizontal, Share2, Trash2, UserIcon } from "lucide-react";

import type { MenuAction } from "@/ui/menu";
import type { Project } from "@/types/projects";

import { GridCard, PinButton } from "@/common";
import { useIsProjectOwner } from "@/hooks";
import { Button, CardContent, CardDescription, CardHeader, CardTitle, Menu, Popover, PopoverContent, PopoverTrigger, Tooltip, TooltipContent, TooltipTrigger } from "@/ui";

interface ProjectCardProps {
    project: Project;
    onClick?: () => void;
    onDelete?: (project: Project) => void;
    onExport?: (project: Project) => void;
    onShare?: (project: Project) => void;
    onTogglePin?: (project: Project) => void;
    isPinToggling?: boolean;
}

export const ProjectCard: React.FC<ProjectCardProps> = ({ project, onClick, onDelete, onExport, onShare, onTogglePin, isPinToggling }) => {
    const [menuOpen, setMenuOpen] = useState(false);
    const [isTruncated, setIsTruncated] = useState(false);
    const titleRef = useRef<HTMLDivElement>(null);
    const isOwner = useIsProjectOwner(project.userId);

    useEffect(() => {
        const el = titleRef.current;
        if (!el) return;
        const check = () => setIsTruncated(el.scrollWidth > el.clientWidth);
        check();
        const observer = new ResizeObserver(check);
        observer.observe(el);
        return () => observer.disconnect();
    }, []);

    const menuActions: MenuAction[] = [
        ...(isOwner && onShare
            ? [
                  {
                      id: "share",
                      label: "Share Project",
                      icon: <Share2 size={14} />,
                      onClick: () => {
                          setMenuOpen(false);
                          onShare(project);
                      },
                  },
              ]
            : []),
        ...(onExport
            ? [
                  {
                      id: "export",
                      label: "Export Project",
                      icon: <Download size={14} />,
                      onClick: () => {
                          setMenuOpen(false);
                          onExport(project);
                      },
                  },
              ]
            : []),
        {
            id: "delete",
            label: "Delete",
            icon: <Trash2 size={14} />,
            onClick: () => {
                setMenuOpen(false);
                if (onDelete) {
                    onDelete(project);
                }
            },
        },
    ];

    return (
        <GridCard onClick={onClick}>
            <CardHeader className="min-w-0 gap-0 overflow-hidden">
                <div className="flex min-w-0 items-start justify-between gap-2">
                    <div className="flex min-w-0 flex-1 items-center gap-2">
                        <FolderOpen className="h-6 w-6 flex-shrink-0 text-(--brand-wMain)" />
                        <Tooltip open={isTruncated ? undefined : false}>
                            <TooltipTrigger asChild>
                                <CardTitle ref={titleRef} className="min-w-0 truncate text-lg font-semibold text-(--primary-text-wMain)">
                                    {project.name}
                                </CardTitle>
                            </TooltipTrigger>
                            <TooltipContent side="top">{project.name}</TooltipContent>
                        </Tooltip>
                    </div>
                    <div className="flex shrink-0 items-center gap-1">
                        {onTogglePin && (
                            <PinButton
                                isPinned={project.isPinned ?? false}
                                disabled={isPinToggling}
                                onClick={e => {
                                    e.stopPropagation();
                                    onTogglePin(project);
                                }}
                            />
                        )}
                        {isOwner && onDelete && (
                            <Popover open={menuOpen} onOpenChange={setMenuOpen}>
                                <PopoverTrigger asChild>
                                    <Button variant="ghost" size="icon" className="h-8 w-8" tooltip="More options" onClick={e => e.stopPropagation()}>
                                        <MoreHorizontal className="h-4 w-4" />
                                    </Button>
                                </PopoverTrigger>
                                <PopoverContent align="start" side="bottom" className="w-48 p-1" sideOffset={0} onClick={e => e.stopPropagation()}>
                                    <Menu actions={menuActions} />
                                </PopoverContent>
                            </Popover>
                        )}
                    </div>
                </div>
            </CardHeader>

            <CardContent className="flex flex-1 flex-col justify-between gap-4">
                <div>{project.description ? <CardDescription className="line-clamp-3">{project.description}</CardDescription> : <div />}</div>

                <div className="flex items-center justify-between">
                    <div className="max-w-[200px] truncate text-(--secondary-text-wMain)">{project.userId}</div>
                    <div className="flex items-center gap-4">
                        {isOwner && project.artifactCount !== undefined && project.artifactCount !== null && (
                            <div className="flex items-center gap-1">
                                <span className="text-(--secondary-text-wMain)">
                                    {project.artifactCount} {project.artifactCount === 1 ? "file" : "files"}
                                </span>
                            </div>
                        )}
                        {onShare && (
                            <Tooltip>
                                <TooltipTrigger asChild>
                                    <span className="cursor-default text-(--secondary-text-wMain)">{isOwner ? <UserIcon className="h-6 w-6" /> : <Eye className="h-6 w-6" />}</span>
                                </TooltipTrigger>
                                <TooltipContent side="top">{isOwner ? "You are the owner of this project" : "You are a viewer of this project"}</TooltipContent>
                            </Tooltip>
                        )}
                    </div>
                </div>
            </CardContent>
        </GridCard>
    );
};
