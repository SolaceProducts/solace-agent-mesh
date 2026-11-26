// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { ArrowRight, Signpost, TriangleAlert } from "lucide-react";
import type { FC } from "react";

import { cn } from "@/utils/cnTailwind";
import { clickableNodeProps } from "@/utils/nodeInteraction";

import { decisionAnswerSummary, decisionEscalationLabel, decisionHeadlineAnswer } from "../../decisionResult";
import { NODE_COLORS } from "../../nodeColors";
import { ACTIVITY_NODE_BASE_STYLES, ACTIVITY_NODE_SELECTED_CLASS } from "../utils/nodeStyles";
import type { LayoutNode } from "../utils/types";

interface DecisionNodeProps {
    node: LayoutNode;
    isSelected?: boolean;
    onClick?: (node: LayoutNode) => void;
}

const DecisionNode: FC<DecisionNodeProps> = ({ node, isSelected, onClick }) => {
    const decision = node.data.decision;
    const route = node.data.selectedBranch;
    const headline = decision ? decisionHeadlineAnswer(decision) : undefined;
    const hasDetails = !!headline || !!route || !!decision?.escalated;

    return (
        <div
            {...clickableNodeProps(() => onClick?.(node))}
            data-testid="activity-decision-node"
            className={cn(ACTIVITY_NODE_BASE_STYLES.CONTAINER_HEADER, isSelected && ACTIVITY_NODE_SELECTED_CLASS)}
            style={{ width: "fit-content", minWidth: "280px" }}
        >
            <div className="flex min-w-0 items-center gap-2 px-4 py-2">
                <Signpost className={cn("h-4 w-4 flex-shrink-0", NODE_COLORS.decision)} />
                <span className="text-sm font-semibold">Decision</span>
            </div>

            {hasDetails && (
                <div className="flex flex-col gap-1.5 px-4 pt-0 pb-3">
                    {headline && (
                        <div className="truncate text-xs text-(--secondary-text-wMain)" title={`${headline.key}: ${decisionAnswerSummary(headline)}`}>
                            <span className="font-semibold">{headline.key}:</span> {decisionAnswerSummary(headline)}
                        </div>
                    )}
                    {route && (
                        <span className="flex items-center gap-1 truncate rounded bg-(--secondary-w10) px-2 py-1 text-sm text-(--secondary-text-wMain)" title={route}>
                            <ArrowRight className="h-3 w-3 flex-shrink-0" />
                            {route}
                        </span>
                    )}
                    {decision?.escalated && (
                        <span className="flex items-center gap-1 rounded bg-(--warning-w10) px-2 py-1 text-xs font-semibold text-(--warning-wMain)">
                            <TriangleAlert className="h-3 w-3 flex-shrink-0" />
                            {decisionEscalationLabel(decision)}
                        </span>
                    )}
                    {node.data.escalationRoute && (
                        <span className="flex items-center gap-1 truncate rounded bg-(--success-w10) px-2 py-1 text-sm text-(--success-wMain)" title={`routed after escalation: ${node.data.escalationRoute}`}>
                            <ArrowRight className="h-3 w-3 flex-shrink-0" />
                            {node.data.escalationRoute}
                            <span className="text-xs text-(--secondary-text-wMain)">after escalation</span>
                        </span>
                    )}
                </div>
            )}
        </div>
    );
};

export default DecisionNode;
