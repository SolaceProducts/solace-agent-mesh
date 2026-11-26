// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import * as React from "react";

import { cn } from "@/utils";

interface LifecycleBadgeProps {
    className?: string;
    children?: React.ReactNode;
    variant?: "default" | "transparent";
}

export const LifecycleBadge = ({ className, children = "EXPERIMENTAL", variant = "default" }: LifecycleBadgeProps) => {
    const isTransparent = variant === "transparent";

    return (
        <span
            className={cn(
                "inline-flex shrink-0 items-center justify-center rounded-sm border-solid bg-transparent px-1 py-0.5 text-[10px] uppercase",
                isTransparent ? "border-2 text-(--secondary-text-wMain)" : "border border-(--secondary-text-wMain)",
                className
            )}
        >
            {children}
        </span>
    );
};
