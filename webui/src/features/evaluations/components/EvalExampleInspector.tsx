// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useMemo, useState } from "react";
import { ChevronDown, ChevronRight, Info, X } from "lucide-react";

import { evaluationKeys, type EvaluationsApi, evaluationsApi } from "@/api/evaluations";

import { cn } from "@/utils/cnTailwind";

import { Button } from "@/ui/button";
import { Separator } from "@/ui/separator";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/ui/table";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/ui/tooltip";
import type { EvalArtifact, EvalEvaluatorType, EvalRunResult, EvaluatorScoring, ExperimentModelConfig } from "@/types/eval";

import { cellStateOf, collectExampleArtifacts, createExperimentModelLookup, MODEL_COLORS, scoreBgClass, trialProblem } from "../utils";

import { ArtifactPreviewView } from "./ArtifactPreviewView";
import { ArtifactsSection } from "./ArtifactsSection";
import { explainsItsScore, reasoningHeading } from "./decisionScore";
import { ExampleFilesSection } from "./ExampleFilesSection";
import type { ExampleRow } from "./ExamplesTable";
import { ReasoningRow } from "./ReasoningRow";
import { ScoreCellContent, type ScoreCellState } from "./ScoreCellContent";
import { TrialResponse } from "./TrialResponse";

export type ArtifactPreviewTarget = { kind: "input" | "expected" | "output"; resultId: string; artifact: EvalArtifact };

function fetchArtifactBlob(evalApi: EvaluationsApi, target: ArtifactPreviewTarget): Promise<Blob> {
    const { resultId, artifact } = target;
    return target.kind === "output" ? evalApi.getRunResultArtifactBlob(resultId, artifact.filename, artifact.version) : evalApi.getRunResultExampleArtifactBlob(resultId, target.kind, artifact.filename, artifact.version);
}

function artifactBlobKey(target: ArtifactPreviewTarget): readonly unknown[] {
    const { resultId, artifact } = target;
    return target.kind === "output" ? evaluationKeys.runResultArtifactBlob(resultId, artifact.filename, artifact.version) : evaluationKeys.runResultExampleArtifactBlob(resultId, target.kind, artifact.filename, artifact.version);
}

export type InspectorEvaluator = { id: string; name: string; type?: EvalEvaluatorType; heuristicType?: string; scoring?: EvaluatorScoring };

interface EvalExampleInspectorProps {
    readonly row: ExampleRow;
    readonly exampleNumber: number;
    readonly models: string[];
    readonly allModels: string[];
    readonly experimentModels?: ExperimentModelConfig[];
    readonly evaluators: InspectorEvaluator[];
    readonly onClose: () => void;
    readonly onOpenFullDetails?: () => void;
}

const PROMPT_PREVIEW_CHARS = 320;

