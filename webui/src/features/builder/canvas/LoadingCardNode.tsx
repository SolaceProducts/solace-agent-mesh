// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { memo } from "react";
import type { NodeProps } from "@xyflow/react";

import { CardShell } from "./CardShell";

function LoadingCardNodeInner({ data, selected }: NodeProps) {
    const { width } = data as unknown as { width?: number };
    const w = width ?? 300;

    return (
        <CardShell selected={selected} width={w}>
            <div className="animate-pulse px-4 py-3">
                <div className="flex items-center gap-2.5">
                    <div className="h-8 w-8 shrink-0 rounded-lg" />
                    <div className="min-w-0 flex-1 space-y-1.5">
                        <div className="h-4 w-2/3 rounded" />
                        <div className="h-2.5 w-1/3 rounded" />
                    </div>
                </div>
                <div className="mt-3 space-y-1.5">
                    <div className="h-2.5 w-full rounded" />
                    <div className="h-2.5 w-4/5 rounded" />
                </div>
            </div>
        </CardShell>
    );
}

export const LoadingCardNode = memo(LoadingCardNodeInner);
