// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Network, X } from "lucide-react";
import { useLoaderData, useNavigate } from "react-router-dom";

import { evaluationKeys, evaluationsApi } from "@/api/evaluations";

import { PageContentWrapper, PageSection } from "@/common/layout";

import { cn } from "@/utils/cnTailwind";

import { Button } from "@/ui/button";
import { Separator } from "@/ui/separator";
import { type TaskFE } from "@/types/chat";
import type { EvalArtifact, EvalArtifactRole, EvalExperiment, EvalRunResult } from "@/types/eval";
import { type A2AEventSSEPayload } from "@/types/sse";

import FlowChartPanel from "@/features/activities/flowChart/FlowChartPanel";
import { processTaskForVisualization } from "@/features/activities/taskVisualizerProcessor";

import { EmptyState, Header, PageLayout } from "@/common";

import {
    ArtifactPreviewView,
    buildExampleRows,
    ExampleFilesSection,
    type ExampleRow,
    explainsItsScore,
    type InspectorEvaluator,
    knownEvaluatorType,
    reasoningHeading,
    ReasoningRow,
    ResizableSidePanel,
    ScoreCellContent,
    type ScoreCellState,
    TrialResponse,
} from "./components";
import { EVAL_ROUTES } from "./constants";
import { useEvalModelAxis, useEvalRunGroup, useEvalRunGroupResults, useResizableSidePanel } from "./hooks";
import { cellStateOf, collectExampleArtifacts, createExperimentModelLookup, type ExampleArtifactEntry, isWorkflowRow, MODEL_COLORS, scoreBgClass, splitWorkflowPrompt, trialProblem } from "./utils";

const PROMPT_PREVIEW_CHARS = 320;

type SidePanelTarget = { kind: "activity"; result: EvalRunResult; trialIdx: number; modelAlias: string } | { kind: "preview"; artifact: EvalArtifact; role: EvalArtifactRole; resultId: string };

function gridColsClassForModelCount(count: number): string {
    if (count <= 1) return "grid-cols-1";
    if (count === 2) return "grid-cols-1 lg:grid-cols-2";
    if (count === 3) return "grid-cols-1 lg:grid-cols-3";
    return "grid-cols-1 lg:grid-cols-2 xl:grid-cols-4";
}

function deriveScoreCellState(result: EvalRunResult | undefined, val: number | undefined): ScoreCellState {
    return cellStateOf(result, val, trialProblem(result));
}

