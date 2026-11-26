// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useEffect, useRef } from "react";

import type { CrossUserFeedback } from "@/api/feedback";

import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/ui/table";

import { formatFeedbackTime, formatFeedbackTimeWithYear, RatingBadge, SUBJECT_TYPE_ICONS, subjectLabel } from "./feedbackLabels";

interface FeedbackHistoryTableProps {
    rows: readonly CrossUserFeedback[];
    selectedId?: string;
    onSelect?: (row: CrossUserFeedback) => void;
}

export function FeedbackHistoryTable({ rows, selectedId, onSelect }: Readonly<FeedbackHistoryTableProps>) {
    const selectedRowRef = useRef<HTMLTableRowElement | null>(null);
    const isSelectedOnPage = rows.some(row => row.id === selectedId);

    useEffect(() => {
        if (isSelectedOnPage) selectedRowRef.current?.scrollIntoView({ block: "nearest" });
    }, [selectedId, isSelectedOnPage]);

    return (
        <Table className="table-fixed" containerClassName="overflow-x-visible">
            <TableHeader sticky>
                <TableRow>
                    <TableHead className="w-1/6 px-4 font-semibold">Timestamp</TableHead>
                    <TableHead className="w-1/6 px-4 font-semibold">Agent / Workflow</TableHead>
                    <TableHead className="w-1/6 px-4 font-semibold">User</TableHead>
                    <TableHead className="w-32 px-4 font-semibold">Rating</TableHead>
                    <TableHead className="px-4 font-semibold">Comment</TableHead>
                </TableRow>
            </TableHeader>
            <TableBody borderLastRow>
                {rows.map(row => {
                    const SubjectIcon = SUBJECT_TYPE_ICONS[row.subjectType];
                    return (
                        <TableRow
                            key={row.id}
                            ref={row.id === selectedId ? selectedRowRef : undefined}
                            className={onSelect ? "h-14 cursor-pointer focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-(--primary-wMain)" : "h-14"}
                            {...(onSelect && {
                                tabIndex: 0,
                                "aria-selected": row.id === selectedId,
                                "data-state": row.id === selectedId ? "selected" : undefined,
                                onClick: () => onSelect(row),
                                onKeyDown: (e: React.KeyboardEvent<HTMLTableRowElement>) => {
                                    if (e.key !== "Enter" && e.key !== " ") return;
                                    e.preventDefault();
                                    onSelect(row);
                                },
                            })}
                        >
                            <TableCell className="truncate px-4" title={formatFeedbackTimeWithYear(row.createdAt)}>
                                {formatFeedbackTime(row.createdAt)}
                            </TableCell>
                            <TableCell className="truncate px-4" title={subjectLabel(row)}>
                                <div className="flex items-center gap-2">
                                    {SubjectIcon && <SubjectIcon className="h-4 w-4 shrink-0 text-(--secondary-text-wMain)" aria-hidden />}
                                    <span className="truncate">{subjectLabel(row)}</span>
                                </div>
                            </TableCell>
                            <TableCell className="truncate px-4" title={row.userId}>
                                {row.userId}
                            </TableCell>
                            <TableCell className="px-4">
                                <RatingBadge rating={row.rating} />
                            </TableCell>
                            <TableCell className="truncate px-4" title={row.comment ?? undefined}>
                                {row.comment ?? "—"}
                            </TableCell>
                        </TableRow>
                    );
                })}
            </TableBody>
        </Table>
    );
}
