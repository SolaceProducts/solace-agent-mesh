// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";

import { cn } from "@/utils/cnTailwind";

export const PageContentWrapper = ({ children, className, innerClassName }: { children: React.ReactNode; className?: string; innerClassName?: string }) => {
    return (
        <div className={cn("flex-1 overflow-y-auto p-4 leading-5 sm:p-8", className)}>
            <div className={cn("flex flex-col gap-8", innerClassName)}>{children}</div>
        </div>
    );
};
