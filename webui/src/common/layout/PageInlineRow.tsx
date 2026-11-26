// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";

import { cn } from "@/utils/cnTailwind";

export const PageInlineRow = ({
    label,
    required,
    children,
    action,
    className = "",
    labelWidth = "w-40",
}: {
    label: React.ReactNode;
    required?: boolean;
    children: React.ReactNode;
    action?: React.ReactNode;
    className?: string;
    labelWidth?: string;
}) => {
    return (
        <div className={cn("flex flex-col gap-2 sm:grid sm:grid-cols-[auto_minmax(0,1fr)] sm:items-baseline sm:gap-x-4 sm:gap-y-1", className)}>
            <div className={cn("shrink-0 text-sm text-(--secondary-text-wMain)", action && "sm:row-start-2", labelWidth)}>
                {label}
                {required && <span className="ml-1 text-(--brand-wMain)">*</span>}
            </div>
            {action && <div className="flex justify-end sm:col-start-2 sm:row-start-1">{action}</div>}
            <div className="flex min-w-0 flex-col gap-1 sm:col-start-2">{children}</div>
        </div>
    );
};
