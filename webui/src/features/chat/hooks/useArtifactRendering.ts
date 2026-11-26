// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import { isUserControllableRendering, shouldAutoRender } from "@/features/chat/file/fileUtils";

interface UseArtifactRenderingOptions {
    filename?: string;
    mimeType?: string;
    shouldAutoExpand?: boolean;
}

interface UseArtifactRenderingReturn {
    shouldRender: boolean;
    isExpandable: boolean;
    isExpanded: boolean;
    toggleExpanded: () => void;
}

export const useArtifactRendering = ({ filename, mimeType, shouldAutoExpand }: UseArtifactRenderingOptions): UseArtifactRenderingReturn => {
    const shouldAutoRenderArtifact = useMemo(() => {
        return shouldAutoRender(filename, mimeType);
    }, [filename, mimeType]);

    const [isExpanded, setIsExpanded] = useState(() => shouldAutoExpand ?? shouldAutoRenderArtifact);

    const userHasToggled = useRef(false);

    useEffect(() => {
        if (shouldAutoExpand && !isExpanded && !userHasToggled.current) {
            setIsExpanded(true);
        }
    }, [shouldAutoExpand, isExpanded]);

    const isUserControllable = useMemo(() => {
        return isUserControllableRendering(filename, mimeType);
    }, [filename, mimeType]);

    const shouldRender = useMemo(() => {
        if (isUserControllable) {
            return isExpanded;
        }

        return false;
    }, [isUserControllable, isExpanded]);

    const isExpandable = useMemo(() => {
        return isUserControllable;
    }, [isUserControllable]);

    const toggleExpanded = useCallback(() => {
        if (!filename || !isExpandable) {
            console.log(`[useArtifactRendering] Toggle blocked - filename: ${filename}, isExpandable: ${isExpandable}`);
            return;
        }

        userHasToggled.current = true;

        setIsExpanded(prev => {
            const newValue = !prev;
            console.log(`[useArtifactRendering] ${newValue ? "Expanding" : "Collapsing"} ${filename}`);
            return newValue;
        });
    }, [filename, isExpandable]);

    return {
        shouldRender,
        isExpandable,
        isExpanded,
        toggleExpanded,
    };
};

export const useArtifactRenderingPreferences = () => {
    const getAutoRenderPreference = useCallback((filename?: string, mimeType?: string) => {
        return shouldAutoRender(filename, mimeType);
    }, []);

    const getUserControllablePreference = useCallback((filename?: string, mimeType?: string) => {
        return isUserControllableRendering(filename, mimeType);
    }, []);

    return {
        getAutoRenderPreference,
        getUserControllablePreference,
    };
};
