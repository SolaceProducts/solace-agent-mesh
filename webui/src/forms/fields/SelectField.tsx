// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/ui/select";

import { type FieldProps, FieldShell } from "../FieldShell";

export interface SelectOption {
    value: string;
    label: string;
}

export interface SelectFieldProps extends FieldProps {
    options: SelectOption[];
}

const withStoredValue = (options: SelectOption[], value: string): SelectOption[] => (value && !options.some(option => option.value === value) ? [...options, { value, label: value }] : options);

export function SelectField({ options, placeholder, disabled, ...shell }: Readonly<SelectFieldProps>) {
    return (
        <FieldShell {...shell}>
            {({ field, fieldState, controlProps }) => (
                <Select value={field.value ?? ""} onValueChange={field.onChange} disabled={disabled}>
                    <SelectTrigger {...controlProps} ref={field.ref} onBlur={field.onBlur} invalid={fieldState.invalid} className="w-full">
                        <SelectValue placeholder={placeholder} />
                    </SelectTrigger>
                    <SelectContent>
                        {withStoredValue(options, field.value ?? "").map(option => (
                            <SelectItem key={option.value} value={option.value}>
                                {option.label}
                            </SelectItem>
                        ))}
                    </SelectContent>
                </Select>
            )}
        </FieldShell>
    );
}
