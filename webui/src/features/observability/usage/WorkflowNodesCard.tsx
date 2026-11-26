// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useMemo, useState } from "react";
import { ArrowDown, ArrowUp } from "lucide-react";

import type { UsageWorkflowNode } from "@/api/usage";

import { Button } from "@/ui/button";

import { cn, formatUsageMoney, formatUsagePercent, isUsageMoney, isZeroUsageMoney, sumUsageMoney, usageMoneyToNumber } from "@/utils";

import { type DonutSlice, UsageDonut } from "./UsageDonut";
import { NODE_KIND_ICONS, REMOVED_NODE_ICON } from "./usageIcons";
import { formatSliceDetail, OTHER_COLOR, SPENDER_COLORS } from "./usageSpenders";

const NODES_SHOWN = 5;

export function NodeChange({ node, hasComparison }: Readonly<{ node: UsageWorkflowNode; hasComparison: boolean }>) {
    if (!hasComparison) return <span className="text-xs text-(--secondary-text-wMain)">—</span>;
    if (node.changePercent === null) {
        return <span className="text-xs text-(--secondary-text-wMain)">{isZeroUsageMoney(node.cost) ? "—" : "New"}</span>;
    }
    const magnitude = formatUsagePercent(Math.abs(node.changePercent));
    if (magnitude === formatUsagePercent(0)) return <span className="text-xs text-(--secondary-text-wMain)">{magnitude}</span>;
    const rising = node.changePercent > 0;
    const Arrow = rising ? ArrowUp : ArrowDown;
    return (
        <span className={cn("flex items-center text-xs font-semibold", rising ? "text-(--error-wMain)" : "text-(--success-wMain)")}>
            <Arrow className="size-3" aria-hidden />
            <span className="sr-only">{rising ? "Up" : "Down"}</span>
            {magnitude}
        </span>
    );
}

function NodeRow({ node, rank, hasComparison }: Readonly<{ node: UsageWorkflowNode; rank: number; hasComparison: boolean }>) {
    const Icon = (node.kind === null ? undefined : NODE_KIND_ICONS[node.kind]) ?? REMOVED_NODE_ICON;
    return (
        <li className="flex items-center gap-2 border-b py-1.5 last:border-b-0">
            <span className="w-4 shrink-0 text-right text-xs text-(--secondary-text-wMain)">{rank}</span>
            <Icon className="size-4 shrink-0 text-(--secondary-text-wMain)" aria-hidden />
            <span className="min-w-0 flex-1 truncate" title={node.name}>
                {node.name}
                {node.kind === null && <span className="ml-2 text-xs text-(--secondary-text-wMain)">Removed node</span>}
            </span>
            <span className="w-16 shrink-0 text-right">
                <NodeChange node={node} hasComparison={hasComparison} />
            </span>
            <span className="w-20 shrink-0 text-right font-medium tabular-nums">{formatUsageMoney(node.cost)}</span>
            <span className="w-14 shrink-0 text-right text-xs text-(--secondary-text-wMain) tabular-nums">{formatUsagePercent(node.shareOfWorkflow)}</span>
        </li>
    );
}

function moreNodesLabel(count: number): string {
    return `View ${count} more ${count === 1 ? "node" : "nodes"}`;
}

function noComparisonReason(historyComplete: boolean): string {
    return historyComplete ? "No comparison for this period" : "No comparison until older usage is calculated";
}

export function WorkflowNodesCard({ nodes, hasComparison, historyComplete }: Readonly<{ nodes: readonly UsageWorkflowNode[]; hasComparison: boolean; historyComplete: boolean }>) {
    const [expanded, setExpanded] = useState(false);
    const shown = expanded ? nodes : nodes.slice(0, NODES_SHOWN);
    const hidden = nodes.slice(NODES_SHOWN);
    const hiddenCost = hidden.every(node => isUsageMoney(node.cost)) ? sumUsageMoney(hidden.map(node => node.cost)) : "";
    const hiddenShare = hidden.reduce((sum, node) => sum + node.shareOfWorkflow, 0);
    const slices = useMemo<DonutSlice[]>(
        () =>
            nodes.map((node, index) => ({
                key: node.nodeId,
                value: usageMoneyToNumber(node.cost),
                color: index < SPENDER_COLORS.length ? SPENDER_COLORS[index] : OTHER_COLOR,
                label: node.name,
                detail: formatSliceDetail(node.cost, node.shareOfWorkflow, "the workflow's cost"),
            })),
        [nodes]
    );

    return (
        <section aria-label="Cost by Node" className="flex flex-col gap-3 rounded-sm border p-4">
            <header className="flex items-baseline justify-between">
                <h3 className="text-sm font-semibold">Cost by Node</h3>
                {!hasComparison && <span className="text-xs text-(--secondary-text-wMain)">{noComparisonReason(historyComplete)}</span>}
            </header>
            {nodes.length === 0 ? (
                <p className="text-sm text-(--secondary-text-wMain)">This workflow has no nodes</p>
            ) : (
                <div className="flex items-start gap-4">
                    <UsageDonut slices={slices} size={96} />
                    <div className="flex min-w-0 flex-1 flex-col text-sm">
                        <ol className="flex flex-col">
                            {shown.map((node, index) => (
                                <NodeRow key={node.nodeId} node={node} rank={index + 1} hasComparison={hasComparison} />
                            ))}
                        </ol>
                        {hidden.length > 0 && (
                            <div className="flex items-center justify-between pt-1.5">
                                <Button variant="link" size="sm" className="h-auto px-0" onClick={() => setExpanded(open => !open)} aria-expanded={expanded}>
                                    {expanded ? "Show fewer nodes" : moreNodesLabel(hidden.length)}
                                </Button>
                                {!expanded && (
                                    <span className="text-xs text-(--secondary-text-wMain) tabular-nums">
                                        {formatUsageMoney(hiddenCost)} · {formatUsagePercent(hiddenShare)}
                                    </span>
                                )}
                            </div>
                        )}
                    </div>
                </div>
            )}
        </section>
    );
}
