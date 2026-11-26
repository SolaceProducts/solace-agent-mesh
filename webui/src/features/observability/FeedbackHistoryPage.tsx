// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { ThumbsDown, ThumbsUp, X } from "lucide-react";
import { useNavigate, useSearchParams } from "react-router-dom";

import { useAgentCards } from "@/api/agent-cards/hooks";
import { type CrossUserFeedback, useFeedbackHistory, useFeedbackItem } from "@/api/feedback";

import { useConfigContext, useUserContext } from "@/providers/contexts";

import { useIsPeopleSearchAvailable } from "@/hooks/useIsPeopleSearchAvailable";

import { PageSidePanelWrapper } from "@/common/layout";
import { PaginationControls } from "@/common/layout/PaginationControls";
import { TableStateBoundary } from "@/common/status";

import { resolveActivityMonitor } from "@/utils/activityMonitor";
import { hasStrictScope } from "@/utils/scopeMatcher";
import { SCOPES } from "@/utils/scopes";

import { Button } from "@/ui/button";
import { Label } from "@/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/ui/select";

import { resolveWindow, subjectFilterFor } from "./dashboardFilters";
import { DateRangeFilter } from "./DateRangeFilter";
import { FeedbackAccessDenied } from "./FeedbackAccessDenied";
import { FeedbackDetailPanel } from "./FeedbackDetailPanel";
import { DATE_PRESETS, type DatePreset, type DateRange, DEFAULT_DATE_PRESET, isSameRange, parseRange, PRESET_DAYS, serializeRange, startDateFor, type SubjectOption, subjectOptionFromHandoff, subjectOptionsFrom } from "./feedbackFilters";
import { FeedbackHistoryTable } from "./FeedbackHistoryTable";
import { ANY_RATING, ANY_SUBJECT, type FeedbackHistoryUrlState, type RatingFilter, useFeedbackHistoryUrlState } from "./feedbackHistoryUrlState";
import { SUBJECT_TYPE_ICONS } from "./feedbackLabels";
import { isRowWithinFilters, type TableFilters, useFeedbackRowPage } from "./feedbackRowPage";
import { buildFeedbackTracePrefill } from "./feedbackTracePrefill";
import { FeedbackUserFilter } from "./FeedbackUserFilter";
import { defaultRangeWithinRetention, presetsWithinRetention, rangeWithinRetention, retentionFloorDate, todayCalendarDay } from "./retentionLimit";

const PAGE_SIZE = 20;

function subjectOptionKey(option: SubjectOption): string {
    return `${option.kind}:${option.id}`;
}

interface SidePanelArgs {
    displayedFeedback: CrossUserFeedback | null;
    isSelectedItemNotFound: boolean;
    closePanel: () => void;
    openFeedback: (row: CrossUserFeedback) => void;
    viewMoreForSelected: (() => void) | undefined;
    startActivityMonitorChat: () => void;
    canStartActivityChat: boolean;
    isOutsideTableFilters: boolean;
}

function renderSidePanel({ displayedFeedback, isSelectedItemNotFound, closePanel, openFeedback, viewMoreForSelected, startActivityMonitorChat, canStartActivityChat, isOutsideTableFilters }: Readonly<SidePanelArgs>) {
    if (displayedFeedback) {
        return (
            <FeedbackDetailPanel
                feedback={displayedFeedback}
                onClose={closePanel}
                onSelect={openFeedback}
                onViewMore={viewMoreForSelected}
                onStartActivityChat={startActivityMonitorChat}
                canStartActivityChat={canStartActivityChat}
                isOutsideTableFilters={isOutsideTableFilters}
            />
        );
    }
    if (isSelectedItemNotFound) {
        return <FeedbackItemNotFound onClose={closePanel} />;
    }
    return null;
}

interface RetentionClampArgs {
    range: DateRange;
    setRange: FeedbackHistoryUrlState["setRange"];
    presets: ReadonlyArray<{ value: DatePreset; label: string }>;
    floorDate: string | undefined;
    ceilingDate: string;
}

function useRetentionClampedRange({ range, setRange, presets, floorDate, ceilingDate }: RetentionClampArgs): void {
    useEffect(() => {
        const allowed = rangeWithinRetention({ range, presets, floorDate, ceilingDate, defaultPreset: DEFAULT_DATE_PRESET });
        if (!isSameRange(allowed, range)) setRange(allowed, { replace: true });
    }, [range, presets, floorDate, ceilingDate, setRange]);
}

