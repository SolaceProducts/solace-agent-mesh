// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useRef } from "react";
import type { KeyboardEvent, RefObject } from "react";

interface UseCopyReturn<T extends HTMLElement> {
    ref: RefObject<T | null>;
    handleKeyDown: (e: KeyboardEvent) => void;
    selectAllContent: () => void;
}

export function useCopy<T extends HTMLElement>(): UseCopyReturn<T> {
    const ref = useRef<T>(null);

    const selectAllContent = () => {
        if (ref.current) {
            const range = document.createRange();
            range.selectNodeContents(ref.current);
            const selection = window.getSelection();
            if (selection) {
                selection.removeAllRanges();
                selection.addRange(range);
            }
        }
    };

    const handleKeyDown = (e: KeyboardEvent) => {
        if ((e.metaKey || e.ctrlKey) && e.key === "a") {
            e.preventDefault();
            selectAllContent();
        }
    };

    return {
        ref,
        handleKeyDown,
        selectAllContent,
    };
}
