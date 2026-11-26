// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect } from "react";

interface UseClickOutsideProps {
    ref: React.RefObject<HTMLElement | null>;
    anchorRef?: React.RefObject<HTMLElement | null>;
    onClickOutside: () => void;
    enabled?: boolean;
}

export function useClickOutside({ ref, anchorRef, onClickOutside, enabled = true }: UseClickOutsideProps) {
    useEffect(() => {
        if (!enabled) return;

        const handleClickOutside = (event: MouseEvent) => {
            const target = event.target as Node;

            const isOutsideMain = ref.current && !ref.current.contains(target);

            const isOutsideAnchor = anchorRef?.current ? !anchorRef.current.contains(target) : true;

            if (isOutsideMain && isOutsideAnchor) {
                onClickOutside();
            }
        };

        document.addEventListener("mousedown", handleClickOutside);

        return () => {
            document.removeEventListener("mousedown", handleClickOutside);
        };
    }, [ref, anchorRef, onClickOutside, enabled]);
}
