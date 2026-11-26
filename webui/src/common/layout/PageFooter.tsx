// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";

export const PageFooter = ({ children, className = "" }: { children: React.ReactNode; className?: string }) => {
    return <div className={`flex min-h-16 flex-wrap items-center justify-end gap-2 border-t px-4 py-3 sm:h-16 sm:flex-nowrap sm:gap-4 sm:px-8 sm:py-0 ${className}`}>{children}</div>;
};
