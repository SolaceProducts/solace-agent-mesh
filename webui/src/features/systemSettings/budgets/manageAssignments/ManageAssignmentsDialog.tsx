// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { Fragment, type ReactNode, useEffect, useRef, useState } from "react";
import { Check, Undo2, X } from "lucide-react";

import type { BudgetAssignment, BudgetSubject, UpdateBudgetAssignmentsRequest } from "@/api/budgets";

import { Badge } from "@/ui/badge";
import { Button } from "@/ui/button";
import { ComboBox, type ComboBoxItem } from "@/ui/combobox";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/ui/dialog";
import { Spinner } from "@/ui/spinner";

import { MessageBanner } from "@/common";
import { cn, getErrorMessage } from "@/utils";

import { type AddChange, type ChangeSubject, MAX_ASSIGNMENT_CHANGES, useUnsavedAssignmentChanges } from "./useUnsavedAssignmentChanges";

export interface ManageAssignmentsDialogProps {
    open: boolean;
    onOpenChange: (open: boolean) => void;
    budget: { id: string; name: string; isDefault: boolean };
    canEdit: boolean;
    assigned: {
        items: readonly BudgetAssignment[];
        total: number;
        isLoading: boolean;
        error: Error | null;
        hasMore: boolean;
        isLoadingMore: boolean;
        onLoadMore: () => void;
    };
    candidates: {
        items: readonly BudgetSubject[];
        isLoading: boolean;
        error: Error | null;
        search: string;
        onSearchChange: (value: string) => void;
    };
    onSave: (changes: UpdateBudgetAssignmentsRequest) => Promise<void>;
}

const subjectOf = (candidate: BudgetSubject): ChangeSubject => ({ id: candidate.id, displayName: candidate.displayName, user: candidate.user });

