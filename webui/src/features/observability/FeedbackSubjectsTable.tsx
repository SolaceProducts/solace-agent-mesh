// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type React from "react";

import type { FeedbackSummaryRow, FeedbackSummarySort } from "@/api/feedback";

import { SortableTableHead, Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/ui/table";

import { formatCount, formatNegativeRatio } from "./feedbackFormat";
import { SUBJECT_TYPE_ICONS } from "./feedbackLabels";
import { TrendSparkline } from "./TrendSparkline";

interface SubjectRef {
    id: string;
    name: string;
    type: "agent" | "workflow";
}

interface FeedbackSubjectsTableProps {
    rows: readonly FeedbackSummaryRow[];
    sortKey: FeedbackSummarySort;
    sortDir: "asc" | "desc";
    onSort: (column: string) => void;
    onSelectSubject: (subject: SubjectRef) => void;
}

export const NEGATIVE_RATIO_TOOLTIP = "Negative feedback divided by total activities in the selected window — not by the number of rated activities.";

const COLUMNS: ReadonlyArray<{ key: FeedbackSummarySort; label: string; className: string; tooltip?: string }> = [
    { key: "name", label: "Agent / Workflow", className: "w-[19%]" },
    { key: "type", label: "Type", className: "w-[13%]" },
    { key: "totalActivities", label: "Total Activities", className: "w-[13%]" },
    { key: "positiveFeedback", label: "Positive Feedback", className: "w-[15%]" },
    { key: "negativeFeedback", label: "Negative Feedback", className: "w-[15%]" },
    { key: "negativeRatio", label: "Negative Ratio (%)", className: "w-[15%]", tooltip: NEGATIVE_RATIO_TOOLTIP },
];

const WRAPPING_HEADER = "whitespace-normal [&_button]:whitespace-normal [&_button]:text-left";

export function FeedbackSubjectsTable({ rows, sortKey, sortDir, onSort, onSelectSubject }: Readonly<FeedbackSubjectsTableProps>) {
    const subjectRows = rows.flatMap(row => (row.subject ? [{ row, subject: row.subject }] : []));

    return (
        <Table className="table-fixed" containerClassName="overflow-x-visible">
            <TableHeader sticky>
                <TableRow>
                    {COLUMNS.map(column => (
                        <SortableTableHead key={column.key} column={column.key} currentSortKey={sortKey} sortDir={sortDir} onSort={onSort} headerTooltip={column.tooltip} className={`px-4 ${WRAPPING_HEADER} ${column.className}`}>
                            {column.label}
                        </SortableTableHead>
                    ))}
                    <TableHead className="w-[10%] px-4 font-semibold">Trend</TableHead>
                </TableRow>
            </TableHeader>
            <TableBody borderLastRow>
                {subjectRows.map(({ row, subject }) => {
                    const TypeIcon = SUBJECT_TYPE_ICONS[subject.type];
                    return (
                        <TableRow
                            key={subject.id}
                            className="h-14 cursor-pointer focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-(--primary-wMain)"
                            tabIndex={0}
                            onClick={() => onSelectSubject(subject)}
                            onKeyDown={(event: React.KeyboardEvent<HTMLTableRowElement>) => {
                                if (event.key !== "Enter" && event.key !== " ") return;
                                event.preventDefault();
                                onSelectSubject(subject);
                            }}
                        >
                            <TableCell className="truncate px-4" title={subject.name || subject.id}>
                                {subject.name || subject.id}
                            </TableCell>
                            <TableCell className="px-4">
                                <div className="flex items-center gap-2">
                                    {TypeIcon && <TypeIcon className="h-4 w-4 shrink-0 text-(--secondary-text-wMain)" aria-hidden />}
                                    <span className="truncate capitalize">{subject.type}</span>
                                </div>
                            </TableCell>
                            <TableCell className="px-4">{formatCount(row.totalActivities)}</TableCell>
                            <TableCell className="px-4">{formatCount(row.positiveFeedback)}</TableCell>
                            <TableCell className="px-4 font-semibold">{formatCount(row.negativeFeedback)}</TableCell>
                            <TableCell className="px-4 font-semibold">{formatNegativeRatio(row.negativeRatio)}</TableCell>
                            <TableCell className="px-4">
                                <TrendSparkline trend={row.trend} subjectName={subject.name || subject.id} />
                            </TableCell>
                        </TableRow>
                    );
                })}
            </TableBody>
        </Table>
    );
}
