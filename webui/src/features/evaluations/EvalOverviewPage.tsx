// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useMemo, useState } from "react";
import { FileText, Play } from "lucide-react";
import { useNavigate } from "react-router-dom";

import { useSampleExperiment } from "@/api/evaluations/hooks";
import { evaluationsApi } from "@/api/evaluations/versioned";

import { useChatContext, useUserContext } from "@/providers/contexts";

import { useLocalStorage } from "@/hooks/useLocalStorage";

import { PageContentWrapper } from "@/common/layout";

import { getErrorMessage } from "@/utils/api";
import { formatTimestamp } from "@/utils/format";

import { Button } from "@/ui/button";
import { SearchInput } from "@/ui/search-input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/ui/table";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/ui/tooltip";
import type { EvalRunGroupSummary, EvalRunStatus, EvalTargetType } from "@/types/eval";

import { EmptyState, Header, MessageBanner, PageLayout, PaginationControls } from "@/common";
import { useIsWorkflowEvalsEnabled } from "@/hooks";

import { EvalFlowExplainer, EvalStatusBadge, ExperimentQuickStartBanner, RunExperimentsDialog, TargetTypeIcon, WatchlistSection } from "./components";
import { EVAL_ROUTES } from "./constants";
import { useEvalReportAgents, useEvalReportsList } from "./hooks";

const ALL_AGENTS_OPTION = "__all__";
const ALL_TYPES_OPTION = "__all_types__";
const PAGE_SIZE = 20;
const PYTHON_EDITION_QUICK_START_DISMISSED_KEY = "eval.reportsQuickStartDismissed";

function formatDurationSeconds(seconds: number | null | undefined): string {
    if (seconds == null || seconds < 0) return "—";
    if (seconds < 60) return `${Math.round(seconds)}s`;
    const minutes = Math.floor(seconds / 60);
    const secs = Math.round(seconds % 60);
    if (minutes < 60) return secs > 0 ? `${minutes}m ${secs}s` : `${minutes}m`;
    const hours = Math.floor(minutes / 60);
    const remMin = minutes % 60;
    return remMin > 0 ? `${hours}h ${remMin}m` : `${hours}h`;
}

interface RunGroupRowProps {
    readonly summary: EvalRunGroupSummary;
    readonly onNameClick: () => void;
    readonly agentLookup: (brokerName: string) => string;
    readonly showTargetType: boolean;
}

function reportTargetLabel(summary: EvalRunGroupSummary, agentLookup: (name: string) => string): string {
    if (!summary.targetAgent) return "—";
    return summary.targetType === "workflow" ? summary.targetAgent : agentLookup(summary.targetAgent);
}

function RunGroupRow({ summary, onNameClick, agentLookup, showTargetType }: RunGroupRowProps) {
    const displayName = summary.runName?.trim() || summary.experimentName;
    const agentDisplay = reportTargetLabel(summary, agentLookup);
    return (
        <TableRow className="h-14">
            <TableCell className="max-w-55 pl-4">
                <div className="flex items-center gap-2">
                    <FileText className="size-4 shrink-0 text-(--secondary-text-wMain)" />
                    <Tooltip>
                        <TooltipTrigger asChild>
                            <button type="button" onClick={onNameClick} className="block w-full cursor-pointer truncate text-left font-semibold text-(--primary-wMain) hover:underline">
                                {displayName}
                            </button>
                        </TooltipTrigger>
                        <TooltipContent>{displayName}</TooltipContent>
                    </Tooltip>
                </div>
            </TableCell>
            <TableCell className="text-sm text-(--secondary-text-wMain)" title={agentDisplay}>
                <span className="flex min-w-0 items-center gap-2">
                    {showTargetType && <TargetTypeIcon targetType={summary.targetType} className="size-4" />}
                    <span className="truncate">{agentDisplay}</span>
                </span>
            </TableCell>
            <TableCell>
                <EvalStatusBadge status={summary.status as EvalRunStatus} />
            </TableCell>
            <TableCell className="text-sm text-(--secondary-text-wMain)">{formatDurationSeconds(summary.durationSeconds)}</TableCell>
            <TableCell className="text-sm text-(--secondary-text-wMain)">{summary.completedAt ? formatTimestamp(summary.completedAt) : "—"}</TableCell>
        </TableRow>
    );
}

