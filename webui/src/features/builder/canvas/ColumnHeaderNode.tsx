// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { memo } from "react";
import type { NodeProps } from "@xyflow/react";

interface ColumnHeaderData {
    label: string;
    count: number;
}

function ColumnHeaderNodeInner({ data }: NodeProps) {
    const { label } = data as unknown as ColumnHeaderData;

    return (
        <div className="whitespace-nowrap select-none">
            <span className="font-semibold tracking-widest uppercase" style={{ fontSize: 20 }}>
                {label}
            </span>
        </div>
    );
}

export const ColumnHeaderNode = memo(ColumnHeaderNodeInner);