function useAccumulatedSubjectOptions(rows: readonly CrossUserFeedback[], searchParams: URLSearchParams): SubjectOption[] {
    const [subjectOptions, setSubjectOptions] = useState<SubjectOption[]>(() => {
        const handoff = subjectOptionFromHandoff(searchParams);
        return handoff ? [handoff] : [];
    });
    const knownSubjectKeys = new Set(subjectOptions.map(subjectOptionKey));
    const newSubjectOptions = subjectOptionsFrom(rows).filter(o => !knownSubjectKeys.has(subjectOptionKey(o)));
    if (newSubjectOptions.length > 0) {
        setSubjectOptions(prev => [...prev, ...newSubjectOptions]);
    }
    return subjectOptions;
}

interface SelectedFeedbackArgs {
    rows: readonly CrossUserFeedback[];
    selectedId: string | null;
    setSelectedId: (id: string | null) => void;
    filters: TableFilters;
    page: number;
    setPage: (page: number) => void;
}

interface SelectedFeedbackState {
    displayedFeedback: CrossUserFeedback | null;
    isSelectedItemNotFound: boolean;
    isPanelOpen: boolean;
    isOutsideTableFilters: boolean;
    openFeedback: (row: CrossUserFeedback) => void;
    closePanel: () => void;
    clearSelection: () => void;
}

function useSelectedFeedback({ rows, selectedId, setSelectedId, filters, page, setPage }: SelectedFeedbackArgs): SelectedFeedbackState {
    const selectedOnPage = useMemo(() => rows.find(row => row.id === selectedId) ?? null, [rows, selectedId]);
    const selectedItem = useFeedbackItem(selectedOnPage ? null : selectedId);
    const selectedFeedback = selectedOnPage ?? selectedItem.data ?? null;
    const isSelectedItemNotFound = selectedOnPage === null && selectedItem.isError;

    const [heldFeedback, setHeldFeedback] = useState<CrossUserFeedback | null>(null);
    useEffect(() => {
        if (selectedFeedback) setHeldFeedback(selectedFeedback);
    }, [selectedFeedback]);

    let displayedFeedback: CrossUserFeedback | null;
    if (selectedId === null) {
        displayedFeedback = heldFeedback;
    } else {
        const heldMatchesSelection = heldFeedback?.id === selectedId ? heldFeedback : null;
        displayedFeedback = selectedFeedback ?? heldMatchesSelection;
    }

    const isOutsideTableFilters = displayedFeedback !== null && !isRowWithinFilters(displayedFeedback, filters);
    const rowPage = useFeedbackRowPage({
        row: displayedFeedback,
        filters,
        pageSize: PAGE_SIZE,
        enabled: selectedId !== null && selectedOnPage === null && !isOutsideTableFilters,
    });
    const jumpedForId = useRef<string | null>(null);
    useEffect(() => {
        if (rowPage === null || displayedFeedback === null) return;
        if (jumpedForId.current === displayedFeedback.id) return;
        jumpedForId.current = displayedFeedback.id;
        if (rowPage !== page) setPage(rowPage);
    }, [rowPage, displayedFeedback, page, setPage]);

    const openFeedback = useCallback(
        (row: CrossUserFeedback) => {
            setSelectedId(row.id);
            setHeldFeedback(row);
        },
        [setSelectedId]
    );
    const closePanel = useCallback(() => setSelectedId(null), [setSelectedId]);
    const clearSelection = useCallback(() => {
        setHeldFeedback(null);
        jumpedForId.current = null;
    }, []);

    return {
        displayedFeedback,
        isSelectedItemNotFound,
        isPanelOpen: selectedId !== null && (displayedFeedback !== null || isSelectedItemNotFound),
        isOutsideTableFilters,
        openFeedback,
        closePanel,
        clearSelection,
    };
}

