// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEvalDatasets } from "@/api/evaluations";

import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/ui/select";

interface DatasetSelectorProps {
    readonly value: string;
    readonly onChange: (value: string) => void;
    readonly disabled?: boolean;
    readonly placeholder?: string;
}

export function DatasetSelector({ value, onChange, disabled, placeholder = "Select a dataset..." }: DatasetSelectorProps) {
    const { data: datasets = [], isLoading: loading } = useEvalDatasets();
    return (
        <Select value={value} onValueChange={onChange} disabled={disabled || loading}>
            <SelectTrigger>
                <SelectValue placeholder={loading ? "Loading..." : placeholder} />
            </SelectTrigger>
            <SelectContent>
                {datasets.map(ds => (
                    <SelectItem key={ds.id} value={ds.id}>
                        {ds.name} ({ds.exampleCount} examples)
                    </SelectItem>
                ))}
            </SelectContent>
        </Select>
    );
}
