// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/ui/table";

export interface ChartDataColumn {
    key: string;
    label: string;
}

export interface ChartDataRow {
    key: string;
    label: string;
    values: Readonly<Record<string, string>>;
}

interface ChartDataTableProps {
    labelledBy: string;
    rowHeader: string;
    columns: readonly ChartDataColumn[];
    rows: readonly ChartDataRow[];
}

export function ChartDataTable({ labelledBy, rowHeader, columns, rows }: Readonly<ChartDataTableProps>) {
    return (
        <div className="sr-only">
            <Table aria-labelledby={labelledBy}>
                <TableHeader>
                    <TableRow>
                        <TableHead scope="col">{rowHeader}</TableHead>
                        {columns.map(column => (
                            <TableHead key={column.key} scope="col">
                                {column.label}
                            </TableHead>
                        ))}
                    </TableRow>
                </TableHeader>
                <TableBody>
                    {rows.map(row => (
                        <TableRow key={row.key}>
                            <TableHead scope="row">{row.label}</TableHead>
                            {columns.map(column => (
                                <TableCell key={column.key}>{row.values[column.key]}</TableCell>
                            ))}
                        </TableRow>
                    ))}
                </TableBody>
            </Table>
        </div>
    );
}