export function EvalExampleDetailPage() {
    const { groupId, exampleKey } = useLoaderData() as { groupId?: string; exampleKey?: string };
    const navigate = useNavigate();

    const { group, loading: groupLoading, isActive } = useEvalRunGroup(groupId);
    const { results, loading: resultsLoading } = useEvalRunGroupResults(groupId, isActive);

    const [experiment, setExperiment] = useState<EvalExperiment | null>(null);
    const [sidePanel, setSidePanel] = useState<SidePanelTarget | null>(null);

    const {
        containerRef,
        width: sidePanelWidth,
        handleDragStart,
    } = useResizableSidePanel(!!sidePanel, {
        minWidth: 480,
        defaultFraction: 0.5,
        maxFraction: 0.8,
    });

    useEffect(() => {
        if (!group) return;
        evaluationsApi
            .getExperiment(group.experimentId)
            .then(setExperiment)
            .catch(() => setExperiment(null));
    }, [group]);

    const { effectiveModels, allModels } = useEvalModelAxis(experiment, group);

    const evaluators = useMemo<InspectorEvaluator[]>(() => {
        if (!group) return [];
        const seen = new Map<string, InspectorEvaluator>();
        for (const run of group.runs) {
            for (const snap of run.evaluatorSnapshot ?? []) {
                const { id, name, type: rawType } = snap;
                if (!id || !name || seen.has(id)) continue;
                seen.set(id, { id, name, type: knownEvaluatorType(rawType), heuristicType: snap.heuristic_type, scoring: snap.decision_config?.scoring });
            }
        }
        return Array.from(seen.values());
    }, [group]);

    const { row, exampleNumber } = useMemo(() => {
        if (!results || !exampleKey) return { row: null as ExampleRow | null, exampleNumber: 0 };
        const rows = buildExampleRows(results.resultsByModel, allModels);
        const idx = rows.findIndex(r => r.key === exampleKey);
        return { row: idx >= 0 ? rows[idx] : null, exampleNumber: idx + 1 };
    }, [results, allModels, exampleKey]);

    const trialCount = useMemo(() => {
        if (!row) return 0;
        let max = 0;
        for (const m of allModels) {
            const trials = row.perModelTrials[m] ?? [];
            if (trials.length > max) max = trials.length;
        }
        return max;
    }, [row, allModels]);

    const reportLabel = experiment ? `Experiment Report: ${experiment.name}` : "Experiment Report";
    const breadcrumbs = group
        ? [
              {
                  label: "Reports",
                  onClick: () => {
                      navigate(EVAL_ROUTES.REPORTS);
                  },
              },
              {
                  label: reportLabel,
                  onClick: () => {
                      navigate(EVAL_ROUTES.runGroup(group.id));
                  },
              },
              { label: `Example ${exampleNumber}` },
          ]
        : [
              {
                  label: "Reports",
                  onClick: () => {
                      navigate(EVAL_ROUTES.REPORTS);
                  },
              },
              { label: `Example ${exampleNumber}` },
          ];

    const title = exampleNumber > 0 ? `Example ${exampleNumber}` : "Example";

    const inputArtifacts = useMemo(() => (row ? collectExampleArtifacts(row, allModels) : new Map()), [row, allModels]);

    if (groupLoading || resultsLoading) {
        return (
            <PageLayout>
                <Header title={title} breadcrumbs={breadcrumbs} />
                <EmptyState variant="loading" title="Loading…" />
            </PageLayout>
        );
    }

    if (!group || !row) {
        return (
            <PageLayout>
                <Header title={title} breadcrumbs={breadcrumbs} />
                <EmptyState
                    variant="error"
                    title="Example not found"
                    buttons={[
                        {
                            text: group ? "Back to Report" : "Back to Reports",
                            variant: "default",
                            onClick: () => navigate(group ? EVAL_ROUTES.runGroup(group.id) : EVAL_ROUTES.REPORTS),
                        },
                    ]}
                />
            </PageLayout>
        );
    }

    const { aliasFor } = createExperimentModelLookup(effectiveModels);
    const handleOpenActivity = (result: EvalRunResult, trialIdx: number, model: string) => {
        setSidePanel({ kind: "activity", result, trialIdx, modelAlias: aliasFor(model) });
    };
    const handlePreviewInputFile = (entry: ExampleArtifactEntry) => {
        setSidePanel({ kind: "preview", artifact: entry.artifact, role: entry.role, resultId: entry.resultId });
    };

    return (
        <PageLayout>
            <Header title={title} breadcrumbs={breadcrumbs} />
            <div ref={containerRef} className="relative flex-1 overflow-hidden">
                <div className="flex h-full flex-col">
                    <PageContentWrapper className="p-6" innerClassName="max-w-[1200px] gap-6">
                        {isWorkflowRow(row) ? <WorkflowInputSection prompt={row.prompt} /> : <PromptSection prompt={row.prompt} />}
                        {inputArtifacts.size > 0 && <ExampleFilesSection entries={inputArtifacts} onPreview={handlePreviewInputFile} />}
                        {row.expectedResponse !== undefined && <ExpectedResponseSection expectedResponse={row.expectedResponse} />}
                        <ResponsesSection row={row} models={allModels} experimentModels={effectiveModels} evaluators={evaluators} trialCount={trialCount} onOpenActivity={handleOpenActivity} />
                    </PageContentWrapper>
                </div>

                <ResizableSidePanel open={!!sidePanel} width={sidePanelWidth} onDragStart={handleDragStart}>
                    {sidePanel?.kind === "activity" && (
                        <ActivityPanel resultId={sidePanel.result.id} modelAlias={sidePanel.modelAlias} trialIdx={sidePanel.trialIdx} initialRequestText={sidePanel.result.inputPrompt ?? ""} onClose={() => setSidePanel(null)} />
                    )}
                    {sidePanel?.kind === "preview" && (
                        <ArtifactPreviewView
                            artifact={sidePanel.artifact}
                            fetchBlob={() => evaluationsApi.getRunResultExampleArtifactBlob(sidePanel.resultId, sidePanel.role, sidePanel.artifact.filename, sidePanel.artifact.version)}
                            cacheKey={evaluationKeys.runResultExampleArtifactBlob(sidePanel.resultId, sidePanel.role, sidePanel.artifact.filename, sidePanel.artifact.version)}
                            onClose={() => setSidePanel(null)}
                        />
                    )}
                </ResizableSidePanel>
            </div>
        </PageLayout>
    );
}

