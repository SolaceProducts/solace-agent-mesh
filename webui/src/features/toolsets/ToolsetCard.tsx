// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { Wrench } from "lucide-react";
import type { ReactNode } from "react";

import type { Toolset } from "@/types";

interface ToolsetCardProps {
    toolset: Toolset;
    actionButton?: ReactNode;
}

export function ToolsetCard({ toolset, actionButton }: ToolsetCardProps) {
    return (
        <div className="flex w-full items-center gap-2 rounded-sm border p-4">
            <div className="shrink-0">
                <div className="mr-4 flex h-12 w-12 items-center justify-center rounded-full bg-(--secondary-w10)">
                    <Wrench className="size-5" />
                </div>
            </div>
            <div className="relative flex min-w-0 flex-1 flex-col">
                <div className="flex items-center gap-2">
                    <div className="truncate font-medium" title={toolset.name}>
                        {toolset.name}
                    </div>
                </div>
                <div className="mt-1 truncate text-sm text-(--secondary-text-wMain)">{toolset.description}</div>
            </div>
            {actionButton && <div className="flex items-center">{actionButton}</div>}
        </div>
    );
}
