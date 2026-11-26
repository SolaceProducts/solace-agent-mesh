// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { GripVertical } from "lucide-react";

import { cn } from "@/utils/cnTailwind";

import type { useDragReorder } from "../hooks/useDragReorder";

interface DragHandleProps {
    readonly label: string;
    readonly disabled?: boolean;
    readonly handleProps: ReturnType<ReturnType<typeof useDragReorder>["handleProps"]>;
}

export function DragHandle({ label, disabled, handleProps }: DragHandleProps) {
    return (
        <span
            role="button"
            tabIndex={disabled ? -1 : 0}
            aria-disabled={disabled}
            aria-label={`Reorder ${label}`}
            {...handleProps}
            className={cn("flex shrink-0 items-center", disabled ? "cursor-not-allowed opacity-50" : "cursor-grab active:cursor-grabbing")}
        >
            <GripVertical className="size-4 text-(--secondary-text-wMain)" aria-hidden />
        </span>
    );
}
