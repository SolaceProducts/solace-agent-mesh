// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { Loader2, MoreHorizontal, Trash2 } from "lucide-react";

import type { TaskExecution } from "@/types/scheduled-tasks";

import { getStatusBadge, IN_PROGRESS_STATUSES } from "@/features/scheduledTasks/StatusBadge";

import { PaginationControls } from "@/common";
import { Button, DatePicker, DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger, Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/ui";
import { cn } from "@/utils";

import { formatDurationVerbose, formatExecutionLabel, PAGE_SIZE } from "./helpers";

interface ExecutionHistoryTableProps {
    executions: TaskExecution[];
    totalCount: number;
    page: number;
    onPageChange: (p: number) => void;
    onRowClick: (executionId: string) => void;
    isLoading: boolean;
    onDeleteExecution: (execution: TaskExecution) => void;
    filterFrom: string;
    filterTo: string;
    onFilterFromChange: (value: string) => void;
    onFilterToChange: (value: string) => void;
}

export const ExecutionHistoryTable: React.FC<ExecutionHistoryTableProps> = ({ executions, totalCount, page, onPageChange, onRowClick, isLoading, onDeleteExecution, filterFrom, filterTo, onFilterFromChange, onFilterToChange }) => {
    const totalPages = Math.max(1, Math.ceil(totalCount / PAGE_SIZE));
    const showingFrom = totalCount === 0 ? 0 : (page - 1) * PAGE_SIZE + 1;
    const showingTo = Math.min(page * PAGE_SIZE, totalCount);

    const hasFilter = !!(filterFrom || filterTo);

    const renderBody = () => {
        if (isLoading && executions.length === 0) {
            return (
                <TableRow>
                    <TableCell colSpan={4} className="py-8 text-center text-sm text-(--secondary-text-wMain)">
                        <Loader2 className="mx-auto h-4 w-4 animate-spin" />
                    </TableCell>
                </TableRow>
            );
        }
        if (executions.length === 0) {
            return (
                <TableRow>
                    <TableCell colSpan={4} className="py-8 text-center text-sm text-(--secondary-text-wMain) italic">
                        No executions yet.
                    </TableCell>
                </TableRow>
            );
        }
        return executions.map(ex => {
            const isInFlight = IN_PROGRESS_STATUSES.has(ex.status);
            return (
                <TableRow key={ex.id} className="hover:bg-(--secondary-w10)">
                    <TableCell className="px-4">
                        <Button variant="link" className="h-auto p-0" onClick={() => onRowClick(ex.id)}>
                            {formatExecutionLabel(ex)}
                        </Button>
                    </TableCell>
                    <TableCell className="px-4">
                        {isInFlight ? (
                            <span className="inline-flex items-center gap-1.5 text-sm text-(--primary-text-wMain)">
                                <Loader2 className="h-3.5 w-3.5 animate-spin text-(--brand-wMain)" />
                                {ex.status === "pending" ? "Pending" : "In progress"}
                            </span>
                        ) : (
                            getStatusBadge(ex.status)
                        )}
                    </TableCell>
                    <TableCell className="hidden px-4 sm:table-cell">{ex.durationMs ? formatDurationVerbose(ex.durationMs) : "—"}</TableCell>
                    <TableCell className="w-10" onClick={e => e.stopPropagation()}>
                        <DropdownMenu>
                            <DropdownMenuTrigger asChild>
                                <Button variant="ghost" size="icon" className="h-8 w-8">
                                    <MoreHorizontal className="h-4 w-4" />
                                </Button>
                            </DropdownMenuTrigger>
                            <DropdownMenuContent align="end">
                                <DropdownMenuItem disabled={isInFlight} onSelect={() => onDeleteExecution(ex)}>
                                    <Trash2 size={14} className="mr-2" />
                                    Delete
                                </DropdownMenuItem>
                            </DropdownMenuContent>
                        </DropdownMenu>
                    </TableCell>
                </TableRow>
            );
        });
    };

    return (
        <section>
            <h3 className="mb-3 text-base font-semibold">Execution History</h3>

            <div className="mb-3 flex flex-wrap items-center gap-2">
                <span className="text-xs text-(--secondary-text-wMain)">Date Range</span>
                <DatePicker value={filterFrom} onChange={onFilterFromChange} placeholder="YYYY-MM-DD" className={cn("w-36 sm:w-48", filterFrom && "text-(--primary-text-wMain)")} />
                <span className="text-sm text-(--secondary-text-wMain)">to</span>
                <DatePicker value={filterTo} onChange={onFilterToChange} min={filterFrom || undefined} placeholder="YYYY-MM-DD" className={cn("w-36 sm:w-48", filterTo && "text-(--primary-text-wMain)")} />
                {hasFilter && (
                    <Button
                        variant="ghost"
                        size="sm"
                        onClick={() => {
                            onFilterFromChange("");
                            onFilterToChange("");
                        }}
                    >
                        Clear
                    </Button>
                )}
            </div>

            <div className="overflow-hidden rounded-md border">
                <Table>
                    <TableHeader>
                        <TableRow className="hover:bg-transparent">
                            <TableHead className="px-4 font-semibold">Run Time</TableHead>
                            <TableHead className="px-4 font-semibold">Status</TableHead>
                            <TableHead className="hidden px-4 font-semibold sm:table-cell">Duration</TableHead>
                            <TableHead className="w-10" />
                        </TableRow>
                    </TableHeader>
                    <TableBody borderLastRow>{renderBody()}</TableBody>
                </Table>
            </div>

            {totalCount > 0 && (
                <PaginationControls
                    variant="compact"
                    totalPages={totalPages}
                    currentPage={page}
                    onPageChange={onPageChange}
                    summary={
                        <>
                            Showing {showingFrom}-{showingTo} of {totalCount} results
                        </>
                    }
                />
            )}
        </section>
    );
};