function PromptSection({ prompt }: { readonly prompt: string }) {
    const [expanded, setExpanded] = useState(false);
    const isLong = prompt.length > PROMPT_PREVIEW_CHARS;
    const display = !isLong || expanded ? prompt : prompt.slice(0, PROMPT_PREVIEW_CHARS).trimEnd() + "…";
    return (
        <PageSection>
            <h3 className="text-sm font-bold">Prompt</h3>
            <div className="text-sm whitespace-pre-wrap text-(--primary-text-wMain)">{display}</div>
            {isLong && (
                <button type="button" onClick={() => setExpanded(prev => !prev)} className="self-start text-xs font-bold text-(--primary-wMain) hover:underline">
                    {expanded ? "Show Less" : "Show More"}
                </button>
            )}
        </PageSection>
    );
}

function WorkflowInputSection({ prompt }: { readonly prompt: string }) {
    const { description, input } = splitWorkflowPrompt(prompt);
    return (
        <>
            {input && (
                <PageSection>
                    <h3 className="text-sm font-bold">Input Data</h3>
                    <div className="font-mono text-sm break-all whitespace-pre-wrap text-(--primary-text-wMain)">{input}</div>
                </PageSection>
            )}
            {description && (
                <PageSection>
                    <h3 className="text-sm font-bold">Evaluator Instructions</h3>
                    <div className="text-sm whitespace-pre-wrap text-(--primary-text-wMain)">{description}</div>
                </PageSection>
            )}
        </>
    );
}

function ExpectedResponseSection({ expectedResponse }: { readonly expectedResponse: string }) {
    return (
        <PageSection>
            <h3 className="text-sm font-bold">Expected Response</h3>
            <div className="text-sm whitespace-pre-wrap text-(--secondary-text-wMain)">{expectedResponse || "—"}</div>
        </PageSection>
    );
}

interface ResponsesSectionProps {
    readonly row: ExampleRow;
    readonly models: string[];
    readonly experimentModels?: EvalExperiment["models"];
    readonly evaluators: InspectorEvaluator[];
    readonly trialCount: number;
    readonly onOpenActivity: (result: EvalRunResult, trialIdx: number, model: string) => void;
}

function ResponsesSection({ row, models, experimentModels, evaluators, trialCount, onOpenActivity }: ResponsesSectionProps) {
    return (
        <section className="flex flex-col gap-4">
            <h3 className="text-sm font-bold">Responses</h3>
            {trialCount === 0 ? (
                <div className="rounded-xs border bg-(--background-w10) p-4 text-sm text-(--secondary-text-wMain)">No responses yet.</div>
            ) : (
                Array.from({ length: trialCount }).map((_, trialIdx) => (
                    <RunCard key={`${row.key}-trial-${trialIdx}`} trialIdx={trialIdx} row={row} models={models} experimentModels={experimentModels} evaluators={evaluators} onOpenActivity={onOpenActivity} />
                ))
            )}
        </section>
    );
}

interface RunCardProps {
    readonly trialIdx: number;
    readonly row: ExampleRow;
    readonly models: string[];
    readonly experimentModels?: EvalExperiment["models"];
    readonly evaluators: InspectorEvaluator[];
    readonly onOpenActivity: (result: EvalRunResult, trialIdx: number, model: string) => void;
}

