// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useId, useMemo, useState } from "react";
import { isCancelledError, useQueryClient } from "@tanstack/react-query";
import { RefreshCcw } from "lucide-react";
import { useNavigate } from "react-router-dom";

import { usageKeys, useUsageSummary } from "@/api/usage";

import { useUserContext } from "@/providers/contexts";

import { OnboardingBanner } from "@/common/banners/OnboardingBanner";
import { ErrorDialog } from "@/common/dialogs/ErrorDialog";
import { Header } from "@/common/layout/Header";
import { PageLayout } from "@/common/layout/PageLayout";
import { usageErrorDetail } from "@/common/usage";

import { DOC_LINKS } from "@/utils/docs";

import { Button } from "@/ui/button";

import { BUDGETS_ROUTE } from "@/features/systemSettings/routes";
import { USER_MANAGEMENT_ROUTES } from "@/features/userManagement";

import { useBudgetsEnabled, useIsUserBudgetsSupported, useIsUserManagementEnabled } from "@/hooks";

import { formatRangeLabel } from "../dashboardFilters";
import { DateRangeFilter } from "../DateRangeFilter";

import { MeshBudgetBanner } from "./MeshBudgetBanner";
import { UnpricedCallsBanner } from "./UnpricedCallsBanner";
import { UsageByType } from "./UsageByType";
import { UsageDetailPanel } from "./UsageDetailPanel";
import { parseDetailTarget } from "./usageDetailTarget";
import { USAGE_KINDS, USAGE_KINDS_WITHOUT_USERS } from "./usageKinds";
import { UsageOverview } from "./UsageOverview";
import { comparisonLabel, isThisMonth, parseUsageRange, rangeWithinHistory, toWindowParams, USAGE_HISTORY_DAYS, USAGE_PERIOD_PRESETS, usageHistoryFloor, utcDay } from "./usagePeriod";
import { useListsFollowSummary } from "./useListsFollowSummary";
import { useUsageUrlState } from "./useUsageUrlState";

export const USAGE_PAGE_TITLE = "Cost and Token Usage";

const MODELS_PATH = "/models";

const ONBOARDING_STORAGE_KEY = "sam-usage-onboarding-dismissed";
const ONBOARDING_HEADER = "Monitor cost and token usage.";
const ONBOARDING_DESCRIPTION = "See what Agent Mesh spends on model calls, by agent, workflow, user, and model. Costs come from the prices set on each model.";
const ONBOARDING_DESCRIPTION_WITHOUT_USERS = "See what Agent Mesh spends on model calls, by agent, workflow, and model. Costs come from the prices set on each model.";
const ONBOARDING_LEARN_MORE_TEXT = "Learn about Cost and Token Usage";

