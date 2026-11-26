// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useRef } from "react";
import { Plus, Trash2 } from "lucide-react";

import { ErrorLabel } from "@/common/status";

import { uuid } from "@/utils/uuid";

import { Button } from "@/ui/button";
import { Input } from "@/ui/input";

interface StringListFieldProps {
    value?: Array<Record<string, string>>;
    onChange: (next: Array<Record<string, string>>) => void;
    itemKey: string;
    itemLabel?: string;
    placeholder?: string;
}

export const StringListField = ({ value = [], onChange, itemKey, itemLabel, placeholder }: StringListFieldProps) => {
    const label = itemLabel ?? "Value";
    const items = value.map(entry => entry?.[itemKey] ?? "");

    const rowIds = useRef<string[]>([]);
    while (rowIds.current.length < items.length) rowIds.current.push(uuid());

    const emit = (next: string[]) => onChange(next.map(v => ({ [itemKey]: v })));

    const handleChange = (index: number, next: string) => {
        const copy = [...items];
        copy[index] = next;
        emit(copy);
    };

    const handleAdd = () => emit([...items, ""]);

    const handleRemove = (index: number) => {
        rowIds.current.splice(index, 1);
        emit(items.filter((_, i) => i !== index));
    };

    return (
        <div className="space-y-2">
            {items.map((item, index) => {
                const isEmpty = item.trim() === "";
                return (
                    <div key={rowIds.current[index]} className="flex items-start gap-2">
                        <div className="flex-1 space-y-1">
                            <Input value={item} placeholder={placeholder} autoComplete="off" aria-label={`${label} ${index + 1}`} aria-invalid={isEmpty} onChange={event => handleChange(index, event.target.value)} />
                            {isEmpty && <ErrorLabel message={`${label} is required`} />}
                        </div>
                        <Button type="button" variant="ghost" size="sm" aria-label={`Remove ${label} ${index + 1}`} onClick={() => handleRemove(index)}>
                            <Trash2 />
                        </Button>
                    </div>
                );
            })}
            <Button type="button" variant="ghost" size="sm" onClick={handleAdd}>
                <Plus /> Add {label.toLowerCase()}
            </Button>
        </div>
    );
};
