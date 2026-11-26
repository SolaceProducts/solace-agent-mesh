// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { ReactNode } from "react";

import { cn } from "@/utils";

interface PageLayoutProps {
    children: ReactNode;
    className?: string;
}

export function PageLayout({ children, className }: PageLayoutProps) {
    return <div className={cn("flex min-h-0 w-full flex-1 flex-col overflow-hidden", className)}>{children}</div>;
}
