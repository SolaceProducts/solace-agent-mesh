// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect, useMemo, useState } from "react";
import { Paperclip } from "lucide-react";

import { cn } from "@/utils/cnTailwind";

import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/ui/table";
import type { EvalRunResult, ExperimentModelConfig } from "@/types/eval";

import { PaginationControls } from "@/common";

import { createExperimentModelLookup, exampleLabel, isWorkflowRow, MODEL_COLORS, scoreBgClass, trialProblem, trialsDisagree, trialVerdicts } from "../utils";

import { ScoreCellContent, type ScoreCellState } from "./ScoreCellContent";

export interface ExampleRow {
    readonly key: string;
    readonly exampleId?: string;
    readonly prompt: string;
    readonly expectedResponse?: string;
    readonly perModelTrials: Record<string, EvalRunResult[]>;
}

export function aggregateCell(trials: EvalRunResult[] | undefined, evaluatorId: string): ScoreCellState {
    if (!trials || trials.length === 0) return { state: "pending" };
    if (trials.some(t => trialProblem(t) === "error")) return { state: "err" };
    const scores = trials.map(t => t.scores?.find(s => s.evaluatorId === evaluatorId)?.score).filter((v): v is number => v != null);
    if (scores.length === 0) return trials.some(t => trialProblem(t) === "ended") ? { state: "ended" } : { state: "missing" };
    return { state: "score", value: scores.reduce((a, b) => a + b, 0) / scores.length };
}

interface ExamplesTableProps {
    readonly resultsByModel: Record<string, EvalRunResult[]>;
    readonly models: string[];
    readonly allModels: string[];
    readonly evaluatorNameMap: Map<string, string>;
    readonly experimentModels?: ExperimentModelConfig[];
    readonly visibleEvaluatorNames?: string[];
    readonly onRowClick?: (row: ExampleRow, exampleNumber: number) => void;
    readonly selectedRowKey?: string | null;
}

function rowHasInputFile(row: ExampleRow): boolean {
    return Object.values(row.perModelTrials).some(trials => trials.some(t => t.inputArtifact));
}

function rowHasExpectedFile(row: ExampleRow): boolean {
    return Object.values(row.perModelTrials).some(trials => trials.some(t => t.expectedArtifact));
}

export function buildExampleRows(resultsByModel: Record<string, EvalRunResult[]>, models: string[]): ExampleRow[] {
    interface MutableRow {
        key: string;
        exampleId?: string;
        prompt: string;
        expectedResponse?: string;
        perModelTrials: Record<string, EvalRunResult[]>;
    }
    const byExample = new Map<string, MutableRow>();
    const order: string[] = [];
    for (const model of models) {
        const rows = resultsByModel[model] ?? [];
        for (const r of rows) {
            const key = r.exampleId ?? r.id;
            if (!byExample.has(key)) {
                byExample.set(key, {
                    key,
                    exampleId: r.exampleId,
                    prompt: r.inputPrompt ?? "",
                    expectedResponse: r.expectedResponse,
                    perModelTrials: {},
                });
                order.push(key);
            }
            const bucket = byExample.get(key)!;
            const trials = bucket.perModelTrials[model] ?? (bucket.perModelTrials[model] = []);
            trials.push(r);
            if (bucket.expectedResponse === undefined && r.expectedResponse !== undefined) {
                bucket.expectedResponse = r.expectedResponse;
            }
        }
    }
    return order.map(k => {
        const m = byExample.get(k)!;
        for (const model of Object.keys(m.perModelTrials)) {
            m.perModelTrials[model].sort((a, b) => a.trialIndex - b.trialIndex);
        }
        return {
            key: m.key,
            exampleId: m.exampleId,
            prompt: m.prompt,
            expectedResponse: m.expectedResponse,
            perModelTrials: m.perModelTrials,
        };
    });
}

const PAGE_SIZE = 10;

