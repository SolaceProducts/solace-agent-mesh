// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect } from "react";

interface UseEscapeKeyProps {
    onEscape: () => void;
    enabled?: boolean;
}

export function useEscapeKey({ onEscape, enabled = true }: UseEscapeKeyProps) {
    useEffect(() => {
        if (!enabled) return;

        const handleEscape = (event: KeyboardEvent) => {
            if (event.key === "Escape") {
                onEscape();
            }
        };

        document.addEventListener("keydown", handleEscape);

        return () => {
            document.removeEventListener("keydown", handleEscape);
        };
    }, [onEscape, enabled]);
}
