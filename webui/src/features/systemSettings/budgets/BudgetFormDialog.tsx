// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect } from "react";
import { zodResolver } from "@hookform/resolvers/zod";
import { useForm } from "react-hook-form";

import { DuplicateBudgetNameError } from "@/api/budgets";

import { MessageBanner } from "@/common/banners/MessageBanner";
import { CustomCheckbox } from "@/common/CustomCheckbox";
import { PageLabel, PageLabelWithValue, PageValue } from "@/common/layout";

import { getErrorMessage } from "@/utils/api";

import { Button } from "@/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/ui/dialog";
import { Input } from "@/ui/input";

import { cn } from "@/utils";

import { type BudgetFormInput, budgetFormSchema, type BudgetFormValues, budgetToFormInput, emptyBudgetForm, formInputToValues } from "./budgetForm.schema";
import { BUDGET_SCOPE_LABELS, type BudgetRow, type BudgetScope, LIMITED_DEFAULT_SYSTEM_USERS_NOTE, MESH_BUDGET_NAME } from "./types";

const ADORNMENT_DISABLED = "opacity-50";

const WARN_HINT = "Send an alert at this usage percentage. No requests are blocked.";

const SCOPE_HINTS: Record<BudgetScope, { limit: string; unlimited: string }> = {
    user: {
        limit: "Block a user's requests when their spending reaches this limit.",
        unlimited: "Users on this budget have no spending limit.",
    },
    mesh: {
        limit: "Block all requests when Agent Mesh spending reaches this limit.",
        unlimited: "Agent Mesh has no spending limit.",
    },
};

function dialogTitle(isEdit: boolean, isMesh: boolean): string {
    const noun = isMesh ? "Agent Mesh Budget" : "User Budget";
    return `${isEdit ? "Edit" : "Create"} ${noun}`;
}

function DescriptionText({ budget, isMesh }: Readonly<{ budget: BudgetRow | null | undefined; isMesh: boolean }>) {
    if (isMesh) return "Set the spending limit for all of Agent Mesh.";
    if (!budget) return "Name the budget and set its spending limit.";
    return (
        <>
            Update the <span className="font-semibold">{budget.name}</span> budget.
        </>
    );
}

interface BudgetFormDialogProps {
    open: boolean;
    onOpenChange: (open: boolean) => void;
    budget?: BudgetRow | null;
    scope?: BudgetScope;
    onSubmit: (values: BudgetFormValues) => Promise<void>;
}

