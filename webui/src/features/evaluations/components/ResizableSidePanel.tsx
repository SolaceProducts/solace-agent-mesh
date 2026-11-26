// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type ReactNode, useEffect, useRef, useState } from "react";

interface ResizableSidePanelProps {
    readonly open: boolean;
    readonly width: number | null;
    readonly onDragStart: (e: React.MouseEvent<HTMLDivElement>) => void;
    readonly children: ReactNode;
}

export function ResizableSidePanel({ open, width, onDragStart, children }: ResizableSidePanelProps) {
    const [shouldRender, setShouldRender] = useState(open);

    const lastChildrenRef = useRef<ReactNode>(children);
    if (open) lastChildrenRef.current = children;

    useEffect(() => {
        if (open) setShouldRender(true);
    }, [open]);

    if (!shouldRender) return null;

    return (
        <div
            data-state={open ? "open" : "closed"}
            className="data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=open]:slide-in-from-right data-[state=closed]:slide-out-to-right data-[state=closed]:fill-mode-forwards absolute top-0 right-0 bottom-0 z-10 flex border-l border-(--secondary-w20) bg-(--background-w10) shadow-lg transition-none duration-500 ease-in-out"
            style={{ width: width ?? undefined }}
            onAnimationEnd={e => {
                if (e.target !== e.currentTarget) return;
                if (!open) setShouldRender(false);
            }}
        >
            <div
                role="separator"
                aria-orientation="vertical"
                aria-label="Resize side panel"
                onMouseDown={onDragStart}
                className="flex w-3 shrink-0 cursor-col-resize items-center justify-center border-r border-(--secondary-w20) bg-(--background-w10) hover:bg-(--secondary-w10)"
            >
                <div className="flex flex-col gap-1">
                    <span className="block size-1 rounded-full bg-(--secondary-w40)" />
                    <span className="block size-1 rounded-full bg-(--secondary-w40)" />
                    <span className="block size-1 rounded-full bg-(--secondary-w40)" />
                </div>
            </div>
            <div className="min-w-0 flex-1 overflow-hidden">{lastChildrenRef.current}</div>
        </div>
    );
}
