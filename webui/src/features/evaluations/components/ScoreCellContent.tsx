// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { cn } from "@/utils/cnTailwind";

import { type ScoreCellState, scoreColorClass } from "../utils";

export type { ScoreCellState } from "../utils";

export function ScoreCellContent({ cell }: { cell: ScoreCellState }) {
    if (cell.state === "pending") {
        return <span className="text-xs text-(--secondary-text-wMain)">…</span>;
    }
    if (cell.state === "err") {
        return <span className="text-xs font-medium text-(--error-wMain)">err</span>;
    }
    if (cell.state === "ended") {
        return <span className="text-xs text-(--secondary-text-wMain)">ended early</span>;
    }
    if (cell.state === "missing") {
        return <span className="text-xs text-(--secondary-text-wMain)">—</span>;
    }
    return <span className={cn("font-semibold", scoreColorClass(cell.value))}>{Math.round(cell.value * 100)}%</span>;
}
