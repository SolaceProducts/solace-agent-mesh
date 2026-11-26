// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";

import { cn } from "@/utils";

import { useClickOutside } from "./hooks/useClickOutside";
import { useEscapeKey } from "./hooks/useEscapeKey";
import { type PopoverPlacement, usePopoverPosition } from "./hooks/usePopoverPosition";

interface PopoverProps {
    isOpen: boolean;
    onClose: () => void;
    anchorRef: React.RefObject<HTMLElement | null>;
    children: React.ReactNode;
    placement?: PopoverPlacement;
    offset?: { x: number; y: number };
    className?: string;
    closeOnClickOutside?: boolean;
    closeOnEscape?: boolean;
    portal?: boolean;
    animationDuration?: number;
    fallbackPlacements?: PopoverPlacement[];
}

export function PopoverManual({ isOpen, onClose, anchorRef, children, placement = "bottom-start", offset, className, closeOnClickOutside = true, closeOnEscape = true, portal = true, animationDuration = 150, fallbackPlacements }: PopoverProps) {
    const popoverRef = useRef<HTMLDivElement>(null);
    const [positionStyle, setPositionStyle] = useState<React.CSSProperties>({});
    const [isPositioned, setIsPositioned] = useState(false);

    useClickOutside({
        ref: popoverRef,
        anchorRef,
        onClickOutside: onClose,
        enabled: isOpen && closeOnClickOutside,
    });

    useEscapeKey({
        onEscape: onClose,
        enabled: isOpen && closeOnEscape,
    });

    const { getPositionStyle: calculatePosition } = usePopoverPosition({
        anchorRef,
        placement,
        offset,
        fallbackPlacements,
    });

    useEffect(() => {
        if (isOpen && popoverRef.current) {
            setIsPositioned(false);

            requestAnimationFrame(() => {
                if (popoverRef.current) {
                    const newPositionStyle = calculatePosition(popoverRef.current);
                    setPositionStyle(newPositionStyle);
                    setIsPositioned(true);
                }
            });
        } else {
            setIsPositioned(false);
        }
    }, [isOpen, calculatePosition]);

    if (!isOpen) {
        return null;
    }

    const popoverContent = (
        <div
            ref={popoverRef}
            role="dialog"
            aria-modal="false"
            className={cn("min-w-[8rem] overflow-hidden border bg-(--background-w10) shadow-md", className)}
            style={{
                ...positionStyle,
                opacity: isOpen && isPositioned ? 1 : 0,
                transform: `scale(${isOpen && isPositioned ? 1 : 0.95})`,
                transition: `opacity ${animationDuration}ms ease-in-out, transform ${animationDuration}ms ease-in-out`,
                pointerEvents: isOpen && isPositioned ? "auto" : "none",
                visibility: isPositioned ? "visible" : "hidden",
            }}
        >
            {children}
        </div>
    );

    if (portal) {
        return createPortal(popoverContent, document.body);
    }

    return popoverContent;
}
