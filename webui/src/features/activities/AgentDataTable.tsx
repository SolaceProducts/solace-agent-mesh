// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import * as React from "react";
import { type ColumnDef, flexRender, type SortingState, useTable } from "@tanstack/react-table";

import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/ui/table";
import { type AgentPerformanceMetrics } from "@/types/activities";

import { type AgentTableFeatures, agentTableFeatures } from "./agentTableFeatures";

interface AgentDataTableProps<TData extends AgentPerformanceMetrics> {
    columns: ColumnDef<AgentTableFeatures, TData>[];
    data: TData[];
    onRowClick: (row: TData) => void;
    selectedRowId?: string;
}

export function AgentDataTable<TData extends AgentPerformanceMetrics>({ columns, data, onRowClick, selectedRowId }: Readonly<AgentDataTableProps<TData>>) {
    const [sorting, setSorting] = React.useState<SortingState>([]);

    const table = useTable({
        features: agentTableFeatures,
        data,
        columns,
        state: {
            sorting,
        },
        onSortingChange: setSorting,
    });

    return (
        <div className="rounded-md border">
            <Table>
                <TableHeader>
                    {table.getHeaderGroups().map(headerGroup => (
                        <TableRow key={headerGroup.id}>
                            {headerGroup.headers.map(header => {
                                return <TableHead key={header.id}>{header.isPlaceholder ? null : flexRender(header.column.columnDef.header, header.getContext())}</TableHead>;
                            })}
                        </TableRow>
                    ))}
                </TableHeader>
                <TableBody borderLastRow>
                    {table.getRowModel().rows?.length ? (
                        table.getRowModel().rows.map(row => (
                            <TableRow key={row.original.instanceId} data-state={row.original.instanceId === selectedRowId && "selected"} onClick={() => onRowClick(row.original)} className="cursor-pointer">
                                {row.getVisibleCells().map(cell => (
                                    <TableCell key={cell.id}>{flexRender(cell.column.columnDef.cell, cell.getContext())}</TableCell>
                                ))}
                            </TableRow>
                        ))
                    ) : (
                        <TableRow>
                            <TableCell colSpan={columns.length} className="h-24 text-center">
                                No agent data available.
                            </TableCell>
                        </TableRow>
                    )}
                </TableBody>
            </Table>
        </div>
    );
}
