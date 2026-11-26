// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useState } from "react";

import type { UsageSortDir, UsageUserRow } from "@/api/usage";

import { MorePopoverButton } from "@/common/buttons/MorePopoverButton";
import { HelpPopover } from "@/common/HelpPopover";

import { Menu, type MenuAction } from "@/ui/menu";
import { Popover, PopoverContent, PopoverTrigger } from "@/ui/popover";
import { SortableTableHead, Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/ui/table";

import { EditUserBudgetDialog, SystemUserBadge } from "@/features/userManagement";

import { formatBudgetMoney, formatTopUpExpiry, formatUsagePercent } from "@/utils";

import { BudgetStatusBadge } from "./BudgetStatusBadge";
import { SubjectName } from "./SubjectName";
import { TableMoney } from "./TableMoney";
import { detailTargetFor, type UsageDetailTarget } from "./usageDetailTarget";
import { rowKey } from "./usageKinds";

export type BudgetUsersSortField = "name" | "cost" | "budgetUsage";

interface BudgetUsersTableProps {
    rows: readonly UsageUserRow[];
    sort: { key: BudgetUsersSortField; dir: UsageSortDir; onSort: (column: string) => void };
    onViewUsage: (target: UsageDetailTarget) => void;
    canManageBudget: boolean;
}

function AdditionalAmountCell({ row }: Readonly<{ row: UsageUserRow }>) {
    const topUp = row.budget?.topUp;
    if (!topUp) return <span className="text-(--secondary-text-wMain)">—</span>;
    return <>+{formatBudgetMoney(topUp.amount)}</>;
}

function additionalAmountHelp(now: Date): string {
    const ends = new Date(Date.UTC(now.getUTCFullYear(), now.getUTCMonth() + 1, 1)).toISOString();
    return `An additional amount raises a user's budget for this month only. It ends ${formatTopUpExpiry(ends)}.`;
}

function UsageCell({ row }: Readonly<{ row: UsageUserRow }>) {
    const { budget } = row;
    if (budget?.usagePercent == null) return <span className="text-(--secondary-text-wMain)">{budget?.status === "unlimited" ? "No limit" : "—"}</span>;
    return <>{formatUsagePercent(budget.usagePercent)}</>;
}

function rowActions(row: UsageUserRow, onViewUsage: (target: UsageDetailTarget) => void, onManageBudget: ((row: UsageUserRow) => void) | null): MenuAction[] {
    const actions: MenuAction[] = [];
    const target = detailTargetFor("users", row);
    if (target) actions.push({ id: "viewUsage", label: "View Usage", onClick: () => onViewUsage(target) });
    if (row.id && onManageBudget) actions.push({ id: "manageBudget", label: "Manage Budget", onClick: () => onManageBudget(row) });
    return actions;
}

function UserRowActions({ actions }: Readonly<{ actions: MenuAction[] }>) {
    return (
        <Popover>
            <PopoverTrigger data-testid="budgetUserMoreTrigger" asChild>
                {MorePopoverButton}
            </PopoverTrigger>
            <PopoverContent align="end" side="bottom" className="w-auto" sideOffset={0}>
                <Menu actions={actions} />
            </PopoverContent>
        </Popover>
    );
}

export function BudgetUsersTable({ rows, sort, onViewUsage, canManageBudget }: Readonly<BudgetUsersTableProps>) {
    const [managed, setManaged] = useState<UsageUserRow | null>(null);
    return (
        <>
            <Table containerClassName="overflow-x-auto">
                <TableHeader sticky>
                    <TableRow className="hover:bg-transparent">
                        <SortableTableHead column="name" currentSortKey={sort.key} sortDir={sort.dir} onSort={sort.onSort} className="w-full min-w-48 px-4">
                            Name
                        </SortableTableHead>
                        <TableHead className="min-w-44 px-4 text-right font-semibold">
                            <span className="flex items-center justify-end gap-1">
                                Additional Amount
                                <HelpPopover content={additionalAmountHelp(new Date())} size="16" />
                            </span>
                        </TableHead>
                        <SortableTableHead column="cost" currentSortKey={sort.key} sortDir={sort.dir} onSort={sort.onSort} className="min-w-24 px-4" numeric>
                            Cost
                        </SortableTableHead>
                        <SortableTableHead column="budgetUsage" currentSortKey={sort.key} sortDir={sort.dir} onSort={sort.onSort} className="min-w-32 px-4" numeric>
                            Budget Usage
                        </SortableTableHead>
                        <TableHead className="w-12 px-4">
                            <span className="sr-only">Actions</span>
                        </TableHead>
                    </TableRow>
                </TableHeader>
                <TableBody borderLastRow>
                    {rows.map((row, index) => {
                        const actions = rowActions(row, onViewUsage, canManageBudget ? setManaged : null);
                        return (
                            <TableRow key={rowKey(row, index)}>
                                <TableCell className="max-w-64 px-4">
                                    <div className="flex min-w-0 items-center gap-1 overflow-hidden">
                                        <SubjectName kind="users" row={row} className="shrink" />
                                        {row.user?.isSystemUser && (
                                            <span className="ml-1 flex shrink-0">
                                                <SystemUserBadge />
                                            </span>
                                        )}
                                        <span className="ml-1 flex min-w-0 shrink-[100]">
                                            <BudgetStatusBadge budget={row.budget} isThisMonth />
                                        </span>
                                    </div>
                                </TableCell>
                                <TableCell className="px-4 text-right tabular-nums">
                                    <AdditionalAmountCell row={row} />
                                </TableCell>
                                <TableCell className="px-4 text-right tabular-nums">
                                    <TableMoney amount={row.cost} />
                                </TableCell>
                                <TableCell className="px-4 text-right tabular-nums">
                                    <UsageCell row={row} />
                                </TableCell>
                                <TableCell className="px-4">{actions.length > 0 && <UserRowActions actions={actions} />}</TableCell>
                            </TableRow>
                        );
                    })}
                </TableBody>
            </Table>
            {managed?.id && <EditUserBudgetDialog user={{ id: managed.id, displayName: managed.name ?? undefined, email: managed.user?.email ?? undefined }} open onOpenChange={open => !open && setManaged(null)} />}
        </>
    );
}