export function EvalExampleInspector({ row, exampleNumber, models, allModels, experimentModels, evaluators, onClose, onOpenFullDetails }: EvalExampleInspectorProps) {
    const { aliasFor, rawModelFor } = createExperimentModelLookup(experimentModels);
    const [promptExpanded, setPromptExpanded] = useState(false);
    const [bannerDismissed, setBannerDismissed] = useState(false);
    const [previewTarget, setPreviewTarget] = useState<ArtifactPreviewTarget | null>(null);
    const [expandedTrials, setExpandedTrials] = useState<Set<number>>(() => new Set());

    const promptIsLong = row.prompt.length > PROMPT_PREVIEW_CHARS;
    const promptDisplay = !promptIsLong || promptExpanded ? row.prompt : row.prompt.slice(0, PROMPT_PREVIEW_CHARS).trimEnd() + "…";

    const trialCount = useMemo(() => {
        let max = 0;
        for (const m of models) {
            const trials = row.perModelTrials[m] ?? [];
            if (trials.length > max) max = trials.length;
        }
        return Math.max(1, max);
    }, [row.perModelTrials, models]);

    const inputArtifacts = useMemo(() => collectExampleArtifacts(row, models), [row, models]);

    const toggleTrial = (idx: number) => {
        setExpandedTrials(prev => {
            const next = new Set(prev);
            if (next.has(idx)) next.delete(idx);
            else next.add(idx);
            return next;
        });
    };

    if (previewTarget) {
        return (
            <div className="flex h-full w-full flex-col overflow-hidden bg-(--background-w10)">
                <ArtifactPreviewView artifact={previewTarget.artifact} fetchBlob={() => fetchArtifactBlob(evaluationsApi, previewTarget)} cacheKey={artifactBlobKey(previewTarget)} onClose={() => setPreviewTarget(null)} />
            </div>
        );
    }

    return (
        <div className="flex h-full w-full flex-col overflow-hidden bg-(--background-w10)">
            <div className="flex shrink-0 items-center justify-between gap-3 px-5 py-4">
                <span className="min-w-0 truncate text-base font-semibold">Example {exampleNumber}</span>
                <div className="flex shrink-0 items-center gap-2">
                    {onOpenFullDetails && (
                        <Button variant="outline" size="sm" onClick={onOpenFullDetails}>
                            Open Full Details
                        </Button>
                    )}
                    <Tooltip>
                        <TooltipTrigger asChild>
                            <Button variant="ghost" size="icon" onClick={onClose} aria-label="Close">
                                <X className="size-5" />
                            </Button>
                        </TooltipTrigger>
                        <TooltipContent>Close</TooltipContent>
                    </Tooltip>
                </div>
            </div>

            <Separator />

            <div className="flex min-h-0 flex-1 flex-col gap-10 overflow-y-auto px-5 py-4">
                <section className="flex flex-col gap-2">
                    <h3 className="text-sm font-bold">Prompt</h3>
                    <div className="text-sm whitespace-pre-wrap text-(--primary-text-wMain)">{promptDisplay}</div>
                    {promptIsLong && (
                        <button type="button" onClick={() => setPromptExpanded(prev => !prev)} className="self-start text-xs font-bold text-(--primary-wMain) hover:underline">
                            {promptExpanded ? "Show Less" : "Show More"}
                        </button>
                    )}
                </section>

                {row.expectedResponse !== undefined && (
                    <section className="flex flex-col gap-2">
                        <h3 className="text-sm font-bold">Expected Response</h3>
                        <div className="text-sm whitespace-pre-wrap">{row.expectedResponse || "—"}</div>
                    </section>
                )}

                {inputArtifacts.size > 0 && <ExampleFilesSection entries={inputArtifacts} onPreview={entry => setPreviewTarget({ kind: entry.role, resultId: entry.resultId, artifact: entry.artifact })} />}

                <section className="flex flex-col gap-3">
                    <h3 className="text-sm font-bold">Responses</h3>

                    {!bannerDismissed && (
                        <div className="flex items-start gap-3 rounded-xs border-l-4 border-(--info-w100) bg-(--info-w10) p-3">
                            <Info className="mt-0.5 size-5 shrink-0 text-(--info-w100)" aria-hidden />
                            <p className="flex-1 text-sm text-(--info-w100)">
                                LLM-as-a-Judge evaluators can only evaluate text artifacts (markdown, JSON, CSV, etc.). Binary artifacts (images, PDFs, etc.) must be verified manually. Heuristic evaluators (Response Match, JSON Diff, etc.) cannot
                                evaluate any artifact type.
                            </p>
                            <Button variant="ghost" size="icon" onClick={() => setBannerDismissed(true)} aria-label="Dismiss">
                                <X className="size-4" />
                            </Button>
                        </div>
                    )}

                    <ResponsesTable
                        row={row}
                        models={models}
                        allModels={allModels}
                        aliasFor={aliasFor}
                        rawModelFor={rawModelFor}
                        evaluators={evaluators}
                        trialCount={trialCount}
                        expandedTrials={expandedTrials}
                        onToggleTrial={toggleTrial}
                        onPreviewArtifact={setPreviewTarget}
                    />
                </section>
            </div>
        </div>
    );
}

