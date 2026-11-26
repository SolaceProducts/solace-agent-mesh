// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { cn } from "@/utils";

export const Footer = ({ children, className = "" }: { children: React.ReactNode; className?: string }) => {
    return <div className={cn("flex h-16 items-center justify-end gap-4 border-t px-8", className)}>{children}</div>;
};