export function UsagePageContainer() {
    const userBudgetsSupported = useIsUserBudgetsSupported();
    const kinds = userBudgetsSupported ? USAGE_KINDS : USAGE_KINDS_WITHOUT_USERS;
    const { serializedRange, setRange, kind, setKind, nameFilter, setNameFilter, serializedDetail, setDetail } = useUsageUrlState(kinds);
    const [refreshedAt, setRefreshedAt] = useState(() => new Date());
    const [today, floor] = useMemo(() => [utcDay(refreshedAt), usageHistoryFloor(refreshedAt)], [refreshedAt]);
    const linkedRange = useMemo(() => parseUsageRange(serializedRange), [serializedRange]);
    const range = useMemo(() => rangeWithinHistory(linkedRange, floor, today), [linkedRange, floor, today]);
    const windowParams = useMemo(() => toWindowParams(range), [range]);
    useEffect(() => {
        if (range !== linkedRange) setRange(range, { replace: true });
    }, [range, linkedRange, setRange]);
    const detail = useMemo(() => {
        const target = parseDetailTarget(serializedDetail);
        return target && kinds.includes(target.kind) ? target : null;
    }, [serializedDetail, kinds]);

    const summaryQuery = useUsageSummary(windowParams);
    const periodLabelId = useId();
    const queryClient = useQueryClient();
    const navigate = useNavigate();
    const { userHasCapability, CAPABILITIES } = useUserContext();
    const canSetModelPrices = userHasCapability(CAPABILITIES.readModelConfig) && userHasCapability(CAPABILITIES.updateModelConfig);
    const canManageUsers = useIsUserManagementEnabled();
    const canSeeBudgets = useBudgetsEnabled();
    const [refreshError, setRefreshError] = useState<Error | null>(null);
    const refresh = useCallback(() => {
        if (serializedDetail !== null) setDetail(null);
        setRefreshError(null);
        setRefreshedAt(new Date());
        queryClient.invalidateQueries({ queryKey: usageKeys.all }, { throwOnError: true }).catch((error: Error) => {
            if (!isCancelledError(error)) setRefreshError(error);
        });
    }, [serializedDetail, setDetail, queryClient]);
    const summary = summaryQuery.data;
    useListsFollowSummary(summary?.dataAsOf);

    return (
        <PageLayout>
            <Header
                title={<h1 className="truncate">{USAGE_PAGE_TITLE}</h1>}
                buttons={[
                    <Button key="refresh" variant="ghost" title="Refresh usage" onClick={refresh} className="px-3 sm:px-4">
                        <RefreshCcw />
                        <span className="hidden sm:inline">Refresh</span>
                    </Button>,
                ]}
            />
            <div className="relative flex min-h-0 flex-1">
                <div key={refreshedAt.getTime()} inert={detail !== null} className="flex min-h-0 min-w-0 flex-1 flex-col gap-6 overflow-auto p-6 md:p-8">
                    <OnboardingBanner
                        storageKey={ONBOARDING_STORAGE_KEY}
                        header={ONBOARDING_HEADER}
                        description={userBudgetsSupported ? ONBOARDING_DESCRIPTION : ONBOARDING_DESCRIPTION_WITHOUT_USERS}
                        learnMoreText={ONBOARDING_LEARN_MORE_TEXT}
                        learnMoreUrl={DOC_LINKS.costAndTokenUsage}
                    />
                    {summary?.meshBudget && isThisMonth(range) && <MeshBudgetBanner budget={summary.meshBudget} now={refreshedAt} onGoToBudgets={canSeeBudgets ? () => navigate(BUDGETS_ROUTE) : undefined} />}
                    {summary && <UnpricedCallsBanner unpricedCalls={summary.totals.unpricedCalls} totalCalls={summary.totals.calls} onGoToModels={canSetModelPrices ? () => navigate(MODELS_PATH) : undefined} />}
                    <UsageOverview
                        summary={summary}
                        windowParams={windowParams}
                        isLoading={summaryQuery.isLoading}
                        isStale={summaryQuery.isPlaceholderData}
                        error={summaryQuery.error}
                        comparisonLabel={comparisonLabel(range)}
                        isThisMonth={isThisMonth(range)}
                        periodPicker={
                            <div className="flex items-center gap-2">
                                <span id={periodLabelId} className="text-sm text-(--secondary-text-wMain)">
                                    Period
                                </span>
                                <DateRangeFilter labelledBy={periodLabelId} value={range} onChange={setRange} presets={USAGE_PERIOD_PRESETS} minDate={floor} maxDate={today} maxSpanDays={USAGE_HISTORY_DAYS} />
                            </div>
                        }
                        today={today}
                    />
                    <UsageByType
                        kind={kind}
                        kinds={kinds}
                        onKindChange={setKind}
                        nameFilter={nameFilter}
                        onNameFilterChange={setNameFilter}
                        windowParams={windowParams}
                        costByKind={summary?.costByKind}
                        totalCost={summary?.totals.cost}
                        daily={summary?.daily}
                        summaryWindow={summary?.window}
                        summaryError={summaryQuery.error}
                        totalIsStale={summaryQuery.isPlaceholderData}
                        today={today}
                        isThisMonth={isThisMonth(range)}
                        openDetail={detail}
                        onOpenDetail={target => setDetail(target)}
                    />
                </div>
                <UsageDetailPanel
                    target={detail}
                    windowParams={windowParams}
                    periodLabel={formatRangeLabel(range, USAGE_PERIOD_PRESETS)}
                    today={today}
                    onClose={() => setDetail(null)}
                    onGoToUsers={canManageUsers ? () => navigate(USER_MANAGEMENT_ROUTES.users) : undefined}
                />
            </div>
            <ErrorDialog
                open={refreshError !== null}
                onOpenChange={open => !open && setRefreshError(null)}
                title="Couldn't Refresh Usage"
                error={`Some figures may be out of date. ${usageErrorDetail(refreshError)}`.trim()}
                onRetry={refresh}
                errorSource="api"
            />
        </PageLayout>
    );
}
