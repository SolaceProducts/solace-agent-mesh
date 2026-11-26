// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect, useMemo } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";

import { useAgentCards } from "@/api/agent-cards/hooks";
import { type FeedbackSubjectRef, type FeedbackSummariesParams, type FeedbackSummaryRow, type FeedbackSummarySort, useFeedbackSummaries } from "@/api/feedback";

import { useConfigContext } from "@/providers/contexts";

import { PaginationControls } from "@/common/layout/PaginationControls";
import { TableStateBoundary } from "@/common/status";

import { resolveActivityMonitor } from "@/utils/activityMonitor";

import { usePageNumber } from "@/hooks";
import { useSortState } from "@/ui";
import { cn } from "@/utils";

import { buildAskAiPrefill } from "./askAiPrefill";
import {
    ALL_SUBJECTS,
    DASHBOARD_DATE_PRESETS,
    DEFAULT_DASHBOARD_PRESET,
    granularityFor,
    isSameRange,
    parseRange,
    PRESET_DAYS,
    resolveWindow,
    serializeRange,
    SHARED_FILTER_PARAM,
    subjectFilterFor,
    subjectKeyOf,
    subjectKindOf,
} from "./dashboardFilters";
import { useDashboardUrlState } from "./dashboardUrlState";
import { FeedbackAccessDenied } from "./FeedbackAccessDenied";
import { FeedbackDashboardFilters, type SubjectChoice } from "./FeedbackDashboardFilters";
import { FeedbackKpiRow } from "./FeedbackKpiRow";
import { FeedbackSubjectsTable } from "./FeedbackSubjectsTable";
import { NegativeTrendChart } from "./NegativeTrendChart";
import { defaultRangeWithinRetention, presetsWithinRetention, rangeWithinRetention, retentionFloorDate, todayCalendarDay } from "./retentionLimit";
import { OBSERVABILITY_ROUTES } from "./routes";

const SUBJECT_OPTIONS_PAGE_SIZE = 100;

const TOP_OFFENDER_COUNT = 5;

const SUBJECTS_HEADING_ID = "feedback-subjects-heading";

const TABLE_PAGE_SIZE = 20;

function statusOf(error: unknown): number | undefined {
    return (error as (Error & { status?: number }) | null)?.status;
}