interface ResponsesTableProps {
    readonly row: ExampleRow;
    readonly models: string[];
    readonly allModels: string[];
    readonly aliasFor: (id: string) => string;
    readonly rawModelFor: (id: string) => string | undefined;
    readonly evaluators: InspectorEvaluator[];
    readonly trialCount: number;
    readonly expandedTrials: Set<number>;
    readonly onToggleTrial: (idx: number) => void;
    readonly onPreviewArtifact: (target: ArtifactPreviewTarget) => void;
}

function ResponsesTable({ row, models, allModels, aliasFor, rawModelFor, evaluators, trialCount, expandedTrials, onToggleTrial, onPreviewArtifact }: ResponsesTableProps) {
    const evCount = Math.max(1, evaluators.length);

    return (
        <div className="overflow-auto rounded-xs border">
            <Table className="w-full table-fixed">
                <TableHeader>
                    <TableRow className="hover:bg-transparent">
                        <TableHead rowSpan={2} className="w-12 border-r align-bottom" />
                        {models.map(m => (
                            <TableHead key={m} colSpan={evCount} className="overflow-hidden border-l-2 px-2 text-center font-semibold">
                                <div className="flex w-full min-w-0 items-center justify-center gap-2">
                                    <span className="size-2 shrink-0 rounded-full" style={{ backgroundColor: MODEL_COLORS[allModels.indexOf(m) % MODEL_COLORS.length] }} />
                                    <span className="min-w-0 truncate text-sm text-(--primary-text-wMain)" title={aliasFor(m)}>
                                        {aliasFor(m)}
                                    </span>
                                    {rawModelFor(m) && rawModelFor(m) !== aliasFor(m) && (
                                        <span className="min-w-0 truncate text-xs font-normal text-(--secondary-text-wMain)" title={rawModelFor(m)}>
                                            {rawModelFor(m)}
                                        </span>
                                    )}
                                </div>
                            </TableHead>
                        ))}
                    </TableRow>
                    <TableRow className="hover:bg-transparent">
                        {models.map(m =>
                            evaluators.length === 0 ? (
                                <TableHead key={`${m}-empty`} className="border-l-2 text-center font-normal text-(--secondary-text-wMain)">
                                    —
                                </TableHead>
                            ) : (
                                evaluators.map((ev, evIdx) => (
                                    <TableHead key={`${m}-${ev.id}`} className={cn("truncate text-center text-xs font-normal text-(--secondary-text-wMain)", evIdx === 0 ? "border-l-2" : "border-l")}>
                                        {ev.name}
                                    </TableHead>
                                ))
                            )
                        )}
                    </TableRow>
                </TableHeader>
                <TableBody borderLastRow>
                    {Array.from({ length: trialCount }).map((_, trialIdx) => (
                        <TrialRows key={trialIdx} trialIdx={trialIdx} row={row} models={models} evaluators={evaluators} expanded={expandedTrials.has(trialIdx)} onToggle={() => onToggleTrial(trialIdx)} onPreviewArtifact={onPreviewArtifact} />
                    ))}
                </TableBody>
            </Table>
        </div>
    );
}

interface TrialRowsProps {
    readonly trialIdx: number;
    readonly row: ExampleRow;
    readonly models: string[];
    readonly evaluators: InspectorEvaluator[];
    readonly expanded: boolean;
    readonly onToggle: () => void;
    readonly onPreviewArtifact: (target: ArtifactPreviewTarget) => void;
}