export function FeedbackHistoryPage() {
    const navigate = useNavigate();
    const [searchParams] = useSearchParams();
    const { dataRetention } = useConfigContext();
    const { scopes } = useUserContext();
    const peopleSearchAvailable = useIsPeopleSearchAvailable();
    const retentionDays = dataRetention?.feedbackDays;
    const datePresets = useMemo(() => presetsWithinRetention(DATE_PRESETS, PRESET_DAYS, retentionDays), [retentionDays]);
    const effectiveDefaultRange = useMemo(() => defaultRangeWithinRetention(DEFAULT_DATE_PRESET, datePresets), [datePresets]);

    const { subjectKey, setSubjectKey, rating, setRating, userId, setUserId, range, setRange, page, setPage, selectedId, setSelectedId, hasActiveFilters, clearFilters } = useFeedbackHistoryUrlState(effectiveDefaultRange);
    const minDate = useMemo(() => retentionFloorDate(retentionDays), [retentionDays]);
    const maxDate = useMemo(() => todayCalendarDay(), []);

    useRetentionClampedRange({ range, setRange, presets: datePresets, floorDate: minDate, ceilingDate: maxDate });

    const serializedRange = serializeRange(range);
    const { startDate, endDate } = useMemo(() => {
        const parsed = parseRange(serializedRange);
        if (parsed.kind === "preset") return { startDate: startDateFor(parsed.preset), endDate: undefined };
        const window = resolveWindow(parsed);
        return { startDate: window.startDate, endDate: window.endDate };
    }, [serializedRange]);
    const { agentIds, workflowIds } = subjectFilterFor(subjectKey);
    const apiRating = rating === ANY_RATING ? undefined : rating;

    const { data, isLoading, error } = useFeedbackHistory({
        pageNumber: page,
        pageSize: PAGE_SIZE,
        agentIds,
        workflowIds,
        rating: apiRating,
        ...(userId ? { userIds: [userId] } : {}),
        startDate,
        ...(endDate === undefined ? {} : { endDate }),
    });

    const rows = useMemo(() => data?.data ?? [], [data]);
    const pagination = data?.meta.pagination;
    const totalCount = pagination?.count ?? 0;
    const totalPages = Math.max(1, pagination?.totalPages ?? 1);

    useEffect(() => {
        if (page > totalPages) setPage(totalPages);
    }, [page, totalPages, setPage]);

    const subjectOptions = useAccumulatedSubjectOptions(rows, searchParams);

    const pageStart = (page - 1) * PAGE_SIZE;

    const { displayedFeedback, isSelectedItemNotFound, isPanelOpen, isOutsideTableFilters, openFeedback, closePanel, clearSelection } = useSelectedFeedback({
        rows,
        selectedId,
        setSelectedId,
        filters: { subjectKey, rating, userId, startDate, endDate },
        page,
        setPage,
    });

    const selectedSubjectFilterKey = displayedFeedback && displayedFeedback.subjectType !== "unknown" ? `${displayedFeedback.subjectType}:${displayedFeedback.subjectId}` : null;
    const viewMoreForSelected = displayedFeedback && selectedSubjectFilterKey && selectedSubjectFilterKey !== subjectKey ? () => setSubjectKey(selectedSubjectFilterKey, displayedFeedback.subjectName || displayedFeedback.subjectId) : undefined;
    const { data: agentCards } = useAgentCards();
    const activityMonitor = resolveActivityMonitor(agentCards);
    const startActivityMonitorChat = () => {
        if (!displayedFeedback || !activityMonitor) return;
        const prefill = buildFeedbackTracePrefill(displayedFeedback);
        navigate("/chat", { state: { promptText: prefill, groupId: `feedback-trace-${displayedFeedback.id}`, groupName: "Activity Monitor", agentName: activityMonitor.name } });
    };

    const isAccessDenied = (error as (Error & { status?: number }) | null)?.status === 403;
    if (isAccessDenied) return <FeedbackAccessDenied />;

    return (
        <div data-testid="feedback-history-page" className="flex min-h-0 flex-1">
            <div className="flex min-w-0 flex-1 flex-col gap-4 p-6 md:p-8">
                <div className="flex flex-wrap items-end gap-3">
                    <div className="flex flex-col gap-1">
                        <Label className="font-semibold text-(--secondary-text-wMain)">Agent / Workflow</Label>
                        <Select value={subjectKey} onValueChange={key => setSubjectKey(key, subjectOptions.find(o => subjectOptionKey(o) === key)?.label)}>
                            <SelectTrigger className="w-64" aria-label="Agent / Workflow">
                                <SelectValue placeholder="All Agents & Workflows" />
                            </SelectTrigger>
                            <SelectContent>
                                <SelectItem value={ANY_SUBJECT}>All Agents & Workflows</SelectItem>
                                {subjectOptions.map(option => {
                                    const SubjectIcon = SUBJECT_TYPE_ICONS[option.kind];
                                    return (
                                        <SelectItem key={`${option.kind}:${option.id}`} value={`${option.kind}:${option.id}`}>
                                            <span className="flex min-w-0 items-center gap-2">
                                                {SubjectIcon && <SubjectIcon className="size-4 shrink-0 text-(--secondary-text-wMain)" aria-hidden />}
                                                <span className="truncate">{option.label}</span>
                                            </span>
                                        </SelectItem>
                                    );
                                })}
                            </SelectContent>
                        </Select>
                    </div>
                    <div className="flex flex-col gap-1">
                        <Label className="font-semibold text-(--secondary-text-wMain)">Rating</Label>
                        <Select value={rating} onValueChange={value => setRating(value as RatingFilter)}>
                            <SelectTrigger className="w-40" aria-label="Filter by rating">
                                <SelectValue placeholder="Any Rating" />
                            </SelectTrigger>
                            <SelectContent>
                                <SelectItem value={ANY_RATING}>Any Rating</SelectItem>
                                <SelectItem value="up">
                                    <span className="flex items-center gap-2">
                                        <ThumbsUp className="size-4 shrink-0 text-(--success-wMain)" aria-hidden />
                                        Positive
                                    </span>
                                </SelectItem>
                                <SelectItem value="down">
                                    <span className="flex items-center gap-2">
                                        <ThumbsDown className="size-4 shrink-0 text-(--error-wMain)" aria-hidden />
                                        Negative
                                    </span>
                                </SelectItem>
                            </SelectContent>
                        </Select>
                    </div>
                    {peopleSearchAvailable && (
                        <div className="flex flex-col gap-1">
                            <Label className="font-semibold text-(--secondary-text-wMain)">User</Label>
                            <FeedbackUserFilter value={userId} onChange={setUserId} />
                        </div>
                    )}
                    <div className="flex flex-col gap-1">
                        <Label className="font-semibold text-(--secondary-text-wMain)">Date Range</Label>
                        <DateRangeFilter value={range} onChange={setRange} presets={datePresets} minDate={minDate} maxDate={maxDate} />
                    </div>
                    {hasActiveFilters && (
                        <Button variant="ghost" onClick={clearFilters}>
                            <X className="size-4" /> Reset Filters
                        </Button>
                    )}
                </div>

                <TableStateBoundary
                    resourceName="Feedback"
                    isLoading={isLoading}
                    error={error}
                    itemCount={totalCount}
                    hasActiveFilters={hasActiveFilters}
                    onClearFilters={clearFilters}
                    emptyDescription="Feedback appears here once users rate agent or workflow responses in chat."
                >
                    <div className="min-h-0 flex-1 overflow-auto rounded-xs border">
                        <FeedbackHistoryTable rows={rows} selectedId={isPanelOpen ? displayedFeedback?.id : undefined} onSelect={openFeedback} />
                    </div>
                    <PaginationControls variant="compact" totalPages={totalPages} currentPage={page} onPageChange={setPage} summary={`Showing ${pageStart + 1}–${pageStart + rows.length} of ${totalCount}`} />
                </TableStateBoundary>
            </div>
            <PageSidePanelWrapper
                isSidePanelOpen={isPanelOpen}
                onClose={closePanel}
                onCloseComplete={clearSelection}
                sidepanel={renderSidePanel({
                    displayedFeedback,
                    isSelectedItemNotFound,
                    closePanel,
                    openFeedback,
                    viewMoreForSelected,
                    startActivityMonitorChat,
                    canStartActivityChat: Boolean(displayedFeedback) && Boolean(activityMonitor) && hasStrictScope(scopes, SCOPES.activity.readAny),
                    isOutsideTableFilters,
                })}
            />
        </div>
    );
}

interface FeedbackItemNotFoundProps {
    onClose: () => void;
}

function FeedbackItemNotFound({ onClose }: Readonly<FeedbackItemNotFoundProps>) {
    return (
        <aside aria-label="Feedback details" className="flex h-full w-full flex-col border-l bg-(--background-w10)">
            <div className="flex h-16 items-center justify-between gap-2 border-b px-4">
                <h2 className="text-xl font-semibold">Feedback details</h2>
                <Button variant="ghost" tooltip="Close feedback details" onClick={onClose}>
                    <X />
                </Button>
            </div>
            <div className="flex flex-1 items-center justify-center p-4 text-center text-sm text-(--secondary-text-wMain)">This feedback cannot be found.</div>
        </aside>
    );
}