export function FeedbackDashboardPage() {
    const { dataRetention } = useConfigContext();
    const retentionDays = dataRetention ? Math.min(dataRetention.taskDays, dataRetention.feedbackDays) : undefined;
    const datePresets = useMemo(() => presetsWithinRetention(DASHBOARD_DATE_PRESETS, PRESET_DAYS, retentionDays), [retentionDays]);
    const effectiveDefaultRange = useMemo(() => defaultRangeWithinRetention(DEFAULT_DASHBOARD_PRESET, datePresets), [datePresets]);

    const { subjectKey, subjectLabel, setSubject, range, setRange, hasActiveFilters, resetFilters } = useDashboardUrlState(effectiveDefaultRange);
    const minDate = useMemo(() => retentionFloorDate(retentionDays), [retentionDays]);
    const maxDate = useMemo(() => todayCalendarDay(), []);

    useEffect(() => {
        const allowed = rangeWithinRetention({ range, presets: datePresets, floorDate: minDate, ceilingDate: maxDate, defaultPreset: DEFAULT_DASHBOARD_PRESET });
        if (!isSameRange(allowed, range)) setRange(allowed, { replace: true });
    }, [range, datePresets, minDate, maxDate, setRange]);

    const serializedRange = serializeRange(range);
    const windowRange = useMemo(() => resolveWindow(parseRange(serializedRange)), [serializedRange]);
    const subjectFilter = subjectFilterFor(subjectKey);
    const granularity = granularityFor(windowRange);

    const kpiQuery = useFeedbackSummaries({
        groupBy: "tenant",
        trendGranularity: "none",
        startDate: windowRange.startDate,
        endDate: windowRange.endDate,
        ...subjectFilter,
    });

    const optionsParams: FeedbackSummariesParams = {
        groupBy: "subject",
        trendGranularity: "none",
        sortBy: "name",
        sortDir: "asc",
        pageSize: SUBJECT_OPTIONS_PAGE_SIZE,
        startDate: windowRange.startDate,
        endDate: windowRange.endDate,
    };
    const optionsQuery = useFeedbackSummaries(optionsParams);

    const chartQuery = useFeedbackSummaries({
        groupBy: "subject",
        trendGranularity: granularity,
        sortBy: "negativeRatio",
        sortDir: "desc",
        pageSize: TOP_OFFENDER_COUNT,
        pageNumber: 1,
        startDate: windowRange.startDate,
        endDate: windowRange.endDate,
        ...subjectFilter,
    });

    const subjectChoices: SubjectChoice[] = useMemo(() => {
        const fromOptions = (optionsQuery.data?.data ?? [])
            .filter((row): row is FeedbackSummaryRow & Required<Pick<FeedbackSummaryRow, "subject">> => row.subject !== undefined)
            .map(row => ({ key: subjectKeyOf(row.subject), label: row.subject.name || row.subject.id }));
        if (subjectKey !== ALL_SUBJECTS && !fromOptions.some(choice => choice.key === subjectKey)) {
            return [...fromOptions, { key: subjectKey, label: subjectLabel ?? subjectKey }];
        }
        return fromOptions;
    }, [optionsQuery.data, subjectKey, subjectLabel]);
    const subjectChoicesTruncated = (optionsQuery.data?.meta.pagination.count ?? 0) > SUBJECT_OPTIONS_PAGE_SIZE;

    const resolvedSubjectLabel = subjectKey === ALL_SUBJECTS ? undefined : subjectChoices.find(choice => choice.key === subjectKey)?.label;

    const { sortKey, sortDir, handleSort } = useSortState<FeedbackSummarySort>("negativeRatio", "desc");
    const [page, setPage] = usePageNumber(`${subjectKey}|${serializedRange}|${sortKey}|${sortDir}`);

    const tableQuery = useFeedbackSummaries({
        groupBy: "subject",
        trendGranularity: granularity,
        sortBy: sortKey,
        sortDir,
        pageSize: TABLE_PAGE_SIZE,
        pageNumber: page,
        startDate: windowRange.startDate,
        endDate: windowRange.endDate,
        ...subjectFilter,
    });
    const tableRowCount = tableQuery.data?.data.length ?? 0;
    const tableTotalCount = tableQuery.data?.meta.pagination.count ?? 0;
    const tableTotalPages = Math.max(1, tableQuery.data?.meta.pagination.totalPages ?? 1);
    const pageStart = (page - 1) * TABLE_PAGE_SIZE;

    useEffect(() => {
        if (page > tableTotalPages) setPage(tableTotalPages);
    }, [page, tableTotalPages, setPage]);

    const navigate = useNavigate();
    const [searchParams] = useSearchParams();
    const openSubjectHistory = (subject: { id: string; name: string; type: "agent" | "workflow" }) => {
        const next = new URLSearchParams(searchParams);
        next.set(SHARED_FILTER_PARAM.subject, subjectKeyOf(subject));
        next.set(SHARED_FILTER_PARAM.name, subject.name || subject.id);
        navigate({ pathname: OBSERVABILITY_ROUTES.feedbackHistory, search: `?${next.toString()}` });
    };

    const filterToSubject = (subject: FeedbackSubjectRef) => setSubject(subjectKeyOf(subject), subject.name || subject.id);

    const { data: agentCards } = useAgentCards();
    const activityMonitor = resolveActivityMonitor(agentCards);
    const askAi = activityMonitor
        ? () => {
              const prefill = buildAskAiPrefill({ range, presets: DASHBOARD_DATE_PRESETS, subjectLabel: resolvedSubjectLabel });
              navigate("/chat", { state: { promptText: prefill, groupId: "ask-ai", groupName: "Ask AI", agentName: activityMonitor.name } });
          }
        : undefined;

    if (statusOf(kpiQuery.error) === 403) return <FeedbackAccessDenied />;

    return (
        <div className="flex min-h-0 flex-1 flex-col gap-4 overflow-auto p-6 md:p-8">
            <FeedbackDashboardFilters
                subjectKey={subjectKey}
                subjectChoices={subjectChoices}
                subjectChoicesTruncated={subjectChoicesTruncated}
                optionsError={optionsQuery.isError}
                onSubjectChange={key => setSubject(key, subjectChoices.find(choice => choice.key === key)?.label)}
                range={range}
                datePresets={datePresets}
                onRangeChange={setRange}
                minDate={minDate}
                maxDate={maxDate}
                hasActiveFilters={hasActiveFilters}
                onResetFilters={resetFilters}
                onAskAi={askAi}
            />

            <FeedbackKpiRow row={kpiQuery.data?.data[0]} isLoading={kpiQuery.isLoading} isFetching={kpiQuery.isFetching} error={kpiQuery.error} />

            <NegativeTrendChart
                rows={chartQuery.data?.data}
                isLoading={chartQuery.isLoading}
                isFetching={chartQuery.isFetching}
                error={chartQuery.error}
                filteredSubject={subjectKey !== ALL_SUBJECTS && resolvedSubjectLabel ? { label: resolvedSubjectLabel, kind: subjectKindOf(subjectKey) } : undefined}
                granularity={granularity}
                onSelectSubject={subjectKey === ALL_SUBJECTS ? filterToSubject : undefined}
            />

            <section className="flex flex-col gap-3" aria-labelledby={SUBJECTS_HEADING_ID}>
                <div className="flex flex-col gap-0.5">
                    <h2 id={SUBJECTS_HEADING_ID} className="text-sm font-semibold text-(--secondary-text-wMain)">
                        Agents &amp; Workflows
                    </h2>
                    <p className="text-xs text-(--secondary-text-wMain)">Every agent and workflow with activity in the selected window.</p>
                </div>

                <TableStateBoundary
                    resourceName="Agents & Workflows"
                    isLoading={tableQuery.isLoading}
                    error={tableQuery.error}
                    itemCount={tableRowCount}
                    hasActiveFilters={hasActiveFilters}
                    onClearFilters={resetFilters}
                    emptyDescription="Agents and workflows appear here once they handle activity in the selected window."
                >
                    <div className={cn("flex flex-col gap-4 pb-4", tableQuery.isFetching && "opacity-60")} aria-busy={tableQuery.isFetching}>
                        <div className="max-h-[60vh] overflow-auto rounded-xs border">
                            <FeedbackSubjectsTable rows={tableQuery.data?.data ?? []} sortKey={sortKey} sortDir={sortDir} onSort={handleSort} onSelectSubject={openSubjectHistory} />
                        </div>
                        <PaginationControls variant="compact" totalPages={tableTotalPages} currentPage={page} onPageChange={setPage} summary={`Showing ${pageStart + 1}–${pageStart + tableRowCount} of ${tableTotalCount}`} />
                    </div>
                </TableStateBoundary>
            </section>
        </div>
    );
}
