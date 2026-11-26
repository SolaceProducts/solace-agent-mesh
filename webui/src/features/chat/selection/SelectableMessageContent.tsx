// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useCallback, useEffect, useRef } from "react";

import { calculateMenuPosition, getSelectedText, getSelectionBoundingRect, getSelectionRange, isSelectionContainedInElement, isValidSelection } from "./selectionUtils";
import type { SelectableMessageContentProps } from "./types";
import { useTextSelection } from "./useTextSelection";

export const SelectableMessageContent: React.FC<SelectableMessageContentProps> = ({ messageId, taskId, children, isAIMessage }) => {
    const { setSelection, clearSelection } = useTextSelection();
    const containerRef = useRef<HTMLDivElement>(null);

    const handleMouseUp = useCallback(() => {
        if (!isAIMessage) {
            return;
        }

        setTimeout(() => {
            const text = getSelectedText();
            const range = getSelectionRange();
            const rect = getSelectionBoundingRect();

            if (!isValidSelection(text) || !range || !rect || !text) {
                return;
            }

            const container = containerRef.current;
            if (!container) {
                return;
            }

            if (!isSelectionContainedInElement(range, container)) {
                return;
            }

            const position = calculateMenuPosition(rect);

            setSelection(text, range, messageId, taskId || "", position);
        }, 10);
    }, [isAIMessage, messageId, taskId, setSelection]);

    useEffect(() => {
        const container = containerRef.current;
        if (!container || !isAIMessage) {
            return;
        }

        document.addEventListener("mouseup", handleMouseUp);

        return () => {
            document.removeEventListener("mouseup", handleMouseUp);
        };
    }, [handleMouseUp, isAIMessage]);

    useEffect(() => {
        return () => {
            clearSelection();
        };
    }, [clearSelection]);

    return (
        <div ref={containerRef} className="selectable-message-content">
            {children}
        </div>
    );
};