interface EmptyStateProps {
    readonly sampleAvailable: boolean;
    readonly canRunSample: boolean;
    readonly canCreate: boolean;
    readonly onRunSample: () => void;
    readonly onCreateExperiment: () => void;
    readonly runSampleDisabled?: boolean;
    readonly runSampleError?: string | null;
    readonly onDismissRunSampleError?: () => void;
}

function ReportsEmptyState({ sampleAvailable, canRunSample, canCreate, onRunSample, onCreateExperiment, runSampleDisabled, runSampleError, onDismissRunSampleError }: EmptyStateProps) {
    return (
        <div className="flex flex-col gap-6">
            <div className="flex flex-col gap-3">
                <h2 className="text-sm font-semibold">Performance Over Time</h2>
                <div className="flex h-48 items-center justify-center rounded-xs border">
                    <div className="flex flex-col items-center gap-2 text-center text-(--secondary-text-wMain)">
                        <span className="text-sm font-semibold">No Data Found</span>
                        <span className="text-xs">Run experiments to see performance of your agent mesh over time</span>
                    </div>
                </div>
            </div>
            <div className="flex flex-col gap-3">
                <h2 className="text-sm font-semibold">Experiment History</h2>
                <div className="rounded-xs border p-6">
                    <EvalFlowExplainer />
                </div>
                <div className="flex flex-col items-center gap-2 pt-6 text-center">
                    <h3 className="text-base font-semibold">Evaluate and Coach Your Agents</h3>
                    <p className="max-w-2xl text-sm text-(--secondary-text-wMain)">
                        Experiments validate that your agents respond consistently and safely allowing you to coach your agent in the correct behaviour before you ship and catch regressions whenever a prompt, model, or tool changes.
                    </p>
                </div>
                <div className="mx-auto flex w-full max-w-3xl items-center justify-between gap-4 rounded-xs border p-5">
                    <div className="flex min-w-0 flex-col gap-1">
                        <span className="text-xs font-semibold tracking-wider text-(--secondary-text-wMain) uppercase">Quick Start</span>
                        {sampleAvailable ? (
                            <>
                                <span className="text-sm font-semibold">See a completed report</span>
                                <span className="text-xs text-(--secondary-text-wMain)">We'll run a sample experiment so you can see a finished report in one click.</span>
                            </>
                        ) : (
                            <>
                                <span className="text-sm font-semibold">Create your first experiment</span>
                                <span className="text-xs text-(--secondary-text-wMain)">Select the agent and models you want to evaluate, along with the criteria you want to evaluate against using a combination of a dataset and evaluators.</span>
                            </>
                        )}
                    </div>
                    {sampleAvailable
                        ? canRunSample && (
                              <Button data-testid="runSampleExperiment" onClick={onRunSample} disabled={runSampleDisabled}>
                                  Run Sample Experiment
                              </Button>
                          )
                        : canCreate && (
                              <Button data-testid="createExperimentBanner" onClick={onCreateExperiment}>
                                  Create Experiment
                              </Button>
                          )}
                </div>
                {runSampleError && (
                    <div className="mx-auto w-full max-w-3xl">
                        <MessageBanner variant="error" message={runSampleError} dismissible onDismiss={onDismissRunSampleError} />
                    </div>
                )}
            </div>
        </div>
    );
}

