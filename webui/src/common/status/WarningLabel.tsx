// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { TriangleAlert } from "lucide-react";

export const WarningLabel = ({ children }: { children: React.ReactNode }) => {
    return (
        <span className="flex items-start gap-2 text-xs text-(--secondary-text-wMain)">
            <TriangleAlert className="size-4 shrink-0 text-(--warning-w100)" />
            {children}
        </span>
    );
};
