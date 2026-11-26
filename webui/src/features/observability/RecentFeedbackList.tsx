// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useId, useMemo } from "react";

import { type CrossUserFeedback, useFeedbackHistory } from "@/api/feedback";

import { Button } from "@/ui/button";
import { Skeleton } from "@/ui/skeleton";

import { startDateFor } from "./feedbackFilters";
import { formatFeedbackTimeWithYear, RatingBadge } from "./feedbackLabels";

const MAX_ITEMS = 5;
export const RECENT_FETCH_SIZE = MAX_ITEMS + 1;

function shortTime(iso: string): string {
    const d = new Date(iso);
    const pad = (n: number) => String(n).padStart(2, "0");
    return `${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

interface RecentFeedbackListProps {
    feedback: CrossUserFeedback;
    onSelect: (row: CrossUserFeedback) => void;
    onViewMore?: () => void;
}

function filterActionLabel(subjectType: CrossUserFeedback["subjectType"]): string {
    return subjectType === "workflow" ? "Filter by Workflow" : "Filter by Agent";
}

export function RecentFeedbackList({ feedback, onSelect, onViewMore }: Readonly<RecentFeedbackListProps>) {
    if (feedback.subjectType === "unknown") return null;
    return <RecentFeedbackSection feedback={feedback} onSelect={onSelect} onViewMore={onViewMore} />;
}

function RecentFeedbackSection({ feedback, onSelect, onViewMore }: Readonly<RecentFeedbackListProps>) {
    const headingId = useId();
    const { subjectType, subjectId } = feedback;
    // eslint-disable-next-line react-hooks/exhaustive-deps
    const startDate = useMemo(() => startDateFor("7d"), [subjectType, subjectId]);
    const { data, isLoading, isPlaceholderData, error } = useFeedbackHistory({
        pageNumber: 1,
        pageSize: RECENT_FETCH_SIZE,
        startDate,
        ...(subjectType === "workflow" ? { workflowIds: [subjectId] } : { agentIds: [subjectId] }),
    });

    const items = (data?.data ?? []).filter(r => r.id !== feedback.id).slice(0, MAX_ITEMS);

    return (
        <section aria-labelledby={headingId} className="flex flex-col gap-2">
            <h3 id={headingId} className="text-base font-semibold">
                Recent Feedback (7 Days)
            </h3>
            <RecentFeedbackBody isLoading={isLoading || isPlaceholderData} error={error} items={items} onSelect={onSelect} onViewMore={onViewMore} viewMoreLabel={filterActionLabel(subjectType)} />
        </section>
    );
}

interface RecentFeedbackBodyProps {
    isLoading: boolean;
    error: Error | null;
    items: CrossUserFeedback[];
    onSelect: (row: CrossUserFeedback) => void;
    onViewMore?: () => void;
    viewMoreLabel: string;
}

function RecentFeedbackBody({ isLoading, error, items, onSelect, onViewMore, viewMoreLabel }: Readonly<RecentFeedbackBodyProps>) {
    if (isLoading) {
        return (
            <div data-testid="recent-feedback-loading" className="flex flex-col gap-2">
                <Skeleton className="h-8 w-full" />
                <Skeleton className="h-8 w-full" />
            </div>
        );
    }
    if (error) {
        return <p className="text-sm text-(--secondary-text-wMain)">Couldn't load recent feedback</p>;
    }
    if (items.length === 0) {
        return <p className="text-sm text-(--secondary-text-wMain)">No other feedback in the last 7 days</p>;
    }
    return (
        <>
            <ul className="flex flex-col">
                {items.map(item => (
                    <li key={item.id} className="border-b last:border-b-0">
                        <button
                            type="button"
                            className="group flex w-full cursor-pointer items-center gap-3 rounded-xs px-1 py-2 text-left hover:bg-(--primary-w10) focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-(--primary-wMain)"
                            onClick={() => onSelect(item)}
                        >
                            <span className="shrink-0 font-mono text-xs text-(--secondary-text-wMain)" title={formatFeedbackTimeWithYear(item.createdAt)}>
                                {shortTime(item.createdAt)}
                            </span>
                            <span className="min-w-0 flex-1 truncate text-sm text-(--primary-wMain) group-hover:underline">{item.comment ?? "—"}</span>
                            <RatingBadge rating={item.rating} />
                        </button>
                    </li>
                ))}
            </ul>
            {onViewMore && (
                <Button variant="outline" size="sm" className="mt-3 self-start" onClick={onViewMore}>
                    {viewMoreLabel}
                </Button>
            )}
        </>
    );
}
