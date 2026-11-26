// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { MorePopoverButton } from "@/common/buttons/MorePopoverButton";

import { getErrorMessage } from "@/utils/api";

import { Menu } from "@/ui/menu";
import { Popover, PopoverContent, PopoverTrigger } from "@/ui/popover";
import { Skeleton } from "@/ui/skeleton";

import { MessageBanner } from "@/common";
import { formatBudgetLimit } from "@/utils";

import { formatWarnPercentage } from "./format";
import type { BudgetRow } from "./types";

interface MeshBudgetSummaryProps {
    budget: BudgetRow | null;
    isLoading?: boolean;
    error?: Error | null;
    canEdit: boolean;
    onEdit: (budget: BudgetRow) => void;
}

export function MeshBudgetSummary({ budget, isLoading, error, canEdit, onEdit }: Readonly<MeshBudgetSummaryProps>) {
    return (
        <section aria-labelledby="mesh-budget-title" data-testid="meshBudgetSummary" className="mb-4 flex flex-col gap-4">
            <h2 id="mesh-budget-title" className="font-semibold">
                Agent Mesh Budget
            </h2>
            <div className="flex items-center gap-x-6 border p-4">
                <div className="min-w-0 flex-1">
                    <MeshBudgetDetails budget={budget} isLoading={isLoading} error={error} />
                </div>
                {!isLoading && !error && budget && canEdit && (
                    <Popover>
                        <PopoverTrigger data-testid="meshBudgetMoreTrigger" asChild>
                            {MorePopoverButton}
                        </PopoverTrigger>
                        <PopoverContent align="end" side="bottom" className="w-50" sideOffset={0}>
                            <Menu actions={[{ id: "edit", label: "Edit Budget", onClick: () => onEdit(budget) }]} />
                        </PopoverContent>
                    </Popover>
                )}
            </div>
        </section>
    );
}

function MeshBudgetDetails({ budget, isLoading, error }: Readonly<Pick<MeshBudgetSummaryProps, "budget" | "isLoading" | "error">>) {
    if (isLoading) return <Skeleton className="h-5 w-80" />;
    if (error) {
        return <MessageBanner variant="error" message={`Couldn't load the Agent Mesh budget. ${getErrorMessage(error, "")}`} />;
    }
    if (!budget) return <span className="text-sm text-(--secondary-text-wMain)">No limit set</span>;
    return (
        <dl className="flex flex-wrap gap-x-12 gap-y-2 text-sm">
            <SummaryField label="Limit (USD)" value={formatBudgetLimit(budget.amount)} />
            <SummaryField label="Alert Threshold" value={formatWarnPercentage(budget.warnPercentage)} />
            <SummaryField label="Period" value="Calendar month" />
        </dl>
    );
}

function SummaryField({ label, value }: Readonly<{ label: string; value: string }>) {
    return (
        <div className="flex min-w-0 flex-col gap-1">
            <dt className="text-(--secondary-text-wMain)">{label}</dt>
            <dd className="truncate" title={value}>
                {value}
            </dd>
        </div>
    );
}
