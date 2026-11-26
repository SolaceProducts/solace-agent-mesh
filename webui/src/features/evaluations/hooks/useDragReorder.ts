// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useState } from "react";
import type React from "react";

const REORDER_DRAG_TYPE = "application/x-sam-reorder";

export function useDragReorder<T>(items: readonly T[], onReorder: (next: T[]) => void, disabled?: boolean) {
    const [dragIndex, setDragIndex] = useState<number | null>(null);
    const [dragOverIndex, setDragOverIndex] = useState<number | null>(null);

    const reset = () => {
        setDragIndex(null);
        setDragOverIndex(null);
    };

    const rowProps = (index: number) => ({
        onDragOver: (e: React.DragEvent) => {
            if (disabled || dragIndex === null) return;
            e.preventDefault();
            e.dataTransfer.dropEffect = "move";
            if (index !== dragOverIndex) setDragOverIndex(index);
        },
        onDrop: (e: React.DragEvent) => {
            if (disabled || dragIndex === null) return;
            e.preventDefault();
            if (dragIndex !== index) {
                const next = [...items];
                const [moved] = next.splice(dragIndex, 1);
                next.splice(index, 0, moved);
                onReorder(next);
            }
            reset();
        },
    });

    const handleProps = (index: number) => ({
        draggable: !disabled,
        onDragStart: (e: React.DragEvent) => {
            if (disabled) return;
            setDragIndex(index);
            e.dataTransfer.effectAllowed = "move";
            e.dataTransfer.setData(REORDER_DRAG_TYPE, String(index));
        },
        onDragEnd: reset,
    });

    const isDragging = (index: number) => dragIndex === index;
    const isDropTarget = (index: number) => dragOverIndex === index && dragIndex !== index;

    return { rowProps, handleProps, isDragging, isDropTarget };
}
