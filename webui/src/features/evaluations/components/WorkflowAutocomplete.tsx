// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { SearchableCombobox } from "@/ui/searchable-combobox";

import type { WorkflowOption } from "../hooks";
import { isWorkflowNotDeployed } from "../utils";

interface WorkflowAutocompleteProps {
    readonly options: WorkflowOption[];
    readonly value: string;
    readonly onChange: (name: string) => void;
    readonly disabled?: boolean;
}

const getWorkflowId = (o: WorkflowOption) => o.name;
const getWorkflowSearchText = (o: WorkflowOption) => `${o.displayName} ${o.name}`;

function renderWorkflowSelected(o: WorkflowOption) {
    return <span className="flex items-center">{o.displayName}</span>;
}

function renderWorkflowItem(o: WorkflowOption) {
    return (
        <span className="flex flex-1 items-center gap-2">
            <span className="flex-1 font-medium">{o.displayName}</span>
            {isWorkflowNotDeployed(o) && <span className="rounded-sm bg-(--warning-w100)/15 px-1.5 py-0.5 text-xs text-(--warning-w100)">Not Deployed</span>}
        </span>
    );
}

export function WorkflowAutocomplete({ options, value, onChange, disabled }: WorkflowAutocompleteProps) {
    return (
        <SearchableCombobox
            items={options}
            value={value}
            onChange={onChange}
            disabled={disabled}
            getId={getWorkflowId}
            getSearchText={getWorkflowSearchText}
            renderSelected={renderWorkflowSelected}
            renderItem={renderWorkflowItem}
            searchPlaceholder="Search workflows..."
            noMatchText="No workflows match your search."
        />
    );
}
