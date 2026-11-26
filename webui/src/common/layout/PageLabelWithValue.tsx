// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";

export const PageLabelWithValue = ({ children, className = "", ...rest }: { children: React.ReactNode; className?: string } & React.HTMLAttributes<HTMLDivElement>) => {
    return (
        <div className={`grid grid-rows-[auto_1fr] gap-2 ${className}`} {...rest}>
            {children}
        </div>
    );
};
