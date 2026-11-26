// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Column, RowData } from "@tanstack/react-table";
import { ArrowDown, ArrowUp, ChevronsUpDown, EyeOff } from "lucide-react";

import { cn } from "@/utils/cnTailwind";

import { Button } from "@/ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from "@/ui/dropdown-menu";

import { type AgentTableFeatures } from "./agentTableFeatures";

interface DataTableColumnHeaderProps<TData extends RowData, TValue> extends React.HTMLAttributes<HTMLDivElement> {
    column: Column<AgentTableFeatures, TData, TValue>;
    title: string;
}

export function DataTableColumnHeader<TData extends RowData, TValue>({ column, title, className }: Readonly<DataTableColumnHeaderProps<TData, TValue>>) {
    if (!column.getCanSort()) {
        return <div className={cn(className)}>{title}</div>;
    }

    return (
        <div className={cn("flex items-center space-x-2", className)}>
            <DropdownMenu>
                <DropdownMenuTrigger asChild>
                    <Button variant="ghost" size="sm" className="-ml-3 h-8 data-[state=open]:bg-(--secondary-w10)">
                        <span>{title}</span>
                        {(() => {
                            const sortOrder = column.getIsSorted();
                            if (sortOrder === "desc") return <ArrowDown className="ml-2 h-4 w-4" />;
                            if (sortOrder === "asc") return <ArrowUp className="ml-2 h-4 w-4" />;
                            return <ChevronsUpDown className="ml-2 h-4 w-4" />;
                        })()}
                    </Button>
                </DropdownMenuTrigger>
                <DropdownMenuContent align="start">
                    <DropdownMenuItem onClick={() => column.toggleSorting(false)}>
                        <ArrowUp className="mr-2 h-3.5 w-3.5 text-(--secondary-text-wMain)/70" />
                        Asc
                    </DropdownMenuItem>
                    <DropdownMenuItem onClick={() => column.toggleSorting(true)}>
                        <ArrowDown className="mr-2 h-3.5 w-3.5 text-(--secondary-text-wMain)/70" />
                        Desc
                    </DropdownMenuItem>
                    <DropdownMenuSeparator />
                    <DropdownMenuItem onClick={() => column.toggleVisibility(false)}>
                        <EyeOff className="mr-2 h-3.5 w-3.5 text-(--secondary-text-wMain)/70" />
                        Hide
                    </DropdownMenuItem>
                </DropdownMenuContent>
            </DropdownMenu>
        </div>
    );
}
