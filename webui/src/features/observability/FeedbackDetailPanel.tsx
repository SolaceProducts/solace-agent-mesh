// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useId, useState } from "react";
import { X } from "lucide-react";

import type { CrossUserFeedback } from "@/api/feedback";

import { PageLabel } from "@/common/layout";

import { Button } from "@/ui/button";
import { useClampOverflow } from "@/ui/hooks";

import { cn } from "@/utils";

import { entrypointDisplay, formatFeedbackTime, formatFeedbackTimeWithYear, RatingBadge, SUBJECT_TYPE_ICONS, subjectLabel } from "./feedbackLabels";
import { RecentFeedbackList } from "./RecentFeedbackList";

interface FeedbackDetailPanelProps {
    feedback: CrossUserFeedback;
    onClose: () => void;
    onSelect: (row: CrossUserFeedback) => void;
    onViewMore?: () => void;
    onStartActivityChat: () => void;
    canStartActivityChat: boolean;
    isOutsideTableFilters?: boolean;
}

export function FeedbackDetailPanel({ feedback, onClose, onSelect, onViewMore, onStartActivityChat, canStartActivityChat, isOutsideTableFilters = false }: Readonly<FeedbackDetailPanelProps>) {
    const title = subjectLabel(feedback);
    const SubjectIcon = SUBJECT_TYPE_ICONS[feedback.subjectType];
    const entrypoint = entrypointDisplay(feedback.entrypointType);
    const detailsHeadingId = useId();

    return (
        <aside aria-label="Feedback details" className="flex h-full w-full flex-col border-l bg-(--background-w10)">
            <div className="flex h-16 items-center justify-between gap-2 border-b px-4">
                <h2 className="flex min-w-0 items-center gap-2 text-xl font-semibold" title={title}>
                    {SubjectIcon && <SubjectIcon className="size-5 shrink-0 text-(--secondary-text-wMain)" aria-hidden />}
                    <span className="truncate">{title}</span>
                </h2>
                <Button variant="ghost" tooltip="Close feedback details" onClick={onClose}>
                    <X />
                </Button>
            </div>

            <div className="flex flex-1 flex-col gap-6 overflow-y-auto p-4">
                {isOutsideTableFilters && (
                    <p data-testid="outside-table-filters" className="rounded-sm bg-(--warning-w10) px-3 py-2 text-sm text-(--primary-text-wMain)">
                        This feedback is outside the table&apos;s current filters, so it is not highlighted in the list.
                    </p>
                )}

                <section aria-labelledby={detailsHeadingId} className="flex flex-col gap-4 rounded-sm bg-(--secondary-w10) p-4">
                    <h3 id={detailsHeadingId} className="flex items-center gap-2 text-base font-semibold">
                        Feedback Details
                        <RatingBadge rating={feedback.rating} />
                    </h3>
                    <div className="flex justify-between gap-4">
                        <div className="min-w-0">
                            <PageLabel>User</PageLabel>
                            <div className="truncate" title={feedback.userId}>
                                {feedback.userId}
                            </div>
                        </div>
                        <div className="shrink-0">
                            <PageLabel>Time</PageLabel>
                            <div title={formatFeedbackTimeWithYear(feedback.createdAt)}>{formatFeedbackTime(feedback.createdAt)}</div>
                        </div>
                    </div>
                    <div>
                        <PageLabel>Entrypoint</PageLabel>
                        <div className="flex items-center gap-2">
                            {entrypoint.Icon && (
                                <span data-testid="entrypoint-icon" className="inline-flex">
                                    <entrypoint.Icon size="16" />
                                </span>
                            )}
                            {entrypoint.label}
                        </div>
                    </div>
                    <div>
                        <PageLabel>Comment</PageLabel>
                        <FeedbackComment key={feedback.id} comment={feedback.comment} />
                    </div>
                    {feedback.taskId && canStartActivityChat && <ActivityMonitorAction onStart={onStartActivityChat} />}
                </section>

                <RecentFeedbackList feedback={feedback} onSelect={onSelect} onViewMore={onViewMore} />
            </div>
        </aside>
    );
}

function FeedbackComment({ comment }: Readonly<{ comment: string | null }>) {
    const [isExpanded, setIsExpanded] = useState(false);
    const { ref, isOverflowing } = useClampOverflow<HTMLDivElement>(!isExpanded, [comment]);

    return (
        <div className="flex flex-col gap-1">
            <div ref={ref} data-testid="feedback-comment" className={cn("break-words whitespace-pre-wrap", !isExpanded && "line-clamp-6")}>
                {comment ?? "—"}
            </div>
            {comment && (isOverflowing || isExpanded) && (
                <button type="button" aria-expanded={isExpanded} onClick={() => setIsExpanded(prev => !prev)} className="cursor-pointer self-start text-xs font-bold text-(--primary-wMain) hover:underline">
                    {isExpanded ? "Read Less" : "Read More"}
                </button>
            )}
        </div>
    );
}

interface ActivityMonitorActionProps {
    onStart: () => void;
}

function ActivityMonitorAction({ onStart }: Readonly<ActivityMonitorActionProps>) {
    return (
        <Button className="w-full" onClick={onStart}>
            Ask Activity Monitor
        </Button>
    );
}
