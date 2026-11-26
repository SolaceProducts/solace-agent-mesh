// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useState } from "react";

import { useBudgetAssignments, useBudgetSubjects, useUpdateBudgetAssignmentsMutation } from "@/api/budgets";

import { useChatContext, useUserContext } from "@/providers/contexts";

import { useDebounce } from "@/hooks";

import type { BudgetRow } from "../types";

import { ManageAssignmentsDialog } from "./ManageAssignmentsDialog";
import { summarizeAssignmentChanges } from "./summary";

const SEARCH_DEBOUNCE_MS = 300;
const CANDIDATES_SHOWN = 10;

interface ManageAssignmentsDialogContainerProps {
    budget: BudgetRow | null;
    onClose: () => void;
}

export function ManageAssignmentsDialogContainer({ budget, onClose }: Readonly<ManageAssignmentsDialogContainerProps>) {
    const { userHasCapability, CAPABILITIES } = useUserContext();
    const { addNotification } = useChatContext();
    const open = budget !== null;

    const [addSearch, setAddSearch] = useState("");
    const debouncedAddSearch = useDebounce(addSearch, SEARCH_DEBOUNCE_MS).trim();

    const assignments = useBudgetAssignments({ budgetId: budget?.id ?? "" }, { enabled: open });
    const candidates = useBudgetSubjects({ subjectType: "user", search: debouncedAddSearch, pageNumber: 1, pageSize: CANDIDATES_SHOWN }, { enabled: open && debouncedAddSearch !== "" });
    const save = useUpdateBudgetAssignmentsMutation();

    if (!budget) return null;

    const close = () => {
        setAddSearch("");
        onClose();
    };

    return (
        <ManageAssignmentsDialog
            open
            onOpenChange={next => !next && close()}
            budget={{ id: budget.id, name: budget.name, isDefault: budget.isDefault }}
            canEdit={userHasCapability(CAPABILITIES.assignBudgets)}
            assigned={{
                items: assignments.assignments,
                total: assignments.total,
                isLoading: assignments.isLoading,
                error: assignments.error,
                hasMore: assignments.hasNextPage,
                isLoadingMore: assignments.isFetchingNextPage,
                onLoadMore: () => {
                    assignments.fetchNextPage();
                },
            }}
            candidates={{ items: debouncedAddSearch === "" ? [] : (candidates.data?.data ?? []), isLoading: candidates.isFetching, error: candidates.error, search: addSearch, onSearchChange: setAddSearch }}
            onSave={async changes => {
                const result = await save.mutateAsync({ budgetId: budget.id, changes });
                addNotification(summarizeAssignmentChanges(result), "success");
            }}
        />
    );
}
