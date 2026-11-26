// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { Fragment, type ReactNode, useState } from "react";
import { ChevronDown, ChevronRight } from "lucide-react";

import type { UsageAnySortField, UsageKind, UsageModelRow, UsageRow, UsageServedModel, UsageSortDir, UsageUserRow } from "@/api/usage";

import { HelpPopover } from "@/common/HelpPopover";
import { CurrentBudgetAmount, STATUS_INDICATOR } from "@/common/usage";

import { Button } from "@/ui/button";
import { Progress } from "@/ui/progress";
import { SortableTableHead, Table, TableBody, TableCell, TableHeader, TableRow } from "@/ui/table";

import { SystemUserBadge } from "@/features/userManagement";

import { cn, formatUsagePercent } from "@/utils";

import { formatCount } from "../feedbackFormat";

import { BudgetStatusBadge } from "./BudgetStatusBadge";
import { openFromRow } from "./openFromRow";
import { SubjectName } from "./SubjectName";
import { TableMoney } from "./TableMoney";
import { detailTargetFor, serializeDetailTarget, type UsageDetailTarget } from "./usageDetailTarget";
import { isUnknownUser, KIND_CONFIG, rowKey, servedModelKey, UNKNOWN_USER_HELP } from "./usageKinds";

export interface UsageTableSort {
    key: UsageAnySortField;
    dir: UsageSortDir;
    onSort: (column: string) => void;
}

interface CostBreakdownTableProps {
    kind: UsageKind;
    rows: readonly UsageRow[];
    sort: UsageTableSort;
    isThisMonth: boolean;
    selected?: UsageDetailTarget | null;
    onOpen?: (target: UsageDetailTarget) => void;
}

interface Column {
    key: UsageAnySortField;
    title: string;
    className?: string;
    render: (row: UsageRow) => ReactNode;
    renderServed?: (model: UsageServedModel) => ReactNode;
}

const MODEL_NOT_RECORDED = "Model not recorded";

function ServedModelName({ model }: Readonly<{ model: UsageServedModel }>) {
    if (model.name === null) {
        return (
            <span className="text-(--secondary-text-wMain)" title={MODEL_NOT_RECORDED}>
                <span aria-hidden>—</span>
                <span className="sr-only">{MODEL_NOT_RECORDED}</span>
            </span>
        );
    }
    return (
        <span className="block truncate" title={model.provider ? `${model.provider} · ${model.name}` : model.name}>
            {model.name}
        </span>
    );
}

interface ModelsCellProps {
    row: UsageModelRow;
    expanded: boolean;
    onToggle: () => void;
}

function ModelsCell({ row, expanded, onToggle }: Readonly<ModelsCellProps>) {
    if (row.models.length === 0) return <span className="text-(--secondary-text-wMain)">—</span>;
    if (row.models.length === 1) return <ServedModelName model={row.models[0]} />;
    const Chevron = expanded ? ChevronDown : ChevronRight;
    return (
        <Button variant="link" size="sm" className="h-auto gap-1 px-0 font-normal text-(--primary-text-wMain) has-[>svg]:px-0" aria-expanded={expanded} onClick={onToggle}>
            <Chevron className="size-3.5" aria-hidden />
            {row.models.length} models
        </Button>
    );
}

function ShareCell({ share }: Readonly<{ share: number | null }>) {
    if (share === null) return <>—</>;
    return (
        <div className="flex items-center justify-end gap-2">
            <div className="w-20 shrink-0">
                <Progress value={share} />
            </div>
            <span className="w-12 text-right tabular-nums">{formatUsagePercent(share)}</span>
        </div>
    );
}

function BudgetCell({ row }: Readonly<{ row: UsageUserRow }>) {
    if (!row.budget) return <>—</>;
    return <CurrentBudgetAmount budget={row.budget} />;
}

function BudgetUsageCell({ row }: Readonly<{ row: UsageUserRow }>) {
    const { budget } = row;
    if (budget?.usagePercent == null) return <span className="text-(--secondary-text-wMain)">{budget?.status === "unlimited" ? "No limit" : "—"}</span>;
    return (
        <div className="flex items-center justify-end gap-2">
            <div className="w-24 shrink-0">
                <Progress value={budget.usagePercent} indicatorClassName={STATUS_INDICATOR[budget.status]} />
            </div>
            <span className="w-14 text-right tabular-nums">{formatUsagePercent(budget.usagePercent)}</span>
        </div>
    );
}

function columnsFor(kind: UsageKind, isThisMonth: boolean): Column[] {
    const cost: Column = { key: "cost", title: "Cost", className: "min-w-24", render: row => <TableMoney amount={row.cost} /> };
    const activities: Column = { key: "activities", title: "Activities", className: "min-w-24", render: row => formatCount(row.activities) };
    if (kind === "users") {
        const users: Column[] = [cost, activities, { key: "budget", title: "Current Budget", className: "min-w-36", render: row => <BudgetCell row={row as UsageUserRow} /> }];
        return isThisMonth ? [...users, { key: "budgetUsage", title: "Budget Usage", className: "min-w-52", render: row => <BudgetUsageCell row={row as UsageUserRow} /> }] : users;
    }
    return [
        { ...cost, renderServed: model => <TableMoney amount={model.cost} /> },
        { ...activities, renderServed: model => formatCount(model.activities) },
        { key: "costPerActivity", title: "Cost / Activity", className: "min-w-32", render: row => (row.costPerActivity === null ? "—" : <TableMoney amount={row.costPerActivity} />) },
        { key: "shareOfSpend", title: "Share of Total Cost", className: "min-w-40", render: row => <ShareCell share={row.shareOfSpend} /> },
        { key: "uncachedInputCost", title: "Input", className: "min-w-24", render: row => <TableMoney amount={row.costByBucket.uncachedInput} />, renderServed: model => <TableMoney amount={model.costByBucket.uncachedInput} /> },
        { key: "outputCost", title: "Output", className: "min-w-24", render: row => <TableMoney amount={row.costByBucket.output} />, renderServed: model => <TableMoney amount={model.costByBucket.output} /> },
        { key: "cacheReadCost", title: "Cache Read", className: "min-w-28", render: row => <TableMoney amount={row.costByBucket.cacheRead} />, renderServed: model => <TableMoney amount={model.costByBucket.cacheRead} /> },
        { key: "cacheWriteCost", title: "Cache Write", className: "min-w-28", render: row => <TableMoney amount={row.costByBucket.cacheWrite} />, renderServed: model => <TableMoney amount={model.costByBucket.cacheWrite} /> },
    ];
}

