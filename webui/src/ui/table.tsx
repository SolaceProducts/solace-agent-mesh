// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import * as React from "react";
import { ChevronDown, ChevronsUpDown, ChevronUp } from "lucide-react";

import { cn } from "@/utils";

import { Button } from "./button";
import { Tooltip, TooltipContent, TooltipTrigger } from "./tooltip";

type SortDir = "asc" | "desc";

interface SortableTableHeadProps extends React.ComponentProps<"th"> {
    column: string;
    currentSortKey: string;
    sortDir: SortDir;
    onSort: (column: string) => void;
    headerTooltip?: string;
    numeric?: boolean;
}

function SortableTableHead({ column, currentSortKey, sortDir, onSort, children, className, headerTooltip, numeric = false, ...props }: Readonly<SortableTableHeadProps>) {
    const isActive = currentSortKey === column;
    const sortControl = (
        <Button variant="ghost" size="icon" className={cn("h-auto w-auto gap-1 px-1.5 py-0.5 text-(--primary-text-wMain)", numeric ? "-mr-1.5" : "-ml-1.5")} onClick={() => onSort(column)}>
            {children}
            {isActive ? sortDir === "asc" ? <ChevronUp className="h-3.5 w-3.5" /> : <ChevronDown className="h-3.5 w-3.5" /> : <ChevronsUpDown className="h-3.5 w-3.5 opacity-40" />}
        </Button>
    );

    return (
        <TableHead className={cn("font-semibold", numeric && "text-right", className)} {...props}>
            {headerTooltip ? (
                <Tooltip>
                    <TooltipTrigger asChild>{sortControl}</TooltipTrigger>
                    <TooltipContent>{headerTooltip}</TooltipContent>
                </Tooltip>
            ) : (
                sortControl
            )}
        </TableHead>
    );
}

function Table({ className, containerClassName, ...props }: React.ComponentProps<"table"> & { containerClassName?: string }) {
    return (
        <div data-slot="table-container" className={cn("relative w-full overflow-x-auto", containerClassName)}>
            <table data-slot="table" className={cn("w-full caption-bottom text-sm", className)} {...props} />
        </div>
    );
}

const STICKY_HEADER_CELLS_WITH_BACKGROUND_SEPARATOR =
    "[&_th]:sticky [&_th]:top-0 [&_th]:z-10 [&_th]:bg-(--background-w10) [&_th]:bg-[linear-gradient(var(--secondary-w40),var(--secondary-w40))] [&_th]:bg-[length:100%_1px] [&_th]:bg-bottom [&_th]:bg-no-repeat";

function TableHeader({ className, sticky, ...props }: React.ComponentProps<"thead"> & { sticky?: boolean }) {
    return <thead data-slot="table-header" className={cn(sticky ? STICKY_HEADER_CELLS_WITH_BACKGROUND_SEPARATOR : "[&_tr]:border-b", className)} {...props} />;
}

function TableBody({ className, borderLastRow = false, ...props }: React.ComponentProps<"tbody"> & { borderLastRow?: boolean }) {
    return <tbody data-slot="table-body" className={cn(borderLastRow ? "[&_tr:last-child]:border-b" : "[&_tr:last-child]:border-0", className)} {...props} />;
}

function TableFooter({ className, ...props }: React.ComponentProps<"tfoot">) {
    return <tfoot data-slot="table-footer" className={cn("border-t bg-(--secondary-w10) font-medium [&>tr]:last:border-b-0", className)} {...props} />;
}

function TableRow({ className, ...props }: React.ComponentProps<"tr">) {
    return <tr data-slot="table-row" className={cn("border-b transition-colors hover:bg-(--primary-w10) data-[state=selected]:bg-(--secondary-w10)", className)} {...props} />;
}

function TableHead({ className, ...props }: React.ComponentProps<"th">) {
    return <th data-slot="table-head" className={cn("h-10 px-2 text-left align-middle font-medium whitespace-nowrap text-(--primary-text-wMain) [&:has([role=checkbox])]:pr-0 [&>[role=checkbox]]:translate-y-[2px]", className)} {...props} />;
}

function TableCell({ className, ...props }: React.ComponentProps<"td">) {
    return <td data-slot="table-cell" className={cn("p-2 align-middle whitespace-nowrap [&:has([role=checkbox])]:pr-0 [&>[role=checkbox]]:translate-y-[2px]", className)} {...props} />;
}

function TableCaption({ className, ...props }: React.ComponentProps<"caption">) {
    return <caption data-slot="table-caption" className={cn("mt-4 text-sm text-(--secondary-text-wMain)", className)} {...props} />;
}

export { SortableTableHead, Table, TableBody, TableCaption, TableCell, TableFooter, TableHead, TableHeader, TableRow };
