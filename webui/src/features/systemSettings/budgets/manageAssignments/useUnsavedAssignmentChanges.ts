// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useMemo, useState } from "react";

import type { BudgetSubjectUserIdentity, UpdateBudgetAssignmentsRequest } from "@/api/budgets";

export interface ChangeSubject {
    id: string;
    displayName: string | null;
    user: BudgetSubjectUserIdentity;
}

export interface AddChange {
    subject: ChangeSubject;
    moveFrom: { id: string; name: string } | null;
}

export const MAX_ASSIGNMENT_CHANGES = 1000;

export function useUnsavedAssignmentChanges() {
    const [adds, setAdds] = useState<ReadonlyMap<string, AddChange>>(new Map());
    const [removes, setRemoves] = useState<ReadonlySet<string>>(new Set());

    const add = useCallback(
        (change: AddChange) => {
            const id = change.subject.id;
            if (removes.has(id)) {
                setRemoves(current => withoutId(current, id));
                return;
            }
            setAdds(current => new Map(current).set(id, change));
        },
        [removes]
    );

    const remove = useCallback(
        (id: string) => {
            if (adds.has(id)) {
                setAdds(current => {
                    const next = new Map(current);
                    next.delete(id);
                    return next;
                });
                return;
            }
            setRemoves(current => new Set(current).add(id));
        },
        [adds]
    );

    const undoRemove = useCallback((id: string) => setRemoves(current => withoutId(current, id)), []);

    const reset = useCallback(() => {
        setAdds(new Map());
        setRemoves(new Set());
    }, []);

    return useMemo(
        () => ({
            addsToSave: [...adds.values()],
            isAdded: (id: string) => adds.has(id),
            isRemoved: (id: string) => removes.has(id),
            removeCount: removes.size,
            changeCount: adds.size + removes.size,
            request: (): UpdateBudgetAssignmentsRequest => ({ subjectType: "user", add: [...adds.keys()], remove: [...removes] }),
            add,
            remove,
            undoRemove,
            reset,
        }),
        [adds, removes, add, remove, undoRemove, reset]
    );
}

function withoutId(ids: ReadonlySet<string>, id: string): ReadonlySet<string> {
    const next = new Set(ids);
    next.delete(id);
    return next;
}