function NameCell({ kind, row, isThisMonth, onOpen }: Readonly<{ kind: UsageKind; row: UsageRow; isThisMonth: boolean; onOpen?: (target: UsageDetailTarget) => void }>) {
    return (
        <div className="flex min-w-0 items-center gap-1 overflow-hidden">
            <SubjectName kind={kind} row={row} onOpen={onOpen} className="shrink" />
            {isUnknownUser(kind, row) && <HelpPopover content={UNKNOWN_USER_HELP} size="16" />}
            {kind === "users" && (row as UsageUserRow).user?.isSystemUser && (
                <span className="ml-1 flex shrink-0">
                    <SystemUserBadge />
                </span>
            )}
            {kind === "users" && (
                <span className="ml-1 flex min-w-0 shrink-[100]">
                    <BudgetStatusBadge budget={(row as UsageUserRow).budget} isThisMonth={isThisMonth} />
                </span>
            )}
        </div>
    );
}

export function CostBreakdownTable({ kind, rows, sort, isThisMonth, selected = null, onOpen }: Readonly<CostBreakdownTableProps>) {
    const columns = columnsFor(kind, isThisMonth);
    const isModels = kind === "models";
    const [expanded, setExpanded] = useState<ReadonlySet<string>>(() => new Set());
    const toggle = (key: string) =>
        setExpanded(open => {
            const next = new Set(open);
            if (!next.delete(key)) next.add(key);
            return next;
        });
    return (
        <Table containerClassName="overflow-x-visible">
            <TableHeader sticky>
                <TableRow className="hover:bg-transparent">
                    <SortableTableHead column="name" currentSortKey={sort.key} sortDir={sort.dir} onSort={sort.onSort} className="w-full min-w-48 px-4">
                        {KIND_CONFIG[kind].column}
                    </SortableTableHead>
                    {isModels && (
                        <SortableTableHead column="model" currentSortKey={sort.key} sortDir={sort.dir} onSort={sort.onSort} className="min-w-40 px-4">
                            Model
                        </SortableTableHead>
                    )}
                    {columns.map(column => (
                        <SortableTableHead key={column.key} column={column.key} currentSortKey={sort.key} sortDir={sort.dir} onSort={sort.onSort} numeric className={cn("px-4", column.className)}>
                            {column.title}
                        </SortableTableHead>
                    ))}
                </TableRow>
            </TableHeader>
            <TableBody borderLastRow>
                {rows.map((row, index) => {
                    const key = rowKey(row, index);
                    const served = isModels ? (row as UsageModelRow).models : [];
                    const isOpen = served.length > 1 && expanded.has(key);
                    const target = detailTargetFor(kind, row);
                    const isSelected = target !== null && selected !== null && serializeDetailTarget(target) === serializeDetailTarget(selected);
                    return (
                        <Fragment key={key}>
                            <TableRow
                                onClick={target && onOpen ? event => openFromRow(event, target, onOpen) : undefined}
                                className={cn(target && onOpen && "cursor-pointer hover:bg-(--secondary-w10)/50")}
                                data-state={isSelected ? "selected" : undefined}
                            >
                                <TableCell className="max-w-64 px-4">
                                    <NameCell kind={kind} row={row} isThisMonth={isThisMonth} onOpen={onOpen} />
                                </TableCell>
                                {isModels && (
                                    <TableCell className="max-w-56 px-4">
                                        <ModelsCell row={row as UsageModelRow} expanded={isOpen} onToggle={() => toggle(key)} />
                                    </TableCell>
                                )}
                                {columns.map(column => (
                                    <TableCell key={column.key} className="px-4 text-right tabular-nums">
                                        {column.render(row)}
                                    </TableCell>
                                ))}
                            </TableRow>
                            {isOpen &&
                                served.map(model => (
                                    <TableRow key={`${key}:${servedModelKey(model)}`} className="bg-(--secondary-w10) text-sm">
                                        <TableCell className="px-4">
                                            <span className="sr-only">{row.name}</span>
                                        </TableCell>
                                        <TableCell className="max-w-56 pr-4 pl-8">
                                            <ServedModelName model={model} />
                                        </TableCell>
                                        {columns.map(column => (
                                            <TableCell key={column.key} className="px-4 text-right text-(--secondary-text-wMain) tabular-nums">
                                                {column.renderServed?.(model)}
                                            </TableCell>
                                        ))}
                                    </TableRow>
                                ))}
                        </Fragment>
                    );
                })}
            </TableBody>
        </Table>
    );
}
