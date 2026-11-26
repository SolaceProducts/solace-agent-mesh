// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { Handle, Position } from "@xyflow/react";

import { ESCALATION_ANSWER_SIDES, escalationAnswerHandles } from "../utils/types";

const HANDLE_STYLE = { width: 1, height: 1, background: "transparent", border: "none", minWidth: 0, minHeight: 0 };

export function EscalationAnswerHandles({ type }: Readonly<{ type: "source" | "target" }>) {
    return (
        <>
            {ESCALATION_ANSWER_SIDES.map(side => {
                const ids = escalationAnswerHandles(side);
                return <Handle key={side} type={type} position={side === "left" ? Position.Left : Position.Right} id={type === "source" ? ids.sourceHandle : ids.targetHandle} style={HANDLE_STYLE} />;
            })}
        </>
    );
}
