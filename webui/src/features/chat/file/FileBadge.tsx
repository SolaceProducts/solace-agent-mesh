// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { XIcon } from "lucide-react";

import { Badge, Button } from "@/ui";
import { cn } from "@/utils";

interface FileBadgeProps {
    fileName: string;
    onRemove?: () => void;
    onClick?: () => void;
    leadingIcon?: React.ReactNode;
    title?: string;
    className?: string;
}

export const FileBadge: React.FC<FileBadgeProps> = ({ fileName, onRemove, onClick, leadingIcon, title, className }) => {
    const labelContent = (
        <>
            {leadingIcon && <span className="flex flex-shrink-0 items-center [&_svg]:size-3.5">{leadingIcon}</span>}
            <span className="min-w-0 flex-1 truncate text-xs md:text-sm" title={title ?? fileName}>
                {fileName}
            </span>
        </>
    );

    return (
        <Badge className={cn("max-w-50 gap-1.5 rounded-full bg-(--secondary-w10) pr-1", className)}>
            {onClick ? (
                <Button variant="ghost" onClick={onClick} title={title ?? fileName} className={cn("flex h-auto min-h-0 min-w-0 flex-1 justify-start gap-1.5 rounded-full px-0 py-0 text-left enabled:hover:bg-transparent")}>
                    {labelContent}
                </Button>
            ) : (
                labelContent
            )}
            {onRemove && (
                <Button
                    variant="ghost"
                    size="icon"
                    onClick={event => {
                        event.stopPropagation();
                        onRemove();
                    }}
                    className={"h-2 min-h-0 w-2 min-w-0 p-2"}
                    title="Remove file"
                >
                    <XIcon />
                </Button>
            )}
        </Badge>
    );
};
