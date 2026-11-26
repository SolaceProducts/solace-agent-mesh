// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type ReactNode, useEffect, useState } from "react";

import {
    type AppliedTopUp,
    type Budget,
    BUDGET_OPTIONS_PAGE_SIZE,
    useBudgets,
    useCreateBudgetAssignmentMutation,
    useDeleteBudgetAssignmentMutation,
    useDeleteBudgetTopUpMutation,
    useGrantBudgetTopUpMutation,
    useMoveBudgetAssignmentMutation,
} from "@/api/budgets";
import { useUser } from "@/api/rbac";

import { useLocalStorage } from "@/hooks/useLocalStorage";

import { MessageBanner } from "@/common/banners/MessageBanner";
import { PageLabel } from "@/common/layout";

import { Button } from "@/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/ui/dialog";
import { Input } from "@/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/ui/select";

import { DefaultBudgetBadge } from "@/features/systemSettings/budgets/DefaultBudgetBadge";

import type { User } from "@/types";
import { AMOUNT_PATTERN, cn, formatBudgetLimit, formatTopUpExpiry, formatUsd, getErrorMessage, toCents } from "@/utils";

const NONE = "";

interface EditUserBudgetDialogProps {
    user: Pick<User, "id" | "displayName" | "email">;
    open: boolean;
    onOpenChange: (open: boolean) => void;
}

const STALE_STATUSES = new Set([404, 409]);

function saveErrorMessage(error: unknown): string {
    if (STALE_STATUSES.has((error as { status?: number }).status ?? 0)) {
        return "This user's budget was changed by someone else. The dialog has been refreshed; select a budget again and save.";
    }
    return getErrorMessage(error, "Couldn't update the user's budget.");
}

function topUpEdit(text: string, current: AppliedTopUp | null, budget: Budget | undefined) {
    const blocked = budget?.amount === null;
    const entered = text.trim();
    const cancelling = entered === "" && current !== null;
    if (blocked) {
        const budgetName = budget?.name ?? "the budget";
        const keepsHeld = current !== null && AMOUNT_PATTERN.test(entered) && toCents(entered) === toCents(current.amount);
        const error = current !== null && !cancelling && !keepsHeld ? unlimitedTopUpEditError(budgetName) : null;
        return { blocked, entered, error, cancelling, changed: cancelling, help: error ?? unlimitedTopUpHelp(budgetName, keepsHeld ? current : null) };
    }
    const error = topUpAmountError(entered);
    const granting = error === null && entered !== "" && (current === null || toCents(entered) !== toCents(current.amount));
    return { blocked, entered, error, cancelling, changed: cancelling || granting, help: error };
}

function nextPeriodStart(now: Date): string {
    return new Date(Date.UTC(now.getUTCFullYear(), now.getUTCMonth() + 1, 1)).toISOString();
}

function unlimitedTopUpEditError(budgetName: string): string {
    return `Can't change the amount while ${budgetName} is unlimited. Delete it or choose a limited budget.`;
}

function unlimitedTopUpHelp(budgetName: string, held: AppliedTopUp | null): string {
    return held ? `${formatUsd(held.amount)} is kept but has no effect while ${budgetName} is unlimited.` : "Not available when the selected budget is unlimited.";
}

function SplitRow({ children, detail }: Readonly<{ children: ReactNode; detail: string }>) {
    return (
        <span className="flex w-full items-center justify-between gap-4">
            <span className="flex min-w-0 items-center gap-2">{children}</span> <span className="shrink-0 text-(--secondary-text-wMain)">{detail}</span>
        </span>
    );
}

function BudgetRow({ budget, detail }: Readonly<{ budget: Budget; detail: string }>) {
    return (
        <SplitRow detail={detail}>
            <span className="truncate">{budget.name}</span>
            {budget.isDefault && (
                <>
                    {" "}
                    <DefaultBudgetBadge withTooltip={false} />
                </>
            )}
        </SplitRow>
    );
}

const CURRENT_TAG = "Current";

const STRETCH_ITEM_TEXT = "[&>div>span:last-child]:min-w-0 [&>div>span:last-child]:flex-1";

const MAX_TOP_UP_CENTS = 10_000_000n;