function RunCard({ trialIdx, row, models, experimentModels, evaluators, onOpenActivity }: RunCardProps) {
    const { aliasFor, rawModelFor } = createExperimentModelLookup(experimentModels);
    return (
        <div className="overflow-hidden rounded-xs border bg-(--background-w10)">
            <div className="border-b px-4 py-2 text-sm font-semibold">Run {trialIdx + 1}</div>
            <div className={cn("grid", gridColsClassForModelCount(models.length))}>
                {models.map((model, modelIdx) => {
                    const result = row.perModelTrials[model]?.[trialIdx];
                    return (
                        <ModelColumn
                            key={model}
                            model={model}
                            modelIdx={modelIdx}
                            result={result}
                            evaluators={evaluators}
                            aliasFor={aliasFor}
                            rawModelFor={rawModelFor}
                            isLastColumn={modelIdx === models.length - 1}
                            onClickActivity={result ? () => onOpenActivity(result, trialIdx, model) : undefined}
                        />
                    );
                })}
            </div>
        </div>
    );
}

interface ModelColumnProps {
    readonly model: string;
    readonly modelIdx: number;
    readonly result: EvalRunResult | undefined;
    readonly evaluators: InspectorEvaluator[];
    readonly aliasFor: (id: string) => string;
    readonly rawModelFor: (id: string) => string | undefined;
    readonly isLastColumn: boolean;
    readonly onClickActivity?: () => void;
}

function ModelColumn({ model, modelIdx, result, evaluators, aliasFor, rawModelFor, isLastColumn, onClickActivity }: ModelColumnProps) {
    const judgeEvaluators = evaluators.filter(explainsItsScore);
    return (
        <div className={cn("flex min-w-0 flex-col", !isLastColumn && "lg:border-r", modelIdx > 0 && "border-t lg:border-t-0")}>
            <ModelHeader model={model} modelIdx={modelIdx} aliasFor={aliasFor} rawModelFor={rawModelFor} onClickActivity={onClickActivity} />
            <ScoreRow result={result} evaluators={evaluators} />
            <TrialResponse result={result} />
            {judgeEvaluators.length > 0 && (result?.scores.length ?? 0) > 0 && (
                <div className="flex flex-col gap-1.5 px-4 py-3">
                    <div className="text-xs text-(--secondary-text-wMain)">{reasoningHeading(judgeEvaluators)}</div>
                    <div className="flex flex-col divide-y rounded-xs border bg-(--background-w20)">
                        {judgeEvaluators.map(ev => {
                            const score = result?.scores.find(s => s.evaluatorId === ev.id);
                            if (!score) return null;
                            return <ReasoningRow key={ev.id} name={ev.name} reasoning={score.reasoning} details={score.details} scoring={ev.scoring} />;
                        })}
                    </div>
                </div>
            )}
        </div>
    );
}

interface ModelHeaderProps {
    readonly model: string;
    readonly modelIdx: number;
    readonly aliasFor: (id: string) => string;
    readonly rawModelFor: (id: string) => string | undefined;
    readonly onClickActivity?: () => void;
}

function ModelHeader({ model, modelIdx, aliasFor, rawModelFor, onClickActivity }: ModelHeaderProps) {
    const alias = aliasFor(model);
    const raw = rawModelFor(model);
    return (
        <div className="flex items-center gap-2 border-b bg-(--background-w20) px-4 py-2">
            <span className="size-2 shrink-0 rounded-full" style={{ backgroundColor: MODEL_COLORS[modelIdx % MODEL_COLORS.length] }} />
            <span className="min-w-0 truncate text-sm font-semibold text-(--primary-text-wMain)" title={alias}>
                {alias}
            </span>
            {raw && (
                <span className="min-w-0 truncate text-xs text-(--secondary-text-wMain)" title={raw}>
                    {raw}
                </span>
            )}
            <button
                type="button"
                onClick={onClickActivity}
                disabled={!onClickActivity}
                title="Show Activity"
                aria-label="Show Activity"
                className={cn("ml-auto inline-flex shrink-0 items-center justify-center rounded-xs p-1", onClickActivity ? "text-(--primary-wMain) hover:bg-(--stateLayer-w10)" : "cursor-default text-(--secondary-text-wMain) opacity-40")}
            >
                <Network className="size-4" aria-hidden />
            </button>
        </div>
    );
}

