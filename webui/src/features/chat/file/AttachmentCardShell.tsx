// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { XIcon } from "lucide-react";

import { useIsMobile } from "@/hooks";
import { Button, Tooltip, TooltipContent, TooltipTrigger } from "@/ui";
import { cn } from "@/utils";

interface AttachmentCardShellProps {
    filename: string;
    preview: React.ReactNode;
    inlineText?: React.ReactNode;
    tooltipText?: string;
    removeTooltip?: string;
    onClick?: () => void;
    onRemove?: () => void;
}

interface RemoveButtonProps {
    onRemove: () => void;
    tooltip: string;
    className: string;
}

const RemoveButton: React.FC<RemoveButtonProps> = ({ onRemove, tooltip, className }) => (
    <Button
        variant="ghost"
        size="icon"
        onClick={event => {
            event.stopPropagation();
            onRemove();
        }}
        className={className}
        tooltip={tooltip}
        tooltipSide="left"
    >
        <XIcon className="h-3 w-3" />
    </Button>
);

interface WithTooltipProps {
    enabled: boolean;
    text: string;
    children: React.ReactElement;
}

const WithTooltip: React.FC<WithTooltipProps> = ({ enabled, text, children }) => {
    if (!enabled) return children;
    return (
        <Tooltip>
            <TooltipTrigger asChild>{children}</TooltipTrigger>
            <TooltipContent side="top">
                <p>{text}</p>
            </TooltipContent>
        </Tooltip>
    );
};

interface MobileThumbProps {
    filename: string;
    preview: React.ReactNode;
    inlineText?: React.ReactNode;
    tooltipText?: string;
    removeTooltip: string;
    onClick?: () => void;
    onRemove?: () => void;
}

const MobileThumb: React.FC<MobileThumbProps> = ({ filename, preview, inlineText, tooltipText, removeTooltip, onClick, onRemove }) => {
    const boxClassName = cn("inline-flex size-16 overflow-hidden rounded-lg border bg-(--secondary-w10) shadow-sm transition-colors", onClick && "cursor-pointer hover:border-(--primary-w20)");
    const removeBtn = onRemove ? <RemoveButton onRemove={onRemove} tooltip={removeTooltip} className="absolute -top-1.5 -left-1.5 z-10 h-5 w-5 rounded-full border bg-(--background-w10) p-0 shadow-sm hover:bg-(--secondary-w10)" /> : null;
    const content = inlineText ?? preview;
    const box = onClick ? (
        <button type="button" onClick={onClick} className={cn(boxClassName, "text-left")} aria-label={filename} title={tooltipText ?? filename}>
            {content}
        </button>
    ) : (
        <div className={boxClassName} aria-label={filename} title={tooltipText ?? filename}>
            {content}
        </div>
    );
    return (
        <WithTooltip enabled={!!onClick} text={tooltipText ?? filename}>
            <div className="relative inline-flex">
                {box}
                {removeBtn}
            </div>
        </WithTooltip>
    );
};

interface DesktopBodyProps {
    filename: string;
    preview: React.ReactNode;
    inlineText?: React.ReactNode;
    removeTooltip: string;
    onClick?: () => void;
    onRemove?: () => void;
}

const DesktopBody: React.FC<DesktopBodyProps> = ({ filename, preview, inlineText, removeTooltip, onClick, onRemove }) => {
    const boxClassName = cn("inline-flex w-[200px] flex-col rounded-lg border bg-(--background-w10) shadow-sm transition-colors", onClick && "cursor-pointer hover:border-(--primary-w20)");
    const removeBtn = onRemove ? <RemoveButton onRemove={onRemove} tooltip={removeTooltip} className="absolute -top-2 -left-2 z-10 h-5 w-5 rounded-full border bg-(--background-w10) p-0 shadow-sm hover:bg-(--secondary-w10)" /> : null;
    const body = inlineText ? <div className="min-h-20 w-full min-w-0">{inlineText}</div> : <div className="relative h-20 w-full overflow-hidden rounded-t-lg bg-(--secondary-w10)">{preview}</div>;
    const filenamePill = (
        <div className="flex items-center gap-1 px-2 pb-2">
            <span className="inline-block max-w-[170px] truncate rounded bg-(--secondary-w10) px-2 py-0.5 text-[10px] font-semibold tracking-wider text-(--secondary-text-wMain)" title={filename}>
                {filename}
            </span>
        </div>
    );
    if (onClick) {
        return (
            <div className="relative inline-flex">
                <button type="button" onClick={onClick} className={cn(boxClassName, "text-left")}>
                    {body}
                    {filenamePill}
                </button>
                {removeBtn}
            </div>
        );
    }
    return (
        <div className={cn(boxClassName, "relative")}>
            {removeBtn}
            {body}
            {filenamePill}
        </div>
    );
};

export const AttachmentCardShell: React.FC<AttachmentCardShellProps> = ({ filename, preview, inlineText, tooltipText, removeTooltip = "Remove", onClick, onRemove }) => {
    const isMobile = useIsMobile();
    if (isMobile) {
        return <MobileThumb filename={filename} preview={preview} inlineText={inlineText} tooltipText={tooltipText} removeTooltip={removeTooltip} onClick={onClick} onRemove={onRemove} />;
    }
    return (
        <WithTooltip enabled={!!onClick} text={tooltipText ?? filename}>
            <DesktopBody filename={filename} preview={preview} inlineText={inlineText} removeTooltip={removeTooltip} onClick={onClick} onRemove={onRemove} />
        </WithTooltip>
    );
};

const SNIPPET_MAX_LINES = 3;
const SNIPPET_LINE_MAX_CHARS = 40;

export const AttachmentInlineText: React.FC<{ lines: string[] }> = ({ lines }) => {
    const nonBlank = lines.map(l => l.trim()).filter(l => l.length > 0);
    const visible = nonBlank.slice(0, SNIPPET_MAX_LINES);
    const hasMore = nonBlank.length > visible.length;

    const ELLIPSIS = "…";

    return (
        <div className="overflow-hidden px-3 pt-3 pb-2 font-mono text-xs leading-relaxed text-(--secondary-text-wMain)">
            {visible.map((line, index) => {
                const isLast = index === visible.length - 1;
                let display: string;
                if (isLast && hasMore) {
                    const cap = SNIPPET_LINE_MAX_CHARS - 1;
                    display = line.length > cap ? line.substring(0, cap) + ELLIPSIS : line + ELLIPSIS;
                } else {
                    display = line.length > SNIPPET_LINE_MAX_CHARS ? line.substring(0, SNIPPET_LINE_MAX_CHARS - 1) + ELLIPSIS : line;
                }
                return (
                    <div key={`${index}-${line}`} className="truncate">
                        {display}
                    </div>
                );
            })}
        </div>
    );
};