function topUpAmountError(text: string): string | null {
    if (text === "") return null;
    if (!AMOUNT_PATTERN.test(text)) return "Enter an amount such as 100 or 100.50";
    const cents = toCents(text);
    if (cents === 0n) return "Enter an amount above 0, or leave it empty for none";
    if (cents > MAX_TOP_UP_CENTS) return `Enter at most ${formatUsd("100000")}`;
    return null;
}

const DEFAULT_HELP = "The user follows the default budget.";
const PINNED_HELP = "The user is pinned to this budget.";

const DEFAULT_INFO_DISMISSED_KEY = "budgets.manageBudgetDefaultInfoDismissed";

const DEFAULT_INFO = "When no budget is assigned, a user follows the default budget. If the default changes, they move with it. Assigning the user to the current default pins them to that budget, even if the default changes.";

export function EditUserBudgetDialog({ user, open, onOpenChange }: Readonly<EditUserBudgetDialogProps>) {
    const userQuery = useUser(user.id, { expandBudget: true }, { enabled: open });
    const budgetsQuery = useBudgets({ pageNumber: 1, pageSize: BUDGET_OPTIONS_PAGE_SIZE, subjectType: "user" });
    const create = useCreateBudgetAssignmentMutation();
    const move = useMoveBudgetAssignmentMutation();
    const remove = useDeleteBudgetAssignmentMutation();
    const grantTopUp = useGrantBudgetTopUpMutation();
    const cancelTopUp = useDeleteBudgetTopUpMutation();

    const current = userQuery.data?.budget;
    const currentTopUp = userQuery.data?.budgetTopUp ?? null;
    const assigned = current?.source === "assigned" && current.assignmentId ? { id: current.assignmentId, budgetId: current.id } : null;
    const budgets = budgetsQuery.data?.data ?? [];
    const defaultBudget = budgets.find(budget => budget.isDefault);

    const [selected, setSelected] = useState(NONE);
    const [topUpText, setTopUpText] = useState("");
    const [saveError, setSaveError] = useState<string | null>(null);
    const [defaultInfoDismissed, setDefaultInfoDismissed] = useLocalStorage(DEFAULT_INFO_DISMISSED_KEY, false);

    const loaded = userQuery.isSuccess && budgetsQuery.isSuccess;
    const loadError = userQuery.error ?? budgetsQuery.error;
    const hasBanner = !!saveError || !!loadError || !defaultInfoDismissed;

    useEffect(() => {
        if (open && loaded) setSelected(assigned?.budgetId ?? NONE);
    }, [open, loaded, assigned?.budgetId]);

    useEffect(() => {
        if (open && loaded) setTopUpText(currentTopUp?.amount ?? "");
    }, [open, loaded, currentTopUp?.amount]);

    const identity = user.displayName || user.email || user.id;
    const budgetChanged = selected !== (assigned?.budgetId ?? NONE);
    const resultingBudget = selected === NONE ? defaultBudget : budgets.find(budget => budget.id === selected);
    const { blocked: topUpBlocked, entered: enteredTopUp, error: topUpError, cancelling, changed: topUpChanged, help: topUpHelp } = topUpEdit(topUpText, currentTopUp, resultingBudget);
    const placeholder = defaultBudget ? (
        <SplitRow detail={`Default limit: ${formatBudgetLimit(defaultBudget.amount)}`}>
            <span className="truncate">No budget assigned</span>
        </SplitRow>
    ) : (
        "No budget assigned"
    );
    const selectedBudget = budgets.find(budget => budget.id === selected);
    const topUpExpiry = formatTopUpExpiry(currentTopUp?.expiresTime ?? nextPeriodStart(new Date()));

    const budgetHelp = selected === NONE ? DEFAULT_HELP : PINNED_HELP;

    const saveBudget = async () => {
        if (selected === NONE && assigned) await remove.mutateAsync(assigned.id);
        else if (selected !== NONE && !assigned) await create.mutateAsync({ subjectType: "user", subjectId: user.id, budgetId: selected });
        else if (selected !== NONE && assigned) await move.mutateAsync({ id: assigned.id, budgetId: selected });
    };

    const saveTopUp = async () => {
        if (cancelling && currentTopUp) await cancelTopUp.mutateAsync(user.id);
        else if (enteredTopUp !== "") await grantTopUp.mutateAsync({ subjectType: "user", subjectId: user.id, amount: enteredTopUp });
    };

    const onSave = async () => {
        setSaveError(null);
        if (budgetChanged) {
            try {
                await saveBudget();
            } catch (e) {
                setSaveError(saveErrorMessage(e));
                return;
            }
        }
        if (topUpChanged) {
            try {
                await saveTopUp();
            } catch (e) {
                const reason = getErrorMessage(e, "Couldn't update the additional amount.");
                setSaveError(budgetChanged ? `The budget was saved, but the additional amount was not: ${reason}` : reason);
                return;
            }
        }
        onOpenChange(false);
    };
    const saving = create.isPending || move.isPending || remove.isPending || grantTopUp.isPending || cancelTopUp.isPending;

    return (
        <Dialog open={open} onOpenChange={next => (next || !saving) && onOpenChange(next)}>
            <DialogContent className="flex max-h-[85vh] flex-col sm:max-w-3xl">
                <DialogHeader>
                    <DialogTitle>Manage Budget</DialogTitle>
                    <DialogDescription>
                        Manage the budget for <span className="font-semibold">{identity}</span>. Assign a budget and, optionally, a temporary additional amount that applies until {topUpExpiry}.
                    </DialogDescription>
                </DialogHeader>
                <div className="flex min-h-0 flex-col gap-2 overflow-y-auto py-4">
                    {hasBanner && (
                        <div className="mb-2 flex flex-col gap-2">
                            {saveError && <MessageBanner variant="error" message={saveError} />}
                            {loadError && <MessageBanner variant="error" message={getErrorMessage(loadError, "Couldn't load budgets. Close and try again.")} />}
                            {!defaultInfoDismissed && <MessageBanner variant="info" message={DEFAULT_INFO} dismissible onDismiss={() => setDefaultInfoDismissed(true)} />}
                        </div>
                    )}
                    <PageLabel>Budget</PageLabel>
                    <div className="flex items-center gap-2">
                        <Select value={selected} onValueChange={setSelected} disabled={!loaded}>
                            <SelectTrigger className="h-9 min-w-0 flex-1 py-0" aria-label="Budget" aria-describedby={loaded && budgetHelp ? "user-budget-help" : undefined}>
                                <SelectValue placeholder={placeholder}>{selectedBudget && <BudgetRow budget={selectedBudget} detail={formatBudgetLimit(selectedBudget.amount)} />}</SelectValue>
                            </SelectTrigger>
                            <SelectContent>
                                {budgets.map(budget => (
                                    <SelectItem key={budget.id} value={budget.id} className={STRETCH_ITEM_TEXT} description={formatBudgetLimit(budget.amount)}>
                                        <BudgetRow budget={budget} detail={budget.id === assigned?.budgetId ? CURRENT_TAG : ""} />
                                    </SelectItem>
                                ))}
                            </SelectContent>
                        </Select>
                        <Button variant="outline" onClick={() => setSelected(NONE)} disabled={!loaded || selected === NONE}>
                            Revert to Default
                        </Button>
                    </div>
                    {loaded && budgetHelp && (
                        <span id="user-budget-help" className="text-xs text-(--secondary-text-wMain)">
                            {budgetHelp}
                        </span>
                    )}

                    <PageLabel htmlFor="user-budget-top-up" className="pt-4">
                        Additional Temporary Amount (USD)
                    </PageLabel>
                    <div className="relative">
                        <span className="pointer-events-none absolute top-1/2 left-3 -translate-y-1/2 text-sm text-(--secondary-text-wMain)" aria-hidden="true">
                            $
                        </span>
                        <Input
                            id="user-budget-top-up"
                            inputMode="decimal"
                            className="pl-7"
                            value={topUpText}
                            onChange={e => setTopUpText(e.target.value)}
                            disabled={!loaded || (topUpBlocked && currentTopUp === null)}
                            aria-invalid={topUpError !== null}
                            aria-describedby={topUpHelp ? "user-budget-top-up-help" : undefined}
                        />
                    </div>
                    <span id="user-budget-top-up-help" className={cn("min-h-4 text-xs", topUpError ? "text-(--error-wMain)" : "text-(--secondary-text-wMain)")}>
                        {topUpHelp}
                    </span>
                </div>
                <DialogFooter>
                    <Button variant="outline" onClick={() => onOpenChange(false)} disabled={saving}>
                        Cancel
                    </Button>
                    <Button onClick={onSave} disabled={saving || !loaded || topUpError !== null || !(budgetChanged || topUpChanged)} loading={saving}>
                        Save
                    </Button>
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
}
