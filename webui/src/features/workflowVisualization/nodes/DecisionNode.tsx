// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { Route, Signpost } from "lucide-react";
import type { FC } from "react";

import { decisionQuestionTypeLabel, routedQuestionFirst } from "@/utils/decisionNode";
import { clickableNodeProps } from "@/utils/nodeInteraction";

import { Badge } from "@/ui/badge";

import { cn } from "@/utils";

import { NODE_BASE_STYLES, NODE_HIGHLIGHT_CLASSES, NODE_SELECTED_CLASS, type NodeProps } from "../utils/types";

const DecisionNode: FC<NodeProps> = ({ node, isSelected, isHighlighted, onClick }) => {
    const { routeOn } = node.data;
    const questions = routedQuestionFirst(node.data.questions ?? [], routeOn);

    return (
        <div
            data-testid="decision-node"
            className={cn(NODE_BASE_STYLES.SWITCH, isSelected && NODE_SELECTED_CLASS, isHighlighted && NODE_HIGHLIGHT_CLASSES)}
            style={{ width: `${node.width}px` }}
            {...(onClick ? clickableNodeProps(() => onClick(node)) : {})}
        >
            <div className="flex items-center justify-between gap-2 px-4 py-3">
                <div className="flex min-w-0 items-center gap-2">
                    <Signpost className="h-4 w-4 flex-shrink-0 text-(--accent-n0-wMain)" />
                    <span className="text-sm font-semibold">Decision</span>
                </div>
            </div>

            {questions.length > 0 && (
                <div className="px-4 pt-0 pb-3">
                    <div className="flex flex-col gap-1.5">
                        {questions.map(question => (
                            <div key={question.key} className="flex h-7 items-center gap-2">
                                {question.key === routeOn && <Route className="size-4 flex-shrink-0 text-(--secondary-text-wMain)" role="img" aria-label="Routes on this question" />}
                                <span className="block min-w-0 flex-1 truncate rounded bg-(--secondary-w10) px-2 py-1 text-sm text-(--secondary-text-wMain)" title={question.instructions || question.key}>
                                    {question.key}
                                </span>
                                <Badge variant="outline">{decisionQuestionTypeLabel(question.type)}</Badge>
                            </div>
                        ))}
                    </div>
                </div>
            )}
        </div>
    );
};

export default DecisionNode;
