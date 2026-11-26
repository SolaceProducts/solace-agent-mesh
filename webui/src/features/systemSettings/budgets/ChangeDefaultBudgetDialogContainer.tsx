// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useState } from "react";

import { useDefaultBudget, useMakeDefaultBudgetMutation } from "@/api/budgets";

import { getErrorMessage } from "@/utils/api";

import { ChangeDefaultBudgetDialog } from "./ChangeDefaultBudgetDialog";
import { toBudgetRow } from "./mapping";
import type { BudgetRow } from "./types";

interface ChangeDefaultBudgetDialogContainerProps {
    budget: BudgetRow | null;
    listedDefault: BudgetRow | null;
    onClose: () => void;
}

export function ChangeDefaultBudgetDialogContainer({ budget, listedDefault, onClose }: Readonly<ChangeDefaultBudgetDialogContainerProps>) {
    if (!budget) return null;
    return <OpenChangeDefaultBudgetDialog budget={budget} listedDefault={listedDefault} onClose={onClose} />;
}

interface DefaultComparison {
    currentDefault: BudgetRow | null;
    currentDefaultUnavailable: boolean;
}

function OpenChangeDefaultBudgetDialog({ budget, listedDefault, onClose }: Readonly<{ budget: BudgetRow; listedDefault: BudgetRow | null; onClose: () => void }>) {
    const lookup = useDefaultBudget("user");
    const makeDefault = useMakeDefaultBudgetMutation();
    const [saveError, setSaveError] = useState<string | null>(null);
    const [confirmed, setConfirmed] = useState<DefaultComparison | null>(null);

    const live: DefaultComparison = lookup.isError ? { currentDefault: listedDefault, currentDefaultUnavailable: listedDefault === null } : { currentDefault: lookup.data ? toBudgetRow(lookup.data) : null, currentDefaultUnavailable: false };
    const shown = confirmed ?? live;

    const save = async () => {
        setConfirmed(live);
        setSaveError(null);
        try {
            await makeDefault.mutateAsync(budget.id);
            onClose();
        } catch (e) {
            setConfirmed(null);
            setSaveError(getErrorMessage(e, "Couldn't update the default budget."));
        }
    };

    return (
        <ChangeDefaultBudgetDialog
            open
            onOpenChange={open => !open && onClose()}
            budget={budget}
            currentDefault={shown.currentDefault}
            isLoadingCurrentDefault={lookup.isPending}
            currentDefaultUnavailable={shown.currentDefaultUnavailable}
            isSaving={makeDefault.isPending}
            saveError={saveError}
            onSave={() => {
                save();
            }}
        />
    );
}
