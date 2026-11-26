// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/ui/table";
import type { EvalRunResult } from "@/types/eval";

import { MessageBanner } from "@/common";

import type { TrialOutcomeText } from "../utils";

const numberFormat = new Intl.NumberFormat();
const costFormat = new Intl.NumberFormat(undefined, { maximumFractionDigits: 6 });

const formatCost = (cost: string) => (Number.isFinite(Number(cost)) ? costFormat.format(Number(cost)) : cost);

export function TrialOutcomeNote({ title, body, details, tone, testId, className }: TrialOutcomeText & { readonly testId: string; readonly className?: string }) {
    return (
        <MessageBanner
            data-testid={testId}
            variant={tone}
            className={className}
            message={
                <div className="flex flex-col gap-1 break-words">
                    <span className="font-semibold">{title}</span>
                    <span className="font-normal">{body}</span>
                    {details && (
                        <details className="text-xs font-normal">
                            <summary className="cursor-pointer">Details</summary>
                            <div className="mt-1 font-mono whitespace-pre-wrap">{details}</div>
                        </details>
                    )}
                </div>
            }
        />
    );
}

export function WorkflowTrialDetails({ result }: { readonly result: EvalRunResult }) {
    const breakdown = result.tokenBreakdown;
    if (!breakdown) return null;
    const totalTokens = Object.values(breakdown.byModel).reduce((sum, c) => sum + c.totalTokens, 0);
    const hasUnattributed = (breakdown.unattributed?.totalTokens ?? 0) > 0;
    return (
        <div data-testid="workflowTrialDetails" className="flex flex-col gap-2 px-4 py-3">
            <div className="flex flex-wrap items-center gap-4 text-xs text-(--secondary-text-wMain)">
                <span>
                    Tokens: <span className="font-medium text-(--primary-text-wMain)">{numberFormat.format(totalTokens)}</span>
                </span>
                <span>
                    Cost: <span className="font-medium text-(--primary-text-wMain)">{result.cost == null ? "unknown" : formatCost(result.cost)}</span>
                </span>
                {breakdown.incomplete && <span data-testid="tokensLowerBound">Some model calls were not counted, so the real token total may be higher.</span>}
            </div>
            {(breakdown.byNode.length > 0 || hasUnattributed) && (
                <Table>
                    <TableHeader>
                        <TableRow>
                            <TableHead>Step</TableHead>
                            <TableHead>Models</TableHead>
                            <TableHead className="text-right">Input</TableHead>
                            <TableHead className="text-right">Output</TableHead>
                            <TableHead className="text-right">Total</TableHead>
                        </TableRow>
                    </TableHeader>
                    <TableBody>
                        {breakdown.byNode.map(node => (
                            <TableRow key={node.nodeId}>
                                <TableCell className="font-mono text-xs">{node.nodeId}</TableCell>
                                <TableCell className="text-xs">{node.models.join(", ") || "—"}</TableCell>
                                <TableCell className="text-right text-xs">{numberFormat.format(node.inputTokens)}</TableCell>
                                <TableCell className="text-right text-xs">{numberFormat.format(node.outputTokens)}</TableCell>
                                <TableCell className="text-right text-xs">{numberFormat.format(node.totalTokens)}</TableCell>
                            </TableRow>
                        ))}
                        {hasUnattributed && breakdown.unattributed && (
                            <TableRow data-testid="unattributedTokens">
                                <TableCell className="text-xs">Other steps, such as decisions</TableCell>
                                <TableCell className="text-xs">—</TableCell>
                                <TableCell className="text-right text-xs">{numberFormat.format(breakdown.unattributed.inputTokens)}</TableCell>
                                <TableCell className="text-right text-xs">{numberFormat.format(breakdown.unattributed.outputTokens)}</TableCell>
                                <TableCell className="text-right text-xs">{numberFormat.format(breakdown.unattributed.totalTokens)}</TableCell>
                            </TableRow>
                        )}
                    </TableBody>
                </Table>
            )}
        </div>
    );
}