function TrialRows({ trialIdx, row, models, evaluators, expanded, onToggle, onPreviewArtifact }: TrialRowsProps) {
    const evCount = Math.max(1, evaluators.length);
    const perModelResult: Record<string, EvalRunResult | undefined> = {};
    for (const m of models) {
        perModelResult[m] = row.perModelTrials[m]?.[trialIdx];
    }
    const anyResult = Object.values(perModelResult).some(r => r !== undefined);
    const interactive = anyResult;

    return (
        <>
            <TableRow className={cn("align-middle", interactive && "cursor-pointer hover:bg-(--stateLayer-w10)", !interactive && "opacity-60")} onClick={interactive ? onToggle : undefined}>
                <TableCell className="border-r pl-3 text-left font-mono text-xs text-(--secondary-text-wMain)">
                    <span className="inline-flex items-center gap-1">
                        {interactive && (expanded ? <ChevronDown className="size-3.5" aria-hidden /> : <ChevronRight className="size-3.5" aria-hidden />)}
                        {trialIdx + 1}
                    </span>
                </TableCell>
                {models.map(m => {
                    const result = perModelResult[m];
                    const problem = trialProblem(result);
                    const hasError = problem === "error";
                    if (evaluators.length === 0) {
                        return (
                            <TableCell key={m} className="border-l-2 text-center text-xs text-(--secondary-text-wMain)">
                                {result ? "—" : "…"}
                            </TableCell>
                        );
                    }
                    return evaluators.map((ev, evIdx) => {
                        const score = result?.scores?.find(s => s.evaluatorId === ev.id);
                        const scoreVal = score?.score;
                        const cell: ScoreCellState = cellStateOf(result, scoreVal, problem);
                        return (
                            <TableCell
                                key={`${m}-${ev.id}`}
                                className={cn("text-center text-sm", evIdx === 0 ? "border-l-2" : "border-l", !result && "opacity-40", hasError && "bg-(--error-wMain)/5", !hasError && scoreVal != null && scoreBgClass(scoreVal))}
                            >
                                <ScoreCellContent cell={cell} />
                            </TableCell>
                        );
                    });
                })}
            </TableRow>

            {expanded && interactive && (
                <TableRow className="hover:bg-transparent">
                    <TableCell className="border-r" />
                    {models.map(m => (
                        <TableCell key={`detail-${m}`} colSpan={evCount} className="overflow-hidden border-l-2 p-0 align-top">
                            <ModelResponseCell result={perModelResult[m]} evaluators={evaluators} onPreviewArtifact={onPreviewArtifact} />
                        </TableCell>
                    ))}
                </TableRow>
            )}
        </>
    );
}

interface ModelResponseCellProps {
    readonly result: EvalRunResult | undefined;
    readonly evaluators: InspectorEvaluator[];
    readonly onPreviewArtifact: (target: ArtifactPreviewTarget) => void;
}

function ModelResponseCell({ result, evaluators, onPreviewArtifact }: ModelResponseCellProps) {
    const judgeEvaluators = useMemo(() => evaluators.filter(explainsItsScore), [evaluators]);
    if (!result) {
        return <div className="px-4 py-3 text-sm text-(--secondary-text-wMain)">No result for this trial.</div>;
    }

    return (
        <div className="flex w-full min-w-0 flex-col gap-3 px-4 py-3">
            <TrialResponse result={result} inset={false} />

            {judgeEvaluators.length > 0 && result.scores.length > 0 && (
                <div className="flex w-full min-w-0 flex-col gap-1.5 pt-2">
                    <div className="text-xs font-bold tracking-wide break-words whitespace-normal text-(--secondary-text-wMain) uppercase">{reasoningHeading(judgeEvaluators)}</div>
                    <div className="flex w-full min-w-0 flex-col divide-y rounded-xs border bg-(--background-w10)">
                        {judgeEvaluators.map(ev => {
                            const score = result.scores.find(s => s.evaluatorId === ev.id);
                            if (!score) return null;
                            return <ReasoningRow key={ev.id} name={ev.name} reasoning={score.reasoning} details={score.details} scoring={ev.scoring} />;
                        })}
                    </div>
                </div>
            )}

            {result.artifacts && result.artifacts.length > 0 && (
                <div className="flex w-full min-w-0 flex-col gap-1.5 pt-2">
                    <div className="text-xs font-bold tracking-wide break-words whitespace-normal text-(--secondary-text-wMain) uppercase">Artifacts</div>
                    <ArtifactsSection artifacts={result.artifacts} fetchBlob={a => evaluationsApi.getRunResultArtifactBlob(result.id, a.filename, a.version)} onPreview={a => onPreviewArtifact({ kind: "output", resultId: result.id, artifact: a })} />
                </div>
            )}
        </div>
    );
}