export function BudgetFormDialog({ open, onOpenChange, budget, scope: createScope = "user", onSubmit }: Readonly<BudgetFormDialogProps>) {
    const isEdit = budget != null;
    const scope = budget?.scope ?? createScope;
    const isMesh = scope === "mesh";

    const {
        register,
        handleSubmit,
        reset,
        setError,
        setValue,
        clearErrors,
        watch,
        formState: { errors, isSubmitting },
    } = useForm<BudgetFormInput>({
        resolver: zodResolver(budgetFormSchema),
        defaultValues: emptyBudgetForm,
    });

    useEffect(() => {
        if (open) reset(budget ? budgetToFormInput(budget) : { ...emptyBudgetForm, scope, name: scope === "mesh" ? MESH_BUDGET_NAME : "" });
    }, [open, budget, scope, reset]);

    const unlimited = watch("unlimited");

    const setUnlimited = (checked: boolean) => {
        setValue("unlimited", checked);
        if (checked) {
            setValue("amount", "");
            setValue("warnPercentage", "");
            clearErrors(["amount", "warnPercentage"]);
        }
    };

    const submit = handleSubmit(async input => {
        try {
            await onSubmit(formInputToValues(input));
            onOpenChange(false);
        } catch (e) {
            const message = getErrorMessage(e, "Couldn't save the budget.");
            setError(!isMesh && e instanceof DuplicateBudgetNameError ? "name" : "root", { message });
        }
    });

    return (
        <Dialog open={open} onOpenChange={next => !isSubmitting && onOpenChange(next)}>
            <DialogContent className="sm:max-w-xl">
                <DialogHeader>
                    <DialogTitle>{dialogTitle(isEdit, isMesh)}</DialogTitle>
                    <DialogDescription>
                        <DescriptionText budget={budget} isMesh={isMesh} />
                    </DialogDescription>
                </DialogHeader>

                <form onSubmit={submit} noValidate className="mt-4 flex flex-col gap-4">
                    {errors.root && <MessageBanner variant="error" message={errors.root.message} />}
                    {budget?.isDefault && !isMesh && !unlimited && <MessageBanner variant="info" message={LIMITED_DEFAULT_SYSTEM_USERS_NOTE} />}

                    {!isMesh && (
                        <div className="flex flex-col gap-1">
                            <PageLabel htmlFor="budget-name" required>
                                Name
                            </PageLabel>
                            <Input id="budget-name" {...register("name")} aria-invalid={Boolean(errors.name)} aria-describedby={errors.name ? "budget-name-error" : undefined} />
                            {errors.name && (
                                <span id="budget-name-error" className="text-xs text-(--error-wMain)">
                                    {errors.name.message}
                                </span>
                            )}
                        </div>
                    )}

                    <div className="grid grid-cols-2 gap-4" data-testid="budgetScopeAndPeriod">
                        <PageLabelWithValue className="gap-1" data-testid="budgetScope">
                            <PageLabel>Scope</PageLabel>
                            <PageValue>{BUDGET_SCOPE_LABELS[scope]}</PageValue>
                        </PageLabelWithValue>
                        <PageLabelWithValue className="gap-1">
                            <PageLabel>Period</PageLabel>
                            <PageValue>Calendar month</PageValue>
                        </PageLabelWithValue>
                    </div>

                    <div className="flex flex-col gap-1">
                        <PageLabel htmlFor="budget-amount" required={!unlimited}>
                            Limit (USD)
                        </PageLabel>
                        <div className="flex items-center gap-4">
                            <div className="relative w-50">
                                <span className={cn("pointer-events-none absolute top-1/2 left-3 -translate-y-1/2 text-sm text-(--secondary-text-wMain)", unlimited && ADORNMENT_DISABLED)} aria-hidden="true">
                                    $
                                </span>
                                <Input id="budget-amount" inputMode="decimal" className="pl-7" disabled={unlimited} {...register("amount")} aria-invalid={Boolean(errors.amount)} aria-describedby="budget-amount-help" />
                            </div>
                            <label htmlFor="budget-unlimited" className="flex shrink-0 cursor-pointer items-center gap-2 text-sm">
                                <CustomCheckbox id="budget-unlimited" checked={unlimited} onChange={e => setUnlimited(e.target.checked)} />
                                Unlimited
                            </label>
                        </div>
                        {errors.amount ? (
                            <span id="budget-amount-help" className="text-xs text-(--error-wMain)">
                                {errors.amount.message}
                            </span>
                        ) : (
                            <span id="budget-amount-help" className="text-xs text-(--secondary-text-wMain)">
                                {unlimited ? SCOPE_HINTS[scope].unlimited : SCOPE_HINTS[scope].limit}
                            </span>
                        )}
                    </div>

                    <div className="flex flex-col gap-1">
                        <PageLabel htmlFor="budget-warn">Alert Threshold</PageLabel>
                        <div className="relative w-25">
                            <Input id="budget-warn" inputMode="numeric" className="pr-7" disabled={unlimited} {...register("warnPercentage")} aria-invalid={Boolean(errors.warnPercentage)} aria-describedby="budget-warn-help" />
                            <span className={cn("pointer-events-none absolute top-1/2 right-3 -translate-y-1/2 text-sm text-(--secondary-text-wMain)", unlimited && ADORNMENT_DISABLED)} aria-hidden="true">
                                %
                            </span>
                        </div>
                        {errors.warnPercentage ? (
                            <span id="budget-warn-help" className="text-xs text-(--error-wMain)">
                                {errors.warnPercentage.message}
                            </span>
                        ) : (
                            <span id="budget-warn-help" className="text-xs text-(--secondary-text-wMain)">
                                {unlimited ? "Set a limit to add an alert threshold." : WARN_HINT}
                            </span>
                        )}
                    </div>

                    <DialogFooter>
                        <Button type="button" variant="ghost" onClick={() => onOpenChange(false)} disabled={isSubmitting}>
                            Cancel
                        </Button>
                        <Button type="submit" disabled={isSubmitting}>
                            {isEdit ? "Save" : "Create"}
                        </Button>
                    </DialogFooter>
                </form>
            </DialogContent>
        </Dialog>
    );
}
