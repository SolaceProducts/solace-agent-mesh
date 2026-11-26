// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { SearchableCombobox } from "@/ui/searchable-combobox";

import { type AgentOption, isAgentNotDeployed } from "@/features/entrypoints/hooks";

interface AgentAutocompleteProps {
    readonly options: AgentOption[];
    readonly value: string;
    readonly onChange: (name: string) => void;
    readonly disabled?: boolean;
}

const getAgentId = (o: AgentOption) => o.name;
const getAgentSearchText = (o: AgentOption) => `${o.displayName} ${o.name}`;

function renderAgentSelected(o: AgentOption) {
    return <span className="flex items-center">{o.displayName}</span>;
}

function renderAgentItem(o: AgentOption) {
    const notDeployed = isAgentNotDeployed(o);
    return (
        <span className="flex flex-1 items-center gap-2">
            <span className="flex-1 font-medium">{o.displayName}</span>
            {notDeployed && <span className="rounded-sm bg-(--warning-w100)/15 px-1.5 py-0.5 text-xs text-(--warning-w100)">Not Deployed</span>}
        </span>
    );
}

export function AgentAutocomplete({ options, value, onChange, disabled }: AgentAutocompleteProps) {
    return (
        <SearchableCombobox
            items={options}
            value={value}
            onChange={onChange}
            disabled={disabled}
            getId={getAgentId}
            getSearchText={getAgentSearchText}
            renderSelected={renderAgentSelected}
            renderItem={renderAgentItem}
            searchPlaceholder="Search agents..."
            noMatchText="No agents match your search."
        />
    );
}
