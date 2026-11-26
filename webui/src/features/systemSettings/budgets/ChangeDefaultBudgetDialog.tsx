// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { CircleCheck, CircleX } from "lucide-react";

import { PageLabel, PageLabelWithValue, PageValue } from "@/common/layout";

import { Button } from "@/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, VisuallyHidden } from "@/ui/dialog";
import { Spinner } from "@/ui/spinner";
import { TruncatedText } from "@/ui/truncated-text";

import { MessageBanner } from "@/common";
import { formatBudgetLimit } from "@/utils";

import { formatWarnPercentage } from "./format";
import { type BudgetRow, LIMITED_DEFAULT_SYSTEM_USERS_NOTE } from "./types";

interface ChangeDefaultBudgetDialogProps {
    open: boolean;
    onOpenChange: (open: boolean) => void;
    budget: BudgetRow;
    currentDefault: BudgetRow | null;
    isLoadingCurrentDefault: boolean;
    currentDefaultUnavailable: boolean;
    isSaving: boolean;
    saveError: string | null;
    onSave: () => void;
}

export function ChangeDefaultBudgetDialog({ open, onOpenChange, budget, currentDefault, isLoadingCurrentDefault, currentDefaultUnavailable, isSaving, saveError, onSave }: Readonly<ChangeDefaultBudgetDialogProps>) {
    return (
        <Dialog open={open} onOpenChange={next => !isSaving && onOpenChange(next)}>
            <DialogContent className="sm:max-w-2xl">
                <DialogHeader>
                    <DialogTitle>Change Default User Budget</DialogTitle>
                    <VisuallyHidden>
                        <DialogDescription>Review the new default user budget before saving.</DialogDescription>
                    </VisuallyHidden>
                </DialogHeader>

                <div className="mt-4 flex flex-col gap-6">
                    {saveError && <MessageBanner variant="error" message={saveError} />}
                    {isLoadingCurrentDefault ? <Spinner size="medium" /> : <Comparison budget={budget} currentDefault={currentDefault} currentDefaultUnavailable={currentDefaultUnavailable} />}
                </div>

                <DialogFooter className="mt-4">
                    <Button variant="ghost" onClick={() => onOpenChange(false)} disabled={isSaving}>
                        Cancel
                    </Button>
                    <Button onClick={onSave} disabled={isLoadingCurrentDefault} loading={isSaving}>
                        Save
                    </Button>
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
}

function Comparison({ budget, currentDefault, currentDefaultUnavailable }: Readonly<{ budget: BudgetRow; currentDefault: BudgetRow | null; currentDefaultUnavailable: boolean }>) {
    return (
        <>
            {currentDefaultUnavailable && <MessageBanner variant="warning" message="The current default budget couldn't be loaded, so the following comparison doesn't include it." />}
            {budget.amount !== null && <MessageBanner variant="info" message={LIMITED_DEFAULT_SYSTEM_USERS_NOTE} />}
            {currentDefault ? (
                <p>
                    <strong>{currentDefault.name}</strong> remains available but is no longer assigned automatically. Users assigned to <strong>{currentDefault.name}</strong> directly keep that budget. Review the changes in the following comparison.
                </p>
            ) : (
                <p>
                    <strong>{budget.name}</strong> is assigned automatically to every user who has no assigned budget. Review the new default in the following summary.
                </p>
            )}
            <div className="grid grid-cols-2 gap-6">
                {currentDefault && <DefaultColumn heading="Current Default" icon={<CircleX className="size-4 text-(--error-wMain)" />} budget={currentDefault} />}
                <DefaultColumn heading="New Default" icon={<CircleCheck className="size-4 text-(--success-wMain)" />} budget={budget} />
            </div>
        </>
    );
}

function DefaultColumn({ heading, icon, budget }: Readonly<{ heading: string; icon: React.ReactNode; budget: BudgetRow }>) {
    return (
        <section className="flex min-w-0 flex-col gap-4" aria-label={heading}>
            <h3 className="flex items-center gap-2 font-semibold">
                {icon}
                {heading}
            </h3>
            <PageLabelWithValue className="gap-1">
                <PageLabel>Name</PageLabel>
                <PageValue className="flex min-w-0">
                    <TruncatedText text={budget.name} />
                </PageValue>
            </PageLabelWithValue>
            <PageLabelWithValue className="gap-1">
                <PageLabel>Limit (USD)</PageLabel>
                <PageValue>{formatBudgetLimit(budget.amount)}</PageValue>
            </PageLabelWithValue>
            <PageLabelWithValue className="gap-1">
                <PageLabel>Alert Threshold</PageLabel>
                <PageValue>{formatWarnPercentage(budget.warnPercentage)}</PageValue>
            </PageLabelWithValue>
        </section>
    );
}
