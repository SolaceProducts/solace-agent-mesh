// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useState } from "react";

interface UseListKeyboardNavigationOptions {
    itemCount: number;
    isOpen: boolean;
    onSelect: (index: number) => void;
    onClose?: () => void;
    idPrefix?: string;
    selectOnTab?: boolean;
}

export function useListKeyboardNavigation({ itemCount, isOpen, onSelect, onClose, idPrefix, selectOnTab = true }: UseListKeyboardNavigationOptions) {
    const [activeIndex, setActiveIndex] = useState(0);
    const [isKeyboardMode, setIsKeyboardMode] = useState(false);

    useEffect(() => {
        setActiveIndex(0);
    }, [itemCount, isOpen]);

    useEffect(() => {
        if (idPrefix) {
            const el = document.getElementById(`${idPrefix}${activeIndex}`);
            if (el) el.scrollIntoView({ behavior: "smooth", block: "nearest" });
        }
    }, [activeIndex, idPrefix]);

    const handleMouseEnter = useCallback((index: number) => {
        setIsKeyboardMode(false);
        setActiveIndex(index);
    }, []);

    const handleKeyDown = useCallback(
        (e: React.KeyboardEvent) => {
            if (!isOpen) return;

            if (e.key === "Escape") {
                e.preventDefault();
                onClose?.();
                return;
            }
            if (itemCount === 0) return;

            if (e.key === "ArrowDown") {
                e.preventDefault();
                setIsKeyboardMode(true);
                setActiveIndex(prev => Math.min(prev + 1, itemCount - 1));
            } else if (e.key === "ArrowUp") {
                e.preventDefault();
                setIsKeyboardMode(true);
                setActiveIndex(prev => Math.max(prev - 1, 0));
            } else if (e.key === "Enter" || (e.key === "Tab" && selectOnTab)) {
                e.preventDefault();
                onSelect(activeIndex);
            }
        },
        [isOpen, itemCount, activeIndex, onSelect, onClose, selectOnTab]
    );

    return {
        activeIndex,
        isKeyboardMode,
        setActiveIndex,
        handleMouseEnter,
        handleKeyDown,
    };
}