export function ExamplesTable({ resultsByModel, models, allModels, evaluatorNameMap, experimentModels, visibleEvaluatorNames, onRowClick, selectedRowKey }: ExamplesTableProps) {
    const { aliasFor, rawModelFor } = createExperimentModelLookup(experimentModels);

    const rows = useMemo(() => buildExampleRows(resultsByModel, models), [resultsByModel, models]);
    const [page, setPage] = useState(1);
    const totalPages = Math.max(1, Math.ceil(rows.length / PAGE_SIZE));

    useEffect(() => {
        if (page > totalPages) setPage(totalPages);
    }, [page, totalPages]);

    const pagedRows = useMemo(() => rows.slice((page - 1) * PAGE_SIZE, page * PAGE_SIZE), [rows, page]);

    const visibleNameSet = visibleEvaluatorNames && visibleEvaluatorNames.length > 0 ? new Set(visibleEvaluatorNames) : null;

    const visibleEvaluatorIds = useMemo(() => {
        const ids = Array.from(evaluatorNameMap.keys());
        if (!visibleNameSet) return ids;
        return ids.filter(id => visibleNameSet.has(evaluatorNameMap.get(id) ?? id));
    }, [evaluatorNameMap, visibleNameSet]);

    const pageStart = (page - 1) * PAGE_SIZE;

    const renderModelHeader = (m: string) => {
        const idx = allModels.indexOf(m);
        const alias = aliasFor(m);
        const rawModel = rawModelFor(m);
        const showRawModel = rawModel && rawModel !== alias;
        return (
            <span className="flex items-center justify-center gap-2">
                <span className="inline-block size-2 shrink-0 rounded-full" style={{ backgroundColor: MODEL_COLORS[idx % MODEL_COLORS.length] }} />
                <span className="text-sm text-(--primary-text-wMain)">{alias}</span>
                {showRawModel && <span className="truncate text-xs font-normal text-(--secondary-text-wMain)">{rawModel}</span>}
            </span>
        );
    };

    return (
        <div className="flex flex-col gap-3">
            <div className="flex flex-col gap-1">
                <h2 className="text-sm font-bold">Examples</h2>
                <p className="text-xs text-(--secondary-text-wMain)">Click on each row to see detailed information about each example run.</p>
            </div>
            {rows.length === 0 ? (
                <div className="rounded-xs border p-6 text-center text-sm text-(--secondary-text-wMain)">No results yet. Rows will appear as the run progresses.</div>
            ) : (
                <>
                    <div data-testid="examplesTable" className="overflow-auto rounded-xs border">
                        <Table>
                            <TableHeader>
                                <TableRow className="hover:bg-transparent">
                                    <TableHead rowSpan={2} className="w-12 border-r pl-4 align-bottom font-semibold">
                                        #
                                    </TableHead>
                                    <TableHead rowSpan={2} className="min-w-[280px] border-r align-bottom font-semibold">
                                        {rows.some(isWorkflowRow) ? "Example" : "Prompt"}
                                    </TableHead>
                                    {models.map(m => (
                                        <TableHead key={m} colSpan={Math.max(1, visibleEvaluatorIds.length)} className="border-l-2 text-center font-semibold">
                                            {renderModelHeader(m)}
                                        </TableHead>
                                    ))}
                                </TableRow>
                                <TableRow className="hover:bg-transparent">
                                    {models.map(m =>
                                        visibleEvaluatorIds.length === 0 ? (
                                            <TableHead key={`${m}-empty`} className="border-l text-center font-normal text-(--secondary-text-wMain)">
                                                —
                                            </TableHead>
                                        ) : (
                                            visibleEvaluatorIds.map((evId, evIdx) => (
                                                <TableHead key={`${m}-${evId}`} className={cn("min-w-[90px] text-center text-xs font-normal text-(--secondary-text-wMain)", evIdx === 0 ? "border-l-2" : "border-l")}>
                                                    {evaluatorNameMap.get(evId) ?? evId}
                                                </TableHead>
                                            ))
                                        )
                                    )}
                                </TableRow>
                            </TableHeader>
                            <TableBody borderLastRow>
                                {pagedRows.map((row, idx) => {
                                    const hasAnyResult = models.some(m => (row.perModelTrials[m]?.length ?? 0) > 0);
                                    const isSelected = selectedRowKey != null && selectedRowKey === row.key;
                                    const interactive = hasAnyResult;
                                    const exampleNumber = pageStart + idx + 1;
                                    return (
                                        <TableRow
                                            key={row.key}
                                            data-testid={`example-row-${row.key}`}
                                            className={cn("group/row align-middle transition-colors", interactive && "cursor-pointer hover:bg-(--stateLayer-w10)", !interactive && "opacity-60 hover:bg-transparent", isSelected && "bg-(--brand-w10)")}
                                            onClick={interactive ? () => onRowClick?.(row, exampleNumber) : undefined}
                                        >
                                            <TableCell className="border-r pl-4 font-mono text-xs text-(--secondary-text-wMain)">{exampleNumber}</TableCell>
                                            <TableCell className="max-w-[420px] border-r text-sm">
                                                <div className={cn("line-clamp-3 whitespace-pre-wrap", interactive && "text-(--primary-wMain)")}>{exampleLabel(row) || "—"}</div>
                                                {rowHasInputFile(row) && (
                                                    <span className="mt-1 mr-3 inline-flex items-center gap-1 text-xs text-(--secondary-text-wMain)">
                                                        <Paperclip className="size-3 shrink-0" aria-hidden /> Input file
                                                    </span>
                                                )}
                                                {rowHasExpectedFile(row) && (
                                                    <span className="mt-1 inline-flex items-center gap-1 text-xs text-(--secondary-text-wMain)">
                                                        <Paperclip className="size-3 shrink-0" aria-hidden /> Expected file
                                                    </span>
                                                )}
                                            </TableCell>
                                            {models.map(m => {
                                                const trials = row.perModelTrials[m];
                                                if (visibleEvaluatorIds.length === 0) {
                                                    return (
                                                        <TableCell key={m} className="border-l text-center text-xs text-(--secondary-text-wMain)">
                                                            —
                                                        </TableCell>
                                                    );
                                                }
                                                return visibleEvaluatorIds.map((evId, evIdx) => {
                                                    const cell = aggregateCell(trials, evId);
                                                    const verdicts = trialVerdicts(trials, evId);
                                                    return (
                                                        <TableCell
                                                            key={`${m}-${evId}`}
                                                            className={cn(
                                                                "text-center text-sm",
                                                                evIdx === 0 ? "border-l-2" : "border-l",
                                                                cell.state === "pending" && "opacity-40",
                                                                cell.state === "err" && "bg-(--error-wMain)/5",
                                                                cell.state === "score" && scoreBgClass(cell.value)
                                                            )}
                                                        >
                                                            <ScoreCellContent cell={cell} />
                                                            {trialsDisagree(verdicts) && (
                                                                <div className="text-xs text-(--warning-w100)" data-testid="trialsDisagree">
                                                                    {verdicts.passed}/{verdicts.judged} passed
                                                                </div>
                                                            )}
                                                        </TableCell>
                                                    );
                                                });
                                            })}
                                        </TableRow>
                                    );
                                })}
                            </TableBody>
                        </Table>
                    </div>
                    {totalPages > 1 && (
                        <div className="flex items-center justify-between">
                            <span className="text-xs text-(--secondary-text-wMain)">
                                Showing {pageStart + 1}–{Math.min(pageStart + PAGE_SIZE, rows.length)} of {rows.length}
                            </span>
                            <PaginationControls totalPages={totalPages} currentPage={page} onPageChange={setPage} />
                        </div>
                    )}
                </>
            )}
        </div>
    );
}