export function ManageAssignmentsDialog({ open, onOpenChange, budget, canEdit, assigned, candidates, onSave }: Readonly<ManageAssignmentsDialogProps>) {
    const changes = useUnsavedAssignmentChanges();
    const [search, setSearch] = useState("");
    const [saving, setSaving] = useState(false);
    const [saveError, setSaveError] = useState<string | null>(null);
    const addSearchRef = useRef<HTMLDivElement>(null);
    const { reset } = changes;

    useEffect(() => {
        if (open) {
            reset();
            setSearch("");
            setSaveError(null);
        }
    }, [open, reset]);

    const overCap = changes.changeCount > MAX_ASSIGNMENT_CHANGES;
    const hasBanner = budget.isDefault || !!saveError || overCap;
    const removeLabel = budget.isDefault ? "Unpin" : "Remove";
    const removedState = budget.isDefault ? "To Be Unpinned" : "To Be Removed";
    const pendingSummary = pendingChangesSummary(changes, budget.isDefault ? "to unpin" : "to remove");

    const searchFor = (text: string) => {
        setSearch(text);
        candidates.onSearchChange(text);
    };

    const cancelAdd = (id: string) => {
        changes.remove(id);
        addSearchRef.current?.querySelector("input")?.focus();
    };

    const pick = (id: string) => {
        const candidate = candidates.items.find(c => c.id === id);
        if (candidate) changes.add({ subject: subjectOf(candidate), moveFrom: moveFromOf(candidate, budget) });
    };

    const save = async () => {
        setSaving(true);
        setSaveError(null);
        try {
            await onSave(changes.request());
            onOpenChange(false);
        } catch (e) {
            setSaveError(getErrorMessage(e, "Couldn't save the assignments."));
        } finally {
            setSaving(false);
        }
    };

    return (
        <Dialog open={open} onOpenChange={next => (next || !saving) && onOpenChange(next)}>
            <DialogContent className="flex max-h-[85vh] flex-col sm:max-w-3xl">
                <DialogHeader>
                    <DialogTitle>{canEdit ? "Manage Budget Assignments" : "View Budget Assignments"}</DialogTitle>
                    <DialogDescription>
                        {canEdit ? (
                            <>
                                Assign <BudgetName budget={budget} /> to users to limit their spending.
                            </>
                        ) : (
                            <>
                                Users assigned to <BudgetName budget={budget} />.
                            </>
                        )}
                    </DialogDescription>
                </DialogHeader>

                <div className="my-4 flex flex-col gap-4">
                    {hasBanner && (
                        <div className="flex flex-col gap-2">
                            {saveError && <MessageBanner variant="error" message={saveError} />}
                            {overCap && <MessageBanner variant="warning" message={`Save at most ${MAX_ASSIGNMENT_CHANGES.toLocaleString()} changes at a time. You have ${changes.changeCount.toLocaleString()}.`} />}
                            {budget.isDefault && <MessageBanner variant="info" message={`${budget.name} is the default budget, so it applies to users without an assignment. Adding a user here pins them to it.`} />}
                        </div>
                    )}
                    {canEdit && (
                        <div ref={addSearchRef}>
                            <AddSearch search={search} budget={budget} candidates={candidates} changes={changes} onSearchChange={searchFor} onOpen={() => candidates.onSearchChange(search)} onPick={pick} />
                        </div>
                    )}
                </div>

                <div className="flex min-h-0 flex-1 flex-col gap-2">
                    <div className="flex flex-wrap items-baseline justify-between gap-x-4">
                        <h3 className="font-semibold">{`Assigned Users (${assigned.total})`}</h3>
                        <output className="text-(--secondary-text-wMain)">
                            {pendingSummary && (
                                <>
                                    <span className="sr-only">Unsaved changes:</span> {pendingSummary}
                                </>
                            )}
                        </output>
                    </div>
                    <div className="flex min-h-0 flex-1 flex-col gap-2 overflow-y-auto">
                        <AssignedChips assigned={assigned} changes={changes} canEdit={canEdit} removeLabel={removeLabel} removedState={removedState} onCancelAdd={cancelAdd} />
                    </div>
                </div>

                <DialogFooter>
                    {canEdit ? (
                        <>
                            <Button variant="outline" onClick={() => onOpenChange(false)} disabled={saving}>
                                Cancel
                            </Button>
                            <Button onClick={save} disabled={changes.changeCount === 0 || overCap || saving} loading={saving}>
                                Save
                            </Button>
                        </>
                    ) : (
                        <Button variant="outline" onClick={() => onOpenChange(false)}>
                            Close
                        </Button>
                    )}
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
}

type Changes = ReturnType<typeof useUnsavedAssignmentChanges>;
type DialogBudget = ManageAssignmentsDialogProps["budget"];

function BudgetName({ budget }: Readonly<{ budget: DialogBudget }>) {
    return <span className="font-semibold">{budget.name}</span>;
}

function moveFromOf(candidate: BudgetSubject, budget: DialogBudget): AddChange["moveFrom"] {
    const current = candidate.budget;
    return current?.source === "assigned" && current.id !== budget.id ? { id: current.id, name: current.name } : null;
}

function pendingChangesSummary(changes: Changes, removeVerb: string): string {
    const moves = changes.addsToSave.filter(add => add.moveFrom).length;
    const adds = changes.addsToSave.length - moves;
    return [adds > 0 && `${adds} to add`, moves > 0 && `${moves} to move`, changes.removeCount > 0 && `${changes.removeCount} ${removeVerb}`].filter(Boolean).join(", ");
}

function noItemsFoundText(search: string, searchFailed: boolean): string {
    if (!search.trim()) return "Type a name or email.";
    return searchFailed ? "Couldn't search users." : "No users match.";
}

interface AddSearchProps {
    search: string;
    budget: DialogBudget;
    candidates: ManageAssignmentsDialogProps["candidates"];
    changes: Changes;
    onSearchChange: (search: string) => void;
    onOpen: () => void;
    onPick: (id: string) => void;
}

function AddSearch({ search, budget, candidates, changes, onSearchChange, onOpen, onPick }: Readonly<AddSearchProps>) {
    const offered = search.trim() === "" ? [] : candidates.items.filter(c => !changes.isAdded(c.id));
    const items: ComboBoxItem[] = offered.map(candidate => {
        const alreadyHere = candidate.budget?.source === "assigned" && candidate.budget.id === budget.id;
        const label = displayLabel(subjectOf(candidate));
        const email = candidate.user.email ?? undefined;
        return { id: candidate.id, label, subtext: email === label ? undefined : email, disabled: alreadyHere };
    });
    const renderCandidate = (item: ComboBoxItem) => {
        return (
            <>
                <div className="flex min-w-0 items-center justify-between gap-4">
                    <span className="min-w-0 truncate">{item.label}</span>
                    {item.disabled && (
                        <span className="flex shrink-0 items-center gap-2 text-(--secondary-text-wMain)">
                            Current
                            <Check className="size-4" aria-hidden />
                        </span>
                    )}
                </div>
                {item.subtext && <div className="truncate text-xs text-(--secondary-text-wMain)">{item.subtext}</div>}
            </>
        );
    };

    return (
        <ComboBox
            value={undefined}
            onValueChange={id => id && onPick(id)}
            items={items}
            renderItem={renderCandidate}
            searchText={search}
            onSearchTextChange={onSearchChange}
            onOpen={onOpen}
            isLoading={candidates.isLoading}
            placeholder="Add users by name or email"
            noItemsFoundText={noItemsFoundText(search, candidates.error !== null)}
            aria-label="User to add"
            data-testid="manageAssignmentsAddUser"
        />
    );
}

interface AssignedChipsProps {
    assigned: ManageAssignmentsDialogProps["assigned"];
    changes: Changes;
    canEdit: boolean;
    removeLabel: string;
    removedState: string;
    onCancelAdd: (id: string) => void;
}

function AssignedChips({ assigned, changes, canEdit, removeLabel, removedState, onCancelAdd }: Readonly<AssignedChipsProps>) {
    if (assigned.isLoading) return <Spinner size="medium" />;

    const loadError = assigned.error && <MessageBanner variant="error" message={`Couldn't load the assignments: ${getErrorMessage(assigned.error, "unknown error.")}`} />;
    const people = assigned.items.flatMap(signedInPerson);
    const adds = changes.addsToSave;
    if (people.length === 0 && adds.length === 0) {
        return loadError || <p className="rounded-sm bg-(--secondary-w10) p-6 text-center text-(--secondary-text-wMain)">No users are assigned to this budget.</p>;
    }

    return (
        <>
            {loadError}
            <div className="flex flex-col gap-2 rounded-sm border p-3">
                <ul className="flex flex-wrap gap-2" aria-label="Assigned users">
                    {adds.map(({ subject, moveFrom }) => {
                        const label = displayLabel(subject);
                        return (
                            <SubjectChip
                                key={subject.id}
                                subject={subject}
                                state="added"
                                stateText={moveFrom ? `To Be Moved from ${moveFrom.name}` : "To Be Added"}
                                action={{ label: `Cancel adding ${label}`, icon: <X />, onClick: () => onCancelAdd(subject.id) }}
                            />
                        );
                    })}
                    {people.map(({ assignmentId, subject }) => {
                        const label = displayLabel(subject);
                        const removed = changes.isRemoved(subject.id);
                        let action: ChipAction | null = null;
                        if (canEdit) {
                            action = removed ? { label: `Undo removing ${label}`, icon: <Undo2 />, onClick: () => changes.undoRemove(subject.id) } : { label: `${removeLabel} ${label}`, icon: <X />, onClick: () => changes.remove(subject.id) };
                        }
                        return <SubjectChip key={assignmentId} subject={subject} state={removed ? "removed" : "assigned"} stateText={removed ? removedState : undefined} action={action} />;
                    })}
                </ul>
                {assigned.hasMore && (
                    <Button variant="ghost" className="w-full" onClick={assigned.onLoadMore} disabled={assigned.isLoadingMore}>
                        Load more
                    </Button>
                )}
            </div>
        </>
    );
}

interface ChipAction {
    label: string;
    icon: ReactNode;
    onClick: () => void;
}

const CHIP_STATE_STYLE = {
    assigned: { type: undefined, className: "" },
    added: { type: "info", className: "" },
    removed: { type: "warning", className: "line-through" },
} as const;

function SubjectChip({ subject, state, stateText, action }: Readonly<{ subject: ChangeSubject; state: keyof typeof CHIP_STATE_STYLE; stateText?: string; action: ChipAction | null }>) {
    const details = [subject.displayName && subject.user.email, subject.user.isSystemUser && "Service account", stateText].filter((detail): detail is string => Boolean(detail));
    return (
        <li className="max-w-full">
            <Badge type={CHIP_STATE_STYLE[state].type} className={cn("max-w-full gap-1 py-0.5", action && "pr-1", CHIP_STATE_STYLE[state].className)} tooltip={details.join(" · ")} tooltipClassName="max-w-sm wrap-anywhere">
                <span className="min-w-0 truncate">{displayLabel(subject)}</span>
                {details.map(detail => (
                    <Fragment key={detail}>
                        {" "}
                        <span className="sr-only">{detail}</span>
                    </Fragment>
                ))}
                {action && (
                    <Button variant="ghost" size="icon" aria-label={action.label} onClick={action.onClick} className="size-4 rounded-sm p-0 no-underline [&_svg:not([class*='size-'])]:size-3">
                        {action.icon}
                    </Button>
                )}
            </Badge>
        </li>
    );
}

function signedInPerson(assignment: BudgetAssignment): { assignmentId: string; subject: ChangeSubject }[] {
    const { id, displayName, user } = assignment.subject;
    return id === null || user === undefined ? [] : [{ assignmentId: assignment.id, subject: { id, displayName, user } }];
}

function displayLabel(subject: ChangeSubject): string {
    return subject.displayName || subject.user.email || subject.id;
}
