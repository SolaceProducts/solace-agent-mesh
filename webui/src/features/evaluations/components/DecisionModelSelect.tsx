// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { Link } from "react-router-dom";

import { useDecisionModelConfigs } from "@/api/models/hooks";

import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/ui/select";

import { ModelConfigIcon } from "@/features/activation";

import { findModelByRef } from "./evaluatorForm";

interface DecisionModelSelectProps {
    readonly value: string;
    readonly onChange: (id: string) => void;
    readonly disabled?: boolean;
    readonly invalid?: boolean;
    readonly describedBy?: string;
}

export function DecisionModelSelect({ value, onChange, disabled, invalid, describedBy }: DecisionModelSelectProps) {
    const { data: models = [], isLoading, error } = useDecisionModelConfigs();

    if (error) {
        return <span className="text-xs text-(--error-wMain)">Failed to load model configurations.</span>;
    }

    if (!isLoading && models.length === 0) {
        return (
            <div className="text-sm text-(--secondary-text-wMain)" data-testid="noDecisionModels">
                No decision models are registered.{" "}
                <Link to="/models" className="font-semibold text-(--primary-wMain) hover:underline">
                    Add one on the Models page
                </Link>
                , choosing <strong>Decision model</strong> as its type.
            </div>
        );
    }

    const selected = findModelByRef(models, value);
    const selectValue = selected?.id ?? (value || undefined);

    return (
        <Select value={selectValue} onValueChange={onChange} disabled={disabled || isLoading}>
            <SelectTrigger className="w-full" invalid={invalid} aria-label="Decision model" aria-describedby={describedBy} data-testid="decisionModelSelect">
                <SelectValue placeholder={isLoading ? "Loading..." : "Select a decision model"} />
            </SelectTrigger>
            <SelectContent>
                {selected === undefined && value && (
                    <SelectItem value={value} disabled>
                        {value} (not found)
                    </SelectItem>
                )}
                {models.map(m => (
                    <SelectItem key={m.id} value={m.id}>
                        <span className="flex items-center gap-2">
                            <ModelConfigIcon model={m} size="xs" />
                            <span className="font-medium">{m.alias}</span>
                            {m.modelName && <span className="font-mono text-xs text-(--secondary-text-wMain)">{m.modelName}</span>}
                        </span>
                    </SelectItem>
                ))}
            </SelectContent>
        </Select>
    );
}