function ScoreRow({ result, evaluators }: { readonly result: EvalRunResult | undefined; readonly evaluators: InspectorEvaluator[] }) {
    if (evaluators.length === 0) {
        return <div className="px-4 py-3 text-xs text-(--secondary-text-wMain)">{result ? "No evaluators configured." : "Pending…"}</div>;
    }
    return (
        <div className="grid border-b" style={{ gridTemplateColumns: `repeat(${evaluators.length}, minmax(0, 1fr))` }}>
            {evaluators.map((ev, idx) => (
                <div key={`${ev.id}-name`} className={cn("px-3 py-2 text-center text-xs text-(--secondary-text-wMain)", idx > 0 && "border-l")}>
                    <span className="block truncate" title={ev.name}>
                        {ev.name}
                    </span>
                </div>
            ))}
            {evaluators.map((ev, idx) => {
                const score = result?.scores.find(s => s.evaluatorId === ev.id);
                const val = score?.score;
                const hasError = trialProblem(result) === "error";
                const cell = deriveScoreCellState(result, val);
                return (
                    <div key={`${ev.id}-score`} className={cn("border-t px-3 py-2 text-center text-sm", idx > 0 && "border-l", !result && "opacity-40", hasError && "bg-(--error-wMain)/5", !hasError && val != null && scoreBgClass(val))}>
                        <ScoreCellContent cell={cell} />
                    </div>
                );
            })}
        </div>
    );
}

interface ActivityPanelProps {
    readonly resultId: string;
    readonly modelAlias: string;
    readonly trialIdx: number;
    readonly initialRequestText: string;
    readonly onClose: () => void;
}

function ActivityPanel({ resultId, modelAlias, trialIdx, initialRequestText, onClose }: ActivityPanelProps) {
    const { data, isLoading, isError, refetch } = useQuery<A2AEventSSEPayload[] | null>({
        queryKey: ["evalRunResultTaskEvents", resultId],
        queryFn: () => evaluationsApi.getRunResultTaskEvents(resultId),
        retry: false,
    });

    const visualizedSteps = useMemo(() => {
        if (!data || data.length === 0) return [];
        const taskId = data.find(e => e.task_id)?.task_id ?? resultId;
        const firstSeen = new Date(data[0]?.timestamp ?? Date.now());
        const lastUpdated = new Date(data[data.length - 1]?.timestamp ?? Date.now());
        const taskFE: TaskFE = {
            taskId,
            initialRequestText,
            events: data,
            firstSeen,
            lastUpdated,
            parentTaskId: null,
        };
        const viz = processTaskForVisualization(data, { [taskId]: taskFE }, taskFE);
        return viz?.steps ?? [];
    }, [data, resultId, initialRequestText]);

    const renderBody = () => {
        if (isLoading) {
            return <EmptyState variant="loading" title="Loading activity…" />;
        }
        if (isError) {
            return <EmptyState variant="error" title="Failed to load activity" buttons={[{ text: "Retry", variant: "default", onClick: () => void refetch() }]} />;
        }
        if (!data || data.length === 0) {
            return <EmptyState variant="notFound" title="Activity diagram unavailable" subtitle="No task events were captured for this result." />;
        }
        return <FlowChartPanel processedSteps={visualizedSteps} isRightPanelVisible={false} />;
    };

    const isDiagram = !isLoading && !isError && data && data.length > 0;

    return (
        <div className="flex h-full w-full flex-col overflow-hidden bg-(--background-w10)">
            <div className="flex shrink-0 items-center justify-between gap-3 px-5 py-4">
                <span className="min-w-0 truncate text-base font-semibold">
                    Activity: Run {trialIdx + 1} – {modelAlias}
                </span>
                <Button variant="ghost" size="icon" onClick={onClose} aria-label="Close">
                    <X className="size-5" />
                </Button>
            </div>
            <Separator />
            <div className="flex min-h-0 flex-1 flex-col">{isDiagram ? renderBody() : <div className="flex flex-1 items-center justify-center p-6">{renderBody()}</div>}</div>
        </div>
    );
}
