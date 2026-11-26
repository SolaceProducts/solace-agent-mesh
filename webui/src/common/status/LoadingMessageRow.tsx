// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";

import { Spinner } from "@/ui/spinner";

import { ActivityDot } from "./ActivityDot";

interface LoadingMessageRowProps {
    statusText?: string;
}

export const LoadingMessageRow = React.memo<LoadingMessageRowProps>(({ statusText }) => {
    return (
        <div className="flex min-h-8 items-center space-x-3 py-1">
            {statusText ? <Spinner size="small" variant="primary" className="mr-3 ml-2" /> : <ActivityDot className="mr-3 ml-2" />}
            <div className="flex min-w-0 flex-1 items-center gap-1">
                {statusText && (
                    <span className="animate-pulse truncate text-sm text-(--secondary-text-wMain)" title={statusText}>
                        {statusText}
                    </span>
                )}
            </div>
        </div>
    );
});
