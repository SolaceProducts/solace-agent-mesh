// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { ChangeEvent } from "react";

import { Input } from "@/ui/input";

import { type FieldProps, FieldShell } from "../FieldShell";

export interface NumberFieldProps extends FieldProps {
    min?: number;
    max?: number;
}

export const parseNumberInput = (raw: string): number | undefined => {
    if (raw.trim() === "") return undefined;
    const parsed = Number(raw);
    return Number.isNaN(parsed) ? undefined : parsed;
};

export function NumberField({ placeholder, disabled, min, max, ...shell }: Readonly<NumberFieldProps>) {
    return (
        <FieldShell {...shell}>
            {({ field, controlProps }) => (
                <Input
                    {...field}
                    {...controlProps}
                    type="number"
                    value={field.value ?? ""}
                    min={min}
                    max={max}
                    placeholder={placeholder}
                    disabled={disabled}
                    onChange={(event: ChangeEvent<HTMLInputElement>) => field.onChange(parseNumberInput(event.target.value))}
                />
            )}
        </FieldShell>
    );
}
