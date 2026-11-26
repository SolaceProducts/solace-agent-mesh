// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useEffect, useLayoutEffect, useRef } from "react";
import { MessageSquarePlus } from "lucide-react";

import { Button } from "@/ui";

export interface AddCommentMenuProps {
    isOpen: boolean;
    position: { x: number; y: number } | null;
    onAddComment: () => void;
    onClose: () => void;
}

export const AddCommentMenu: React.FC<AddCommentMenuProps> = ({ isOpen, position, onAddComment, onClose }) => {
    const menuRef = useRef<HTMLDivElement>(null);
    const buttonRef = useRef<HTMLButtonElement>(null);
    const anchorRef = useRef<HTMLElement | null>(null);

    const onCloseRef = useRef(onClose);
    onCloseRef.current = onClose;

    useLayoutEffect(() => {
        if (!isOpen) return;
        anchorRef.current = document.activeElement as HTMLElement | null;

        const selection = globalThis.getSelection();
        const savedRange = selection && selection.rangeCount > 0 && !selection.isCollapsed ? selection.getRangeAt(0).cloneRange() : null;

        buttonRef.current?.focus();

        if (savedRange && selection && savedRange.startContainer.isConnected && savedRange.endContainer.isConnected) {
            try {
                selection.removeAllRanges();
                selection.addRange(savedRange);
            } catch {}
        }

        return () => {
            anchorRef.current?.focus?.();
        };
    }, [isOpen]);

    useEffect(() => {
        if (!isOpen) return;

        const handleClickOutside = (event: MouseEvent) => {
            if (menuRef.current && !menuRef.current.contains(event.target as Node)) {
                onCloseRef.current();
            }
        };

        const handleEscape = (event: KeyboardEvent) => {
            if (event.key === "Escape") {
                onCloseRef.current();
            }
        };

        const handleViewportChange = () => {
            onCloseRef.current();
        };

        const timer = setTimeout(() => {
            document.addEventListener("mousedown", handleClickOutside);
            document.addEventListener("keydown", handleEscape);
            globalThis.addEventListener("scroll", handleViewportChange, true);
            globalThis.addEventListener("resize", handleViewportChange);
        }, 100);

        return () => {
            clearTimeout(timer);
            document.removeEventListener("mousedown", handleClickOutside);
            document.removeEventListener("keydown", handleEscape);
            globalThis.removeEventListener("scroll", handleViewportChange, true);
            globalThis.removeEventListener("resize", handleViewportChange);
        };
    }, [isOpen]);

    if (!isOpen || !position) {
        return null;
    }

    return (
        <div
            ref={menuRef}
            className="animate-in fade-in-0 zoom-in-95 fixed z-50 duration-200"
            style={{
                left: `${position.x}px`,
                top: `${position.y}px`,
            }}
        >
            <div className="w-auto max-w-[160px] rounded-md border bg-(--background-w10) p-1 shadow-lg">
                <Button ref={buttonRef} variant="ghost" className="h-auto w-full justify-start gap-1.5 px-2 py-1.5 text-xs font-normal" onClick={onAddComment}>
                    <MessageSquarePlus className="h-3.5 w-3.5 flex-shrink-0" />
                    <span className="truncate">Add Comment</span>
                </Button>
            </div>
        </div>
    );
};
