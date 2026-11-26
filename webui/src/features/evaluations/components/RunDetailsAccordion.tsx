// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { Fragment, useMemo, useState } from "react";
import { BookOpenText, Bot, ChevronRight, Clock, Cpu, Database, Plug, Scale, Sliders, Sparkles, Wrench } from "lucide-react";

import { useModelConfigs } from "@/api/models/hooks";

import { cn } from "@/utils/cnTailwind";
import { formatDuration } from "@/utils/format";

import { Accordion, AccordionContent, AccordionItem, AccordionTrigger } from "@/ui/accordion";
import { Badge } from "@/ui/badge";
import { Button } from "@/ui/button";
import { Dialog, DialogClose, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@/ui/dialog";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/ui/tooltip";
import type { AgentSnapshot, ConfigSnapshotDataset, ConfigSnapshotExampleArtifact, ConfigSnapshotExperiment, EvalRunGroup, EvaluatorSnapshotEntry, ExperimentModelSnapshotItem, ModelConfigSnapshot } from "@/types/eval";

import { ModelConfigIcon } from "@/features/activation";

import { type DatasetExampleRow, DatasetExamplesTable } from "./DatasetExamplesTable";
import { DecisionEvaluatorSummary } from "./DecisionEvaluatorSummary";
import { decisionSettingsFromSnapshot } from "./evaluatorForm";
import { ExamplesPaginationFooter } from "./ExamplesPaginationFooter";

interface RunDetailsAccordionProps {
    readonly group: EvalRunGroup;
}

export interface ModelRowItem {
    modelConfigId?: string;
    alias?: string;
    model?: string;
    provider?: string;
    snapshot?: ModelConfigSnapshot | null;
}

type DetailTarget = { kind: "dataset"; data: ConfigSnapshotDataset } | { kind: "evaluator"; data: EvaluatorSnapshotEntry } | { kind: "model"; data: ModelRowItem } | { kind: "agent"; data: AgentSnapshot };

const EVALUATOR_TYPE_LABELS: Record<string, string> = {
    llm_judge: "LLM as a Judge",
    heuristic: "Heuristic",
    decision: "Decision Model as a Judge",
};

function FieldLabel({ children }: { children: React.ReactNode }) {
    return <div className="text-xs text-(--secondary-text-wMain)">{children}</div>;
}

function inferProvider(model: string | undefined): string {
    if (!model) return "";
    const lower = model.toLowerCase();
    if (lower.includes("claude")) return "anthropic";
    if (lower.includes("gpt") || lower.includes("o1") || lower.includes("o3")) return "openai";
    if (lower.includes("gemini")) return "google";
    if (lower.includes("llama")) return "meta";
    if (lower.includes("mistral")) return "mistral";
    return "";
}

function useLiveModelConfig(modelConfigId: string | undefined) {
    const { data: modelConfigs = [] } = useModelConfigs();
    return modelConfigs.find(mc => mc.id === modelConfigId);
}

function modelRowFromSnapshot(snapshot: ModelConfigSnapshot | null | undefined): ModelRowItem | null {
    if (!snapshot) return null;
    return {
        modelConfigId: snapshot.id,
        alias: snapshot.alias ?? snapshot.modelName ?? undefined,
        model: snapshot.modelName ?? undefined,
        provider: snapshot.provider ?? undefined,
        snapshot,
    };
}

export function RunDetailsAccordion({ group }: RunDetailsAccordionProps) {
    const [detailTarget, setDetailTarget] = useState<DetailTarget | null>(null);
    const { data: modelConfigs = [] } = useModelConfigs();

    const first = group.runs[0];
    const configSnapshot = first?.configSnapshot;
    const evaluatorSnaps: EvaluatorSnapshotEntry[] = first?.evaluatorSnapshot ?? [];

    const datasetSnap = configSnapshot?.dataset ?? null;
    const agentSnap = configSnapshot?.agent ?? null;
    const experimentSnap: ConfigSnapshotExperiment = configSnapshot?.experiment ?? {};

    const modelsInRun = useMemo<ModelRowItem[]>(() => {
        const snapshotItems: ExperimentModelSnapshotItem[] = configSnapshot?.experiment?.models ?? [];
        return group.runs
            .filter(r => r.model)
            .map(r => {
                const live = modelConfigs.find(mc => mc.id === r.model);
                const snapMatch = snapshotItems.find(m => m.modelConfigId === r.model);
                const snapshot = snapMatch?.modelConfig ?? null;
                return {
                    modelConfigId: r.model,
                    alias: live?.alias ?? snapMatch?.alias ?? snapshot?.alias ?? r.model,
                    model: live?.modelName ?? snapMatch?.model ?? snapshot?.modelName ?? r.model,
                    provider: live?.provider ?? snapshot?.provider ?? inferProvider(snapMatch?.model),
                    snapshot,
                };
            });
    }, [group.runs, modelConfigs, configSnapshot]);

    const completedAt = group.completedAt ?? null;
    const duration = completedAt ? formatDuration(new Date(completedAt).getTime() - new Date(group.startedAt).getTime()) : "—";
    const completedOnText = completedAt ? new Date(completedAt).toLocaleString() : "—";

    const titleLabel = experimentSnap.name ?? group.runName ?? "Run";
    const description = experimentSnap.description ?? "";
    const agentName = agentSnap?.name ?? "—";
    const workflowSnap = configSnapshot?.workflow ?? null;
    const datasetName = datasetSnap?.name ?? (typeof experimentSnap.datasetId === "string" ? experimentSnap.datasetId : "—");

    return (
        <>
            <Accordion type="single" collapsible className="rounded-xs border">
                <AccordionItem value="run-details" className="border-none">
                    <AccordionTrigger className="group px-4 py-3 hover:no-underline [&>svg]:hidden">
                        <div className="flex w-full items-start gap-2 text-left">
                            <ChevronRight className="mt-0.5 size-4 shrink-0 transition-transform group-data-[state=open]:rotate-90" />
                            <div className="flex flex-col items-start gap-1">
                                <div className="flex flex-wrap items-center gap-2 text-sm font-semibold">
                                    <span>Report Details:</span>
                                    <span className="font-bold text-(--primary-wMain)">{titleLabel}</span>
                                    {group.configOutdated && (
                                        <Tooltip>
                                            <TooltipTrigger asChild>
                                                <span className="inline-flex items-center gap-1.5 text-xs font-normal text-(--secondary-text-wMain)">
                                                    <Clock className="size-3.5" />
                                                    This run uses an older version of this experiment.
                                                </span>
                                            </TooltipTrigger>
                                            <TooltipContent>The experiment configuration has been edited since this run was executed.</TooltipContent>
                                        </Tooltip>
                                    )}
                                </div>
                                {description && <div className="text-xs font-normal text-(--secondary-text-wMain)">{description}</div>}
                            </div>
                        </div>
                    </AccordionTrigger>
                    <AccordionContent className="border-t px-4 py-4">
                        <div className="flex flex-wrap gap-x-10 gap-y-4">
                            {workflowSnap ? (
                                <div className="flex flex-col gap-1" data-testid="runDetailsWorkflow">
                                    <FieldLabel>Workflow</FieldLabel>
                                    <span className="text-sm font-bold">{workflowSnap.name}</span>
                                </div>
                            ) : (
                                <div className="flex flex-col gap-1">
                                    <FieldLabel>Agent</FieldLabel>
                                    <Button variant="link" dense className="self-start text-sm font-bold" onClick={() => setDetailTarget({ kind: "agent", data: agentSnap ?? { name: agentName } })}>
                                        {agentName}
                                    </Button>
                                </div>
                            )}
                            <div className="flex flex-col gap-1">
                                <FieldLabel>Dataset</FieldLabel>
                                {datasetSnap ? (
                                    <Button variant="link" dense className="self-start text-sm font-bold" onClick={() => setDetailTarget({ kind: "dataset", data: datasetSnap })}>
                                        {datasetName}
                                    </Button>
                                ) : (
                                    <span className="text-sm text-(--secondary-text-wMain)">{datasetName}</span>
                                )}
                            </div>
                            <div className="flex flex-col gap-1">
                                <FieldLabel>Models</FieldLabel>
                                <div className="flex flex-col gap-1.5">
                                    {modelsInRun.length === 0 ? (
                                        <span className="text-sm text-(--secondary-text-wMain)">—</span>
                                    ) : (
                                        modelsInRun.map((m, i) => <ModelRow key={`${m.modelConfigId}-${i}`} model={m} onClick={() => setDetailTarget({ kind: "model", data: m })} />)
                                    )}
                                </div>
                            </div>
                            <div className="flex flex-col gap-1">
                                <FieldLabel>Evaluators</FieldLabel>
                                <div className="flex flex-col gap-1.5">
                                    {evaluatorSnaps.length === 0 ? (
                                        <span className="text-sm text-(--secondary-text-wMain)">—</span>
                                    ) : (
                                        evaluatorSnaps.map((ev, i) => (
                                            <Button key={ev.id ?? i} variant="link" dense className="self-start text-sm font-bold" onClick={() => setDetailTarget({ kind: "evaluator", data: ev })}>
                                                {ev.name ?? "Evaluator"}
                                            </Button>
                                        ))
                                    )}
                                </div>
                            </div>
                            <div className="flex flex-col gap-1">
                                <FieldLabel>Completed On</FieldLabel>
                                <span className="text-sm">{completedOnText}</span>
                            </div>
                            <div className="flex flex-col gap-1">
                                <FieldLabel>Experiment Duration</FieldLabel>
                                <span className="text-sm">{duration}</span>
                            </div>
                        </div>
                    </AccordionContent>
                </AccordionItem>
            </Accordion>

            <DetailDialog
                target={detailTarget}
                onClose={() => setDetailTarget(null)}
                onOpenModel={snapshot => {
                    const row = modelRowFromSnapshot(snapshot);
                    if (row) setDetailTarget({ kind: "model", data: row });
                }}
            />
        </>
    );
}

function ModelRow({ model, onClick, surface }: { model: ModelRowItem; onClick: () => void; surface?: boolean }) {
    const liveModel = useLiveModelConfig(model.modelConfigId);
    const button = (
        <div className="flex items-center gap-2 text-left">
            <ModelConfigIcon model={liveModel} storedProvider={model.provider} size="xs" />
            <Button variant="link" dense className="text-sm font-bold" onClick={onClick}>
                {model.alias}
            </Button>
            {model.alias !== model.model && <span className="text-sm text-(--secondary-text-wMain)">{model.model}</span>}
        </div>
    );
    return surface ? <div className="rounded-sm bg-(--secondary-w10) p-3">{button}</div> : button;
}

function DetailDialog({ target, onClose, onOpenModel }: { target: DetailTarget | null; onClose: () => void; onOpenModel: (snapshot: ModelConfigSnapshot) => void }) {
    const isOpen = target !== null;

    let title = "";
    let body: React.ReactNode = null;

    if (target?.kind === "dataset") {
        title = "Dataset Preview";
        body = <DatasetDetailBody snapshot={target.data} onClose={onClose} />;
    } else if (target?.kind === "evaluator") {
        title = target.data.name ?? "Evaluator";
        body = <EvaluatorDetailBody evaluator={target.data} onOpenModel={onOpenModel} onClose={onClose} />;
    } else if (target?.kind === "model") {
        title = "Model";
        body = <ModelDetailBody model={target.data} onClose={onClose} />;
    } else if (target?.kind === "agent") {
        title = target.data.name ?? "Agent";
        body = <AgentDetailBody agent={target.data} onOpenModel={onOpenModel} onClose={onClose} />;
    }

    return (
        <Dialog
            open={isOpen}
            onOpenChange={open => {
                if (!open) onClose();
            }}
        >
            <DialogContent className="w-200! max-w-[90vw]! p-0">
                <DialogHeader className="sr-only">
                    <DialogTitle>{title}</DialogTitle>
                </DialogHeader>
                {body}
            </DialogContent>
        </Dialog>
    );
}

function DetailDialogShell({ title, subtitle, headerLeading, headerBadges, children }: { title: string; subtitle?: string; headerLeading?: React.ReactNode; headerBadges?: React.ReactNode; children: React.ReactNode }) {
    return (
        <div className="flex max-h-[80vh] flex-col">
            <div className="flex shrink-0 items-start gap-3 px-5 py-4">
                <div className="flex min-w-0 flex-1 flex-col gap-1">
                    <div className="flex min-w-0 flex-wrap items-center gap-2 text-base font-semibold">
                        {headerLeading}
                        <span className="truncate">{title}</span>
                        {headerBadges}
                    </div>
                    {subtitle && <div className="text-sm text-(--secondary-text-wMain)">{subtitle}</div>}
                </div>
            </div>
            <div className="min-h-0 flex-1 overflow-y-auto px-5 py-5">{children}</div>
            <DialogFooter className="px-5 py-3">
                <DialogClose asChild>
                    <Button variant="outline">Close</Button>
                </DialogClose>
            </DialogFooter>
        </div>
    );
}

function Section({ icon: Icon, title, children }: { icon: typeof Bot; title: string; children: React.ReactNode }) {
    return (
        <div className="mt-5 first:mt-0">
            <div className="mb-2 flex items-center gap-2 text-sm font-semibold">
                <Icon className="size-4" />
                {title}
            </div>
            <div>{children}</div>
        </div>
    );
}

function ChipList({ items }: { items: string[] }) {
    return (
        <div className="flex flex-wrap gap-2">
            {items.map(item => (
                <Badge key={item} variant="outline" className="bg-(--secondary-w10) px-3 py-1.5 text-sm font-normal">
                    {item}
                </Badge>
            ))}
        </div>
    );
}

function CardItem({ title, subtitle }: { title: string; subtitle?: string }) {
    return (
        <div className="rounded-xs border p-3">
            <div className="text-sm font-semibold">{title}</div>
            {subtitle && <div className="mt-1 text-xs text-(--secondary-text-wMain)">{subtitle}</div>}
        </div>
    );
}

function CollapsibleCardList({ items, initialCount = 3 }: { items: { id?: string; name: string; description?: string }[]; initialCount?: number }) {
    const [expanded, setExpanded] = useState(false);
    const hasMore = items.length > initialCount;
    const visible = expanded || !hasMore ? items : items.slice(0, initialCount);
    return (
        <div className="flex flex-col gap-2">
            {visible.map((t, i) => (
                <CardItem key={t.id ?? `${t.name}-${i}`} title={t.name} subtitle={t.description} />
            ))}
            {hasMore && (
                <Button variant="ghost" size="sm" className="self-start text-(--primary-wMain) hover:underline" onClick={() => setExpanded(v => !v)}>
                    {expanded ? "Show less" : `Show ${items.length - initialCount} more`}
                </Button>
            )}
        </div>
    );
}

function MetadataGrid({ rows }: { rows: { label: string; value: React.ReactNode }[] }) {
    const visible = rows.filter(r => r.value !== undefined && r.value !== null && r.value !== "");
    if (visible.length === 0) return null;
    return (
        <div className="grid grid-cols-[140px_1fr] gap-x-4 gap-y-2 rounded-sm bg-(--secondary-w10) p-4 text-sm">
            {visible.map(r => (
                <Fragment key={r.label}>
                    <div className="text-(--secondary-text-wMain)">{r.label}</div>
                    <div className="min-w-0 break-words">{r.value}</div>
                </Fragment>
            ))}
        </div>
    );
}

const DATASET_PREVIEW_PAGE_SIZE = 5;

function snapshotArtifactToRow(artifact: ConfigSnapshotExampleArtifact | null | undefined): DatasetExampleRow["inputArtifact"] {
    return artifact ? { filename: artifact.filename, version: artifact.version, mimeType: artifact.mime_type, size: artifact.size_bytes, contentHash: artifact.content_hash } : null;
}

export function DatasetDetailBody({ snapshot, onClose: _onClose }: { readonly snapshot: ConfigSnapshotDataset; readonly onClose: () => void }) {
    const [pageNumber, setPageNumber] = useState(1);
    const examples: DatasetExampleRow[] = (snapshot.examples ?? []).map(ex => ({
        id: ex.id,
        prompt: ex.prompt,
        expectedResponse: ex.expected_response,
        inputArtifact: snapshotArtifactToRow(ex.artifact),
        expectedArtifact: snapshotArtifactToRow(ex.expected_artifact),
        inputData: ex.input_data,
    }));
    const totalPages = Math.max(1, Math.ceil(examples.length / DATASET_PREVIEW_PAGE_SIZE));
    const displayExamples = examples.slice((pageNumber - 1) * DATASET_PREVIEW_PAGE_SIZE, pageNumber * DATASET_PREVIEW_PAGE_SIZE);
    const snapshotHasArtifacts = examples.some(ex => ex.inputArtifact || ex.expectedArtifact);

    return (
        <DetailDialogShell title={snapshot.name ?? "Dataset"} headerLeading={<Database className="size-5 shrink-0 text-(--secondary-text-wMain)" />}>
            {examples.length === 0 ? (
                <div className="py-8 text-center text-sm text-(--secondary-text-wMain)">No examples in this dataset.</div>
            ) : (
                <>
                    <DatasetExamplesTable rows={displayExamples} showArtifacts={snapshotHasArtifacts} showInputData={examples.some(ex => ex.inputData)} />
                    <div className="mt-4">
                        <ExamplesPaginationFooter page={pageNumber} pageSize={DATASET_PREVIEW_PAGE_SIZE} totalPages={totalPages} totalCount={examples.length} onPageChange={setPageNumber} summariseSinglePage layout="stacked" />
                    </div>
                </>
            )}
        </DetailDialogShell>
    );
}

export function AgentDetailBody({ agent, onOpenModel, onClose: _onClose }: { agent: AgentSnapshot; onOpenModel: (snapshot: ModelConfigSnapshot) => void; onClose: () => void }) {
    const modelRow = modelRowFromSnapshot(agent.modelConfig);

    const hasDetails = Boolean(agent.systemPrompt || agent.toolsets?.length || agent.tools?.length || agent.connectors?.length || agent.skills?.length || agent.inputModes?.length || agent.outputModes?.length || modelRow);

    return (
        <DetailDialogShell title={agent.name ?? "Agent"} subtitle={agent.description} headerLeading={<Bot className="size-5 shrink-0 text-(--secondary-text-wMain)" />}>
            {!hasDetails ? (
                <div className="flex flex-col items-center justify-center gap-3 py-10 text-center">
                    <Sparkles className="size-8 text-(--secondary-text-wMain)" />
                    <div className="text-sm font-semibold">No agent configuration captured</div>
                    <div className="max-w-md text-xs text-(--secondary-text-wMain)">This run references a built-in agent loaded from configuration, or was triggered before snapshot capture was enabled.</div>
                </div>
            ) : (
                <>
                    {agent.systemPrompt && (
                        <Section icon={BookOpenText} title="Instructions">
                            <div className="rounded-sm bg-(--secondary-w10) p-4 text-sm whitespace-pre-wrap">{agent.systemPrompt}</div>
                        </Section>
                    )}

                    {modelRow && (
                        <Section icon={Cpu} title="Model">
                            <ModelRow model={modelRow} onClick={() => onOpenModel(agent.modelConfig!)} surface />
                        </Section>
                    )}

                    {agent.toolsets && agent.toolsets.length > 0 && (
                        <Section icon={Wrench} title={`Toolsets (${agent.toolsets.length})`}>
                            <ChipList items={agent.toolsets} />
                        </Section>
                    )}

                    {agent.connectors && agent.connectors.length > 0 && (
                        <Section icon={Plug} title={`Connectors (${agent.connectors.length})`}>
                            <div className="flex flex-col gap-2">
                                {agent.connectors.map(c => (
                                    <CardItem key={c.id} title={c.name} subtitle={[c.type, c.subtype].filter(Boolean).join(" / ") || undefined} />
                                ))}
                            </div>
                        </Section>
                    )}

                    {agent.skills && agent.skills.length > 0 && (
                        <Section icon={Bot} title={`Skills (${agent.skills.length})`}>
                            <div className="flex flex-col gap-2">
                                {agent.skills.map((s, i) => (
                                    <CardItem key={s.id ?? `${s.name}-${i}`} title={s.name} subtitle={s.description} />
                                ))}
                            </div>
                        </Section>
                    )}

                    {Boolean(agent.inputModes?.length || agent.outputModes?.length) && (
                        <Section icon={Bot} title="Communication Modes">
                            <MetadataGrid
                                rows={[
                                    { label: "Input Modes", value: agent.inputModes?.join(", ") || "—" },
                                    { label: "Output Modes", value: agent.outputModes?.join(", ") || "—" },
                                ]}
                            />
                        </Section>
                    )}

                    {agent.tools && agent.tools.length > 0 && (
                        <Section icon={Wrench} title={`Tools (${agent.tools.length})`}>
                            <CollapsibleCardList items={agent.tools} initialCount={3} />
                        </Section>
                    )}
                </>
            )}
        </DetailDialogShell>
    );
}

export function EvaluatorDetailBody({ evaluator, onOpenModel, onClose: _onClose }: { evaluator: EvaluatorSnapshotEntry; onOpenModel: (snapshot: ModelConfigSnapshot) => void; onClose: () => void }) {
    const isLlmJudge = evaluator.type === "llm_judge";
    const modelRow = modelRowFromSnapshot(evaluator.modelConfig);
    const decisionConfig = evaluator.type === "decision" ? evaluator.decision_config : undefined;
    const resolved = decisionConfig?.resolved_model;
    const resolvedDecisionModel = resolved ? [resolved.provider, resolved.model ?? resolved.ref].filter(Boolean).join(" / ") : undefined;

    const typeLabel = EVALUATOR_TYPE_LABELS[evaluator.type ?? ""] ?? evaluator.type;

    const headerBadges = (
        <>
            {typeLabel && (
                <Badge variant="secondary" className="text-xs font-medium">
                    {typeLabel}
                </Badge>
            )}
            {evaluator.heuristic_type && (
                <Badge variant="outline" className="text-xs font-medium">
                    {evaluator.heuristic_type}
                </Badge>
            )}
        </>
    );

    return (
        <DetailDialogShell title={evaluator.name ?? "Evaluator"} subtitle={evaluator.description} headerLeading={<Sparkles className="size-5 shrink-0 text-(--secondary-text-wMain)" />} headerBadges={headerBadges}>
            {modelRow && (
                <Section icon={Cpu} title="Model">
                    <ModelRow model={modelRow} onClick={() => onOpenModel(evaluator.modelConfig!)} surface />
                </Section>
            )}

            {decisionConfig && (
                <Section icon={Scale} title="Decision Settings">
                    <DecisionEvaluatorSummary settings={decisionSettingsFromSnapshot(decisionConfig, evaluator.choice_scores)} model={!modelRow && resolvedDecisionModel ? <span className="font-mono">{resolvedDecisionModel}</span> : undefined} />
                </Section>
            )}

            {isLlmJudge && evaluator.prompt_template && (
                <Section icon={BookOpenText} title="Criteria">
                    <div className="rounded-sm bg-(--secondary-w10) p-4 text-sm whitespace-pre-wrap">{evaluator.prompt_template}</div>
                </Section>
            )}

            {isLlmJudge && evaluator.choice_scores && Object.keys(evaluator.choice_scores).length > 0 && (
                <Section icon={Sliders} title="Choice Scores">
                    <div className="overflow-hidden rounded-sm border">
                        {Object.entries(evaluator.choice_scores).map(([choice, score], i, arr) => (
                            <div key={choice} className={cn("grid grid-cols-[1fr_auto] gap-4 px-4 py-2 text-sm", i < arr.length - 1 && "border-b")}>
                                <span>{choice}</span>
                                <span className="font-mono">{score}</span>
                            </div>
                        ))}
                    </div>
                </Section>
            )}
        </DetailDialogShell>
    );
}

function InferenceParamsView({ params }: { params: Record<string, unknown> }) {
    const flat = Object.values(params).every(v => v === null || typeof v !== "object");
    if (flat) {
        const entries = Object.entries(params);
        return (
            <div className="overflow-hidden rounded-sm border">
                {entries.map(([k, v], i) => (
                    <div key={k} className={cn("grid grid-cols-[160px_1fr] gap-4 px-4 py-2 text-sm", i < entries.length - 1 && "border-b")}>
                        <span className="text-(--secondary-text-wMain)">{k}</span>
                        <span className="font-mono break-all">{v === null ? "null" : String(v)}</span>
                    </div>
                ))}
            </div>
        );
    }
    return <pre className="max-h-72 overflow-auto rounded-sm border bg-(--secondary-w10) p-3 font-mono text-xs">{JSON.stringify(params, null, 2)}</pre>;
}

export function ModelDetailBody({ model, onClose: _onClose }: { model: ModelRowItem; onClose: () => void }) {
    const snap = model.snapshot;
    const liveModel = useLiveModelConfig(model.modelConfigId);
    const provider = snap?.provider ?? model.provider;
    const modelName = snap?.modelName ?? model.model;

    const metadataRows: { label: string; value: React.ReactNode }[] = [
        { label: "Provider", value: provider },
        { label: "Model", value: modelName ? <span className="font-mono">{modelName}</span> : undefined },
        { label: "Base URL", value: snap?.apiBase ? <span className="font-mono break-all">{snap.apiBase}</span> : undefined },
        {
            label: "Max Input Tokens",
            value: snap?.maxInputTokens != null ? <span className="font-mono">{snap.maxInputTokens}</span> : undefined,
        },
        {
            label: "Config ID",
            value: model.modelConfigId ? <span className="font-mono break-all">{model.modelConfigId}</span> : undefined,
        },
        { label: "Description", value: snap?.description ?? undefined },
    ];

    return (
        <DetailDialogShell title={model.alias ?? modelName ?? "Model"} headerLeading={<ModelConfigIcon model={liveModel} storedProvider={provider} size="sm" />}>
            <Section icon={Cpu} title="Configuration">
                <MetadataGrid rows={metadataRows} />
            </Section>

            {snap?.modelParams && Object.keys(snap.modelParams).length > 0 && (
                <Section icon={Sliders} title="Inference Parameters">
                    <InferenceParamsView params={snap.modelParams} />
                </Section>
            )}
        </DetailDialogShell>
    );
}
