// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect, useId, useMemo, useRef, useState } from "react";
import { X } from "lucide-react";

import { type UsageModelDetail, type UsageSubjectDetail, type UsageUserDetail, type UsageWindowParams, type UsageWorkflowDetail, useUsageDetail } from "@/api/usage";

import { PageSidePanelWrapper } from "@/common/layout";
import { SpendVsBudget, usageLoadErrorMessage } from "@/common/usage";

import { Button } from "@/ui/button";
import { Skeleton } from "@/ui/skeleton";

import { MessageBanner } from "@/common";
import { useIsMobile } from "@/hooks";

import { ServedModelsCard } from "./ServedModelsCard";
import { SubjectKpiRow } from "./SubjectKpiRow";
import type { UsageDetailTarget } from "./usageDetailTarget";
import { isUnknownUser, UNKNOWN_USER_LABEL } from "./usageKinds";
import { UsageTrendChart } from "./UsageTrendChart";
import { BudgetStandingBanner, PeriodSpendAndBudget, UserMonthlySpend } from "./UserBudgetSection";
import { UserRootsCard } from "./UserRootsCard";
import { WorkflowNodesCard } from "./WorkflowNodesCard";

const KIND_DESCRIPTION = { workflows: "Workflow usage", agents: "Agent usage", users: "User usage", models: "Model usage" } as const;

interface UsageDetailPanelProps {
    target: UsageDetailTarget | null;
    windowParams: UsageWindowParams;
    periodLabel: string;
    today: string;
    onClose: () => void;
    onGoToUsers?: () => void;
}

function isThisMonthWindow(windowParams: UsageWindowParams): boolean {
    return "period" in windowParams && windowParams.period === "this_month";
}

function statusOf(error: unknown): number | undefined {
    return (error as (Error & { status?: number }) | null)?.status;
}

function DetailLoadError({ error, onRetry }: Readonly<{ error: Error; onRetry: () => void }>) {
    if (statusOf(error) === 404) return <MessageBanner variant="error" message="This item has no usage record." />;
    return (
        <MessageBanner
            variant="error"
            message={
                <span className="flex flex-1 flex-wrap items-center justify-between gap-x-4 gap-y-2">
                    <span>{usageLoadErrorMessage("Couldn't load usage.", error)}</span>
                    <Button variant="ghost" size="sm" className="h-auto p-0" onClick={onRetry}>
                        Retry
                    </Button>
                </span>
            }
        />
    );
}

function DetailBody({ target, windowParams, today, onGoToUsers }: Readonly<{ target: UsageDetailTarget; windowParams: UsageWindowParams; today: string; onGoToUsers?: () => void }>) {
    const query = useUsageDetail(target.kind, target.ref, windowParams);
    const now = useMemo(() => new Date(), []);
    const thisMonth = isThisMonthWindow(windowParams);
    const showRestOfMonth = thisMonth && !query.isPlaceholderData;

    if (query.error && !query.data) return <DetailLoadError error={query.error} onRetry={() => query.refetch()} />;
    if (!query.data) return <Skeleton className="h-64" />;

    if (target.kind === "users") {
        const detail = query.data as UsageUserDetail;
        return (
            <div className="flex flex-col gap-4">
                {thisMonth ? (
                    <>
                        {detail.budget && <BudgetStandingBanner name={detail.name ?? "This user"} budget={detail.budget} now={now} onGoToUsers={onGoToUsers} />}
                        <SpendVsBudget spend={detail.budget?.periodSpend ?? detail.totals.cost} budget={detail.budget} noBudgetLabel="No budget applies" className="rounded-sm border p-4" />
                    </>
                ) : (
                    <>
                        <PeriodSpendAndBudget spend={detail.totals.cost} budget={detail.budget} />
                        <UserMonthlySpend detail={detail} today={today} />
                    </>
                )}
                <UserRootsCard detail={detail} />
            </div>
        );
    }

    const detail = query.data as UsageSubjectDetail;
    return (
        <div className="flex flex-col gap-4">
            <SubjectKpiRow totals={detail.totals} />
            <div className="grid gap-4 @2xl:grid-cols-2">
                <UsageTrendChart title="Cost Over Time" measure="cost" daily={detail.daily} window={detail.window} today={today} showRestOfMonth={showRestOfMonth} />
                <UsageTrendChart title="Activities" measure="activities" daily={detail.daily} window={detail.window} today={today} showRestOfMonth={showRestOfMonth} />
            </div>
            {target.kind === "workflows" && <WorkflowNodesCard nodes={(detail as UsageWorkflowDetail).nodes} hasComparison={(detail as UsageWorkflowDetail).comparisonWindow !== null} historyComplete={detail.historyComplete} />}
            {target.kind === "models" && <ServedModelsCard models={(detail as UsageModelDetail).models} />}
        </div>
    );
}

