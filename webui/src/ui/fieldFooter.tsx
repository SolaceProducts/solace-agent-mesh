// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import * as React from "react";

import { cn } from "@/utils";

interface FieldFooterProps {
    hasError?: boolean;
    message: string;
    error: string;
    align?: "left" | "right";
    className?: string;
    id?: string;
}

const FieldFooter: React.FC<FieldFooterProps> = ({ hasError = false, message, error, align, className, id }) => {
    const defaultAlign = hasError ? "left" : "right";
    const textAlign = align || defaultAlign;

    return (
        <output id={id} className={cn("block text-xs", hasError ? "text-(--error-wMain)" : "text-(--secondary-text-wMain)", textAlign === "right" && "text-right", className)}>
            {hasError ? error : message}
        </output>
    );
};

FieldFooter.displayName = "FieldFooter";

export { FieldFooter };
export type { FieldFooterProps };
