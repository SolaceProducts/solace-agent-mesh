// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { PageLabel, PageLabelWithValue, PageValue } from "@/common/layout";

import { formatBudgetLimit } from "@/utils";

import { formatWarnPercentage } from "./format";
import { BUDGET_SCOPE_LABELS, type BudgetRow } from "./types";

function Field({ label, children }: Readonly<{ label: string; children: React.ReactNode }>) {
    return (
        <PageLabelWithValue className="gap-1">
            <PageLabel>{label}</PageLabel>
            <PageValue className="min-w-0">{children}</PageValue>
        </PageLabelWithValue>
    );
}

export function BudgetDetails({ budget }: Readonly<{ budget: BudgetRow }>) {
    return (
        <div className="flex flex-wrap gap-x-12 gap-y-4">
            <Field label="Scope">{BUDGET_SCOPE_LABELS[budget.scope]}</Field>
            <Field label="Period">Calendar month</Field>
            <Field label="Limit (USD)">{formatBudgetLimit(budget.amount)}</Field>
            <Field label="Alert Threshold">{formatWarnPercentage(budget.warnPercentage)}</Field>
        </div>
    );
}