export function EvalOverviewPage() {
    const navigate = useNavigate();
    const [pageNumber, setPageNumber] = useState(1);
    const [name, setName] = useState("");
    const [agent, setAgent] = useState<string>(ALL_AGENTS_OPTION);
    const [targetType, setTargetType] = useState<string>(ALL_TYPES_OPTION);
    const [runSamplePending, setRunSamplePending] = useState(false);
    const [runSampleError, setRunSampleError] = useState<string | null>(null);
    const [runDialogOpen, setRunDialogOpen] = useState(false);
    const [quickStartDismissed, setQuickStartDismissed] = useLocalStorage(PYTHON_EDITION_QUICK_START_DISMISSED_KEY, false);

    const reportsListQuery = useEvalReportsList({
        pageNumber,
        pageSize: PAGE_SIZE,
        name,
        agent: agent === ALL_AGENTS_OPTION ? undefined : agent,
        targetType: targetType === ALL_TYPES_OPTION ? undefined : (targetType as EvalTargetType),
    });
    const agentsQuery = useEvalReportAgents();
    const workflowEvalsEnabled = useIsWorkflowEvalsEnabled();
    const targetsOfType = [...new Set((agentsQuery.data ?? []).filter(t => targetType === ALL_TYPES_OPTION || t.targetType === targetType).map(t => t.name))];
    const sampleExperimentQuery = useSampleExperiment();

    const { agentNameDisplayNameMap } = useChatContext();
    const { userHasCapability, CAPABILITIES } = useUserContext();
    const canCreate = userHasCapability(CAPABILITIES.createEvaluations);
    const canRun = userHasCapability(CAPABILITIES.invokeEvaluations);
    const agentLookup = useMemo(() => (brokerName: string) => agentNameDisplayNameMap[brokerName] ?? brokerName, [agentNameDisplayNameMap]);

    const summaries = reportsListQuery.data?.data ?? [];
    const totalCount = reportsListQuery.data?.meta.pagination.count ?? 0;
    const totalPages = reportsListQuery.data?.meta.pagination.totalPages ?? 0;

    const noFiltersActive = !name && agent === ALL_AGENTS_OPTION && targetType === ALL_TYPES_OPTION;
    const isInitialLoad = reportsListQuery.isLoading && !reportsListQuery.data;
    const isEmpty = !isInitialLoad && noFiltersActive && totalCount === 0;

    const onRunGroupClick = (groupId: string) => {
        navigate(EVAL_ROUTES.runGroup(groupId));
    };

    const sampleExperiment = sampleExperimentQuery.data ?? null;
    const sampleAvailable = sampleExperiment !== null;

    const runSampleExperiment = async () => {
        if (runSamplePending || !sampleExperiment) return;
        setRunSamplePending(true);
        setRunSampleError(null);
        try {
            const { runGroupId } = await evaluationsApi.triggerRun(sampleExperiment.id, {});
            if (!runGroupId) {
                throw new Error("trigger returned no runs; check platform logs");
            }
            navigate(EVAL_ROUTES.runGroup(runGroupId));
        } catch (err) {
            console.error("[evaluations] runSampleExperiment failed:", err);
            setRunSampleError(`Could not run the sample experiment: ${getErrorMessage(err)}`);
        } finally {
            setRunSamplePending(false);
        }
    };

    const goToNewExperiment = () => navigate(EVAL_ROUTES.EXPERIMENT_NEW);

    const renderContent = () => {
        if (isInitialLoad) return <EmptyState variant="loading" title="Loading..." />;
        if (isEmpty) {
            return (
                <ReportsEmptyState
                    sampleAvailable={sampleAvailable}
                    canRunSample={canRun}
                    canCreate={canCreate}
                    onRunSample={runSampleExperiment}
                    onCreateExperiment={goToNewExperiment}
                    runSampleDisabled={runSamplePending}
                    runSampleError={runSampleError}
                    onDismissRunSampleError={() => setRunSampleError(null)}
                />
            );
        }

        return (
            <div className="flex flex-col gap-6">
                {!quickStartDismissed &&
                    (sampleAvailable
                        ? canRun && (
                              <ExperimentQuickStartBanner
                                  quickStartTitle="See a completed report"
                                  quickStartDescription="We'll run a sample Experiment so you can see a finished report in one click."
                                  ctaLabel="Run Sample Experiment"
                                  ctaTestId="runSampleExperiment"
                                  ctaDisabled={runSamplePending}
                                  onCreateExperiment={runSampleExperiment}
                                  onDismiss={() => setQuickStartDismissed(true)}
                              />
                          )
                        : canCreate && <ExperimentQuickStartBanner onCreateExperiment={goToNewExperiment} onDismiss={() => setQuickStartDismissed(true)} />)}
                {runSampleError && <MessageBanner variant="error" message={runSampleError} dismissible onDismiss={() => setRunSampleError(null)} />}
                <WatchlistSection />
                <div className="flex flex-col gap-3">
                    <div className="flex flex-wrap items-center justify-between gap-2">
                        <h2 className="text-sm font-semibold">Experiment Report History</h2>
                        {canRun && (
                            <Button variant="outline" data-testid="runExperiment" onClick={() => setRunDialogOpen(true)}>
                                <Play className="size-4 fill-current" />
                                Run Experiment
                            </Button>
                        )}
                    </div>
                    <div className="flex flex-wrap items-center gap-3">
                        <SearchInput
                            placeholder="Filter by name"
                            value={name}
                            onChange={value => {
                                setName(value);
                                setPageNumber(1);
                            }}
                        />
                        {workflowEvalsEnabled && (
                            <Select
                                value={targetType}
                                onValueChange={v => {
                                    setTargetType(v);
                                    setAgent(ALL_AGENTS_OPTION);
                                    setPageNumber(1);
                                }}
                            >
                                <SelectTrigger className="w-full max-w-40 sm:w-40" data-testid="reportsTargetType" aria-label="Target type">
                                    <SelectValue />
                                </SelectTrigger>
                                <SelectContent>
                                    <SelectItem value={ALL_TYPES_OPTION}>All Types</SelectItem>
                                    <SelectItem value="agent">Agents</SelectItem>
                                    <SelectItem value="workflow">Workflows</SelectItem>
                                </SelectContent>
                            </Select>
                        )}
                        <Select
                            value={agent}
                            onValueChange={v => {
                                setAgent(v);
                                setPageNumber(1);
                            }}
                        >
                            <SelectTrigger className="w-full max-w-52 sm:w-52">
                                <SelectValue />
                            </SelectTrigger>
                            <SelectContent>
                                <SelectItem value={ALL_AGENTS_OPTION}>{workflowEvalsEnabled ? "All Targets" : "All Agents"}</SelectItem>
                                {targetsOfType.map(a => (
                                    <SelectItem key={a} value={a}>
                                        {agentLookup(a)}
                                    </SelectItem>
                                ))}
                            </SelectContent>
                        </Select>
                    </div>
                    {summaries.length === 0 ? (
                        <div className="py-8 text-center text-sm text-(--secondary-text-wMain)">No reports match the current filter.</div>
                    ) : (
                        <div className="overflow-x-auto rounded-xs border">
                            <Table>
                                <TableHeader>
                                    <TableRow>
                                        <TableHead className="w-64 pl-4 font-semibold">Name</TableHead>
                                        <TableHead className="w-48 font-semibold">{workflowEvalsEnabled ? "Target" : "Agent"}</TableHead>
                                        <TableHead className="w-36 font-semibold">Status</TableHead>
                                        <TableHead className="w-28 font-semibold">Duration</TableHead>
                                        <TableHead className="font-semibold">Completed On</TableHead>
                                    </TableRow>
                                </TableHeader>
                                <TableBody className="[&_tr:last-child]:border-b!">
                                    {summaries.map(s => (
                                        <RunGroupRow key={s.runGroupId} summary={s} onNameClick={() => onRunGroupClick(s.runGroupId)} agentLookup={agentLookup} showTargetType={workflowEvalsEnabled} />
                                    ))}
                                </TableBody>
                            </Table>
                        </div>
                    )}
                    {totalCount > 0 && (
                        <div className="flex items-center justify-between pt-2">
                            <div />
                            {totalPages > 1 && <PaginationControls totalPages={totalPages} currentPage={pageNumber} onPageChange={setPageNumber} />}
                            <span className="text-sm text-(--secondary-text-wMain)">
                                Showing {(pageNumber - 1) * PAGE_SIZE + 1}-{Math.min(pageNumber * PAGE_SIZE, totalCount)} of {totalCount} results
                            </span>
                        </div>
                    )}
                </div>
            </div>
        );
    };

    return (
        <PageLayout>
            <Header title="Reports" />
            <PageContentWrapper className="p-6" innerClassName="gap-6">
                {renderContent()}
            </PageContentWrapper>
            <RunExperimentsDialog
                open={runDialogOpen}
                onOpenChange={setRunDialogOpen}
                onRunComplete={() => {
                    reportsListQuery.refetch().catch(() => {});
                }}
            />
        </PageLayout>
    );
}
