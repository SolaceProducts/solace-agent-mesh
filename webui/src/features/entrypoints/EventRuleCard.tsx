// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { ArrowRight, CopyPlus, Trash2 } from "lucide-react";

import { useChatContext } from "@/providers/contexts";

import { Button } from "@/ui/button";

import type { EventRule } from "@/types";

import { findAgentOption, useAgentOptions } from "./hooks";

interface EventRuleCardProps {
    rule: EventRule;
    onEdit?: () => void;
    onClone?: () => void;
    onDelete?: () => void;
    onView?: () => void;
}

export const EventRuleCard = ({ rule, onEdit, onClone, onDelete, onView }: EventRuleCardProps) => {
    const { agentNameDisplayNameMap } = useChatContext();
    const agentOptions = useAgentOptions();
    const isWorkflow = !!rule.targetWorkflowName;
    const agentDisplayName = rule.targetAgent ? (findAgentOption(agentOptions, rule.targetAgent)?.displayName ?? agentNameDisplayNameMap[rule.targetAgent] ?? rule.targetAgent) : "Unknown";
    const subscriptions = Array.isArray(rule.subscriptions) ? rule.subscriptions : [];
    const topicsDisplay =
        subscriptions
            .map(s => s?.topic || "")
            .filter(Boolean)
            .join(", ") || "(no topics)";

    return (
        <div className="my-4 rounded-sm border p-3">
            <div className="flex justify-between">
                <div className="min-w-0 flex-1 space-y-1.5">
                    {onEdit || onView ? (
                        <Button
                            variant="link"
                            className="p-0"
                            type="button"
                            onClick={() => {
                                onEdit ? onEdit() : onView ? onView() : null;
                            }}
                        >
                            {rule.name || "(unnamed rule)"}
                        </Button>
                    ) : (
                        <div className="font-medium">{rule.name || "(unnamed rule)"}</div>
                    )}
                    <div className="flex flex-wrap items-center gap-1 text-sm text-(--secondary-text-wMain)">
                        <div className="max-w-[200px] truncate" title={topicsDisplay}>
                            Topic: {topicsDisplay}
                        </div>
                        <ArrowRight size="16" />
                        <div className="inline-flex items-center gap-1">{isWorkflow ? `Workflow: ${rule.targetWorkflowName}` : `Agent: ${agentDisplayName}`}</div>
                    </div>
                </div>
                <div className="flex items-center">
                    {onClone && (
                        <Button
                            variant="ghost"
                            type="button"
                            onClick={e => {
                                e.stopPropagation();
                                onClone();
                            }}
                            tooltip="Clone"
                        >
                            <CopyPlus />
                        </Button>
                    )}
                    {onDelete && (
                        <Button
                            variant="ghost"
                            type="button"
                            onClick={e => {
                                e.stopPropagation();
                                onDelete();
                            }}
                            tooltip="Delete"
                        >
                            <Trash2 />
                        </Button>
                    )}
                </div>
            </div>
        </div>
    );
};
