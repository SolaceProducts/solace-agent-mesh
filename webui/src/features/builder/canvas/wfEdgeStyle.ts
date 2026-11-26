// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { EdgeProps } from "@xyflow/react";

export const ESCALATION_ANSWER_DASH = "6 4";

export function wfEdgeStyle(data: EdgeProps["data"]): { strokeWidth: number; stroke: string; strokeDasharray?: string } {
    const edge = data as { highlighted?: boolean; branchKind?: string; escalationAnswer?: boolean } | undefined;
    if (edge?.highlighted) return { strokeWidth: 2.5, stroke: "var(--success-wMain)" };
    if (edge?.escalationAnswer) return { strokeWidth: 2, stroke: "var(--warning-wMain)", strokeDasharray: ESCALATION_ANSWER_DASH };
    if (edge?.branchKind === "escalate") return { strokeWidth: 2, stroke: "var(--warning-wMain)" };
    return { strokeWidth: 2, stroke: "var(--stateLayer-w20)" };
}
