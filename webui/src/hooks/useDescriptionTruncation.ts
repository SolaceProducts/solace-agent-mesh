// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect, useRef, useState } from "react";

interface UseDescriptionTruncationReturn {
    isExpanded: boolean;
    isTruncated: boolean;
    descriptionRef: React.RefObject<HTMLDivElement | null>;
    toggleExpanded: () => void;
}

export const useDescriptionTruncation = (description: string, enabled = false): UseDescriptionTruncationReturn => {
    const [isExpanded, setIsExpanded] = useState(false);
    const [isTruncated, setIsTruncated] = useState(false);
    const descriptionRef = useRef<HTMLDivElement>(null);

    useEffect(() => {
        if (!enabled) return;

        const element = descriptionRef.current;
        if (element) {
            setIsTruncated(element.scrollWidth > element.clientWidth);
        }
    }, [description, enabled]);

    const toggleExpanded = () => setIsExpanded(!isExpanded);

    return {
        isExpanded,
        isTruncated,
        descriptionRef,
        toggleExpanded,
    };
};
