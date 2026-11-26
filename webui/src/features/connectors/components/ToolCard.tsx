// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect, useRef, useState } from "react";

import { CustomCheckbox } from "@/common/CustomCheckbox";

import { Tooltip, TooltipContent, TooltipTrigger } from "@/ui/tooltip";

import { getMethodBadgeClassName } from "@/features/connectors/components/MethodBadge";

import { Deprecated } from "@/common";
import type { Tool } from "@/types";

interface BaseToolCardProps {
    tool: Tool;
}

interface SelectableToolCardProps extends BaseToolCardProps {
    selectable: true;
    isSelected: boolean;
    onToggle: () => void;
    clickable?: never;
    onClick?: never;
}

interface ClickableToolCardProps extends BaseToolCardProps {
    selectable?: false;
    isSelected?: never;
    onToggle?: never;
    clickable?: boolean;
    onClick?: () => void;
}

type ToolCardProps = SelectableToolCardProps | ClickableToolCardProps;

export const ToolCard = ({ tool, selectable, isSelected, onToggle, clickable = false, onClick }: ToolCardProps) => {
    const descriptionRef = useRef<HTMLDivElement>(null);
    const [isTruncated, setIsTruncated] = useState(false);

    useEffect(() => {
        if (!tool.description) return;

        const element = descriptionRef.current;
        if (element) {
            setIsTruncated(element.scrollHeight > element.clientHeight);
        }
    }, [tool.description]);

    const handleClick = () => {
        if (selectable && onToggle) {
            onToggle();
        } else if (clickable && onClick) {
            onClick();
        }
    };

    const toolCardClassName = `card-surface rounded bg-(--background-w10) p-4 min-w-0 ${selectable || clickable ? "card-surface-hover cursor-pointer transition-all" : ""}`;

    return (
        <div className={toolCardClassName} data-testid={`tool-row-${tool.operationId}`} onClick={handleClick}>
            <div className={`flex ${selectable ? "flex-row items-start" : "flex-col"} gap-${selectable ? "3" : "2"} min-w-0`}>
                {selectable && <CustomCheckbox checked={isSelected!} onChange={onToggle!} onClick={e => e.stopPropagation()} />}

                <div className="flex min-w-0 flex-col gap-2">
                    <div className="flex items-center gap-2">
                        <span className="text-sm leading-[21px] font-bold text-(--primary-text-wMain)">{tool.name}</span>
                        {tool.deprecated && <Deprecated>Deprecated</Deprecated>}
                    </div>

                    {tool.method !== "MCP" && (
                        <div className="flex min-w-0 items-center gap-2">
                            {tool.method && tool.method !== "EVENT_MESH" && <span className={getMethodBadgeClassName(tool.method)}>{tool.method.toUpperCase()}</span>}
                            {tool.endpoint && <pre className="min-w-0 truncate text-sm text-(--secondary-text-wMain)">{tool.endpoint}</pre>}
                        </div>
                    )}

                    {tool.description && (
                        <Tooltip open={isTruncated ? undefined : false}>
                            <TooltipTrigger asChild>
                                <div ref={descriptionRef} className="line-clamp-1 text-sm leading-[150%]">
                                    {tool.description}
                                </div>
                            </TooltipTrigger>
                            <TooltipContent className="max-w-md whitespace-normal">{tool.description}</TooltipContent>
                        </Tooltip>
                    )}
                </div>
            </div>
        </div>
    );
};