function DetailTitle({ target, windowParams }: Readonly<{ target: UsageDetailTarget; windowParams: UsageWindowParams }>) {
    const query = useUsageDetail(target.kind, target.ref, windowParams);
    const fallback = "bookedName" in target.ref ? target.ref.bookedName : KIND_DESCRIPTION[target.kind];
    if (query.data && isUnknownUser(target.kind, query.data as UsageUserDetail)) return <>{UNKNOWN_USER_LABEL}</>;
    return <>{query.data?.name ?? fallback}</>;
}

interface PanelContentProps {
    target: UsageDetailTarget;
    windowParams: UsageWindowParams;
    periodLabel: string;
    today: string;
    onClose: () => void;
    onGoToUsers?: () => void;
}

function PanelContent({ target, windowParams, periodLabel, today, onClose, onGoToUsers }: Readonly<PanelContentProps>) {
    const titleId = useId();
    const title = useRef<HTMLHeadingElement>(null);
    const key = JSON.stringify(target);
    const isMobile = useIsMobile();
    useEffect(() => {
        if (isMobile) return undefined;
        const onKeyDown = (event: KeyboardEvent) => {
            if (event.key === "Escape" && !event.defaultPrevented) onClose();
        };
        document.addEventListener("keydown", onKeyDown);
        return () => document.removeEventListener("keydown", onKeyDown);
    }, [onClose, isMobile]);
    useEffect(() => {
        title.current?.focus({ preventScroll: true });
    }, [key]);
    return (
        <aside aria-labelledby={titleId} data-testid="usageDetailPanel" className="flex h-full w-full flex-col border-l bg-(--background-w10)">
            <div className="flex h-16 shrink-0 items-center justify-between gap-2 border-b px-4">
                <div className="flex min-w-0 flex-col">
                    <h2 id={titleId} ref={title} tabIndex={-1} className="truncate text-xl font-semibold outline-none">
                        <DetailTitle target={target} windowParams={windowParams} />
                    </h2>
                    <p className="truncate text-xs text-(--secondary-text-wMain)" data-testid="usageDetailPeriod">
                        <span className="sr-only">{KIND_DESCRIPTION[target.kind]}, </span>
                        {periodLabel}
                    </p>
                </div>
                <Button variant="ghost" tooltip="Close usage details" aria-label="Close usage details" onClick={onClose}>
                    <X />
                </Button>
            </div>
            <div className="@container min-h-0 flex-1 overflow-y-auto p-4">
                <DetailBody key={key} target={target} windowParams={windowParams} today={today} onGoToUsers={onGoToUsers} />
            </div>
        </aside>
    );
}

export function UsageDetailPanel({ target, windowParams, periodLabel, today, onClose, onGoToUsers }: Readonly<UsageDetailPanelProps>) {
    const [shown, setShown] = useState(target);
    if (target !== null && target !== shown) setShown(target);
    const opener = useRef<HTMLElement | null>(null);
    const wasOpen = useRef(false);
    useEffect(() => {
        const isOpen = target !== null;
        if (isOpen && !wasOpen.current) opener.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
        if (!isOpen && wasOpen.current && opener.current?.isConnected) opener.current.focus({ preventScroll: true });
        wasOpen.current = isOpen;
    }, [target]);
    return (
        <PageSidePanelWrapper
            variant="overlay"
            isSidePanelOpen={target !== null}
            onClose={onClose}
            onCloseComplete={() => setShown(null)}
            sidepanel={shown ? <PanelContent target={shown} windowParams={windowParams} periodLabel={periodLabel} today={today} onClose={onClose} onGoToUsers={onGoToUsers} /> : null}
        />
    );
}
